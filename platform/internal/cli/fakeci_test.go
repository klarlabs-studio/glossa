package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The GitHub Actions side of CI authentication (RFC 0004 §6.3), faked
// on both ends: the runner's ID-token service, and the server's
// exchange. The real verification has its own tests against a real
// issuer; what these fakes are for is the CLI's half — does it find
// the variables, ask for the right audience, and use what comes back?

// fakeCI is the server's exchange.
type fakeCI struct {
	// minted is the credential the exchange last handed out, which the
	// server then accepts as a bearer token.
	minted string
	// projects, when longer than one, makes the repository ambiguous.
	projects []string
	// refuse, when set, is the problem code the exchange answers with.
	refuse string
	// idToken is the last ID token presented, and project the last
	// project named.
	idToken, project string
}

const mintedCIToken = "glossa_ci_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

func (f *fakeServer) routeCI(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/github-oidc-exchanges", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDToken   string `json:"id_token"`
			ProjectID string `json:"project_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IDToken == "" {
			problemResp(w, 400, "invalid_request", "id_token is required")
			return
		}
		f.mu.Lock()
		f.ci.idToken, f.ci.project = body.IDToken, body.ProjectID
		refuse, projects := f.ci.refuse, f.ci.projects
		f.mu.Unlock()

		if refuse != "" {
			problemResp(w, statusFor(refuse), refuse, "refused")
			return
		}
		if len(projects) > 1 && body.ProjectID == "" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(409)
			errs := make([]map[string]string, len(projects))
			for i, p := range projects {
				errs[i] = map[string]string{"pointer": "/project_id", "detail": p}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "urn:glossa:problem:ambiguous_project", "code": "ambiguous_project",
				"title": "ambiguous_project", "status": 409,
				"detail": "this repository feeds several projects", "errors": errs,
			})
			return
		}
		f.mu.Lock()
		f.ci.minted = mintedCIToken
		f.mu.Unlock()
		repo := "acme/shop"
		writeJSONResp(w, 201, map[string]any{
			"token": mintedCIToken, "expires_at": time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339),
			"tenant_id": "ten_1", "project_id": "prj_1",
			"permissions":   []string{"catalog.read", "catalog.write"},
			"repository_id": 9001, "repository": repo,
		})
	})
}

func statusFor(code string) int {
	switch code {
	case "invalid_id_token":
		return 401
	case "repository_not_connected":
		return 403
	case "github_not_configured":
		return 503
	}
	return 400
}

// fakeRunner is the ID-token service GitHub puts on a job's localhost.
type fakeRunner struct {
	srv *httptest.Server
	// requests records each audience asked for, with the bearer the
	// runner required.
	audiences []string
	bearer    string
	// status, when not 200, is what the runner answers (403: the
	// workflow forgot `permissions: id-token: write`).
	status int
}

func newFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	r := &fakeRunner{status: 200}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.audiences = append(r.audiences, req.URL.Query().Get("audience"))
		r.bearer = req.Header.Get("Authorization")
		if r.status != 200 {
			w.WriteHeader(r.status)
			return
		}
		writeJSONResp(w, 200, map[string]string{"value": "id-token-for-" + req.URL.Query().Get("audience")})
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// URL is what GitHub puts in ACTIONS_ID_TOKEN_REQUEST_URL: the service
// with its own query already on it, which is why the CLI has to add
// `audience` rather than build a URL.
func (r *fakeRunner) URL() string { return r.srv.URL + "/token?api-version=2.0" }
