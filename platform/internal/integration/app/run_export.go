package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/jsoncat"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tbx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tmx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// FailureNotRepresentable fails an export the format can't express with
// its options: an empty XLIFF document, MF2-only messages in an MF1
// JSON file (export MF2 instead).
const FailureNotRepresentable = "not_representable"

// pageSize is how many units or concepts an export reads at a time.
const pageSize = 500

// exported is what an export wrote.
type exported struct {
	name        string
	contentType string
	items       int
}

// runExport generates a job's file into a temporary file, then streams
// it to object storage.
func (s *Service) runExport(ctx context.Context, c Claim, j *domain.Job) error {
	tmp, err := os.CreateTemp("", "glossa-export-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	out, err := s.writeExport(ctx, j, io.MultiWriter(tmp, h))
	var fe *formats.Error
	if errors.As(err, &fe) {
		return permanent(FailureNotRepresentable, "%v", err)
	}
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := fileKey(tenantOf(ctx), j.ID, "export"+extensionOf(out.name))
	n, err := s.objects.PutStream(ctx, key, tmp, out.contentType)
	if err != nil {
		return err
	}
	j.File = &domain.File{Key: key, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), ContentType: out.contentType}
	j.FileName, j.Summary.Written, j.TotalItems, j.ProcessedItems = out.name, out.items, out.items, out.items
	return s.finish(ctx, c, j, domain.StateSucceeded, "", "")
}

func extensionOf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}

func (s *Service) writeExport(ctx context.Context, j *domain.Job, w io.Writer) (exported, error) {
	var project ProjectInfo
	if j.ProjectID != nil {
		var err error
		if project, err = s.catalog.Project(ctx, *j.ProjectID); errors.Is(err, ErrProjectNotFound) {
			return exported{}, permanent(domain.FailureProjectGone, "the project was deleted")
		} else if err != nil {
			return exported{}, err
		}
	}
	base := "termbase"
	if project.Slug != "" {
		base = project.Slug
	}
	switch j.Kind {
	case domain.KindTM:
		if project.Slug == "" {
			base = "translation-memory"
		}
		return s.writeTM(ctx, j, w, base)
	case domain.KindTermbase:
		return s.writeTermbase(ctx, j, w, base, project)
	}
	return s.writeCatalog(ctx, j, w, project)
}

// writeCatalog writes one file per locale: XLIFF documents for target
// locales (the source alone without locales), JSON catalogs of any
// locale (the source by default). Several files are zipped as
// <locale>.<ext>.
func (s *Service) writeCatalog(ctx context.Context, j *domain.Job, w io.Writer, p ProjectInfo) (exported, error) {
	snap, err := s.localization.Snapshot(ctx, p.ID, j.Options.ExportStates())
	if err != nil {
		return exported{}, err
	}
	cat := CatalogFromSnapshot(snap, j.Options.Namespaces)
	locales := make([]bcp47.Tag, 0, len(j.Options.Locales))
	for _, l := range j.Options.Locales {
		locales = append(locales, bcp47.MustParse(l))
	}
	if len(locales) == 0 {
		var none bcp47.Tag
		if j.Format == domain.FormatJSON {
			none = p.SourceLocale
		}
		locales = append(locales, none)
	}
	type file struct {
		name string
		body bytes.Buffer
	}
	files := make([]*file, len(locales))
	for i, l := range locales {
		f := &file{name: localeName(l) + j.Format.Extension()}
		if err := writeCatalogFile(&f.body, j, cat, l); err != nil {
			return exported{}, err
		}
		files[i] = f
	}
	out := exported{items: len(cat.Entries), contentType: j.Format.ContentType()}
	if len(files) == 1 {
		out.name = p.Slug + "." + files[0].name
		_, err := w.Write(files[0].body.Bytes())
		return out, err
	}
	out.name, out.contentType = p.Slug+"."+string(j.Format)+".zip", domain.ContentTypeZip
	zw := zip.NewWriter(w)
	for _, f := range files {
		fw, err := zw.Create(f.name)
		if err != nil {
			return exported{}, err
		}
		if _, err := fw.Write(f.body.Bytes()); err != nil {
			return exported{}, err
		}
	}
	return out, zw.Close()
}

func localeName(l bcp47.Tag) string {
	if l.IsZero() {
		return "source"
	}
	return l.String()
}

func writeCatalogFile(w io.Writer, j *domain.Job, cat formats.Catalog, locale bcp47.Tag) error {
	if j.Format == domain.FormatXLIFF {
		return xliff.Write(w, cat, xliff.WriteOptions{TargetLocale: locale})
	}
	layout := jsoncat.Flat
	if j.Options.Layout == "nested" {
		layout = jsoncat.Nested
	}
	return jsoncat.Write(w, cat, jsoncat.WriteOptions{Locale: locale, Layout: layout, Syntax: mfcontent.Syntax(j.Options.Syntax)})
}

func (s *Service) writeTM(ctx context.Context, j *domain.Job, w io.Writer, base string) (exported, error) {
	f := TMFilter{ProjectID: j.ProjectID}
	var source bcp47.Tag
	if j.Options.SourceLocale != "" {
		source = bcp47.MustParse(j.Options.SourceLocale)
		f.SourceLocale = &source
	}
	for _, l := range j.Options.Locales {
		f.TargetLocales = append(f.TargetLocales, bcp47.MustParse(l))
	}
	tw := tmx.NewWriter(w, tmx.WriteOptions{SourceLocale: source})
	n, after := 0, ""
	for {
		units, next, err := s.knowledge.TMUnits(ctx, f, after, pageSize)
		if err != nil {
			return exported{}, err
		}
		for _, u := range units {
			fu, err := TMUnitToFile(u)
			if err != nil {
				return exported{}, fmt.Errorf("integration: unit %s: %w", u.ID, err)
			}
			if err := tw.Write(fu); err != nil {
				return exported{}, err
			}
			n++
		}
		if next == "" {
			break
		}
		after = next
	}
	return exported{name: base + ".tmx", contentType: domain.FormatTMX.ContentType(), items: n}, tw.Close()
}

func (s *Service) writeTermbase(ctx context.Context, j *domain.Job, w io.Writer, base string, p ProjectInfo) (exported, error) {
	tb := formats.Termbase{Language: p.SourceLocale}
	after := ""
	for {
		concepts, next, err := s.knowledge.Concepts(ctx, j.ProjectID, after, pageSize)
		if err != nil {
			return exported{}, err
		}
		for _, c := range concepts {
			tb.Concepts = append(tb.Concepts, ConceptToFile(c))
		}
		if next == "" {
			break
		}
		after = next
	}
	return exported{name: base + ".tbx", contentType: domain.FormatTBX.ContentType(), items: len(tb.Concepts)}, tbx.Write(w, tb)
}
