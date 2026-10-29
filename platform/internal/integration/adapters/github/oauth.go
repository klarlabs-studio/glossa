package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// maxOAuthResponse bounds GitHub's token answer, which is one small
// JSON object.
const maxOAuthResponse = 64 << 10

// InstallURL implements app.GitHub.
func (c *Client) InstallURL(state string) string { return c.cfg.InstallURL(state) }

// PullRequestURL implements app.GitHub.
func (c *Client) PullRequestURL(repository string, number int) string {
	return c.cfg.PullRequestURL(repository, number)
}

// ExchangeUserCode implements app.GitHub: it redeems the install flow's
// one-time OAuth code for a user access token.
//
// The exchange goes to the web host (<WebURL>/login/oauth/access_token),
// not the REST API, so it does not run through the installation
// breakers or the tenant bulkheads — there is no installation yet, and
// the code is single-use, so a retry could not help. Neither the code
// nor the token is ever logged.
func (c *Client) ExchangeUserCode(ctx context.Context, code string) (string, error) {
	const op = "oauth.access_token"
	if strings.TrimSpace(code) == "" {
		return "", invalid(op, "an authorization code is required")
	}
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" {
		return "", invalid(op, "the App's OAuth client credentials are not configured")
	}
	form := url.Values{
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code":          {code},
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	endpoint := strings.TrimRight(c.cfg.WebURL, "/") + "/login/oauth/access_token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", &APIError{Op: op, Kind: app.ErrGitHubRejected, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "glossa")
	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", &APIError{Op: op, Kind: app.ErrGitHubUnavailable, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthResponse))
	if err != nil {
		return "", &APIError{Op: op, Status: resp.StatusCode, Kind: app.ErrGitHubUnavailable, Err: err}
	}
	if resp.StatusCode/100 != 2 {
		c.log.WarnContext(ctx, "github oauth exchange failed", "op", op, "status", resp.StatusCode)
		return "", &APIError{Op: op, Status: resp.StatusCode, Kind: app.ErrGitHubUnavailable, Message: "the token exchange failed"}
	}
	// GitHub answers 200 with an `error` field for a bad code.
	var out struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", &APIError{Op: op, Status: resp.StatusCode, Kind: app.ErrGitHubUnavailable, Message: "malformed response", Err: err}
	}
	if out.Error != "" || out.AccessToken == "" {
		c.log.WarnContext(ctx, "github refused the authorization code", "op", op, "reason", safeAction(out.Error))
		return "", &APIError{Op: op, Status: resp.StatusCode, Kind: app.ErrOAuthCodeRejected, Message: safeAction(out.Error)}
	}
	return out.AccessToken, nil
}
