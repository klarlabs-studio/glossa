package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Intelligence types the CLI reads (RFC 0003 §3), re-exported so
// commands don't import the generated package.
type (
	AISettings          = apiclient.AISettings
	AIBudget            = apiclient.AIBudget
	AIProvider          = apiclient.AIProvider
	AIProjectSettings   = apiclient.AIProjectSettings
	AIFill              = apiclient.AIFill
	AIFillPreview       = apiclient.AIFillPreview
	AIFillPreviewLocale = apiclient.AIFillPreviewLocale
	AICostEstimate      = apiclient.AICostEstimate
	AIFillSelect        = apiclient.AIFillSelect
	CreateAIFill        = apiclient.CreateAIFill
	AIJob               = apiclient.AIJob
	AISuggestion        = apiclient.AISuggestion
	AIProviderSpend     = apiclient.AIProviderSpend
	AIReviewPolicy      = apiclient.AIReviewPolicy
)

// AISettings reads the tenant's AI settings: consent, budget cap and
// concurrency.
func (c *Client) AISettings(ctx context.Context, tenant string) (AISettings, error) {
	r, err := c.api.GetAISettingsWithResponse(ctx, tenant)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/ai-settings")); err != nil {
		return AISettings{}, err
	}
	return *r.JSON200, nil
}

// AIBudget reads this month's budget and spend.
func (c *Client) AIBudget(ctx context.Context, tenant string) (AIBudget, error) {
	r, err := c.api.GetAIBudgetWithResponse(ctx, tenant)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/ai-budget")); err != nil {
		return AIBudget{}, err
	}
	return *r.JSON200, nil
}

// AIProviders lists the configured providers (never their keys).
func (c *Client) AIProviders(ctx context.Context, tenant string) ([]AIProvider, error) {
	return limited(0, func(size int, tok *string) ([]AIProvider, *string, error) {
		r, err := c.api.ListAIProvidersWithResponse(ctx, tenant, &apiclient.ListAIProvidersParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/ai-providers")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// ProjectAISettings reads a project's namespace tags, auto-translate
// locales and review routing.
func (c *Client) ProjectAISettings(ctx context.Context, s Scope) (AIProjectSettings, error) {
	r, err := c.api.GetProjectAISettingsWithResponse(ctx, s.Tenant, s.Project)
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/ai-settings", s.Tenant, s.Project)); err != nil {
		return AIProjectSettings{}, err
	}
	return *r.JSON200, nil
}

// CreateAIFill queues AI translation jobs. The Idempotency-Key makes a
// retried request queue them once.
func (c *Client) CreateAIFill(ctx context.Context, s Scope, body CreateAIFill, idempotencyKey string) (AIFill, error) {
	r, err := c.api.CreateAIFillWithResponse(idempotent(ctx), s.Tenant, s.Project,
		&apiclient.CreateAIFillParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/ai-fills", s.Tenant, s.Project)); err != nil {
		return AIFill{}, err
	}
	return *r.JSON201, nil
}

// PreviewAIFill answers what a fill would do, per locale, without
// queueing anything. It writes nothing, so it is retried like a read.
func (c *Client) PreviewAIFill(ctx context.Context, s Scope, body CreateAIFill) (AIFillPreview, error) {
	r, err := c.api.PreviewAIFillWithResponse(idempotent(ctx), s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/ai-fill-previews", s.Tenant, s.Project)); err != nil {
		return AIFillPreview{}, err
	}
	return *r.JSON200, nil
}

// AIFill reads a fill and its jobs' states.
func (c *Client) AIFill(ctx context.Context, tenant, id string) (AIFill, error) {
	r, err := c.api.GetAIFillWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/ai-fills/%s", id)); err != nil {
		return AIFill{}, err
	}
	return *r.JSON200, nil
}

// JobFilter narrows AIJobs.
type JobFilter struct {
	Project string
	Fill    string
	State   string
	Locale  string
}

// AIJobs lists jobs, newest first.
func (c *Client) AIJobs(ctx context.Context, tenant string, f JobFilter) ([]AIJob, error) {
	params := apiclient.ListAIJobsParams{Project: optional(f.Project), Fill: optional(f.Fill), Locale: optional(f.Locale)}
	if f.State != "" {
		st := apiclient.AIJobState(f.State)
		params.State = &st
	}
	return limited(0, func(size int, tok *string) ([]AIJob, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListAIJobsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/ai-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// ReviewQueue lists pending suggestions, riskiest first, at most limit
// (0: all).
func (c *Client) ReviewQueue(ctx context.Context, s Scope, locales []string, limit int) ([]AISuggestion, error) {
	params := apiclient.GetAIReviewQueueParams{}
	if len(locales) > 0 {
		params.Locale = &locales
	}
	return limited(limit, func(size int, tok *string) ([]AISuggestion, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.GetAIReviewQueueWithResponse(ctx, s.Tenant, s.Project, &p)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/ai-review-queue", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// AISuggestion reads a suggestion and its ETag.
func (c *Client) AISuggestion(ctx context.Context, tenant, id string) (AISuggestion, string, error) {
	r, err := c.api.GetAISuggestionWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/ai-suggestions/%s", id)); err != nil {
		return AISuggestion{}, "", err
	}
	return *r.JSON200, r.HTTPResponse.Header.Get("ETag"), nil
}

// AcceptAISuggestion writes the suggestion (or text, in syntax, as an
// edit) as the message's translation. With etag the request is
// conditional, which makes a retry safe.
func (c *Client) AcceptAISuggestion(ctx context.Context, tenant, id, etag, text, syntax string) (AISuggestion, error) {
	body := apiclient.AcceptAISuggestion{Text: optional(text)}
	if text != "" && syntax != "" {
		st := apiclient.Syntax(syntax)
		body.Syntax = &st
	}
	if etag != "" {
		ctx = idempotent(ctx)
	}
	r, err := c.api.AcceptAISuggestionWithResponse(ctx, tenant, id, &apiclient.AcceptAISuggestionParams{IfMatch: optional(etag)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/ai-suggestions/%s/acceptance", id)); err != nil {
		return AISuggestion{}, err
	}
	return *r.JSON200, nil
}

// RejectAISuggestion rejects a suggestion; nothing is written.
func (c *Client) RejectAISuggestion(ctx context.Context, tenant, id, etag, reason string) (AISuggestion, error) {
	if etag != "" {
		ctx = idempotent(ctx)
	}
	r, err := c.api.RejectAISuggestionWithResponse(ctx, tenant, id, &apiclient.RejectAISuggestionParams{IfMatch: optional(etag)},
		apiclient.RejectAISuggestion{Reason: optional(reason)})
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/ai-suggestions/%s/rejection", id)); err != nil {
		return AISuggestion{}, err
	}
	return *r.JSON200, nil
}
