//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/ghoidc/ghoidctest"
)

// CI authenticating with GitHub Actions OIDC, end to end over HTTP
// against the real composition root (RFC 0004 §6.3): a fake issuer
// signs an ID token, the server verifies it against that issuer's
// published keys, matches its repository_id against a Git connection,
// and hands back a credential the CI endpoints accept — and nothing
// else does.

const ciRepositoryID = int64(9001)

// ciToken is the exchange's answer.
type ciToken struct {
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TenantID     string    `json:"tenant_id"`
	ProjectID    string    `json:"project_id"`
	Permissions  []string  `json:"permissions"`
	RepositoryID int64     `json:"repository_id"`
	Repository   string    `json:"repository"`
}

// ciWorld is a server with a GitHub App, a fake Actions issuer, a
// workspace with two projects, and the first project connected to a
// repository.
type ciWorld struct {
	*server
	issuer   *ghoidctest.Issuer
	tenant   string
	project  string
	project2 string
	ada      session
}

func startCIWorld(t *testing.T) *ciWorld {
	t.Helper()
	issuer := ghoidctest.New(t)
	s := startServerWith(t, map[string]string{
		// A GitHub App has to be configured for the exchange to exist:
		// without one there are no Git connections to match against.
		"GLOSSA_GITHUB_APP_ID":          "12345",
		"GLOSSA_GITHUB_APP_SLUG":        "glossa",
		"GLOSSA_GITHUB_APP_PRIVATE_KEY": testAppKey(t),
		"GLOSSA_GITHUB_WEBHOOK_SECRET":  "hook-secret",
		"GLOSSA_GITHUB_CLIENT_ID":       "Iv1.test",
		"GLOSSA_GITHUB_CLIENT_SECRET":   "client-secret",
		"GLOSSA_GITHUB_OIDC_ISSUER":     issuer.URL,
		"GLOSSA_GITHUB_OIDC_AUDIENCE":   "glossa",
	})
	w := &ciWorld{server: s, issuer: issuer}
	w.ada = s.signIn("ada@example.com")

	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: w.ada.cookie, csrf: w.ada.csrf,
		body: map[string]string{"slug": "brotwerk", "name": "Brotwerk"}}).decode(t, &org)
	w.tenant = org.ID
	base := "/v1/tenants/" + w.tenant
	var p1, p2 struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: w.ada.cookie, csrf: w.ada.csrf,
		body: map[string]any{"slug": "web", "name": "Web", "source_locale": "en"}}).decode(t, &p1)
	s.do(call{method: "POST", path: base + "/projects", cookie: w.ada.cookie, csrf: w.ada.csrf,
		body: map[string]any{"slug": "admin", "name": "Admin", "source_locale": "en"}}).decode(t, &p2)
	w.project, w.project2 = p1.ID, p2.ID
	w.connect(ciRepositoryID, w.project, "")
	return w
}

// connect installs the App and connects a repository, straight into the
// database. Doing it through the API would mean walking GitHub's own
// install and OAuth flow, which the Integration slice already tests;
// here the connection is the precondition, not the subject.
func (w *ciWorld) connect(repo int64, project, path string) {
	w.t.Helper()
	ctx := context.Background()
	var installation uuid.UUID
	err := w.db.Super.QueryRow(ctx, `
		INSERT INTO integration_github_installations
			(id, tenant_id, installation_id, account_id, account_login, account_type, state,
			 connected_by, connected_at, updated_at)
		VALUES (gen_random_uuid(), $1, 4242, 77, 'acme', 'Organization', 'active', 'person:test', now(), now())
		ON CONFLICT (installation_id) DO UPDATE SET updated_at = now()
		RETURNING id`, w.tenant).Scan(&installation)
	if err != nil {
		w.t.Fatalf("install the App: %v", err)
	}
	_, err = w.db.Super.Exec(ctx, `
		INSERT INTO integration_git_connections
			(id, tenant_id, installation_id, repository_id, repository_name, project_id, application_id,
			 default_branch, path, created_by, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, 'acme/shop', $4, gen_random_uuid(), 'main', $5, 'person:test', now(), now())`,
		w.tenant, installation, repo, project, path)
	if err != nil {
		w.t.Fatalf("connect the repository: %v", err)
	}
}

func (w *ciWorld) claims() ghoidctest.Claims {
	return ghoidctest.Claims{
		RepositoryID: ciRepositoryID, RepositoryOwnerID: 77, Repository: "acme/shop",
		Ref: "refs/pull/42/merge", SHA: "cafebabe1234", EventName: "pull_request",
		JobWorkflowRef: "acme/shop/.github/workflows/glossa.yml@refs/heads/main",
		RunID:          "77889900", RunnerEnvironment: "github-hosted",
	}
}

// exchange posts an ID token, like `glossa login` in a workflow.
func (w *ciWorld) exchange(body map[string]any) reply {
	w.t.Helper()
	return w.do(call{method: "POST", path: "/v1/auth/github-oidc-exchanges", body: body})
}

func TestCIAuthenticatesWithGitHubActionsOIDC(t *testing.T) {
	w := startCIWorld(t)

	// The exchange itself carries no Glossa credential: the ID token is
	// the credential.
	r := w.exchange(map[string]any{"id_token": w.issuer.Token(t, w.claims())})
	r.want(t, http.StatusCreated, "")
	var minted ciToken
	r.decode(t, &minted)

	if minted.TenantID != w.tenant || minted.ProjectID != w.project {
		t.Fatalf("minted for tenant %s project %s, want %s / %s", minted.TenantID, minted.ProjectID, w.tenant, w.project)
	}
	if minted.RepositoryID != ciRepositoryID || minted.Repository != "acme/shop" {
		t.Errorf("repository = %d %q", minted.RepositoryID, minted.Repository)
	}
	if len(minted.Permissions) != 2 ||
		minted.Permissions[0] != "catalog.read" || minted.Permissions[1] != "catalog.write" {
		t.Errorf("permissions = %v, want [catalog.read catalog.write]", minted.Permissions)
	}
	if d := time.Until(minted.ExpiresAt); d < 25*time.Minute || d > 31*time.Minute {
		t.Errorf("expires in %s, want about 30 minutes", d)
	}

	project := "/v1/tenants/" + w.tenant + "/projects/" + w.project

	// What CI does: push source messages, and upload where they appear.
	w.do(call{method: "POST", path: project + "/message-upserts", bearer: minted.Token,
		body: map[string]any{"items": []map[string]any{{"key": "cart.checkout", "content": "Check out"}}}}).
		want(t, http.StatusOK, "")
	w.do(call{method: "GET", path: project + "/messages", bearer: minted.Token}).want(t, http.StatusOK, "")

	// And nothing else. Each of these is a real endpoint an over-broad
	// CI credential would open.
	for _, out := range []struct {
		what   string
		call   call
		status int
	}{
		{"the workspace's members", call{method: "GET", path: "/v1/tenants/" + w.tenant + "/members"}, http.StatusForbidden},
		{"the workspace's API tokens", call{method: "GET", path: "/v1/tenants/" + w.tenant + "/tokens"}, http.StatusForbidden},
		{"the project's releases", call{method: "GET", path: project + "/releases"}, http.StatusForbidden},
		{"the termbase", call{method: "GET", path: "/v1/tenants/" + w.tenant + "/term-concepts"}, http.StatusForbidden},
		{"importing translations", call{method: "POST", path: project + "/translation-imports",
			body: map[string]any{"items": []map[string]any{{"key": "cart.checkout", "locale": "de", "text": "Zur Kasse"}}}},
			http.StatusForbidden},
	} {
		t.Run("it may not read "+out.what, func(t *testing.T) {
			c := out.call
			c.bearer = minted.Token
			got := w.do(c)
			if got.status != out.status {
				t.Fatalf("%s %s = %d %q, want %d", c.method, c.path, got.status, got.body, out.status)
			}
		})
	}

	// Not even the other project of its own workspace: the credential is
	// bound to the project its repository feeds.
	other := "/v1/tenants/" + w.tenant + "/projects/" + w.project2
	w.do(call{method: "GET", path: other + "/messages", bearer: minted.Token}).
		want(t, http.StatusForbidden, "grant_project_mismatch")

	// And it is not an in-context grant either: the overlay's endpoints
	// take their own credential, not this one.
	w.do(call{method: "POST", path: project + "/in-context-grants", bearer: minted.Token,
		body: map[string]string{"origin": "https://preview.example"}}).want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestTheExchangeRefusesOverHTTP(t *testing.T) {
	w := startCIWorld(t)
	now := time.Now()

	wrongAudience := w.claims()
	wrongAudience.Audience = "https://github.com/acme"
	wrongIssuer := w.claims()
	wrongIssuer.Issuer = "https://token.actions.githubusercontent.com"
	expired := w.claims()
	expired.IssuedAt, expired.Expiry = now.Add(-2*time.Hour), now.Add(-90*time.Minute)
	unknownRepo := w.claims()
	unknownRepo.RepositoryID = 4242

	for _, tc := range []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"the wrong audience", map[string]any{"id_token": w.issuer.Token(t, wrongAudience)},
			http.StatusUnauthorized, "invalid_id_token"},
		{"the wrong issuer", map[string]any{"id_token": w.issuer.Token(t, wrongIssuer)},
			http.StatusUnauthorized, "invalid_id_token"},
		{"an expired token", map[string]any{"id_token": w.issuer.Token(t, expired)},
			http.StatusUnauthorized, "invalid_id_token"},
		{"a token signed by the wrong key", map[string]any{"id_token": w.issuer.TokenFromAnotherKey(t, w.claims())},
			http.StatusUnauthorized, "invalid_id_token"},
		{"a repository nobody connected", map[string]any{"id_token": w.issuer.Token(t, unknownRepo)},
			http.StatusForbidden, "repository_not_connected"},
		{"a project the repository does not feed",
			map[string]any{"id_token": w.issuer.Token(t, w.claims()), "project_id": w.project2},
			http.StatusForbidden, "repository_not_connected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w.exchange(tc.body).want(t, tc.status, tc.code)
		})
	}
}

// A monorepo: one repository, two projects, one connection per path.
// The run has to say which project, and the refusal says which to pick.
func TestAmbiguousRepositoryOverHTTP(t *testing.T) {
	w := startCIWorld(t)
	w.connect(ciRepositoryID, w.project2, "apps/admin")

	r := w.exchange(map[string]any{"id_token": w.issuer.Token(t, w.claims())})
	r.want(t, http.StatusConflict, "ambiguous_project")
	var p struct {
		Errors []struct{ Pointer, Detail string } `json:"errors"`
	}
	r.decode(t, &p)
	if len(p.Errors) != 2 {
		t.Fatalf("the refusal names %d projects, want 2: %s", len(p.Errors), r.body)
	}
	for _, fe := range p.Errors {
		if fe.Pointer != "/project_id" {
			t.Errorf("pointer = %q", fe.Pointer)
		}
	}

	named := w.exchange(map[string]any{"id_token": w.issuer.Token(t, w.claims()), "project_id": w.project2})
	named.want(t, http.StatusCreated, "")
	var minted ciToken
	named.decode(t, &minted)
	if minted.ProjectID != w.project2 {
		t.Errorf("minted for %s, want %s", minted.ProjectID, w.project2)
	}
}

// A deployment with no GitHub App has no connections to match a
// repository against, and says so rather than blaming the token.
func TestTheExchangeIsOffWithoutAGitHubApp(t *testing.T) {
	issuer := ghoidctest.New(t)
	s := startServer(t)
	s.do(call{method: "POST", path: "/v1/auth/github-oidc-exchanges",
		body: map[string]any{"id_token": issuer.Token(t, ghoidctest.Claims{RepositoryID: ciRepositoryID})}}).
		want(t, http.StatusServiceUnavailable, "github_not_configured")
}

// testAppKey is a throwaway RSA key in the PEM form GitHub hands out.
func testAppKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}
