package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// CIToken is what the GitHub Actions exchange returns: a bearer
// credential for one project, with its expiry and the repository it was
// minted for (RFC 0004 §6.3).
type CIToken = apiclient.CIToken

// ExchangeGitHubOIDC swaps a GitHub Actions ID token for a Glossa CI
// token.
//
// It is the one call that carries no Glossa credential, so the Client
// is built with an empty token: the ID token in the body *is* the
// credential. project names a Catalog project and is needed only when
// the repository feeds several.
func (c *Client) ExchangeGitHubOIDC(ctx context.Context, idToken, project string) (CIToken, error) {
	body := apiclient.ExchangeGitHubOIDCTokenJSONRequestBody{IdToken: idToken, ProjectId: optional(project)}
	r, err := c.api.ExchangeGitHubOIDCTokenWithResponse(ctx, body)
	if err := check(r, err, http.MethodPost, c.server+"/v1/auth/github-oidc-exchanges"); err != nil {
		return CIToken{}, err
	}
	return *r.JSON201, nil
}
