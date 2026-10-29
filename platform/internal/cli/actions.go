package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Authenticating inside GitHub Actions without a stored secret
// (RFC 0004 §6.3).
//
// A workflow with `permissions: id-token: write` gets two variables in
// its environment. They are the job's one-time means of asking GitHub
// for an OIDC ID token; the token GitHub signs says which repository,
// ref and workflow asked, and Glossa exchanges it for a credential of
// its own. Nothing is stored in the repository, nothing has to be
// rotated, and the credential dies half an hour later.
const (
	envActionsTokenURL = "ACTIONS_ID_TOKEN_REQUEST_URL"
	envActionsToken    = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// actionsAudience is the `aud` Glossa insists on. GitHub's own default
// audience is the repository owner's URL, which any other service could
// also be handed; asking for Glossa's name is what makes the token
// unusable anywhere else.
const actionsAudience = "glossa"

// idTokenTimeout bounds the call to the runner's local token service,
// which is on the same machine.
const idTokenTimeout = 30 * time.Second

// inGitHubActions reports whether this run can ask GitHub for an ID
// token. Both variables are needed: a workflow that forgot
// `permissions: id-token: write` has neither, which is the common
// mistake and gets its own message.
func (inv *invocation) inGitHubActions() bool {
	return inv.env.getenv(envActionsTokenURL) != "" && inv.env.getenv(envActionsToken) != ""
}

// requestActionsIDToken asks the runner's token service for an ID token
// with Glossa's audience.
//
// The request token is a per-job credential of GitHub's, not Glossa's:
// it is read from the environment, sent to GitHub and never stored,
// logged or passed on.
func (inv *invocation) requestActionsIDToken(ctx context.Context) (string, error) {
	raw := strings.TrimSpace(inv.env.getenv(envActionsTokenURL))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", &Error{Exit: ExitUsage, Code: "invalid_actions_url",
			What: "GitHub Actions gave an unusable ID-token URL", Where: envActionsTokenURL,
			Fix: "this is GitHub's own variable; re-run the job, or authenticate with GLOSSA_TOKEN"}
	}
	q := u.Query()
	q.Set("audience", actionsAudience)
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(ctx, idTokenTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(inv.env.getenv(envActionsToken)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", inv.userAgent())

	hc := inv.env.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: idTokenTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", &Error{Exit: ExitNetwork, Code: "id_token_unavailable",
			What: "can't get an ID token from GitHub Actions", Why: err.Error(),
			Fix: "retry the job; if it persists, authenticate with GLOSSA_TOKEN"}
	}
	defer func() { _ = resp.Body.Close() }()
	// 64 KiB is far more than a JWS needs and far less than a runaway
	// response could send.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", &Error{Exit: ExitNetwork, Code: "id_token_unavailable",
			What: "can't read GitHub's ID token", Why: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &Error{Exit: ExitNetwork, Code: "id_token_unavailable",
			What: "GitHub Actions refused to issue an ID token",
			Why:  http.StatusText(resp.StatusCode),
			Fix:  "add `permissions: { id-token: write }` to the job (or the workflow)"}
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil || strings.TrimSpace(out.Value) == "" {
		return "", &Error{Exit: ExitNetwork, Code: "id_token_unavailable",
			What: "GitHub Actions returned no ID token",
			Fix:  "add `permissions: { id-token: write }` to the job (or the workflow)"}
	}
	return strings.TrimSpace(out.Value), nil
}
