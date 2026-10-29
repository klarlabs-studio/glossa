// Package sources adapts other bounded contexts' application services
// to the ports Identity declares, so Identity depends on its own
// interfaces and the composition root does the joining.
package sources

import (
	"context"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	integrationapp "go.klarlabs.de/glossa/platform/internal/integration/app"
)

// GitRepositories adapts Integration's Git connections to Identity's
// GitRepositories port, for the GitHub Actions OIDC exchange
// (RFC 0004 §6.3): a verified `repository_id` resolves to the tenants
// and projects that repository feeds.
type GitRepositories struct{ gh *integrationapp.GitHubService }

// NewGitRepositories adapts gh. It is only built when the deployment
// configures a GitHub App; without one there is nothing to exchange an
// ID token against, and Identity is left without the port.
func NewGitRepositories(gh *integrationapp.GitHubService) *GitRepositories {
	return &GitRepositories{gh: gh}
}

var _ identityapp.GitRepositories = (*GitRepositories)(nil)

// ConnectionsForRepository implements identityapp.GitRepositories.
func (r *GitRepositories) ConnectionsForRepository(
	ctx context.Context, repositoryID int64,
) ([]identityapp.RepositoryConnection, error) {
	conns, err := r.gh.ConnectionsForRepository(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	out := make([]identityapp.RepositoryConnection, len(conns))
	for i, c := range conns {
		out[i] = identityapp.RepositoryConnection{
			Tenant: c.Tenant, Project: c.Project, Application: c.Application, Path: c.Path,
		}
	}
	return out, nil
}
