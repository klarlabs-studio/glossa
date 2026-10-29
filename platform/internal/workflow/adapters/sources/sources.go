// Package sources adapts Catalog, Localization, Quality and
// Intelligence to the instance runner's ports. Every call goes through
// the owning context's application service with the principal on the
// context — Workflow's reader for the reads, the triggering actor for
// the actions — so each service's own permission check decides
// (RFC 0006 §2.5). Those contexts know nothing of Workflow (§2.1).
package sources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// ── Localization ────────────────────────────────────────────────────

// Translations implements app.Translations over Catalog (the message's
// key and namespace) and Localization (the translation and its review).
type Translations struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
}

// NewTranslations returns the adapter.
func NewTranslations(c *catalogapp.Service, l *localizationapp.Service) *Translations {
	return &Translations{catalog: c, localization: l}
}

var _ app.Translations = (*Translations)(nil)

// Unit implements app.Translations.
func (t *Translations) Unit(ctx context.Context, project, message uuid.UUID, locale string) (app.UnitFacts, error) {
	m, err := t.catalog.MessageByID(ctx, catalogdomain.ProjectID(project), catalogdomain.MessageID(message))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.UnitFacts{}, fmt.Errorf("%w: message %s", app.ErrUnavailable, message)
	}
	if err != nil {
		return app.UnitFacts{}, err
	}
	out := app.UnitFacts{Key: string(m.Key), Namespace: string(m.Namespace)}
	v, err := t.localization.GetTranslation(ctx, project, string(m.Key), locale)
	switch {
	case errors.Is(err, localizationapp.ErrNotFound):
		return out, nil
	case err != nil:
		return app.UnitFacts{}, err
	}
	out.Found = true
	out.ReviewState, out.Origin, out.Author, out.SourceRevision = string(v.State), string(v.Origin), v.By, v.SourceRevision
	return out, nil
}

// Review implements app.Translations through ReviewTranslation, at the
// revision just read: a translation revised in between is a conflict
// the outbox retries, never a review of text nobody looked at.
func (t *Translations) Review(ctx context.Context, project, message uuid.UUID, locale, state string) error {
	m, err := t.catalog.MessageByID(ctx, catalogdomain.ProjectID(project), catalogdomain.MessageID(message))
	if err != nil {
		return refusal(err)
	}
	v, err := t.localization.GetTranslation(ctx, project, string(m.Key), locale)
	if err != nil {
		return refusal(err)
	}
	if string(v.State) == state {
		return nil // already there: a redelivered step
	}
	_, err = t.localization.ReviewTranslation(ctx, project, string(m.Key), locale, state, v.Revision)
	return refusal(err)
}

// Authors implements app.Authors (four-eyes, RFC 0006 §3.2) over the
// same reads: the author of a translation unit is the actor of its
// latest content revision — Localization's Translation.By, which a
// review does not change. It reads as the principal on ctx (the
// decider), so it answers only what they could read themselves.
type Authors struct{ units *Translations }

// NewAuthors returns the adapter.
func NewAuthors(c *catalogapp.Service, l *localizationapp.Service) *Authors {
	return &Authors{units: NewTranslations(c, l)}
}

var _ app.Authors = (*Authors)(nil)

// ErrNoAuthor is a subject whose author cannot be told here: a release
// request (its requester is Release's to say; WorkService asks Release
// for it), or a unit with no translation yet.
var ErrNoAuthor = errors.New("workflow: the subject's author is not known")

// Author implements app.Authors.
func (a *Authors) Author(ctx context.Context, project uuid.UUID, s domain.ApprovalSubject) (string, error) {
	if s.Kind != domain.SubjectTranslation {
		return "", fmt.Errorf("%w: %s", ErrNoAuthor, s.Kind)
	}
	f, err := a.units.Unit(ctx, project, s.ID, s.Locale)
	if err != nil {
		return "", err
	}
	if !f.Found {
		return "", fmt.Errorf("%w: %s has no %s translation", ErrNoAuthor, s.ID, s.Locale)
	}
	return f.Author, nil
}

// refusal maps Localization's and Catalog's answers to the runner's
// outcomes.
func refusal(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, localizationdomain.ErrReviewForbidden):
		return fmt.Errorf("%w: %w", app.ErrRefused, err)
	case errors.Is(err, localizationapp.ErrNotFound), errors.Is(err, catalogapp.ErrNotFound),
		errors.Is(err, localizationdomain.ErrTransition):
		return fmt.Errorf("%w: %w", app.ErrUnavailable, err)
	}
	return err // authz.ErrForbidden passes through; anything else is retried
}

// ── Quality ─────────────────────────────────────────────────────────

// Findings implements app.Findings over Quality.
type Findings struct{ quality *qualityapp.Service }

// NewFindings returns the adapter.
func NewFindings(q *qualityapp.Service) *Findings { return &Findings{quality: q} }

var _ app.Findings = (*Findings)(nil)

// maxFindingPages bounds how much of one unit's findings a guard counts.
const maxFindingPages = 10

// Open implements app.Findings: the unit's unwaived findings in the
// project's newest check run.
func (f *Findings) Open(ctx context.Context, project, _ uuid.UUID, locale, key string) ([]domain.FindingCount, error) {
	waived := false
	counts := map[[2]string]int{}
	page := pagination.Page{Size: 100}
	for range maxFindingPages {
		res, err := f.quality.ListFindings(ctx, project, qualityapp.FindingQuery{
			Filter: qualityapp.FindingFilter{Locale: locale, Key: key, Waived: &waived},
		}, page)
		if errors.Is(err, qualityapp.ErrProjectNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, r := range res.Items {
			counts[[2]string{string(r.Layer), string(r.Severity)}]++
		}
		if res.Next == nil {
			break
		}
		page.After = *res.Next
	}
	out := make([]domain.FindingCount, 0, len(counts))
	for k, n := range counts {
		out = append(out, domain.FindingCount{Layer: k[0], Severity: k[1], Count: n})
	}
	return out, nil
}

// Run implements app.Findings with Quality's server-side check, which
// stores nothing: it answers what is wrong now.
func (f *Findings) Run(ctx context.Context, project uuid.UUID, layers []string) (string, error) {
	rep, err := f.quality.RunCheck(ctx, project, qualityapp.CheckRequest{Layers: layers})
	switch {
	case errors.Is(err, qualityapp.ErrNoSnapshot), errors.Is(err, qualityapp.ErrProjectNotFound):
		return "", fmt.Errorf("%w: %w", app.ErrUnavailable, err)
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("%s: %d errors, %d warnings", rep.Conclusion, rep.Counts.Errors, rep.Counts.Warnings), nil
}

// ── Intelligence ────────────────────────────────────────────────────

// Suggestions implements app.Suggestions over Intelligence.
type Suggestions struct{ intelligence *intelligenceapp.Service }

// NewSuggestions returns the adapter.
func NewSuggestions(i *intelligenceapp.Service) *Suggestions { return &Suggestions{intelligence: i} }

var _ app.Suggestions = (*Suggestions)(nil)

// Latest implements app.Suggestions. Intelligence routes a suggestion
// into a band (its action); it does not expose a translation-memory
// score per suggestion, so tm is 0 and a tm_match_at_least guard does
// not pass on it — a guard that cannot be answered says no.
func (s *Suggestions) Latest(ctx context.Context, project, message uuid.UUID, locale string) (string, float64, error) {
	rows, _, err := s.intelligence.ListSuggestions(ctx, intelligenceapp.SuggestionFilter{
		ProjectID: &project, MessageID: &message, Locale: locale,
	}, pagination.Page{Size: 1})
	if err != nil || len(rows) == 0 {
		return "", 0, err
	}
	return string(rows[0].Action), 0, nil
}

// Fill implements app.Suggestions with a fill request for one key in
// one locale, as the principal on ctx.
func (s *Suggestions) Fill(ctx context.Context, project uuid.UUID, key, locale, idemKey string) (string, error) {
	res, _, err := s.intelligence.RequestFill(ctx, project, intelligenceapp.FillRequest{
		Locales: []string{locale},
		Filter:  intelligenceapp.FillFilter{Keys: []string{key}, Select: intelligenceapp.SelectMissingOrOutdated},
	}, idemKey)
	switch {
	case errors.Is(err, intelligenceapp.ErrProjectNotFound), errors.Is(err, intelligenceapp.ErrNotFound):
		return "", fmt.Errorf("%w: %w", app.ErrUnavailable, err)
	case err != nil:
		return "", err
	}
	detail := "fill " + res.Fill.ID.String()
	if len(res.Warnings) > 0 {
		detail += " (" + strings.Join(res.Warnings, ", ") + ")"
	}
	return detail, nil
}
