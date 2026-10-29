package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// ContextUsage is a current usage as the Context API serves it (RFC 0004
// §2.2), and Application a project's application, re-exported so
// commands don't import the generated package.
type (
	ContextUsage = apiclient.ContextUsage
	Application  = apiclient.Application
)

// CurrentUsages lists every usage in the current builds: the default
// branch's, or with branch that branch's view (its latest builds, the
// default branch's where it didn't rebuild).
func (c *Client) CurrentUsages(ctx context.Context, s Scope, branch string) ([]ContextUsage, error) {
	size := pageSize
	params := apiclient.ListUsagesParams{PageSize: &size}
	if branch != "" {
		params.Branch = &branch
	}
	return collect(func(tok *string) ([]ContextUsage, *string, error) {
		p := params
		p.PageToken = tok
		r, err := c.api.ListUsagesWithResponse(ctx, s.Tenant, s.Project, &p)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/usages", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// Applications lists a project's applications.
func (c *Client) Applications(ctx context.Context, s Scope) ([]Application, error) {
	size := pageSize
	return collect(func(tok *string) ([]Application, *string, error) {
		r, err := c.api.ListApplicationsWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListApplicationsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/applications", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}
