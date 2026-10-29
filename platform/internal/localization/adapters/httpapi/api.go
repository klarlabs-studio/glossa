// Package httpapi is Localization's HTTP edge: its operations of the
// generated /v1 strict server (locales, fallback graph, translations,
// reviews, history, import).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// API serves Localization's operations.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

func projectID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrNotFound)
	}
	return id, nil
}

func projectPath(ctx context.Context, project uuid.UUID, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + "/projects/" + project.String() + sub
}

// ── locales ─────────────────────────────────────────────────────────

func toLocale(l domain.Locale) apiv1.ProjectLocale {
	return apiv1.ProjectLocale{
		Code: l.Code.String(), Direction: apiv1.Direction(l.Direction()), IsSource: l.IsSource, CreatedAt: l.CreatedAt,
	}
}

func (a *API) ListLocales(ctx context.Context, req apiv1.ListLocalesRequestObject) (apiv1.ListLocalesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ls, next, err := a.svc.ListLocales(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListLocales200JSONResponse{Items: make([]apiv1.ProjectLocale, len(ls)), NextPageToken: next}
	for i, l := range ls {
		out.Items[i] = toLocale(l)
	}
	return out, nil
}

func (a *API) AddLocale(ctx context.Context, req apiv1.AddLocaleRequestObject) (apiv1.AddLocaleResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	l, created, err := a.svc.AddLocale(ctx, project, req.Body.Code)
	if err != nil {
		return nil, mapError(err)
	}
	if !created {
		return apiv1.AddLocale200JSONResponse(toLocale(l)), nil
	}
	return apiv1.AddLocale201JSONResponse{Body: toLocale(l), Headers: apiv1.AddLocale201ResponseHeaders{
		Location: apiconv.Ptr(projectPath(ctx, project, "/locales/"+l.Code.String())),
	}}, nil
}

func (a *API) GetLocale(ctx context.Context, req apiv1.GetLocaleRequestObject) (apiv1.GetLocaleResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	l, err := a.svc.GetLocale(ctx, project, req.Locale)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetLocale200JSONResponse(toLocale(l)), nil
}

func (a *API) RemoveLocale(ctx context.Context, req apiv1.RemoveLocaleRequestObject) (apiv1.RemoveLocaleResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	if err := a.svc.RemoveLocale(ctx, project, req.Locale); err != nil {
		return nil, mapError(err)
	}
	return apiv1.RemoveLocale204Response{}, nil
}

func toGraph(g app.Graph) apiv1.FallbackGraph {
	edges := g.Edges
	if edges == nil {
		edges = map[string][]string{}
	}
	return apiv1.FallbackGraph{Fallback: edges}
}

// graphETag is absent until a graph is stored.
func graphETag(g app.Graph) *string {
	if g.Version == 0 {
		return nil
	}
	return apiconv.ETag(g.Version)
}

func (a *API) GetFallbackGraph(ctx context.Context, req apiv1.GetFallbackGraphRequestObject) (apiv1.GetFallbackGraphResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	g, err := a.svc.FallbackGraph(ctx, project)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetFallbackGraph200JSONResponse{Body: toGraph(g), Headers: apiv1.GetFallbackGraph200ResponseHeaders{ETag: graphETag(g)}}, nil
}

func (a *API) PutFallbackGraph(ctx context.Context, req apiv1.PutFallbackGraphRequestObject) (apiv1.PutFallbackGraphResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	g, err := a.svc.PutFallbackGraph(ctx, project, req.Body.Fallback, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PutFallbackGraph200JSONResponse{Body: toGraph(g), Headers: apiv1.PutFallbackGraph200ResponseHeaders{ETag: graphETag(g)}}, nil
}

// ── translations ────────────────────────────────────────────────────

func toTranslation(v app.TranslationView) apiv1.Translation {
	warnings := v.Warnings
	return apiv1.Translation{
		Id: v.ID.String(), MessageId: v.MessageID.String(), Locale: v.Locale.String(), Text: v.Content.Text,
		Syntax: apiv1.Syntax(v.Content.Syntax), Model: apiconv.Model(v.Content), State: apiv1.ReviewState(v.State),
		Origin: apiv1.Origin(v.Origin), Author: v.By, SourceRevision: v.SourceRevision,
		CurrentSourceRevision: max(v.CurrentSourceRevision, v.SourceRevision), Outdated: v.Outdated(),
		Warnings: apiconv.Findings(warnings), Revision: v.Revision, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		ReviewByAuthorAllowed: v.ReviewByAuthorAllowed,
	}
}

func (a *API) ListMessageTranslations(ctx context.Context, req apiv1.ListMessageTranslationsRequestObject) (apiv1.ListMessageTranslationsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ts, next, err := a.svc.ListTranslations(ctx, project, req.Message, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListMessageTranslations200JSONResponse{Items: make([]apiv1.Translation, len(ts)), NextPageToken: next}
	for i, t := range ts {
		out.Items[i] = toTranslation(t)
	}
	return out, nil
}

func (a *API) ListProjectTranslations(ctx context.Context, req apiv1.ListProjectTranslationsRequestObject) (apiv1.ListProjectTranslationsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.TranslationFilter{Locales: req.Params.Locale, Outdated: req.Params.Outdated, Namespace: req.Params.Namespace}
	if req.Params.State != nil {
		for _, st := range *req.Params.State {
			f.States = append(f.States, string(st))
		}
	}
	if req.Params.Origin != nil {
		for _, o := range *req.Params.Origin {
			f.Origins = append(f.Origins, string(o))
		}
	}
	if req.Params.KeyPrefix != nil {
		f.KeyPrefix = *req.Params.KeyPrefix
	}
	if req.Params.MessageState != nil {
		f.MessageState = apiconv.Ptr(string(*req.Params.MessageState))
	}
	ts, next, err := a.svc.ListProjectTranslations(ctx, project, f, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListProjectTranslations200JSONResponse{Items: make([]apiv1.ProjectTranslation, len(ts)), NextPageToken: next}
	for i, t := range ts {
		v := toTranslation(t.TranslationView)
		out.Items[i] = apiv1.ProjectTranslation{
			Id: v.Id, MessageId: v.MessageId, Key: t.Key, Namespace: t.Namespace, MessageState: apiv1.MessageState(t.MessageState),
			Locale: v.Locale, Text: v.Text, Syntax: v.Syntax, Model: v.Model, State: v.State, Origin: v.Origin,
			Author: v.Author, SourceRevision: v.SourceRevision, CurrentSourceRevision: v.CurrentSourceRevision,
			Outdated: v.Outdated, Warnings: v.Warnings, Revision: v.Revision, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		}
	}
	return out, nil
}

func (a *API) GetTranslationStats(ctx context.Context, req apiv1.GetTranslationStatsRequestObject) (apiv1.GetTranslationStatsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	stats, err := a.svc.TranslationStats(ctx, project)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.GetTranslationStats200JSONResponse{Messages: stats.Messages, Locales: make([]apiv1.LocaleStats, len(stats.Locales))}
	for i, l := range stats.Locales {
		out.Locales[i] = apiv1.LocaleStats{
			Code: l.Code.String(), Direction: apiv1.Direction(l.Direction()), IsSource: l.IsSource,
			Translated: l.Translated, Missing: l.Missing, Outdated: l.Outdated,
			States: apiv1.ReviewStateCounts{
				Draft: l.States.Draft, NeedsReview: l.States.NeedsReview, Approved: l.States.Approved, Rejected: l.States.Rejected,
			},
		}
	}
	return out, nil
}

func (a *API) GetTranslation(ctx context.Context, req apiv1.GetTranslationRequestObject) (apiv1.GetTranslationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	t, err := a.svc.GetTranslation(ctx, project, req.Message, req.Locale)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetTranslation200JSONResponse{Body: toTranslation(t), Headers: apiv1.GetTranslation200ResponseHeaders{ETag: apiconv.ETag(t.Revision)}}, nil
}

func detail(m *map[string]any) (json.RawMessage, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(*m)
}

func (a *API) PutTranslation(ctx context.Context, req apiv1.PutTranslationRequestObject) (apiv1.PutTranslationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	in := app.TranslationInput{Text: req.Body.Text, SourceRevision: req.Body.SourceRevision}
	if req.Body.Syntax != nil {
		in.Syntax = string(*req.Body.Syntax)
	}
	if req.Body.State != nil {
		in.State = apiconv.Ptr(string(*req.Body.State))
	}
	if req.Body.Origin != nil {
		in.Origin = string(*req.Body.Origin)
	}
	if in.OriginDetail, err = detail(req.Body.OriginDetail); err != nil {
		return nil, mapError(domain.ErrInvalidOriginInfo)
	}
	t, status, err := a.svc.PutTranslation(ctx, project, req.Message, req.Locale, in, ifMatch)
	var qa *domain.QAError
	if errors.As(err, &qa) {
		return qaProblem(qa), nil
	}
	if err != nil {
		return nil, mapError(err)
	}
	if status == app.WriteCreated {
		return apiv1.PutTranslation201JSONResponse{Body: toTranslation(t), Headers: apiv1.PutTranslation201ResponseHeaders{
			ETag: apiconv.ETag(t.Revision), Location: apiconv.Ptr(projectPath(ctx, project, "/messages/"+req.Message+"/translations/"+t.Locale.String())),
		}}, nil
	}
	return apiv1.PutTranslation200JSONResponse{Body: toTranslation(t), Headers: apiv1.PutTranslation200ResponseHeaders{ETag: apiconv.ETag(t.Revision)}}, nil
}

// qaProblem is the 422 with the structural findings (errors first).
func qaProblem(qa *domain.QAError) apiv1.PutTranslation422ApplicationProblemPlusJSONResponse {
	d := problem.New(http.StatusUnprocessableEntity, "structural_qa_failed",
		"the translation is structurally incompatible with its source; see findings")
	findings := apiconv.Findings(qa.Findings)
	return apiv1.PutTranslation422ApplicationProblemPlusJSONResponse{
		StructuralQAFailedApplicationProblemPlusJSONResponse: apiv1.StructuralQAFailedApplicationProblemPlusJSONResponse{
			Type: d.Type, Title: d.Title, Status: d.Status, Code: string(d.Code), Detail: &d.Detail, Findings: &findings,
		},
	}
}

func (a *API) ReviewTranslation(ctx context.Context, req apiv1.ReviewTranslationRequestObject) (apiv1.ReviewTranslationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	t, err := a.svc.ReviewTranslation(ctx, project, req.Message, req.Locale, string(req.Body.State), ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ReviewTranslation200JSONResponse{Body: toTranslation(t), Headers: apiv1.ReviewTranslation200ResponseHeaders{ETag: apiconv.ETag(t.Revision)}}, nil
}

func (a *API) ListTranslationRevisions(ctx context.Context, req apiv1.ListTranslationRevisionsRequestObject) (apiv1.ListTranslationRevisionsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	revs, next, err := a.svc.TranslationRevisions(ctx, project, req.Message, req.Locale, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListTranslationRevisions200JSONResponse{Items: make([]apiv1.TranslationRevision, len(revs)), NextPageToken: next}
	for i, r := range revs {
		originDetail := map[string]any{}
		_ = json.Unmarshal(r.Provenance.Detail, &originDetail)
		out.Items[i] = apiv1.TranslationRevision{
			Revision: r.Number, Kind: apiv1.TranslationRevisionKind(r.Kind), Text: r.Content.Text,
			Syntax: apiv1.Syntax(r.Content.Syntax), State: apiv1.ReviewState(r.State), Origin: apiv1.Origin(r.Provenance.Origin),
			OriginDetail: originDetail, Author: r.Provenance.By, SourceRevision: r.SourceRevision,
			Findings: apiconv.Findings(r.Findings), SelfReview: r.SelfReview, CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

func (a *API) ImportTranslations(ctx context.Context, req apiv1.ImportTranslationsRequestObject) (apiv1.ImportTranslationsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	items := make([]app.ImportItem, len(req.Body.Items))
	for i, it := range req.Body.Items {
		items[i] = app.ImportItem{Key: it.Key, Locale: it.Locale, Text: it.Text, SourceRevision: it.SourceRevision}
		if it.Syntax != nil {
			items[i].Syntax = string(*it.Syntax)
		}
		if it.State != nil {
			items[i].State = apiconv.Ptr(string(*it.State))
		}
		if items[i].OriginDetail, err = detail(it.OriginDetail); err != nil {
			return nil, mapError(domain.ErrInvalidOriginInfo)
		}
	}
	results, err := a.svc.ImportTranslations(ctx, project, items)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ImportTranslations200JSONResponse{Results: make([]apiv1.TranslationImportItemResult, len(results))}
	for i, r := range results {
		res := apiv1.TranslationImportItemResult{Key: r.Key, Locale: r.Locale}
		if r.Error != nil {
			res.Error = &apiv1.ItemError{Code: r.Error.Code, Detail: r.Error.Detail}
			if len(r.Error.Findings) > 0 {
				res.Error.Findings = apiconv.Ptr(apiconv.Findings(r.Error.Findings))
			}
		} else {
			res.Status = apiconv.Ptr(apiv1.TranslationImportItemResultStatus(r.Status))
			res.Translation = apiconv.Ptr(toTranslation(*r.Translation))
		}
		out.Results[i] = res
	}
	return out, nil
}
