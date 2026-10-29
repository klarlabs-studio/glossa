package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Types the CLI reads, re-exported so commands don't import the
// generated package.
type (
	Tenant                = apiclient.Tenant
	Project               = apiclient.Project
	ProjectLocale         = apiclient.ProjectLocale
	Message               = apiclient.Message
	Translation           = apiclient.Translation
	ProjectTranslation    = apiclient.ProjectTranslation
	TranslationStats      = apiclient.TranslationStats
	QAFinding             = apiclient.QAFinding
	MessageUpsertItem     = apiclient.MessageUpsertItem
	MessageUpsertResult   = apiclient.MessageUpsertItemResult
	TranslationImportItem = apiclient.TranslationImportItem
	TranslationImportRes  = apiclient.TranslationImportItemResult
	ReviewState           = apiclient.ReviewState
	Syntax                = apiclient.Syntax
)

// Scope is the tenant and project a request acts in.
type Scope struct {
	Tenant  string
	Project string
}

func (c *Client) path(format string, args ...any) string {
	esc := make([]any, len(args))
	for i, a := range args {
		esc[i] = url.PathEscape(fmt.Sprint(a))
	}
	return c.server + fmt.Sprintf(format, esc...)
}

// collect pages through a list operation.
func collect[T any](fetch func(token *string) ([]T, *string, error)) ([]T, error) {
	var (
		all   []T
		token *string
	)
	for {
		items, next, err := fetch(token)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == nil || *next == "" {
			return all, nil
		}
		token = next
	}
}

// Tenants lists the tenants the caller can act in; for an API token, its
// own tenant.
func (c *Client) Tenants(ctx context.Context) ([]Tenant, error) {
	size := pageSize
	return collect(func(tok *string) ([]Tenant, *string, error) {
		r, err := c.api.ListTenantsWithResponse(ctx, &apiclient.ListTenantsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// Projects lists a tenant's projects.
func (c *Client) Projects(ctx context.Context, tenant string) ([]Project, error) {
	size := pageSize
	return collect(func(tok *string) ([]Project, *string, error) {
		r, err := c.api.ListProjectsWithResponse(ctx, tenant, &apiclient.ListProjectsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects", tenant)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// CreateProject creates a project. The Idempotency-Key makes a retried
// request return the project the first one created.
func (c *Client) CreateProject(ctx context.Context, tenant, slug, name, sourceLocale, idempotencyKey string) (Project, error) {
	r, err := c.api.CreateProjectWithResponse(idempotent(ctx), tenant, &apiclient.CreateProjectParams{IdempotencyKey: optional(idempotencyKey)},
		apiclient.CreateProject{Slug: slug, Name: name, SourceLocale: sourceLocale})
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects", tenant)); err != nil {
		return Project{}, err
	}
	return *r.JSON201, nil
}

// ErrProjectNotFound means no project has the slug or ID.
type ErrProjectNotFound struct{ Ref, Tenant string }

func (e *ErrProjectNotFound) Error() string {
	return fmt.Sprintf("no project %q in tenant %s", e.Ref, e.Tenant)
}

// ResolveProject finds a project by slug or ID.
func (c *Client) ResolveProject(ctx context.Context, tenant, ref string) (Project, error) {
	projects, err := c.Projects(ctx, tenant)
	if err != nil {
		return Project{}, err
	}
	for _, p := range projects {
		if p.Id == ref || string(p.Slug) == ref {
			return p, nil
		}
	}
	return Project{}, &ErrProjectNotFound{Ref: ref, Tenant: tenant}
}

// Locales lists a project's locales, the source locale included.
func (c *Client) Locales(ctx context.Context, s Scope) ([]ProjectLocale, error) {
	size := pageSize
	return collect(func(tok *string) ([]ProjectLocale, *string, error) {
		r, err := c.api.ListLocalesWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListLocalesParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/locales", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// AddLocale adds a locale (idempotently); created is false when the
// project already had it.
func (c *Client) AddLocale(ctx context.Context, s Scope, code string) (ProjectLocale, bool, error) {
	r, err := c.api.AddLocaleWithResponse(ctx, s.Tenant, s.Project, apiclient.AddLocale{Code: code})
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/locales", s.Tenant, s.Project)); err != nil {
		return ProjectLocale{}, false, err
	}
	if r.JSON201 != nil {
		return *r.JSON201, true, nil
	}
	return *r.JSON200, false, nil
}

// FallbackGraph returns the project's fallback graph.
func (c *Client) FallbackGraph(ctx context.Context, s Scope) (map[string][]string, error) {
	r, err := c.api.GetFallbackGraphWithResponse(ctx, s.Tenant, s.Project)
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/fallback-graph", s.Tenant, s.Project)); err != nil {
		return nil, err
	}
	return r.JSON200.Fallback, nil
}

// MessageFilter narrows Messages.
type MessageFilter struct {
	State      string
	KeyPrefix  string
	Namespace  string
	MissingIn  string
	OutdatedIn string
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// NamespaceSummary is a namespace with its message counts.
type NamespaceSummary = apiclient.NamespaceSummary

// Namespaces lists a project's namespaces by name, with how many of
// their messages are active and obsolete.
func (c *Client) Namespaces(ctx context.Context, s Scope) ([]NamespaceSummary, error) {
	size := pageSize
	return collect(func(tok *string) ([]NamespaceSummary, *string, error) {
		r, err := c.api.ListNamespacesWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListNamespacesParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/namespaces", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// Messages lists a project's messages by key.
func (c *Client) Messages(ctx context.Context, s Scope, f MessageFilter) ([]Message, error) {
	size := pageSize
	params := apiclient.ListMessagesParams{
		PageSize: &size, KeyPrefix: optional(f.KeyPrefix), Namespace: optional(f.Namespace),
		MissingIn: optional(f.MissingIn), OutdatedIn: optional(f.OutdatedIn),
	}
	if f.State != "" {
		st := apiclient.MessageState(f.State)
		params.State = &st
	}
	return collect(func(tok *string) ([]Message, *string, error) {
		p := params
		p.PageToken = tok
		r, err := c.api.ListMessagesWithResponse(ctx, s.Tenant, s.Project, &p)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/messages", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// MaxLocalesPerList is the most locales one translations listing takes.
const MaxLocalesPerList = 20

// TranslationFilter narrows ProjectTranslations.
type TranslationFilter struct {
	// MessageState is "active" or "obsolete"; empty lists both.
	MessageState string
	// States are review states; empty lists every state.
	States []string
}

// ProjectTranslations lists the translations of the given locales
// across the project's messages: one request per page and per 20
// locales, ordered by key and locale within each group of locales. It
// replaces reading one message at a time.
func (c *Client) ProjectTranslations(ctx context.Context, s Scope, locales []string, f TranslationFilter) ([]ProjectTranslation, error) {
	var out []ProjectTranslation
	for start := 0; start < len(locales); start += MaxLocalesPerList {
		chunk := locales[start:min(start+MaxLocalesPerList, len(locales))]
		items, err := c.projectTranslations(ctx, s, chunk, f)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// FirstProjectTranslations reads only the first page of size of the
// listing for each chunk of MaxLocalesPerList locales, and reports
// whether any chunk had another page. It is the bounded read: a caller
// that must not page through everything a project has ever obsoleted
// asks for one page and is told whether it saw all of it.
func (c *Client) FirstProjectTranslations(ctx context.Context, s Scope, locales []string, f TranslationFilter, size int) ([]ProjectTranslation, bool, error) {
	var (
		out  []ProjectTranslation
		more bool
	)
	for start := 0; start < len(locales); start += MaxLocalesPerList {
		chunk := locales[start:min(start+MaxLocalesPerList, len(locales))]
		params := translationParams(chunk, f, size)
		r, err := c.api.ListProjectTranslationsWithResponse(ctx, s.Tenant, s.Project, &params)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/translations", s.Tenant, s.Project)); err != nil {
			return nil, false, err
		}
		out = append(out, r.JSON200.Items...)
		if next := r.JSON200.NextPageToken; next != nil && *next != "" {
			more = true
		}
	}
	return out, more, nil
}

func translationParams(locales []string, f TranslationFilter, size int) apiclient.ListProjectTranslationsParams {
	params := apiclient.ListProjectTranslationsParams{PageSize: &size, Locale: locales}
	if f.MessageState != "" {
		st := apiclient.MessageState(f.MessageState)
		params.MessageState = &st
	}
	if len(f.States) > 0 {
		states := make([]apiclient.ReviewState, len(f.States))
		for i, st := range f.States {
			states[i] = apiclient.ReviewState(st)
		}
		params.State = &states
	}
	return params
}

func (c *Client) projectTranslations(ctx context.Context, s Scope, locales []string, f TranslationFilter) ([]ProjectTranslation, error) {
	params := translationParams(locales, f, pageSize)
	return collect(func(tok *string) ([]ProjectTranslation, *string, error) {
		p := params
		p.PageToken = tok
		r, err := c.api.ListProjectTranslationsWithResponse(ctx, s.Tenant, s.Project, &p)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/translations", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// TranslationStats summarizes every locale of the project in one
// request: translated, missing, outdated and review states.
func (c *Client) TranslationStats(ctx context.Context, s Scope) (TranslationStats, error) {
	r, err := c.api.GetTranslationStatsWithResponse(ctx, s.Tenant, s.Project)
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/translation-stats", s.Tenant, s.Project)); err != nil {
		return TranslationStats{}, err
	}
	return *r.JSON200, nil
}

// UpsertMessages creates or revises messages in batches of MaxBatch.
// Results come back in item order.
func (c *Client) UpsertMessages(ctx context.Context, s Scope, items []MessageUpsertItem) ([]MessageUpsertResult, error) {
	var out []MessageUpsertResult
	for size, start := batchSize(), 0; start < len(items); start += size {
		batch := items[start:min(start+size, len(items))]
		r, err := c.api.UpsertMessagesWithResponse(idempotent(ctx), s.Tenant, s.Project, apiclient.MessageUpsert{Items: batch})
		if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/message-upserts", s.Tenant, s.Project)); err != nil {
			return out, err
		}
		out = append(out, r.JSON200.Results...)
	}
	return out, nil
}

// ImportTranslations writes translations with provenance import, in
// batches of MaxBatch. Results come back in item order.
func (c *Client) ImportTranslations(ctx context.Context, s Scope, items []TranslationImportItem) ([]TranslationImportRes, error) {
	var out []TranslationImportRes
	for size, start := batchSize(), 0; start < len(items); start += size {
		batch := items[start:min(start+size, len(items))]
		r, err := c.api.ImportTranslationsWithResponse(idempotent(ctx), s.Tenant, s.Project, apiclient.TranslationImport{Items: batch})
		if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/translation-imports", s.Tenant, s.Project)); err != nil {
			return out, err
		}
		out = append(out, r.JSON200.Results...)
	}
	return out, nil
}

// SortLocales puts the source locale first, then the rest by code.
func SortLocales(ls []ProjectLocale) {
	sort.SliceStable(ls, func(i, j int) bool {
		if ls[i].IsSource != ls[j].IsSource {
			return ls[i].IsSource
		}
		return ls[i].Code < ls[j].Code
	})
}
