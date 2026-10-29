package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func fromFields(f *apiv1.StyleFields) domain.StyleFields {
	var out domain.StyleFields
	if f == nil {
		return out
	}
	if fo := f.Formality; fo != nil {
		if fo.Register != nil {
			out.Formality.Register = apiconv.Ptr(domain.Register(*fo.Register))
		}
		out.Formality.Pronoun = fo.Pronoun
	}
	if f.Tone != nil {
		out.Tone = *f.Tone
	}
	if p := f.Punctuation; p != nil {
		out.Punctuation = domain.Punctuation{
			Quotes: p.Quotes, NestedQuotes: p.NestedQuotes, SpaceBeforeUnit: p.SpaceBeforeUnit,
			SpaceBeforePunctuation: p.SpaceBeforePunctuation, SerialComma: p.SerialComma, Ellipsis: p.Ellipsis,
		}
		if p.Dash != nil {
			out.Punctuation.Dash = apiconv.Ptr(string(*p.Dash))
		}
	}
	if n := f.Numbers; n != nil {
		out.Numbers = domain.NumberStyle{DecimalSeparator: n.DecimalSeparator, GroupingSeparator: n.GroupingSeparator, Notes: n.Notes}
	}
	if d := f.Dates; d != nil {
		out.Dates = domain.DateStyle{Format: d.Format, Notes: d.Notes}
	}
	return out
}

func toFields(f domain.StyleFields) apiv1.StyleFields {
	var out apiv1.StyleFields
	if f.Formality.Register != nil || f.Formality.Pronoun != nil {
		out.Formality = &apiv1.StyleFormality{Pronoun: f.Formality.Pronoun}
		if f.Formality.Register != nil {
			out.Formality.Register = apiconv.Ptr(apiv1.StyleFormalityRegister(*f.Formality.Register))
		}
	}
	if f.Tone != nil {
		out.Tone = apiconv.Ptr(f.Tone)
	}
	if p := f.Punctuation; p != (domain.Punctuation{}) {
		out.Punctuation = &apiv1.StylePunctuation{
			Quotes: p.Quotes, NestedQuotes: p.NestedQuotes, SpaceBeforeUnit: p.SpaceBeforeUnit,
			SpaceBeforePunctuation: p.SpaceBeforePunctuation, SerialComma: p.SerialComma, Ellipsis: p.Ellipsis,
		}
		if p.Dash != nil {
			out.Punctuation.Dash = apiconv.Ptr(apiv1.StylePunctuationDash(*p.Dash))
		}
	}
	if n := f.Numbers; n != (domain.NumberStyle{}) {
		out.Numbers = &apiv1.StyleNumbers{DecimalSeparator: n.DecimalSeparator, GroupingSeparator: n.GroupingSeparator, Notes: n.Notes}
	}
	if d := f.Dates; d != (domain.DateStyle{}) {
		out.Dates = &apiv1.StyleDates{Format: d.Format, Notes: d.Notes}
	}
	return out
}

func fromRules(rs *[]apiv1.StyleRule) []domain.StyleRule {
	if rs == nil {
		return nil
	}
	out := make([]domain.StyleRule, len(*rs))
	for i, r := range *rs {
		out[i] = domain.StyleRule{
			ID: r.Id, Title: deref(r.Title), Rationale: deref(r.Rationale), Good: deref(r.Good), Bad: deref(r.Bad),
			Disabled: deref(r.Disabled),
		}
	}
	return out
}

func toRules(rs []domain.StyleRule) []apiv1.StyleRule {
	out := make([]apiv1.StyleRule, len(rs))
	for i, r := range rs {
		out[i] = apiv1.StyleRule{Id: r.ID, Title: nonEmpty(r.Title), Rationale: nonEmpty(r.Rationale)}
		if len(r.Good) > 0 {
			out[i].Good = apiconv.Ptr(r.Good)
		}
		if len(r.Bad) > 0 {
			out[i].Bad = apiconv.Ptr(r.Bad)
		}
		if r.Disabled {
			out[i].Disabled = apiconv.Ptr(true)
		}
	}
	return out
}

func tagPtr(t *bcp47.Tag) *string {
	if t == nil {
		return nil
	}
	return apiconv.Ptr(t.String())
}

func toGuide(g domain.StyleGuide) apiv1.StyleGuide {
	return apiv1.StyleGuide{
		Id: g.ID.String(), ProjectId: idPtr(g.Scope.ProjectID), Locale: tagPtr(g.Scope.Locale),
		Namespace: nonEmpty(g.Scope.Namespace), Name: g.Name, Fields: toFields(g.Fields), Rules: toRules(g.Rules),
		Version: g.Version, CreatedBy: g.CreatedBy, CreatedAt: g.CreatedAt, UpdatedBy: g.UpdatedBy, UpdatedAt: g.UpdatedAt,
	}
}

func (a *API) ListStyleGuides(ctx context.Context, req apiv1.ListStyleGuidesRequestObject) (apiv1.ListStyleGuidesResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.StyleFilter{TenantOnly: deref(p.TenantOnly)}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	if f.Locale, err = optionalLocale(p.Locale); err != nil {
		return nil, err
	}
	gs, next, err := a.svc.ListStyleGuides(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListStyleGuides200JSONResponse{Items: make([]apiv1.StyleGuide, len(gs)), NextPageToken: next}
	for i, g := range gs {
		out.Items[i] = toGuide(g)
	}
	return out, nil
}

func (a *API) CreateStyleGuide(ctx context.Context, req apiv1.CreateStyleGuideRequestObject) (apiv1.CreateStyleGuideResponseObject, error) {
	b := req.Body
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	g, replayed, err := a.svc.CreateStyleGuide(ctx, app.NewStyleGuide{
		ProjectID: project, Locale: deref(b.Locale), Namespace: deref(b.Namespace),
		StyleInput: domain.StyleInput{Name: deref(b.Name), Fields: fromFields(b.Fields), Rules: fromRules(b.Rules)},
	}, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateStyleGuide201ResponseHeaders{
		ETag: apiconv.ETag(g.Version), Location: apiconv.Ptr(tenantPath(ctx, "/style-guides/"+g.ID.String())),
	}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateStyleGuide201JSONResponse{Body: toGuide(g), Headers: h}, nil
}

func (a *API) GetStyleGuide(ctx context.Context, req apiv1.GetStyleGuideRequestObject) (apiv1.GetStyleGuideResponseObject, error) {
	id, err := pathID(req.StyleGuide)
	if err != nil {
		return nil, err
	}
	g, err := a.svc.GetStyleGuide(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetStyleGuide200JSONResponse{Body: toGuide(g), Headers: apiv1.GetStyleGuide200ResponseHeaders{ETag: apiconv.ETag(g.Version)}}, nil
}

func (a *API) ReplaceStyleGuide(ctx context.Context, req apiv1.ReplaceStyleGuideRequestObject) (apiv1.ReplaceStyleGuideResponseObject, error) {
	id, err := pathID(req.StyleGuide)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	g, err := a.svc.ReplaceStyleGuide(ctx, id, domain.StyleInput{Name: deref(b.Name), Fields: fromFields(b.Fields), Rules: fromRules(b.Rules)}, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ReplaceStyleGuide200JSONResponse{Body: toGuide(g), Headers: apiv1.ReplaceStyleGuide200ResponseHeaders{ETag: apiconv.ETag(g.Version)}}, nil
}

func (a *API) DeleteStyleGuide(ctx context.Context, req apiv1.DeleteStyleGuideRequestObject) (apiv1.DeleteStyleGuideResponseObject, error) {
	id, err := pathID(req.StyleGuide)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteStyleGuide(ctx, id, ifMatch); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteStyleGuide204Response{}, nil
}

func (a *API) ListStyleGuideVersions(ctx context.Context, req apiv1.ListStyleGuideVersionsRequestObject) (apiv1.ListStyleGuideVersionsResponseObject, error) {
	id, err := pathID(req.StyleGuide)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	vs, next, err := a.svc.StyleGuideVersions(ctx, id, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListStyleGuideVersions200JSONResponse{Items: make([]apiv1.StyleGuideVersion, len(vs)), NextPageToken: next}
	for i, v := range vs {
		out.Items[i] = apiv1.StyleGuideVersion{
			Version: v.Guide.Version, Action: apiv1.RevisionAction(v.Action), Author: v.Author, CreatedAt: v.CreatedAt,
			StyleGuide: toGuide(v.Guide),
		}
	}
	return out, nil
}

func (a *API) GetEffectiveStyleGuide(ctx context.Context, req apiv1.GetEffectiveStyleGuideRequestObject) (apiv1.GetEffectiveStyleGuideResponseObject, error) {
	p := req.Params
	project, err := optionalID("project", p.Project)
	if err != nil {
		return nil, err
	}
	loc, err := optionalLocale(p.Locale)
	if err != nil {
		return nil, err
	}
	q := app.StyleQuery{ProjectID: project, Namespace: deref(p.Namespace)}
	if loc != nil {
		q.Locale = *loc
	}
	e, err := a.svc.EffectiveStyle(ctx, q)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.GetEffectiveStyleGuide200JSONResponse{
		Fields: toFields(e.Fields), Rules: toRules(e.Rules), Sources: make([]apiv1.StyleGuideSource, len(e.Sources)),
	}
	for i, s := range e.Sources {
		out.Sources[i] = apiv1.StyleGuideSource{
			StyleGuideId: s.ID.String(), Version: s.Version, ProjectId: idPtr(s.Scope.ProjectID),
			Locale: tagPtr(s.Scope.Locale), Namespace: nonEmpty(s.Scope.Namespace),
		}
	}
	return out, nil
}
