package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	knowledge "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// Knowledge adapts Knowledge's application service to tools.Knowledge.
type Knowledge struct{ knowledge *knowledgeapp.Service }

// NewKnowledge returns the adapter.
func NewKnowledge(s *knowledgeapp.Service) *Knowledge { return &Knowledge{knowledge: s} }

var _ tools.Knowledge = (*Knowledge)(nil)

// SearchTM implements tools.Knowledge.
func (a *Knowledge) SearchTM(ctx context.Context, q tools.TMSearch) ([]tools.TMMatch, error) {
	source, err := localeOf("source_locale", q.SourceLocale)
	if err != nil {
		return nil, err
	}
	target, err := localeOf("target_locale", q.TargetLocale)
	if err != nil {
		return nil, err
	}
	syntax, err := mfcontent.ParseSyntax(q.Syntax, mfcontent.MF2)
	if err != nil {
		return nil, &app.InvalidArgumentError{Argument: "syntax", Reason: "must be mf1 or mf2"}
	}
	content, err := mfcontent.Parse(syntax, q.Text, source)
	if err != nil {
		return nil, &app.InvalidArgumentError{
			Argument: "text", Reason: fmt.Sprintf("does not parse as %s", syntax),
		}
	}
	project := q.Project
	matches, err := a.knowledge.LookupTM(ctx, knowledgeapp.TMQuery{
		ProjectID: &project, SourceLocale: source, TargetLocale: target, Source: content.Model,
		MessageKey: q.MessageKey, Namespace: q.Namespace, Limit: q.Limit, MinScore: q.MinScore,
	})
	if err != nil {
		return nil, invalidQuery(notFound(err, knowledgeapp.ErrNotFound, knowledgeapp.ErrProjectNotFound))
	}
	out := make([]tools.TMMatch, len(matches))
	for i, m := range matches {
		out[i] = tools.TMMatch{
			Unit: m.Unit.ID.String(), Score: m.Score, Kind: string(m.Kind),
			Source: m.Unit.SourceMF2, Target: m.TargetText, Syntax: string(m.TargetSyntax),
			MessageKey: m.Unit.MessageKey, Namespace: m.Unit.Namespace, Adapted: m.Adapted,
		}
	}
	return out, nil
}

// LookupTerms implements tools.Knowledge.
func (a *Knowledge) LookupTerms(ctx context.Context, q tools.TermSearch) ([]tools.Term, error) {
	locale, err := localeOf("locale", q.Locale)
	if err != nil {
		return nil, err
	}
	project := q.Project
	in := knowledgeapp.TermQuery{ProjectID: &project, Text: q.Text, Locale: locale}
	if q.TargetLocale != "" {
		target, err := localeOf("target_locale", q.TargetLocale)
		if err != nil {
			return nil, err
		}
		in.TargetLocale = &target
	}
	hits, err := a.knowledge.RecognizeTerms(ctx, in)
	if err != nil {
		return nil, invalidQuery(notFound(err, knowledgeapp.ErrNotFound, knowledgeapp.ErrProjectNotFound))
	}
	out := make([]tools.Term, len(hits))
	for i, h := range hits {
		out[i] = tools.Term{
			Concept: h.ConceptID.String(), Text: h.Text, Status: string(h.Term.Status),
			Start: h.Start, End: h.End, Definition: h.Concept.Definition, Domain: h.Concept.Domain,
		}
		for _, t := range h.Targets {
			out[i].Targets = append(out[i].Targets, tools.TermUse{Text: t.Text, Status: string(t.Status)})
		}
	}
	return out, nil
}

// Style implements tools.Knowledge.
func (a *Knowledge) Style(ctx context.Context, project uuid.UUID, locale, namespace string) (tools.Style, error) {
	q := knowledgeapp.StyleQuery{ProjectID: &project, Namespace: namespace}
	if locale != "" {
		tag, err := localeOf("locale", locale)
		if err != nil {
			return tools.Style{}, err
		}
		q.Locale = tag
	}
	e, err := a.knowledge.EffectiveStyle(ctx, q)
	if err != nil {
		return tools.Style{}, notFound(err, knowledgeapp.ErrNotFound, knowledgeapp.ErrProjectNotFound)
	}
	fields, err := json.Marshal(e.Fields)
	if err != nil {
		return tools.Style{}, err
	}
	out := tools.Style{Fields: fields}
	for _, r := range e.Rules {
		out.Rules = append(out.Rules, tools.StyleRule{
			ID: r.ID, Title: r.Title, Rationale: r.Rationale, Good: r.Good, Bad: r.Bad,
		})
	}
	for _, s := range e.Sources {
		out.Sources = append(out.Sources, tools.StyleGuideRef{
			ID: s.ID.String(), Version: s.Version, Scope: scopeOf(s.Scope),
		})
	}
	return out, nil
}

// scopeOf renders a guide's scope for a reader.
func scopeOf(s knowledge.StyleScope) string {
	switch {
	case s.Namespace != "" && s.Locale != nil:
		return s.Locale.String() + "/" + s.Namespace
	case s.Namespace != "":
		return "namespace " + s.Namespace
	case s.Locale != nil:
		return s.Locale.String()
	case s.ProjectID != nil:
		return "project"
	}
	return "tenant"
}

// localeOf parses a BCP 47 argument.
func localeOf(name, s string) (bcp47.Tag, error) {
	tag, err := bcp47.Parse(s)
	if err != nil {
		return bcp47.Tag{}, &app.InvalidArgumentError{Argument: name, Reason: "not a BCP 47 locale"}
	}
	return tag, nil
}

// invalidQuery turns Knowledge's own "this query makes no sense" into
// an invalid argument, so the call is counted as invalid rather than as
// a server error.
func invalidQuery(err error) error {
	if errors.Is(err, knowledgeapp.ErrInvalidQuery) {
		return &app.InvalidArgumentError{Argument: "arguments", Reason: "the query is not one Knowledge accepts"}
	}
	return err
}
