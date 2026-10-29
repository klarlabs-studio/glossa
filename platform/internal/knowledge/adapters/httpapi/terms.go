package httpapi

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func toTerm(t domain.Term) apiv1.Term {
	out := apiv1.Term{
		Id: t.ID.String(), Locale: t.Locale.String(), Text: t.Text, Status: apiv1.TermStatus(t.Status),
		CaseSensitive: t.CaseSensitive, Note: nonEmpty(t.Note),
	}
	if t.PartOfSpeech != "" {
		out.PartOfSpeech = apiconv.Ptr(apiv1.PartOfSpeech(t.PartOfSpeech))
	}
	return out
}

func toTerms(ts []domain.Term) []apiv1.Term {
	out := make([]apiv1.Term, len(ts))
	for i, t := range ts {
		out[i] = toTerm(t)
	}
	return out
}

func toConcept(c domain.Concept) apiv1.TermConcept {
	return apiv1.TermConcept{
		Id: c.ID.String(), ProjectId: idPtr(c.ProjectID), Definition: c.Definition, Domain: c.Domain, Note: c.Note,
		ProductRef: c.ProductRef, Terms: toTerms(c.Terms), Version: c.Version, CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt, UpdatedBy: c.UpdatedBy, UpdatedAt: c.UpdatedAt,
	}
}

func conceptInput(definition, dom, note, productRef *string, terms []apiv1.TermInput) domain.ConceptInput {
	in := domain.ConceptInput{
		Definition: deref(definition), Domain: deref(dom), Note: deref(note), ProductRef: deref(productRef),
		Terms: make([]domain.TermInput, len(terms)),
	}
	for i, t := range terms {
		in.Terms[i] = domain.TermInput{
			Locale: t.Locale, Text: t.Text, Status: string(deref(t.Status)), PartOfSpeech: string(deref(t.PartOfSpeech)),
			CaseSensitive: deref(t.CaseSensitive), Note: deref(t.Note),
		}
	}
	return in
}

func (a *API) ListTermConcepts(ctx context.Context, req apiv1.ListTermConceptsRequestObject) (apiv1.ListTermConceptsResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.ConceptFilter{Domain: p.Domain, Query: p.Q}
	if f.Locale, err = optionalLocale(p.Locale); err != nil {
		return nil, err
	}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	cs, next, err := a.svc.ListConcepts(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListTermConcepts200JSONResponse{Items: make([]apiv1.TermConcept, len(cs)), NextPageToken: next}
	for i, c := range cs {
		out.Items[i] = toConcept(c)
	}
	return out, nil
}

func (a *API) CreateTermConcept(ctx context.Context, req apiv1.CreateTermConceptRequestObject) (apiv1.CreateTermConceptResponseObject, error) {
	b := req.Body
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	c, replayed, err := a.svc.CreateConcept(ctx, project, conceptInput(b.Definition, b.Domain, b.Note, b.ProductRef, b.Terms),
		deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateTermConcept201ResponseHeaders{
		ETag: apiconv.ETag(c.Version), Location: apiconv.Ptr(tenantPath(ctx, "/term-concepts/"+c.ID.String())),
	}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateTermConcept201JSONResponse{Body: toConcept(c), Headers: h}, nil
}

func (a *API) GetTermConcept(ctx context.Context, req apiv1.GetTermConceptRequestObject) (apiv1.GetTermConceptResponseObject, error) {
	id, err := pathID(req.Concept)
	if err != nil {
		return nil, err
	}
	c, err := a.svc.GetConcept(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetTermConcept200JSONResponse{Body: toConcept(c), Headers: apiv1.GetTermConcept200ResponseHeaders{ETag: apiconv.ETag(c.Version)}}, nil
}

func (a *API) ReplaceTermConcept(ctx context.Context, req apiv1.ReplaceTermConceptRequestObject) (apiv1.ReplaceTermConceptResponseObject, error) {
	id, err := pathID(req.Concept)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	c, err := a.svc.ReplaceConcept(ctx, id, conceptInput(b.Definition, b.Domain, b.Note, b.ProductRef, b.Terms), ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ReplaceTermConcept200JSONResponse{Body: toConcept(c), Headers: apiv1.ReplaceTermConcept200ResponseHeaders{ETag: apiconv.ETag(c.Version)}}, nil
}

func (a *API) DeleteTermConcept(ctx context.Context, req apiv1.DeleteTermConceptRequestObject) (apiv1.DeleteTermConceptResponseObject, error) {
	id, err := pathID(req.Concept)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteConcept(ctx, id, ifMatch); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteTermConcept204Response{}, nil
}

func (a *API) ListTermConceptRevisions(ctx context.Context, req apiv1.ListTermConceptRevisionsRequestObject) (apiv1.ListTermConceptRevisionsResponseObject, error) {
	id, err := pathID(req.Concept)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	rs, next, err := a.svc.ConceptRevisions(ctx, id, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListTermConceptRevisions200JSONResponse{Items: make([]apiv1.TermConceptRevision, len(rs)), NextPageToken: next}
	for i, r := range rs {
		out.Items[i] = apiv1.TermConceptRevision{
			Version: r.Concept.Version, Action: apiv1.RevisionAction(r.Action), Author: r.Author, CreatedAt: r.CreatedAt,
			Concept: toConcept(r.Concept),
		}
	}
	return out, nil
}

func (a *API) RecognizeTerms(ctx context.Context, req apiv1.RecognizeTermsRequestObject) (apiv1.RecognizeTermsResponseObject, error) {
	b := req.Body
	loc, err := locale(b.Locale)
	if err != nil {
		return nil, err
	}
	target, err := optionalLocale(b.TargetLocale)
	if err != nil {
		return nil, err
	}
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	text, err := analyzed(b.Text, b.Syntax, loc)
	if err != nil {
		return nil, err
	}
	hits, err := a.svc.RecognizeTerms(ctx, app.TermQuery{ProjectID: project, Text: text, Locale: loc, TargetLocale: target})
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.RecognizeTerms200JSONResponse{AnalyzedText: text, Hits: make([]apiv1.TermHit, len(hits))}
	for i, h := range hits {
		hit := apiv1.TermHit{
			ConceptId: h.ConceptID.String(), Definition: h.Concept.Definition, Term: toTerm(h.Term),
			Start: codePoints(text, h.Start), End: codePoints(text, h.End), Text: h.Text,
		}
		if target != nil {
			hit.Targets = apiconv.Ptr(toTerms(h.Targets))
		}
		out.Hits[i] = hit
	}
	return out, nil
}

func (a *API) CheckTerminology(ctx context.Context, req apiv1.CheckTerminologyRequestObject) (apiv1.CheckTerminologyResponseObject, error) {
	b := req.Body
	srcLoc, err := locale(b.SourceLocale)
	if err != nil {
		return nil, err
	}
	tgtLoc, err := locale(b.TargetLocale)
	if err != nil {
		return nil, err
	}
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	source, err := analyzed(b.Source, b.Syntax, srcLoc)
	if err != nil {
		return nil, err
	}
	target, err := analyzed(b.Target, b.Syntax, tgtLoc)
	if err != nil {
		return nil, err
	}
	fs, err := a.svc.CheckTerminology(ctx, app.TermCheck{
		ProjectID: project, Source: source, SourceLocale: srcLoc, Target: target, TargetLocale: tgtLoc,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CheckTerminology200JSONResponse{SourceText: source, TargetText: target, Findings: termFindings(fs, source, target)}, nil
}

// termFindings maps findings with their spans as code point offsets
// into the source or target text.
func termFindings(fs []domain.TermFinding, source, target string) []apiv1.TermFinding {
	out := make([]apiv1.TermFinding, len(fs))
	for i, f := range fs {
		text := source
		if f.Side == domain.SideTarget {
			text = target
		}
		suggestions := f.Suggestions
		if suggestions == nil {
			suggestions = []string{}
		}
		out[i] = apiv1.TermFinding{
			Code: apiv1.TermFindingCode(f.Code), Severity: apiv1.TermFindingSeverity(f.Severity),
			ConceptId: f.ConceptID.String(), TermId: f.TermID.String(), Side: apiv1.TermFindingSide(f.Side),
			Start: codePoints(text, f.Start), End: codePoints(text, f.End), Text: f.Text, Suggestions: suggestions,
			Message: f.Message,
		}
	}
	return out
}

func (a *API) ListProjectTerminologyFindings(ctx context.Context, req apiv1.ListProjectTerminologyFindingsRequestObject) (apiv1.ListProjectTerminologyFindingsResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	c := app.ProjectTermCheck{
		Namespace: p.Namespace, KeyPrefix: deref(p.KeyPrefix), Keys: deref(p.Key), Page: pg,
	}
	for _, l := range p.Locale {
		tag, err := locale(l)
		if err != nil {
			return nil, err
		}
		c.Locales = append(c.Locales, tag)
	}
	for _, st := range deref(p.State) {
		c.States = append(c.States, string(st))
	}
	r, err := a.svc.CheckProjectTerminology(ctx, project, c)
	if errors.Is(err, app.ErrProjectNotFound) {
		return nil, mapError(app.ErrNotFound)
	}
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListProjectTerminologyFindings200JSONResponse{
		Items: make([]apiv1.TranslationTerminologyFindings, len(r.Items)), Checked: r.Checked, NextPageToken: r.Next,
	}
	for i, it := range r.Items {
		out.Items[i] = apiv1.TranslationTerminologyFindings{
			MessageId: it.MessageID.String(), MessageKey: it.Key, Namespace: it.Namespace, Locale: it.Locale.String(),
			State: apiv1.ReviewState(it.State), SourceRevision: it.SourceRevision, SourceText: it.SourceText, TargetText: it.TargetText,
			Findings: termFindings(it.Findings, it.SourceText, it.TargetText),
		}
	}
	return out, nil
}
