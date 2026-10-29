package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tbx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tmx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// Files renders the fixture's files: fixture.json and the interchange
// files the test imports, by name.
func Files(f *Fixture) (map[string][]byte, error) {
	out := map[string][]byte{}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	out[FileFixture] = bytes.Clone(buf.Bytes())

	for _, l := range f.Locales {
		b, err := catalogXLIFF(f, l)
		if err != nil {
			return nil, fmt.Errorf("xliff %s: %w", l, err)
		}
		if b != nil {
			out[XLIFFFile(f.SourceLocale, l)] = b
		}
	}
	b, err := legacyTMX(f)
	if err != nil {
		return nil, fmt.Errorf("tmx: %w", err)
	}
	out[FileTMX] = b
	if b, err = termbaseTBX(f); err != nil {
		return nil, fmt.Errorf("tbx: %w", err)
	}
	out[FileTBX] = b
	return out, nil
}

// Write writes the fixture's files into dir.
func Write(f *Fixture, dir string) error {
	files, err := Files(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func content(t Text, locale string) (mfcontent.Content, error) {
	syntax := mfcontent.MF1
	if t.Syntax == "mf2" {
		syntax = mfcontent.MF2
	}
	return mfcontent.Parse(syntax, t.Text, bcp47.MustParse(locale))
}

// catalogXLIFF is the XLIFF 2.1 file of one target locale: every message
// with its source, and the translations existing before the fill,
// approved (state final). Locales without translations have none (nil).
func catalogXLIFF(f *Fixture, locale string) ([]byte, error) {
	cat := formats.Catalog{SourceLocale: bcp47.MustParse(f.SourceLocale)}
	target := bcp47.MustParse(locale)
	for _, m := range f.Messages {
		t := m.Translations[locale]
		if t == nil || !t.Existing {
			continue
		}
		src, err := content(m.Source, f.SourceLocale)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Key, err)
		}
		tgt, err := content(t.Text, locale)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", m.Key, locale, err)
		}
		cat.Entries = append(cat.Entries, formats.Entry{
			ID: m.Key, Namespace: m.Namespace, Description: m.Description, MaxLength: m.MaxLength, Source: src,
			Targets: []formats.Target{{Locale: target, Content: tgt, State: formats.StateApproved}},
		})
	}
	if len(cat.Entries) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := xliff.Write(&buf, cat, xliff.WriteOptions{TargetLocale: target}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func legacyTMX(f *Fixture) ([]byte, error) {
	var units []formats.TMUnit
	for _, u := range f.LegacyTM {
		src, err := mfcontent.Parse(mfcontent.MF2, u.Source, bcp47.MustParse(u.SourceLocale))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", u.ID, err)
		}
		tgt, err := mfcontent.Parse(mfcontent.MF2, u.Target, bcp47.MustParse(u.TargetLocale))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", u.ID, err)
		}
		units = append(units, formats.TMUnit{
			ID: u.ID, SourceLocale: bcp47.MustParse(u.SourceLocale), TargetLocale: bcp47.MustParse(u.TargetLocale),
			Source: src, Target: tgt, Notes: []string{"Brotwerk Cloud v3 translation memory"},
		})
	}
	var buf bytes.Buffer
	err := tmx.Write(&buf, units, tmx.WriteOptions{SourceLocale: bcp47.MustParse(f.SourceLocale), CreationTool: "LegacyCAT", CreationToolVersion: "3.2"})
	return buf.Bytes(), err
}

func termbaseTBX(f *Fixture) ([]byte, error) {
	tb := formats.Termbase{Language: bcp47.MustParse("en")}
	for _, c := range f.Concepts {
		fc := formats.Concept{ID: c.ID, Domain: c.Domain, Definitions: []formats.Definition{{Text: c.Definition}}}
		for _, t := range c.Terms {
			fc.Terms = append(fc.Terms, formats.Term{Locale: bcp47.MustParse(t.Locale), Text: t.Text, Status: formats.TermStatus(t.Status)})
		}
		tb.Concepts = append(tb.Concepts, fc)
	}
	var buf bytes.Buffer
	err := tbx.Write(&buf, tb)
	return buf.Bytes(), err
}
