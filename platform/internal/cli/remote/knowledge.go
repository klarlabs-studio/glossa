package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Knowledge types the CLI reads (RFC 0003 §2), re-exported so commands
// don't import the generated package.
type (
	TMLookup            = apiclient.TMLookup
	TMLookupResult      = apiclient.TMLookupResult
	TMMatch             = apiclient.TMMatch
	TMUnit              = apiclient.TMUnit
	TMConcordanceMatch  = apiclient.TMConcordanceMatch
	TermConcept         = apiclient.TermConcept
	Term                = apiclient.Term
	TermInput           = apiclient.TermInput
	TermStatus          = apiclient.TermStatus
	PartOfSpeech        = apiclient.PartOfSpeech
	CreateTermConcept   = apiclient.CreateTermConcept
	ReplaceTermConcept  = apiclient.ReplaceTermConcept
	TerminologyRequest  = apiclient.TerminologyCheckRequest
	TerminologyCheck    = apiclient.TerminologyCheck
	TermFinding         = apiclient.TermFinding
	TermFindingsPage    = apiclient.ProjectTerminologyFindings
	TranslationFindings = apiclient.TranslationTerminologyFindings
	StyleGuide          = apiclient.StyleGuide
	StyleFields         = apiclient.StyleFields
	StyleRule           = apiclient.StyleRule
	StyleGuideSource    = apiclient.StyleGuideSource
	EffectiveStyleGuide = apiclient.EffectiveStyleGuide
	CreateStyleGuide    = apiclient.CreateStyleGuide
	ReplaceStyleGuide   = apiclient.ReplaceStyleGuide
)

func (c *Client) tenantPath(tenant, sub string, args ...any) string {
	return c.path("/v1/tenants/%s"+sub, append([]any{tenant}, args...)...)
}

// limited pages through a list operation until it has limit items (0:
// all of them).
func limited[T any](limit int, fetch func(size int, token *string) ([]T, *string, error)) ([]T, error) {
	var (
		all   []T
		token *string
	)
	for {
		size := pageSize
		if limit > 0 {
			size = min(pageSize, limit-len(all))
		}
		items, next, err := fetch(size, token)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == nil || *next == "" || (limit > 0 && len(all) >= limit) {
			if limit > 0 && len(all) > limit {
				all = all[:limit]
			}
			return all, nil
		}
		token = next
	}
}

// ── translation memory ──────────────────────────────────────────────

// LookupTM finds exact and fuzzy translation-memory matches, best first.
func (c *Client) LookupTM(ctx context.Context, tenant string, q TMLookup) (TMLookupResult, error) {
	r, err := c.api.LookupTranslationMemoryWithResponse(idempotent(ctx), tenant, q)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/tm-lookups")); err != nil {
		return TMLookupResult{}, err
	}
	return *r.JSON200, nil
}

// ConcordanceQuery is a phrase search over units.
type ConcordanceQuery struct {
	Text         string
	Side         string // source (default) or target
	SourceLocale string
	TargetLocale string
	Project      string
	AllProjects  bool
	Limit        int
}

// Concordance finds active units containing a phrase, closest first.
func (c *Client) Concordance(ctx context.Context, tenant string, q ConcordanceQuery) ([]TMConcordanceMatch, error) {
	params := &apiclient.SearchTranslationMemoryParams{Q: q.Text, SourceLocale: optional(q.SourceLocale),
		TargetLocale: optional(q.TargetLocale), Project: optional(q.Project)}
	if q.Side != "" {
		side := apiclient.SearchTranslationMemoryParamsSide(q.Side)
		params.Side = &side
	}
	if q.AllProjects {
		params.AllProjects = &q.AllProjects
	}
	if q.Limit > 0 {
		params.Limit = &q.Limit
	}
	r, err := c.api.SearchTranslationMemoryWithResponse(ctx, tenant, params)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/tm-concordance")); err != nil {
		return nil, err
	}
	return r.JSON200.Matches, nil
}

// UnitFilter narrows TMUnits.
type UnitFilter struct {
	SourceLocale string
	TargetLocale string
	Project      string
	// State is active (default), retired or all.
	State string
}

// TMUnits lists translation-memory units, at most limit (0: all).
func (c *Client) TMUnits(ctx context.Context, tenant string, f UnitFilter, limit int) ([]TMUnit, error) {
	params := apiclient.ListTranslationMemoryUnitsParams{SourceLocale: optional(f.SourceLocale),
		TargetLocale: optional(f.TargetLocale), Project: optional(f.Project)}
	if f.State != "" {
		st := apiclient.ListTranslationMemoryUnitsParamsState(f.State)
		params.State = &st
	}
	return limited(limit, func(size int, tok *string) ([]TMUnit, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListTranslationMemoryUnitsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/tm-units")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// TMUnit reads one unit.
func (c *Client) TMUnit(ctx context.Context, tenant, id string) (TMUnit, error) {
	r, err := c.api.GetTranslationMemoryUnitWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/tm-units/%s", id)); err != nil {
		return TMUnit{}, err
	}
	return *r.JSON200, nil
}

// RetireTMUnit takes a unit out of matching; retiring twice changes
// nothing, so the request is retried.
func (c *Client) RetireTMUnit(ctx context.Context, tenant, id string) error {
	r, err := c.api.RetireTranslationMemoryUnitWithResponse(idempotent(ctx), tenant, id)
	return check(r, err, http.MethodDelete, c.tenantPath(tenant, "/tm-units/%s", id))
}

// ── terminology ─────────────────────────────────────────────────────

// ConceptFilter narrows TermConcepts.
type ConceptFilter struct {
	Query   string
	Locale  string
	Project string
	Domain  string
}

// TermConcepts lists termbase concepts with their terms, at most limit
// (0: all).
func (c *Client) TermConcepts(ctx context.Context, tenant string, f ConceptFilter, limit int) ([]TermConcept, error) {
	params := apiclient.ListTermConceptsParams{Q: optional(f.Query), Locale: optional(f.Locale),
		Project: optional(f.Project), Domain: optional(f.Domain)}
	return limited(limit, func(size int, tok *string) ([]TermConcept, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListTermConceptsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/term-concepts")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// TermConcept reads a concept and its ETag (for ReplaceTermConcept).
func (c *Client) TermConcept(ctx context.Context, tenant, id string) (TermConcept, string, error) {
	r, err := c.api.GetTermConceptWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/term-concepts/%s", id)); err != nil {
		return TermConcept{}, "", err
	}
	return *r.JSON200, r.HTTPResponse.Header.Get("ETag"), nil
}

// CreateTermConcept adds a concept with its terms. The Idempotency-Key
// makes a retried request create it once.
func (c *Client) CreateTermConcept(ctx context.Context, tenant string, body CreateTermConcept, idempotencyKey string) (TermConcept, error) {
	r, err := c.api.CreateTermConceptWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateTermConceptParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/term-concepts")); err != nil {
		return TermConcept{}, err
	}
	return *r.JSON201, nil
}

// ReplaceTermConcept replaces a concept's content and terms if it is
// still at etag; the precondition makes a retry safe.
func (c *Client) ReplaceTermConcept(ctx context.Context, tenant, id, etag string, body ReplaceTermConcept) (TermConcept, error) {
	r, err := c.api.ReplaceTermConceptWithResponse(idempotent(ctx), tenant, id, &apiclient.ReplaceTermConceptParams{IfMatch: etag}, body)
	if err := check(r, err, http.MethodPut, c.tenantPath(tenant, "/term-concepts/%s", id)); err != nil {
		return TermConcept{}, err
	}
	return *r.JSON200, nil
}

// CheckTerminology runs terminology QA on one translation. It stores
// nothing, so it is retried like a read.
func (c *Client) CheckTerminology(ctx context.Context, tenant string, req TerminologyRequest) (TerminologyCheck, error) {
	r, err := c.api.CheckTerminologyWithResponse(idempotent(ctx), tenant, req)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/terminology-checks")); err != nil {
		return TerminologyCheck{}, err
	}
	return *r.JSON200, nil
}

// TermFindingsQuery selects the translations a project check covers.
type TermFindingsQuery struct {
	// Locales are 1 to 20 target locales.
	Locales []string
	// States are review states; empty means every state but rejected.
	States []string
}

// ProjectTermFindings runs the server's terminology QA over a project's
// translations, one page (up to 100 translations scanned) at a time;
// fn sees each page. It stores nothing.
func (c *Client) ProjectTermFindings(ctx context.Context, s Scope, q TermFindingsQuery, fn func(TermFindingsPage) error) error {
	params := apiclient.ListProjectTerminologyFindingsParams{Locale: q.Locales}
	if len(q.States) > 0 {
		states := make([]apiclient.ReviewState, len(q.States))
		for i, st := range q.States {
			states[i] = apiclient.ReviewState(st)
		}
		params.State = &states
	}
	size := pageSize
	params.PageSize = &size
	for {
		r, err := c.api.ListProjectTerminologyFindingsWithResponse(ctx, s.Tenant, s.Project, &params)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/terminology-findings", s.Tenant, s.Project)); err != nil {
			return err
		}
		if err := fn(*r.JSON200); err != nil {
			return err
		}
		next := r.JSON200.NextPageToken
		if next == nil || *next == "" {
			return nil
		}
		params.PageToken = next
	}
}

// ── style guides ────────────────────────────────────────────────────

// StyleScope is where a style guide applies: the tenant (no project),
// a project, and optionally a locale and a namespace.
type StyleScope struct {
	Project   string
	Locale    string
	Namespace string
}

// EffectiveStyle merges every guide that applies to the scope.
func (c *Client) EffectiveStyle(ctx context.Context, tenant string, s StyleScope) (EffectiveStyleGuide, error) {
	r, err := c.api.GetEffectiveStyleGuideWithResponse(ctx, tenant, &apiclient.GetEffectiveStyleGuideParams{
		Project: optional(s.Project), Locale: optional(s.Locale), Namespace: optional(s.Namespace)})
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/effective-style-guide")); err != nil {
		return EffectiveStyleGuide{}, err
	}
	return *r.JSON200, nil
}

// StyleGuideFor finds the guide whose scope is exactly s, with its
// ETag; found is false when the scope has none.
func (c *Client) StyleGuideFor(ctx context.Context, tenant string, s StyleScope) (g StyleGuide, etag string, found bool, err error) {
	params := apiclient.ListStyleGuidesParams{Project: optional(s.Project), Locale: optional(s.Locale)}
	if s.Project == "" {
		tenantOnly := true
		params.TenantOnly = &tenantOnly
	}
	guides, err := limited(0, func(size int, tok *string) ([]StyleGuide, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListStyleGuidesWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/style-guides")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
	if err != nil {
		return StyleGuide{}, "", false, err
	}
	for _, sg := range guides {
		if deref(sg.ProjectId) == s.Project && deref(sg.Locale) == s.Locale && deref(sg.Namespace) == s.Namespace {
			r, err := c.api.GetStyleGuideWithResponse(ctx, tenant, sg.Id)
			if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/style-guides/%s", sg.Id)); err != nil {
				return StyleGuide{}, "", false, err
			}
			return *r.JSON200, r.HTTPResponse.Header.Get("ETag"), true, nil
		}
	}
	return StyleGuide{}, "", false, nil
}

// CreateStyleGuide adds the guide for a scope.
func (c *Client) CreateStyleGuide(ctx context.Context, tenant string, body CreateStyleGuide, idempotencyKey string) (StyleGuide, error) {
	r, err := c.api.CreateStyleGuideWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateStyleGuideParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/style-guides")); err != nil {
		return StyleGuide{}, err
	}
	return *r.JSON201, nil
}

// ReplaceStyleGuide replaces a guide's content if it is still at etag.
func (c *Client) ReplaceStyleGuide(ctx context.Context, tenant, id, etag string, body ReplaceStyleGuide) (StyleGuide, error) {
	r, err := c.api.ReplaceStyleGuideWithResponse(idempotent(ctx), tenant, id, &apiclient.ReplaceStyleGuideParams{IfMatch: etag}, body)
	if err := check(r, err, http.MethodPut, c.tenantPath(tenant, "/style-guides/%s", id)); err != nil {
		return StyleGuide{}, err
	}
	return *r.JSON200, nil
}
