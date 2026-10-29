package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// GitHub types as the Integration API serves them (RFC 0004 §6.1),
// re-exported so commands don't import the generated package. A Git
// connection ties one installation's repository — by GitHub's numeric
// id, so a rename or a transfer doesn't break it — to a project and one
// of its applications.
type (
	GitConnection        = apiclient.GitConnection
	GitConnectionRequest = apiclient.GitConnectionRequest
	GitHubInstallation   = apiclient.GitHubInstallation
	GitHubRepository     = apiclient.GitHubRepository
)

// ConnectionFilter narrows GitConnections; an empty field doesn't.
type ConnectionFilter struct {
	// Project is a project ID, Installation Glossa's installation ID
	// (not GitHub's number).
	Project, Installation string
}

// GitConnections lists the workspace's Git connections, every page of
// them: connections belong to the tenant, not to one project, so the
// filter is what narrows them.
func (c *Client) GitConnections(ctx context.Context, tenant string, f ConnectionFilter) ([]GitConnection, error) {
	size := pageSize
	params := apiclient.ListGitConnectionsParams{PageSize: &size,
		Project: optional(f.Project), Installation: optional(f.Installation)}
	return collect(func(tok *string) ([]GitConnection, *string, error) {
		p := params
		p.PageToken = tok
		r, err := c.api.ListGitConnectionsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/github/connections")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// CreateGitConnection connects a repository to a project and
// application. The Idempotency-Key makes a retried request connect it
// once.
func (c *Client) CreateGitConnection(ctx context.Context, tenant string, body GitConnectionRequest, idempotencyKey string) (GitConnection, error) {
	r, err := c.api.CreateGitConnectionWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateGitConnectionParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/github/connections")); err != nil {
		return GitConnection{}, err
	}
	return *r.JSON201, nil
}

// DeleteGitConnection removes a connection. It only unlinks: the App
// stays installed on GitHub, which only GitHub can undo.
func (c *Client) DeleteGitConnection(ctx context.Context, tenant, id string) error {
	r, err := c.api.DeleteGitConnectionWithResponse(ctx, tenant, id)
	return check(r, err, http.MethodDelete, c.tenantPath(tenant, "/github/connections/%s", id))
}

// GitHubInstallations lists the workspace's installations with the
// repositories the App can see through each. An installation GitHub
// couldn't be reached for still lists, with RepositoriesUnavailable set
// and no repositories, so one unreachable account doesn't empty the
// page.
func (c *Client) GitHubInstallations(ctx context.Context, tenant string) ([]GitHubInstallation, error) {
	size := pageSize
	return collect(func(tok *string) ([]GitHubInstallation, *string, error) {
		r, err := c.api.ListGitHubInstallationsWithResponse(ctx, tenant,
			&apiclient.ListGitHubInstallationsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/github/installations")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// Repositories are the repositories an installation sees; none when
// GitHub couldn't be reached for it.
func Repositories(i GitHubInstallation) []GitHubRepository {
	if i.Repositories == nil {
		return nil
	}
	return *i.Repositories
}
