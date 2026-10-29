// Package sources adapts the Catalog, Localization and Knowledge
// application services to Integration's ports — the only way import and
// export jobs read and write messages, translations, translation memory
// and the termbase (RFC 0002 §4: contexts reach each other through
// application ports, which keep checking the caller's permissions).
package sources

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	knowledgedomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// ── Catalog ──────────────────────────────────────────────────────────

// Catalog implements app.Catalog on Catalog's service.
type Catalog struct{ svc *catalogapp.Service }

// NewCatalog returns the port.
func NewCatalog(svc *catalogapp.Service) *Catalog { return &Catalog{svc: svc} }

var _ app.Catalog = (*Catalog)(nil)

func projectErr(err error) error {
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}

// Project implements app.Catalog.
func (c *Catalog) Project(ctx context.Context, id uuid.UUID) (app.ProjectInfo, error) {
	p, err := c.svc.GetProject(ctx, catalogdomain.ProjectID(id))
	if err != nil {
		return app.ProjectInfo{}, projectErr(err)
	}
	return app.ProjectInfo{
		ID: id, Slug: string(p.Slug), SourceLocale: p.SourceLocale, ReviewRequired: p.Settings.ReviewRequired,
	}, nil
}

// MessagesByKeys implements app.Catalog.
func (c *Catalog) MessagesByKeys(ctx context.Context, project uuid.UUID, keys []string) (map[string]app.Message, error) {
	found, err := c.svc.MessagesByKeys(ctx, catalogdomain.ProjectID(project), keys)
	if err != nil {
		return nil, projectErr(err)
	}
	out := make(map[string]app.Message, len(found))
	for k, m := range found {
		out[k] = app.Message{Key: k, Source: m.Source}
	}
	return out, nil
}

// CheckMessage implements app.Catalog with the rules Catalog's upsert
// applies, and its item codes.
func (c *Catalog) CheckMessage(p app.ProjectInfo, w app.MessageWrite) (string, string) {
	if _, err := catalogdomain.ParseMessageKey(w.Key); err != nil {
		return "invalid_message_key", err.Error()
	}
	if w.SourceOnly {
		return checkSource(p, w)
	}
	ns, err := catalogdomain.ParseNamespace(w.Namespace)
	if err != nil {
		return "invalid_namespace", err.Error()
	}
	d := catalogdomain.Details{Namespace: ns, Description: w.Description}
	if w.MaxLength > 0 {
		d.MaxLength = &w.MaxLength
	}
	if err := d.Validate(); err != nil {
		return "invalid_details", err.Error()
	}
	return checkSource(p, w)
}

func checkSource(p app.ProjectInfo, w app.MessageWrite) (string, string) {
	_, err := mfcontent.Parse(w.Source.Syntax, w.Source.Text, p.SourceLocale)
	var invalid *mfcontent.InvalidError
	switch {
	case err == nil:
		return "", ""
	case errors.As(err, &invalid):
		return "invalid_message", string(invalid.Code) + ": " + invalid.Message
	case errors.Is(err, mfcontent.ErrInvalidSyntax):
		return "invalid_syntax", err.Error()
	case errors.Is(err, mfcontent.ErrTooLong):
		return "message_too_long", err.Error()
	}
	return "invalid_message", err.Error()
}

// UpsertMessages implements app.Catalog through Catalog's bulk upsert.
func (c *Catalog) UpsertMessages(ctx context.Context, project uuid.UUID, items []app.MessageWrite) ([]app.WriteResult, error) {
	in := make([]catalogapp.UpsertItem, len(items))
	for i, w := range items {
		it := catalogapp.UpsertItem{Key: w.Key, Text: w.Source.Text, Syntax: string(w.Source.Syntax)}
		if !w.SourceOnly {
			ns, desc := w.Namespace, w.Description
			it.Namespace, it.Description = &ns, &desc
			if w.MaxLength > 0 {
				n := w.MaxLength
				it.MaxLength = &n
			}
		}
		in[i] = it
	}
	res, err := c.svc.UpsertMessages(ctx, catalogdomain.ProjectID(project), in)
	if err != nil {
		return nil, projectErr(err)
	}
	out := make([]app.WriteResult, len(res))
	for i, r := range res {
		out[i] = app.WriteResult{Status: string(r.Status)}
		if r.Error != nil {
			out[i].Code, out[i].Detail = r.Error.Code, r.Error.Detail
		}
	}
	return out, nil
}

// ── Localization ─────────────────────────────────────────────────────

// Localization implements app.Localization on Localization's service
// (and Catalog's, for the snapshot's messages).
type Localization struct {
	svc     *localizationapp.Service
	catalog *catalogapp.Service
}

// NewLocalization returns the port.
func NewLocalization(svc *localizationapp.Service, catalog *catalogapp.Service) *Localization {
	return &Localization{svc: svc, catalog: catalog}
}

var _ app.Localization = (*Localization)(nil)

func localizationErr(err error) error {
	if errors.Is(err, localizationapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}

// ImportTranslations implements app.Localization.
func (l *Localization) ImportTranslations(ctx context.Context, project uuid.UUID, items []app.TranslationWrite, keepApproved, dryRun bool) ([]app.WriteResult, error) {
	in := make([]localizationapp.ImportItem, len(items))
	for i, w := range items {
		state := w.State
		in[i] = localizationapp.ImportItem{
			Key: w.Key, Locale: w.Locale.String(), Text: w.Content.Text, Syntax: string(w.Content.Syntax), State: &state,
			OriginDetail: w.OriginDetail,
		}
	}
	res, err := l.svc.ImportTranslationsWith(ctx, project, in, localizationapp.ImportOptions{KeepApproved: keepApproved, DryRun: dryRun})
	if err != nil {
		return nil, localizationErr(err)
	}
	out := make([]app.WriteResult, len(res))
	for i, r := range res {
		out[i] = app.WriteResult{Status: string(r.Status)}
		if r.Error != nil {
			out[i].Code, out[i].Detail = r.Error.Code, r.Error.Detail
		}
	}
	return out, nil
}

// Locales implements app.Localization.
func (l *Localization) Locales(ctx context.Context, project uuid.UUID) ([]bcp47.Tag, error) {
	var out []bcp47.Tag
	page := pagination.Page{Size: pagination.MaxPageSize}
	for {
		ls, next, err := l.svc.ListLocales(ctx, project, page)
		if err != nil {
			return nil, localizationErr(err)
		}
		for _, loc := range ls {
			if !loc.IsSource {
				out = append(out, loc.Code)
			}
		}
		if next == nil {
			return out, nil
		}
		if page, err = pagination.Parse(&page.Size, next); err != nil {
			return nil, err
		}
	}
}

// Snapshot implements app.Localization: Catalog's active messages
// joined by ID with Localization's translations in states.
func (l *Localization) Snapshot(ctx context.Context, project uuid.UUID, states []string) (app.Snapshot, error) {
	src, err := l.catalog.ReleaseSource(ctx, catalogdomain.ProjectID(project))
	if err != nil {
		return app.Snapshot{}, projectErr(err)
	}
	var eligible []localizationdomain.ReviewState
	for _, s := range states {
		st, err := localizationdomain.ParseReviewState(s)
		if err != nil {
			return app.Snapshot{}, err
		}
		eligible = append(eligible, st)
	}
	tr, err := l.svc.ReleaseTranslations(ctx, project, eligible)
	if err != nil {
		return app.Snapshot{}, localizationErr(err)
	}
	p := src.Project
	out := app.Snapshot{Project: app.ProjectInfo{
		ID: project, Slug: string(p.Slug), SourceLocale: p.SourceLocale, ReviewRequired: p.Settings.ReviewRequired,
	}}
	for _, loc := range tr.Locales {
		if !loc.IsSource {
			out.Locales = append(out.Locales, loc.Code)
		}
	}
	for _, m := range src.Messages {
		sm := app.SnapshotMessage{
			Key: string(m.Key), Namespace: string(m.Namespace), Description: m.Description, Source: m.Source,
			Translations: map[bcp47.Tag]app.SnapshotTranslation{},
		}
		if m.MaxLength != nil {
			sm.MaxLength = *m.MaxLength
		}
		for locale, byMessage := range tr.Translations {
			if t, ok := byMessage[m.ID.UUID()]; ok {
				sm.Translations[locale] = app.SnapshotTranslation{Content: t.Content, State: string(t.State)}
			}
		}
		out.Messages = append(out.Messages, sm)
	}
	return out, nil
}

// ── Knowledge ────────────────────────────────────────────────────────

// Knowledge implements app.Knowledge on Knowledge's service.
type Knowledge struct{ svc *knowledgeapp.Service }

// NewKnowledge returns the port.
func NewKnowledge(svc *knowledgeapp.Service) *Knowledge { return &Knowledge{svc: svc} }

var _ app.Knowledge = (*Knowledge)(nil)

func knowledgeErr(err error) error {
	if errors.Is(err, knowledgeapp.ErrProjectNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}

func results(res []knowledgeapp.ImportResult) []app.WriteResult {
	out := make([]app.WriteResult, len(res))
	for i, r := range res {
		out[i] = app.WriteResult{Status: string(r.Status), Code: r.Code, Detail: r.Detail}
	}
	return out
}

// ImportTMUnits implements app.Knowledge.
func (k *Knowledge) ImportTMUnits(ctx context.Context, project *uuid.UUID, units []app.TMWrite, dryRun bool) ([]app.WriteResult, error) {
	in := make([]knowledgedomain.ImportedText, len(units))
	for i, u := range units {
		in[i] = knowledgedomain.ImportedText{SourceLocale: u.SourceLocale, TargetLocale: u.TargetLocale, Source: u.Source, Target: u.Target}
	}
	res, err := k.svc.ImportTMUnits(ctx, project, in, dryRun)
	if err != nil {
		return nil, knowledgeErr(err)
	}
	return results(res), nil
}

// ImportConcepts implements app.Knowledge.
func (k *Knowledge) ImportConcepts(ctx context.Context, project *uuid.UUID, concepts []app.ConceptWrite, overwrite, dryRun bool) ([]app.WriteResult, error) {
	in := make([]knowledgeapp.ConceptImport, len(concepts))
	for i, c := range concepts {
		input := knowledgedomain.ConceptInput{Definition: c.Definition, Domain: c.Domain, Note: c.Note}
		for _, t := range c.Terms {
			input.Terms = append(input.Terms, knowledgedomain.TermInput{
				Locale: t.Locale, Text: t.Text, Status: t.Status, PartOfSpeech: t.PartOfSpeech, Note: t.Note,
			})
		}
		in[i] = knowledgeapp.ConceptImport{ID: c.ID, Input: input}
	}
	res, err := k.svc.ImportConcepts(ctx, project, in, overwrite, dryRun)
	if err != nil {
		return nil, knowledgeErr(err)
	}
	return results(res), nil
}

// ConceptExists implements app.Knowledge.
func (k *Knowledge) ConceptExists(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := k.svc.GetConcept(ctx, id)
	if errors.Is(err, knowledgeapp.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// page decodes an opaque continuation into a page of size.
func page(after string, size int) (pagination.Page, error) {
	if after == "" {
		return pagination.Page{Size: size}, nil
	}
	max := pagination.MaxPageSize
	p, err := pagination.Parse(&max, &after)
	p.Size = size
	return p, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TMUnits implements app.Knowledge: active units, in ID order.
func (k *Knowledge) TMUnits(ctx context.Context, f app.TMFilter, after string, limit int) ([]app.TMUnitView, string, error) {
	pg, err := page(after, limit)
	if err != nil {
		return nil, "", err
	}
	uf := knowledgeapp.UnitFilter{ProjectID: f.ProjectID, SourceLocale: f.SourceLocale, State: "active"}
	if len(f.TargetLocales) == 1 {
		uf.TargetLocale = &f.TargetLocales[0]
	}
	units, next, err := k.svc.ListUnits(ctx, uf, pg)
	if err != nil {
		return nil, "", knowledgeErr(err)
	}
	var out []app.TMUnitView
	for _, u := range units {
		if len(f.TargetLocales) > 1 && !slices.Contains(f.TargetLocales, u.TargetLocale) {
			continue
		}
		out = append(out, app.TMUnitView{
			ID: u.ID, ProjectID: u.ProjectID, MessageKey: u.MessageKey, SourceLocale: u.SourceLocale, TargetLocale: u.TargetLocale,
			SourceMF2: u.SourceMF2, TargetMF2: u.TargetMF2, HitCount: u.HitCount, LastHitAt: u.LastHitAt,
			CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		})
	}
	return out, deref(next), nil
}

// Concepts implements app.Knowledge: every concept of the tenant for
// project nil, else only the project's own (not the tenant-wide ones
// that also apply to it).
func (k *Knowledge) Concepts(ctx context.Context, project *uuid.UUID, after string, limit int) ([]app.ConceptView, string, error) {
	pg, err := page(after, limit)
	if err != nil {
		return nil, "", err
	}
	concepts, next, err := k.svc.ListConcepts(ctx, knowledgeapp.ConceptFilter{ProjectID: project}, pg)
	if err != nil {
		return nil, "", knowledgeErr(err)
	}
	var out []app.ConceptView
	for _, c := range concepts {
		if project != nil && (c.ProjectID == nil || *c.ProjectID != *project) {
			continue
		}
		v := app.ConceptView{ID: c.ID, ProjectID: c.ProjectID, Definition: c.Definition, Domain: c.Domain, Note: c.Note}
		for _, t := range c.Terms {
			v.Terms = append(v.Terms, app.TermWrite{
				Locale: t.Locale.String(), Text: t.Text, Status: string(t.Status), PartOfSpeech: string(t.PartOfSpeech), Note: t.Note,
			})
		}
		out = append(out, v)
	}
	return out, deref(next), nil
}
