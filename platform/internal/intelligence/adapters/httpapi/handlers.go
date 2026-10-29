package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// ── providers ────────────────────────────────────────────────────────

func (a *API) ListAIProviders(ctx context.Context, req apiv1.ListAIProvidersRequestObject) (apiv1.ListAIProvidersResponseObject, error) {
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ps, next, err := a.svc.ListProviders(ctx, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListAIProviders200JSONResponse{Items: make([]apiv1.AIProvider, len(ps)), NextPageToken: next}
	for i, p := range ps {
		out.Items[i] = toProvider(p)
	}
	return out, nil
}

func (a *API) CreateAIProvider(ctx context.Context, req apiv1.CreateAIProviderRequestObject) (apiv1.CreateAIProviderResponseObject, error) {
	b := req.Body
	p, replayed, err := a.svc.CreateProvider(ctx, app.ProviderInput{
		Name: b.Name, Kind: string(b.Kind), BaseURL: deref(b.BaseUrl), Models: deref(b.Models), Enabled: b.Enabled, APIKey: deref(b.ApiKey),
	}, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateAIProvider201ResponseHeaders{ETag: apiconv.ETag(p.Version), Location: apiconv.Ptr(tenantPath(ctx, "/ai-providers/"+p.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateAIProvider201JSONResponse{Body: toProvider(p), Headers: h}, nil
}

func (a *API) GetAIProvider(ctx context.Context, req apiv1.GetAIProviderRequestObject) (apiv1.GetAIProviderResponseObject, error) {
	id, err := pathID(req.AiProvider)
	if err != nil {
		return nil, err
	}
	p, err := a.svc.GetProvider(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAIProvider200JSONResponse{Body: toProvider(p), Headers: apiv1.GetAIProvider200ResponseHeaders{ETag: apiconv.ETag(p.Version)}}, nil
}

func (a *API) UpdateAIProvider(ctx context.Context, req apiv1.UpdateAIProviderRequestObject) (apiv1.UpdateAIProviderResponseObject, error) {
	id, err := pathID(req.AiProvider)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p, err := a.svc.UpdateProvider(ctx, id, &ifMatch, app.ProviderPatch{
		Name: b.Name, BaseURL: b.BaseUrl, Models: b.Models, Enabled: b.Enabled, APIKey: b.ApiKey, ClearAPIKey: deref(b.ClearApiKey),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UpdateAIProvider200JSONResponse{Body: toProvider(p), Headers: apiv1.UpdateAIProvider200ResponseHeaders{ETag: apiconv.ETag(p.Version)}}, nil
}

func (a *API) DeleteAIProvider(ctx context.Context, req apiv1.DeleteAIProviderRequestObject) (apiv1.DeleteAIProviderResponseObject, error) {
	id, err := pathID(req.AiProvider)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteProvider(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteAIProvider204Response{}, nil
}

// ── settings, prices, budget ─────────────────────────────────────────

func (a *API) GetAISettings(ctx context.Context, _ apiv1.GetAISettingsRequestObject) (apiv1.GetAISettingsResponseObject, error) {
	s, err := a.svc.GetSettings(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAISettings200JSONResponse{Body: toSettings(s), Headers: apiv1.GetAISettings200ResponseHeaders{ETag: apiconv.ETag(s.Version)}}, nil
}

func (a *API) PutAISettings(ctx context.Context, req apiv1.PutAISettingsRequestObject) (apiv1.PutAISettingsResponseObject, error) {
	ifMatch, err := apiconv.OptionalSingletonIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := app.SettingsInput{ProviderConsent: b.ProviderConsent, MaxConcurrentJobs: b.MaxConcurrentJobs}
	if b.MonthlyBudgetMicroUsd != nil {
		in.MonthlyBudget = apiconv.Ptr(domain.MicroUSD(*b.MonthlyBudgetMicroUsd))
	}
	s, err := a.svc.PutSettings(ctx, in, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PutAISettings200JSONResponse{Body: toSettings(s), Headers: apiv1.PutAISettings200ResponseHeaders{ETag: apiconv.ETag(s.Version)}}, nil
}

func (a *API) GetAIPrices(ctx context.Context, _ apiv1.GetAIPricesRequestObject) (apiv1.GetAIPricesResponseObject, error) {
	p, err := a.svc.GetPrices(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAIPrices200JSONResponse{Body: toPrices(p), Headers: apiv1.GetAIPrices200ResponseHeaders{ETag: apiconv.ETag(p.Version)}}, nil
}

func (a *API) PutAIPrices(ctx context.Context, req apiv1.PutAIPricesRequestObject) (apiv1.PutAIPricesResponseObject, error) {
	ifMatch, err := apiconv.OptionalSingletonIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	overrides := fromPriceTable(req.Body.Overrides)
	if _, err := a.svc.PutSettings(ctx, app.SettingsInput{Prices: &overrides}, ifMatch); err != nil {
		return nil, mapError(err)
	}
	p, err := a.svc.GetPrices(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PutAIPrices200JSONResponse{Body: toPrices(p), Headers: apiv1.PutAIPrices200ResponseHeaders{ETag: apiconv.ETag(p.Version)}}, nil
}

func (a *API) GetAIBudget(ctx context.Context, _ apiv1.GetAIBudgetRequestObject) (apiv1.GetAIBudgetResponseObject, error) {
	b, err := a.svc.GetBudget(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.GetAIBudget200JSONResponse{
		MonthlyBudgetMicroUsd: int64(b.Settings.MonthlyBudget), SpentMicroUsd: int64(b.Spent), RemainingMicroUsd: int64(b.Remaining()),
		Calls: b.Calls, MonthStart: b.MonthStart, ByProvider: make([]apiv1.AIProviderSpend, len(b.ByProvider)),
	}
	for i, p := range b.ByProvider {
		out.ByProvider[i] = apiv1.AIProviderSpend{Provider: p.Provider, Model: p.Model, CostMicroUsd: int64(p.Cost), Calls: p.Calls,
			InputTokens: p.InputTokens, OutputTokens: p.OutputTokens}
	}
	return out, nil
}

func (a *API) ListAISpend(ctx context.Context, req apiv1.ListAISpendRequestObject) (apiv1.ListAISpendResponseObject, error) {
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	rows, next, err := a.svc.ListSpend(ctx, deref(req.Params.Since), pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListAISpend200JSONResponse{Items: make([]apiv1.AISpendEntry, len(rows)), NextPageToken: next}
	for i, e := range rows {
		out.Items[i] = apiv1.AISpendEntry{
			Id: e.ID.String(), JobId: idPtr(e.JobID), ProjectId: idPtr(e.ProjectID), Task: apiv1.AITask(e.Spend.Task),
			Provider: e.Spend.Provider, Model: e.Spend.Model, Usage: toUsage(e.Spend.Usage), CostMicroUsd: int64(e.Spend.Cost),
			Priced: e.Priced, OccurredAt: e.OccurredAt,
		}
	}
	return out, nil
}

// ── routing ──────────────────────────────────────────────────────────

// routingETag is the version of the scope's own policy: "0" while it has
// none and a broader policy (or the default) applies, so If-Match "0"
// creates it only while it is still absent.
func routingETag(v app.RoutingView, scope string) *string {
	if v.Source != scope {
		return apiconv.ETag(0)
	}
	return apiconv.ETag(v.Record.Version)
}

func (a *API) GetAIRoutingPolicy(ctx context.Context, _ apiv1.GetAIRoutingPolicyRequestObject) (apiv1.GetAIRoutingPolicyResponseObject, error) {
	v, err := a.svc.GetRoutingPolicy(ctx, nil)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAIRoutingPolicy200JSONResponse{Body: toRoutingView(v), Headers: apiv1.GetAIRoutingPolicy200ResponseHeaders{ETag: routingETag(v, "tenant")}}, nil
}

func (a *API) PutAIRoutingPolicy(ctx context.Context, req apiv1.PutAIRoutingPolicyRequestObject) (apiv1.PutAIRoutingPolicyResponseObject, error) {
	ifMatch, err := apiconv.OptionalSingletonIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.PutRoutingPolicy(ctx, nil, fromRoutingPolicy(*req.Body), ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PutAIRoutingPolicy200JSONResponse{Body: toRoutingView(v), Headers: apiv1.PutAIRoutingPolicy200ResponseHeaders{ETag: apiconv.ETag(v.Record.Version)}}, nil
}

func (a *API) GetProjectAIRoutingPolicy(ctx context.Context, req apiv1.GetProjectAIRoutingPolicyRequestObject) (apiv1.GetProjectAIRoutingPolicyResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.GetRoutingPolicy(ctx, &project)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetProjectAIRoutingPolicy200JSONResponse{Body: toRoutingView(v), Headers: apiv1.GetProjectAIRoutingPolicy200ResponseHeaders{ETag: routingETag(v, "project")}}, nil
}

func (a *API) PutProjectAIRoutingPolicy(ctx context.Context, req apiv1.PutProjectAIRoutingPolicyRequestObject) (apiv1.PutProjectAIRoutingPolicyResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalSingletonIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.PutRoutingPolicy(ctx, &project, fromRoutingPolicy(*req.Body), ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PutProjectAIRoutingPolicy200JSONResponse{Body: toRoutingView(v), Headers: apiv1.PutProjectAIRoutingPolicy200ResponseHeaders{ETag: apiconv.ETag(v.Record.Version)}}, nil
}

func (a *API) DeleteProjectAIRoutingPolicy(ctx context.Context, req apiv1.DeleteProjectAIRoutingPolicyRequestObject) (apiv1.DeleteProjectAIRoutingPolicyResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteRoutingPolicy(ctx, project); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteProjectAIRoutingPolicy204Response{}, nil
}

// ── project settings ─────────────────────────────────────────────────

func (a *API) GetProjectAISettings(ctx context.Context, req apiv1.GetProjectAISettingsRequestObject) (apiv1.GetProjectAISettingsResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	s, err := a.svc.GetProjectSettings(ctx, project)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetProjectAISettings200JSONResponse{Body: toProjectSettings(s), Headers: apiv1.GetProjectAISettings200ResponseHeaders{ETag: apiconv.ETag(s.Version)}}, nil
}

func (a *API) PutProjectAISettings(ctx context.Context, req apiv1.PutProjectAISettingsRequestObject) (apiv1.PutProjectAISettingsResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalSingletonIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	var in app.ProjectSettingsInput
	if b.NamespaceTags != nil {
		tags := domain.NamespaceTags{}
		for ns, ts := range *b.NamespaceTags {
			for _, t := range ts {
				tags[ns] = append(tags[ns], string(t))
			}
			if len(ts) == 0 {
				tags[ns] = nil
			}
		}
		in.NamespaceTags = &tags
	}
	in.AutoTranslateLocales = b.AutoTranslateLocales
	if b.Review != nil {
		r := fromReview(*b.Review)
		in.Review = &r
	}
	s, err := a.svc.PutProjectSettings(ctx, project, in, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PutProjectAISettings200JSONResponse{Body: toProjectSettings(s), Headers: apiv1.PutProjectAISettings200ResponseHeaders{ETag: apiconv.ETag(s.Version)}}, nil
}

// ── fills and jobs ───────────────────────────────────────────────────

func fillRequest(b *apiv1.CreateAIFill) app.FillRequest {
	return app.FillRequest{
		Locales: b.Locales,
		Filter: app.FillFilter{
			Namespace: deref(b.Namespace), KeyPrefix: deref(b.KeyPrefix), Keys: deref(b.Keys), IncludeOutdated: deref(b.IncludeOutdated),
			Select: app.FillSelect(deref(b.Select)), Force: deref(b.Force),
		},
	}
}

func (a *API) PreviewAIFill(ctx context.Context, req apiv1.PreviewAIFillRequestObject) (apiv1.PreviewAIFillResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	pv, err := a.svc.PreviewFill(ctx, project, fillRequest(req.Body))
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PreviewAIFill200JSONResponse(toFillPreview(pv)), nil
}

func (a *API) CreateAIFill(ctx context.Context, req apiv1.CreateAIFillRequestObject) (apiv1.CreateAIFillResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	res, replayed, err := a.svc.RequestFill(ctx, project, fillRequest(req.Body), deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateAIFill201ResponseHeaders{Location: apiconv.Ptr(tenantPath(ctx, "/ai-fills/"+res.Fill.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateAIFill201JSONResponse{Body: toFill(res), Headers: h}, nil
}

func (a *API) GetAIFill(ctx context.Context, req apiv1.GetAIFillRequestObject) (apiv1.GetAIFillResponseObject, error) {
	id, err := pathID(req.AiFill)
	if err != nil {
		return nil, err
	}
	res, err := a.svc.GetFill(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAIFill200JSONResponse(toFill(res)), nil
}

func (a *API) CancelAIFill(ctx context.Context, req apiv1.CancelAIFillRequestObject) (apiv1.CancelAIFillResponseObject, error) {
	id, err := pathID(req.AiFill)
	if err != nil {
		return nil, err
	}
	res, err := a.svc.CancelFill(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CancelAIFill200JSONResponse(toFill(res)), nil
}

func (a *API) ListAIJobs(ctx context.Context, req apiv1.ListAIJobsRequestObject) (apiv1.ListAIJobsResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.JobFilter{Locale: deref(p.Locale)}
	if p.State != nil {
		f.State = apiconv.Ptr(domain.JobState(*p.State))
	}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	if f.FillID, err = optionalID("fill", p.Fill); err != nil {
		return nil, err
	}
	if f.MessageID, err = optionalID("message", p.Message); err != nil {
		return nil, err
	}
	jobs, next, err := a.svc.ListJobs(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListAIJobs200JSONResponse{Items: make([]apiv1.AIJob, len(jobs)), NextPageToken: next}
	for i, j := range jobs {
		out.Items[i] = toJob(j, nil)
	}
	return out, nil
}

func (a *API) GetAIJob(ctx context.Context, req apiv1.GetAIJobRequestObject) (apiv1.GetAIJobResponseObject, error) {
	id, err := pathID(req.AiJob)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.GetJob(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAIJob200JSONResponse(toJob(v.Job, v.Audit)), nil
}

func (a *API) CancelAIJob(ctx context.Context, req apiv1.CancelAIJobRequestObject) (apiv1.CancelAIJobResponseObject, error) {
	id, err := pathID(req.AiJob)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.CancelJob(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CancelAIJob200JSONResponse(toJob(v.Job, v.Audit)), nil
}

// ── suggestions ──────────────────────────────────────────────────────

func (a *API) ListAISuggestions(ctx context.Context, req apiv1.ListAISuggestionsRequestObject) (apiv1.ListAISuggestionsResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.SuggestionFilter{Locale: deref(p.Locale)}
	if p.Status != nil {
		f.Status = apiconv.Ptr(domain.SuggestionStatus(*p.Status))
	}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	if f.MessageID, err = optionalID("message", p.Message); err != nil {
		return nil, err
	}
	if f.JobID, err = optionalID("job", p.Job); err != nil {
		return nil, err
	}
	rows, next, err := a.svc.ListSuggestions(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	sources, err := a.svc.SuggestionSources(ctx, rows)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ListAISuggestions200JSONResponse{Items: toSuggestions(rows, sources), NextPageToken: next}, nil
}

// ListUnitAISuggestions is the translation workspace's read of the
// suggestions for one unit, without what the job cost or drew on.
func (a *API) ListUnitAISuggestions(ctx context.Context, req apiv1.ListUnitAISuggestionsRequestObject) (apiv1.ListUnitAISuggestionsResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	loc, err := bcp47.Parse(req.Locale)
	if err != nil {
		return nil, mapError(app.ErrNotFound)
	}
	rows, err := a.svc.UnitSuggestions(ctx, project, req.Message, loc)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ListUnitAISuggestions200JSONResponse{Items: toUnitSuggestions(rows)}, nil
}

func (a *API) GetAISuggestion(ctx context.Context, req apiv1.GetAISuggestionRequestObject) (apiv1.GetAISuggestionResponseObject, error) {
	id, err := pathID(req.AiSuggestion)
	if err != nil {
		return nil, err
	}
	r, err := a.svc.GetSuggestion(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	sources, err := a.svc.SuggestionSources(ctx, []domain.SuggestionRecord{r})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAISuggestion200JSONResponse{Body: toSuggestion(r, sources), Headers: apiv1.GetAISuggestion200ResponseHeaders{ETag: apiconv.ETag(r.Version)}}, nil
}

func (a *API) AcceptAISuggestion(ctx context.Context, req apiv1.AcceptAISuggestionRequestObject) (apiv1.AcceptAISuggestionResponseObject, error) {
	id, err := pathID(req.AiSuggestion)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	in := app.AcceptInput{IfMatch: ifMatch}
	if b := req.Body; b != nil {
		in.Text, in.Syntax = deref(b.Text), string(deref(b.Syntax))
		in.InContext = fromInContext(b.InContext)
	}
	r, err := a.svc.AcceptSuggestion(ctx, id, in)
	if err != nil {
		return nil, mapError(err)
	}
	sources, err := a.svc.SuggestionSources(ctx, []domain.SuggestionRecord{r})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.AcceptAISuggestion200JSONResponse{Body: toSuggestion(r, sources), Headers: apiv1.AcceptAISuggestion200ResponseHeaders{ETag: apiconv.ETag(r.Version)}}, nil
}

// fromInContext reads where the person was when they accepted, for the
// revision's origin_detail (RFC 0004 5.3).
func fromInContext(in *apiv1.InContextProvenance) *domain.InContext {
	if in == nil {
		return nil
	}
	out := &domain.InContext{Route: in.Route}
	if v := in.Viewport; v != nil {
		out.Viewport = &domain.Viewport{Width: v.Width, Height: v.Height}
	}
	return out
}

func (a *API) RejectAISuggestion(ctx context.Context, req apiv1.RejectAISuggestionRequestObject) (apiv1.RejectAISuggestionResponseObject, error) {
	id, err := pathID(req.AiSuggestion)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	reason := ""
	if req.Body != nil {
		reason = deref(req.Body.Reason)
	}
	r, err := a.svc.RejectSuggestion(ctx, id, reason, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	sources, err := a.svc.SuggestionSources(ctx, []domain.SuggestionRecord{r})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.RejectAISuggestion200JSONResponse{Body: toSuggestion(r, sources), Headers: apiv1.RejectAISuggestion200ResponseHeaders{ETag: apiconv.ETag(r.Version)}}, nil
}

func (a *API) GetAIReviewQueue(ctx context.Context, req apiv1.GetAIReviewQueueRequestObject) (apiv1.GetAIReviewQueueResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	rows, next, err := a.svc.ReviewQueue(ctx, project, deref(req.Params.Locale), pg)
	if err != nil {
		return nil, mapError(err)
	}
	sources, err := a.svc.SuggestionSources(ctx, rows)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAIReviewQueue200JSONResponse{Items: toSuggestions(rows, sources), NextPageToken: next}, nil
}

// ── insights ─────────────────────────────────────────────────────────

func (a *API) ListAIDisclosures(ctx context.Context, req apiv1.ListAIDisclosuresRequestObject) (apiv1.ListAIDisclosuresResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.DisclosureFilter{Provider: deref(p.Provider)}
	if f.JobID, err = optionalID("job", p.Job); err != nil {
		return nil, err
	}
	if f.MessageID, err = optionalID("message", p.Message); err != nil {
		return nil, err
	}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	rows, next, err := a.svc.ListDisclosures(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListAIDisclosures200JSONResponse{Items: make([]apiv1.AIDisclosure, len(rows)), NextPageToken: next}
	for i, d := range rows {
		out.Items[i] = toDisclosure(d)
	}
	return out, nil
}

func (a *API) GetAIMetrics(ctx context.Context, req apiv1.GetAIMetricsRequestObject) (apiv1.GetAIMetricsResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	rows, since, err := a.svc.AcceptanceMetrics(ctx, project, deref(req.Params.Since))
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.GetAIMetrics200JSONResponse{Since: since, Locales: make([]apiv1.AILocaleMetrics, len(rows))}
	for i, r := range rows {
		out.Locales[i] = apiv1.AILocaleMetrics{
			Locale: r.Locale, Accepted: r.Accepted, Edited: r.Edited, Rejected: r.Rejected, AcceptanceRate: r.AcceptanceRate,
			MeanEditDistance: r.MeanEditDistance, MeanEditRatio: r.MeanEditRatio,
		}
	}
	return out, nil
}

func (a *API) GetAIEvalBaseline(ctx context.Context, _ apiv1.GetAIEvalBaselineRequestObject) (apiv1.GetAIEvalBaselineResponseObject, error) {
	if err := a.svc.CanRead(ctx); err != nil {
		return nil, err
	}
	out := apiv1.GetAIEvalBaseline200JSONResponse{Pairs: map[string]apiv1.AIEvalMetrics{}}
	for pair, e := range a.baseline {
		out.Pairs[pair] = apiv1.AIEvalMetrics{
			Cases: e.Cases, StructuralPassRate: e.StructuralPassRate, TerminologyCompliance: e.TerminologyCompliance,
			FormalityCompliance: e.FormalityCompliance, MeanEditRatio: e.MeanEditRatio, OriginAccuracy: e.OriginAccuracy,
		}
	}
	return out, nil
}
