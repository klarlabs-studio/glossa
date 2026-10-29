// Package sources adapts the Catalog, Localization, Release and
// Knowledge application services to Intelligence's ports — the only way
// Intelligence reads messages, translations, environments and
// linguistic knowledge, and writes translations (RFC 0002 §4: contexts
// reach each other through application ports, which keep checking the
// caller's permissions).
package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	knowledgedomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	releasedomain "go.klarlabs.de/glossa/platform/internal/release/domain"
)

// ── Catalog ──────────────────────────────────────────────────────────

// Catalog implements app.Catalog on Catalog's service.
type Catalog struct{ svc *catalogapp.Service }

// NewCatalog returns the port.
func NewCatalog(svc *catalogapp.Service) *Catalog { return &Catalog{svc: svc} }

var _ app.Catalog = (*Catalog)(nil)

func catalogErr(err error, notFound error) error {
	if errors.Is(err, catalogapp.ErrNotFound) {
		return notFound
	}
	return err
}

func message(m catalogdomain.Message) app.SourceMessage {
	return app.SourceMessage{
		ID: m.ID.UUID(), ProjectID: m.ProjectID.UUID(), Key: string(m.Key), Namespace: string(m.Namespace),
		Active: m.Translatable(), Revision: m.Revision, Source: m.Source.Model,
		SourceText: m.Source.Text, SourceSyntax: string(m.Source.Syntax), Description: m.Description, MaxLength: m.MaxLength,
	}
}

// Project implements app.Catalog.
func (c *Catalog) Project(ctx context.Context, id uuid.UUID) (app.ProjectInfo, error) {
	p, err := c.svc.GetProject(ctx, catalogdomain.ProjectID(id))
	if err != nil {
		return app.ProjectInfo{}, catalogErr(err, app.ErrProjectNotFound)
	}
	return app.ProjectInfo{ID: id, SourceLocale: p.SourceLocale.String()}, nil
}

// MessageByID implements app.Catalog.
func (c *Catalog) MessageByID(ctx context.Context, project, id uuid.UUID) (app.SourceMessage, error) {
	m, err := c.svc.MessageByID(ctx, catalogdomain.ProjectID(project), catalogdomain.MessageID(id))
	if err != nil {
		return app.SourceMessage{}, catalogErr(err, app.ErrNotFound)
	}
	return message(m), nil
}

// MessagesByKeys implements app.Catalog.
func (c *Catalog) MessagesByKeys(ctx context.Context, project uuid.UUID, keys []string) ([]app.SourceMessage, error) {
	found, err := c.svc.MessagesByKeys(ctx, catalogdomain.ProjectID(project), keys)
	if err != nil {
		return nil, catalogErr(err, app.ErrProjectNotFound)
	}
	out := make([]app.SourceMessage, 0, len(found))
	for _, m := range found {
		out = append(out, message(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// MessagesByIDs implements app.Catalog.
func (c *Catalog) MessagesByIDs(ctx context.Context, project uuid.UUID, ids []uuid.UUID) ([]app.SourceMessage, error) {
	mids := make([]catalogdomain.MessageID, len(ids))
	for i, id := range ids {
		mids[i] = catalogdomain.MessageID(id)
	}
	found, err := c.svc.MessagesByIDs(ctx, catalogdomain.ProjectID(project), mids)
	if err != nil {
		return nil, catalogErr(err, app.ErrProjectNotFound)
	}
	out := make([]app.SourceMessage, 0, len(found))
	for _, m := range found {
		out = append(out, message(m))
	}
	return out, nil
}

// Messages implements app.Catalog.
func (c *Catalog) Messages(ctx context.Context, project uuid.UUID, q app.MessageQuery, afterKey string, limit int) ([]app.SourceMessage, error) {
	rows, _, err := c.svc.ListMessages(ctx, catalogdomain.ProjectID(project), catalogapp.MessageQuery{
		Namespace: q.Namespace, State: string(catalogdomain.MessageActive), KeyPrefix: q.KeyPrefix,
		MissingIn: q.MissingIn, OutdatedIn: q.OutdatedIn,
	}, pagination.Page{Size: limit, After: afterKey})
	if err != nil {
		return nil, catalogErr(err, app.ErrProjectNotFound)
	}
	out := make([]app.SourceMessage, len(rows))
	for i, m := range rows {
		out[i] = message(m)
	}
	return out, nil
}

// ── Localization ─────────────────────────────────────────────────────

// Localization implements app.Localization on Localization's service
// (and Catalog's, for neighbouring messages' source).
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
	var qa *localizationdomain.QAError
	switch {
	case errors.Is(err, localizationapp.ErrNotFound):
		return app.ErrNotFound
	case errors.Is(err, localizationapp.ErrLocaleNotFound):
		return app.ErrLocaleNotFound
	case errors.Is(err, localizationapp.ErrPreconditionFailed), errors.Is(err, localizationapp.ErrPreconditionRequired),
		errors.Is(err, localizationapp.ErrStaleVersion), errors.Is(err, localizationapp.ErrConcurrentWrite):
		return fmt.Errorf("%w: %w", app.ErrTranslationConflict, err)
	case errors.As(err, &qa), errors.Is(err, localizationdomain.ErrReviewForbidden),
		errors.Is(err, localizationdomain.ErrInvalidSourceRev), errors.Is(err, localizationdomain.ErrSourceLocale):
		return fmt.Errorf("%w: %w", app.ErrTranslationRejected, err)
	}
	return err
}

// Locales implements app.Localization.
func (l *Localization) Locales(ctx context.Context, project uuid.UUID) ([]string, error) {
	var out []string
	page := pagination.Page{Size: pagination.MaxPageSize}
	for {
		rows, next, err := l.svc.ListLocales(ctx, project, page)
		if err != nil {
			if errors.Is(err, localizationapp.ErrNotFound) {
				return nil, app.ErrProjectNotFound
			}
			return nil, localizationErr(err)
		}
		for _, r := range rows {
			if !r.IsSource {
				out = append(out, r.Code.String())
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

// Translation implements app.Localization.
func (l *Localization) Translation(ctx context.Context, project uuid.UUID, key, locale string) (app.TranslationState, error) {
	v, err := l.svc.GetTranslation(ctx, project, key, locale)
	if err != nil {
		return app.TranslationState{}, localizationErr(err)
	}
	text, err := mf.Stringify(v.Content.Model)
	if err != nil {
		return app.TranslationState{}, err
	}
	return app.TranslationState{
		Exists: true, Revision: v.Revision, SourceRevision: v.SourceRevision, State: string(v.State), Text: text,
	}, nil
}

// Neighbours implements app.Localization: the active messages sharing
// the key prefix (Catalog) with their translations in locale
// (Localization).
func (l *Localization) Neighbours(ctx context.Context, project uuid.UUID, keyPrefix, key, locale string, limit int) ([]app.Neighbour, error) {
	msgs, _, err := l.catalog.ListMessages(ctx, catalogdomain.ProjectID(project), catalogapp.MessageQuery{
		State: string(catalogdomain.MessageActive), KeyPrefix: keyPrefix,
	}, pagination.Page{Size: limit + 1})
	if err != nil {
		return nil, catalogErr(err, app.ErrProjectNotFound)
	}
	translated := map[string]string{}
	rows, _, err := l.svc.ListProjectTranslations(ctx, project, localizationapp.TranslationFilter{
		Locales: []string{locale}, KeyPrefix: keyPrefix,
	}, pagination.Page{Size: pagination.MaxPageSize})
	if err != nil {
		return nil, localizationErr(err)
	}
	for _, r := range rows {
		if r.State == localizationdomain.StateRejected {
			continue
		}
		if text, err := mf.Stringify(r.Content.Model); err == nil {
			translated[r.Key] = text
		}
	}
	var out []app.Neighbour
	for _, m := range msgs {
		if string(m.Key) == key || len(out) == limit {
			continue
		}
		src, err := mf.Stringify(m.Source.Model)
		if err != nil {
			continue
		}
		out = append(out, app.Neighbour{Key: string(m.Key), SourceMF2: src, Translation: translated[string(m.Key)]})
	}
	return out, nil
}

// Write implements app.Localization: a PUT of the translation in MF2
// with the suggestion's provenance, as the principal on ctx.
func (l *Localization) Write(ctx context.Context, project uuid.UUID, key, locale string, w app.TranslationWrite, ifMatch int) (int, error) {
	var match *int
	if ifMatch > 0 {
		match = &ifMatch
	}
	rev := w.SourceRevision
	v, _, err := l.svc.PutTranslation(ctx, project, key, locale, localizationapp.TranslationInput{
		Text: w.Text, Syntax: "mf2", State: w.State, Origin: string(w.Origin), OriginDetail: w.OriginDetail, SourceRevision: &rev,
	}, match)
	if err != nil {
		return 0, localizationErr(err)
	}
	return v.Revision, nil
}

// ── Release ──────────────────────────────────────────────────────────

// Environments implements app.Environments on Release's service.
type Environments struct{ svc *releaseapp.Service }

// NewEnvironments returns the port.
func NewEnvironments(svc *releaseapp.Service) *Environments { return &Environments{svc: svc} }

var _ app.Environments = (*Environments)(nil)

// ShipsApproved implements app.Environments.
func (e *Environments) ShipsApproved(ctx context.Context, project uuid.UUID, environment string) (bool, error) {
	env, err := e.svc.GetEnvironment(ctx, project, environment)
	if errors.Is(err, releaseapp.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.Contains(env.Policy.States, releasedomain.StateApproved), nil
}

// ── Knowledge ────────────────────────────────────────────────────────

// Knowledge implements app.Knowledge (and so the agent's translation
// memory, termbase and style ports) on Knowledge's Reader.
type Knowledge struct{ svc *knowledgeapp.Service }

// NewKnowledge returns the port.
func NewKnowledge(svc *knowledgeapp.Service) *Knowledge { return &Knowledge{svc: svc} }

var _ app.Knowledge = (*Knowledge)(nil)

func projectOf(scope domain.Scope) (*uuid.UUID, error) {
	if scope.ProjectID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(scope.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("intelligence knowledge: project %q: %w", scope.ProjectID, err)
	}
	return &id, nil
}

func tags(pair domain.LocalePair) (bcp47.Tag, bcp47.Tag, error) {
	src, err1 := bcp47.Parse(pair.Source)
	tgt, err2 := bcp47.Parse(pair.Target)
	return src, tgt, errors.Join(err1, err2)
}

// LookupTM implements domain.TranslationMemory: exact (101 in context,
// 100) and fuzzy (50–99) matches, targets adapted to the source's
// variable names, each lookup counted as a hit.
func (k *Knowledge) LookupTM(ctx context.Context, scope domain.Scope, q domain.TMQuery) ([]domain.TMMatch, error) {
	project, err := projectOf(scope)
	if err != nil {
		return nil, err
	}
	src, tgt, err := tags(q.Pair)
	if err != nil {
		return nil, err
	}
	msg, err := mf.ParseMF2(q.Source)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrUnparsable, err)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = knowledgeapp.DefaultTMLimit
	}
	tq := knowledgeapp.TMQuery{
		ProjectID: project, SourceLocale: src, TargetLocale: tgt, Source: msg, MessageKey: q.Key, Namespace: q.Namespace,
		Limit: limit, CountHits: !q.Uncounted,
	}
	if q.ExactOnly {
		tq.MinScore = knowledgedomain.ScoreExact
	}
	matches, err := k.svc.LookupTM(ctx, tq)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TMMatch, len(matches))
	for i, m := range matches {
		out[i] = domain.TMMatch{UnitID: m.Unit.ID.String(), Score: m.Score, Source: m.Unit.SourceMF2, Target: m.TargetMF2, Origin: string(m.Unit.Origin)}
	}
	return out, nil
}

func term(t knowledgedomain.Term) domain.Term {
	return domain.Term{
		ID: t.ID.String(), Text: t.Text, Locale: t.Locale.String(), Status: domain.TermStatus(t.Status),
		CaseSensitive: t.CaseSensitive, PartOfSpeech: string(t.PartOfSpeech), Note: t.Note,
	}
}

// RecognizeTerms implements domain.Termbase: one hit per recognized
// concept, with the target locale's terms.
func (k *Knowledge) RecognizeTerms(ctx context.Context, scope domain.Scope, pair domain.LocalePair, sourceText string) ([]domain.TermHit, error) {
	project, err := projectOf(scope)
	if err != nil {
		return nil, err
	}
	src, tgt, err := tags(pair)
	if err != nil {
		return nil, err
	}
	hits, err := k.svc.RecognizeTerms(ctx, knowledgeapp.TermQuery{ProjectID: project, Text: sourceText, Locale: src, TargetLocale: &tgt})
	if err != nil {
		return nil, err
	}
	var out []domain.TermHit
	seen := map[uuid.UUID]bool{}
	for _, h := range hits {
		if seen[h.ConceptID] {
			continue
		}
		seen[h.ConceptID] = true
		hit := domain.TermHit{ConceptID: h.ConceptID.String(), Definition: h.Concept.Definition, Source: term(h.Term)}
		for _, t := range h.Targets {
			hit.Targets = append(hit.Targets, term(t))
		}
		out = append(out, hit)
	}
	return out, nil
}

// CheckTerminology implements domain.Termbase.
func (k *Knowledge) CheckTerminology(ctx context.Context, scope domain.Scope, pair domain.LocalePair, sourceText, translationText string) ([]domain.TermFinding, error) {
	project, err := projectOf(scope)
	if err != nil {
		return nil, err
	}
	src, tgt, err := tags(pair)
	if err != nil {
		return nil, err
	}
	findings, err := k.svc.CheckTerminology(ctx, knowledgeapp.TermCheck{
		ProjectID: project, Source: sourceText, SourceLocale: src, Target: translationText, TargetLocale: tgt,
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.TermFinding, len(findings))
	for i, f := range findings {
		out[i] = domain.TermFinding{Code: string(f.Code), ConceptID: f.ConceptID.String(), TermID: f.TermID.String(), Term: f.Text, Message: f.Message}
	}
	return out, nil
}

// EffectiveStyle implements domain.StyleGuides: the merged guide, its
// version naming every guide version merged.
func (k *Knowledge) EffectiveStyle(ctx context.Context, scope domain.Scope, locale, namespace string) (domain.StyleGuide, error) {
	eff, err := k.effective(ctx, scope, locale, namespace)
	if err != nil {
		return domain.StyleGuide{}, err
	}
	g := domain.StyleGuide{Version: strings.Join(sources(eff), "+"), Tone: eff.Fields.Tone, Punctuation: map[string]string{}}
	if f := eff.Fields.Formality; f.Register != nil {
		switch *f.Register {
		case knowledgedomain.RegisterFormal:
			g.Formality = domain.FormalityFormal
		case knowledgedomain.RegisterInformal:
			g.Formality = domain.FormalityInformal
		}
	}
	if p := eff.Fields.Formality.Pronoun; p != nil {
		g.Pronoun = *p
	}
	p := eff.Fields.Punctuation
	put := func(k string, v *string) {
		if v != nil {
			g.Punctuation[k] = *v
		}
	}
	putBool := func(k string, v *bool) {
		if v != nil {
			g.Punctuation[k] = strconv.FormatBool(*v)
		}
	}
	put("quotes", p.Quotes)
	put("nested_quotes", p.NestedQuotes)
	put("dash", p.Dash)
	put("ellipsis", p.Ellipsis)
	putBool("space_before_unit", p.SpaceBeforeUnit)
	putBool("space_before_punctuation", p.SpaceBeforePunctuation)
	putBool("serial_comma", p.SerialComma)
	put("decimal_separator", eff.Fields.Numbers.DecimalSeparator)
	put("grouping_separator", eff.Fields.Numbers.GroupingSeparator)
	put("number_notes", eff.Fields.Numbers.Notes)
	put("date_format", eff.Fields.Dates.Format)
	put("date_notes", eff.Fields.Dates.Notes)
	if len(g.Punctuation) == 0 {
		g.Punctuation = nil
	}
	for _, r := range eff.Rules {
		rule := r.Title
		if rule == "" {
			rule = r.ID
		}
		g.Rules = append(g.Rules, domain.StyleRule{ID: r.ID, Rule: rule, Rationale: r.Rationale, Good: r.Good, Bad: r.Bad})
	}
	return g, nil
}

func (k *Knowledge) effective(ctx context.Context, scope domain.Scope, locale, namespace string) (knowledgedomain.EffectiveStyle, error) {
	project, err := projectOf(scope)
	if err != nil {
		return knowledgedomain.EffectiveStyle{}, err
	}
	loc, err := bcp47.Parse(locale)
	if err != nil {
		return knowledgedomain.EffectiveStyle{}, err
	}
	return k.svc.EffectiveStyle(ctx, knowledgeapp.StyleQuery{ProjectID: project, Locale: loc, Namespace: namespace})
}

func sources(eff knowledgedomain.EffectiveStyle) []string {
	out := make([]string, len(eff.Sources))
	for i, s := range eff.Sources {
		out[i] = s.ID.String() + "@" + strconv.Itoa(s.Version)
	}
	return out
}

// StyleSources implements app.Knowledge.
func (k *Knowledge) StyleSources(ctx context.Context, scope domain.Scope, locale, namespace string) ([]string, error) {
	eff, err := k.effective(ctx, scope, locale, namespace)
	if err != nil {
		return nil, err
	}
	return sources(eff), nil
}

// TermbaseVersion implements app.Knowledge: a digest of the concepts in
// scope with a term in either locale, with their versions.
func (k *Knowledge) TermbaseVersion(ctx context.Context, scope domain.Scope, pair domain.LocalePair) (string, error) {
	project, err := projectOf(scope)
	if err != nil {
		return "", err
	}
	src, tgt, err := tags(pair)
	if err != nil {
		return "", err
	}
	var entries []string
	for _, l := range []bcp47.Tag{src, tgt} {
		page := pagination.Page{Size: pagination.MaxPageSize}
		for {
			concepts, next, err := k.svc.ListConcepts(ctx, knowledgeapp.ConceptFilter{ProjectID: project, Locale: &l}, page)
			if err != nil {
				return "", err
			}
			for _, c := range concepts {
				entries = append(entries, c.ID.String()+"@"+strconv.Itoa(c.Version))
			}
			if next == nil {
				break
			}
			if page, err = pagination.Parse(&page.Size, next); err != nil {
				return "", err
			}
		}
	}
	slices.Sort(entries)
	sum := sha256.Sum256([]byte(strings.Join(slices.Compact(entries), ",")))
	return "termbase:" + hex.EncodeToString(sum[:16]), nil
}

// Terms implements app.Knowledge: the texts of terms recognized in a
// text of locale.
func (k *Knowledge) Terms(ctx context.Context, scope domain.Scope, locale, text string) ([]string, error) {
	project, err := projectOf(scope)
	if err != nil {
		return nil, err
	}
	loc, err := bcp47.Parse(locale)
	if err != nil {
		return nil, err
	}
	hits, err := k.svc.RecognizeTerms(ctx, knowledgeapp.TermQuery{ProjectID: project, Text: text, Locale: loc})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, h := range hits {
		if !slices.Contains(out, h.Term.Text) {
			out = append(out, h.Term.Text)
		}
	}
	return out, nil
}
