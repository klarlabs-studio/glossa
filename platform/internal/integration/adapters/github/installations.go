package github

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// maxInstallationPages bounds the ownership search (10,000
// installations visible to one person).
const maxInstallationPages = 100

// VerifyInstallationOwner implements app.GitHub. The user token is sent
// to GitHub only and dropped when the call returns.
func (c *Client) VerifyInstallationOwner(ctx context.Context, userToken string, installationID int64) (app.GitHubInstallation, error) {
	const op = "user.installations"
	if userToken == "" || installationID <= 0 {
		return app.GitHubInstallation{}, invalid(op, "a user token and an installation ID are required")
	}
	for page := 1; page <= maxInstallationPages; page++ {
		var out struct {
			TotalCount    int `json:"total_count"`
			Installations []struct {
				ID      int64 `json:"id"`
				Account struct {
					ID    int64  `json:"id"`
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"account"`
			} `json:"installations"`
		}
		err := c.do(ctx, request{
			op: op, userToken: userToken,
			method: http.MethodGet, path: "/user/installations",
			query: url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}},
			out:   &out, idempotent: true,
		})
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
				// A bad or expired user token proves nothing.
				return app.GitHubInstallation{}, errors.Join(app.ErrInstallationNotVisible, err)
			}
			return app.GitHubInstallation{}, err
		}
		for _, in := range out.Installations {
			if in.ID == installationID {
				return app.GitHubInstallation{
					ID: in.ID, AccountID: in.Account.ID, AccountLogin: in.Account.Login, AccountType: in.Account.Type,
				}, nil
			}
		}
		if len(out.Installations) < 100 || page*100 >= out.TotalCount {
			break
		}
	}
	c.log.WarnContext(ctx, "github installation not visible to the user", "installation_id", installationID)
	return app.GitHubInstallation{}, app.ErrInstallationNotVisible
}

// maxRepositoryPages bounds the repository listing (10,000 repositories
// on one installation).
const maxRepositoryPages = 100

// Repositories implements app.GitHub: what the installation can see,
// with an installation token. Only `metadata: read` is needed, so this
// reveals names and default branches, never code.
func (c *Client) Repositories(ctx context.Context, t app.GitHubTarget) ([]app.GitHubRepository, error) {
	const op = "installation.repositories"
	if t.InstallationID <= 0 {
		return nil, invalid(op, "an installation ID is required")
	}
	var out []app.GitHubRepository
	for page := 1; page <= maxRepositoryPages; page++ {
		var body struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				ID            int64  `json:"id"`
				Name          string `json:"name"`
				FullName      string `json:"full_name"`
				Private       bool   `json:"private"`
				DefaultBranch string `json:"default_branch"`
			} `json:"repositories"`
		}
		err := c.do(ctx, request{
			op: op, tenant: t.TenantID, installation: t.InstallationID,
			method: http.MethodGet, path: "/installation/repositories",
			query: url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}},
			out:   &body, idempotent: true,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range body.Repositories {
			out = append(out, app.GitHubRepository{
				ID: r.ID, Name: r.Name, FullName: r.FullName, Private: r.Private, DefaultBranch: r.DefaultBranch,
			})
		}
		if len(body.Repositories) < 100 || len(out) >= body.TotalCount {
			break
		}
	}
	return out, nil
}
