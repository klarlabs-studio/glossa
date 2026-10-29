//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
)

// testAuthSecret is base64 of 42 bytes.
const testAuthSecret = "YS10ZXN0LWF1dGgtc2VjcmV0LXRoYXQtaXMtbG9uZy1lbm91Z2gtMTIz"

// server boots the real composition root (migrations, generated router,
// Guard, Identity) with the log mailer, so the test reads sign-in links
// out of the log like a developer would.
type server struct {
	t    *testing.T
	base string
	logs *syncBuffer
	// db is the server's Postgres; stop shuts the server down (once).
	db   *dbtest.Env
	stop func()
}

func startServer(t *testing.T) *server { return startServerWith(t, nil) }

// startServerWith boots the server with extra environment variables
// (object storage, signing keys) on top of the defaults.
func startServerWith(t *testing.T, extra map[string]string) *server {
	t.Helper()
	env, err := dbtest.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	addr := freeAddr(t)
	logs := &syncBuffer{}
	vars := map[string]string{
		"DATABASE_URL":           env.AppDSN,
		"MIGRATION_DATABASE_URL": env.OwnerDSN,
		"GLOSSA_MIGRATE":         "up",
		"GLOSSA_HTTP_ADDR":       addr,
		"GLOSSA_AUTH_SECRET":     testAuthSecret,
		"GLOSSA_MAIL_DRIVER":     "log",
		"GLOSSA_STUDIO_URL":      "https://studio.test",
		"GLOSSA_STORAGE_DIR":     t.TempDir(),
		// Deliver domain events promptly (Localization follows Catalog).
		"GLOSSA_OUTBOX_POLL_INTERVAL": "50ms",
	}
	for k, v := range extra {
		vars[k] = v
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, lookupFrom(vars), logs) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("server did not stop")
			}
		})
	}
	t.Cleanup(stop)
	waitReady(t, "http://"+addr+"/readyz", done)
	return &server{t: t, base: "http://" + addr, logs: logs, db: env, stop: stop}
}

// call is one request; cookie, csrf and bearer are optional. raw is a
// body sent as it is, with contentType, instead of body as JSON.
type call struct {
	method, path string
	body         any
	raw          []byte
	contentType  string
	cookie       string
	csrf         string
	bearer       string
	headers      map[string]string
}

type reply struct {
	status  int
	header  http.Header
	body    []byte
	problem struct {
		Type, Code string
		Status     int
	}
}

func (s *server) do(c call) reply {
	s.t.Helper()
	var body io.Reader
	if c.body != nil {
		b, err := json.Marshal(c.body)
		if err != nil {
			s.t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	if c.raw != nil {
		body = bytes.NewReader(c.raw)
	}
	req, err := http.NewRequest(c.method, s.base+c.path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.raw != nil {
		req.Header.Set("Content-Type", c.contentType)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", "__Host-glossa_session="+c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	r := reply{status: resp.StatusCode, header: resp.Header}
	r.body, _ = io.ReadAll(resp.Body)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/problem+json") {
		_ = json.Unmarshal(r.body, &r.problem)
	}
	return r
}

func (r reply) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r reply) want(t *testing.T, status int, code string) {
	t.Helper()
	if r.status != status || r.problem.Code != code {
		t.Fatalf("got %d %q (%s), want %d %q", r.status, r.problem.Code, r.body, status, code)
	}
	if code != "" && r.problem.Type != "urn:glossa:problem:"+code {
		t.Errorf("problem type = %q", r.problem.Type)
	}
}

var mailedToken = regexp.MustCompile(`/auth/sign-in#token=([A-Za-z0-9_-]{43})`)

func (s *server) lastSignInToken() string {
	s.t.Helper()
	m := mailedToken.FindAllStringSubmatch(s.logs.String(), -1)
	if len(m) == 0 {
		s.t.Fatalf("no sign-in link in the log:\n%s", s.logs)
	}
	return m[len(m)-1][1]
}

type session struct {
	cookie, csrf string
}

// signIn runs the magic-link flow: request, read the mail, redeem.
func (s *server) signIn(email string) session {
	s.t.Helper()
	s.do(call{method: "POST", path: "/v1/auth/magic-links", body: map[string]string{"email": email}}).
		want(s.t, http.StatusAccepted, "")
	r := s.do(call{method: "POST", path: "/v1/auth/magic-link-redemptions",
		body: map[string]string{"token": s.lastSignInToken()}})
	r.want(s.t, http.StatusOK, "")
	setCookie := r.header.Get("Set-Cookie")
	for _, attr := range []string{"__Host-glossa_session=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(setCookie, attr) {
			s.t.Errorf("Set-Cookie %q lacks %q", setCookie, attr)
		}
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
		Person    struct {
			Email string `json:"email"`
		}
	}
	r.decode(s.t, &body)
	value, _, _ := strings.Cut(strings.TrimPrefix(setCookie, "__Host-glossa_session="), ";")
	return session{cookie: value, csrf: body.CSRFToken}
}

// TestMagicLinkToMeOverHTTP is the first full flow through the generated
// server: magic link request → redeem → session cookie → GET /v1/me.
func TestMagicLinkToMeOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")

	r := s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	var me struct {
		Person struct {
			Email              string `json:"email"`
			EmailVerified      bool   `json:"email_verified"`
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
		CSRFToken   string `json:"csrf_token"`
		Memberships []struct {
			Tenant struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"tenant"`
			Roles []string `json:"roles"`
		} `json:"memberships"`
	}
	r.decode(t, &me)
	if me.Person.Email != "ada@example.com" || !me.Person.EmailVerified || me.CSRFToken != ada.csrf {
		t.Errorf("me = %+v", me)
	}
	if len(me.Memberships) != 1 || me.Memberships[0].Tenant.ID != me.Person.IndividualTenantID ||
		me.Memberships[0].Tenant.Kind != "individual" || me.Memberships[0].Roles[0] != "owner" {
		t.Errorf("memberships = %+v", me.Memberships)
	}

	s.do(call{method: "GET", path: "/v1/me"}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "GET", path: "/v1/me", cookie: "forged"}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "POST", path: "/v1/auth/magic-link-redemptions",
		body: map[string]string{"token": s.lastSignInToken()}}).want(t, http.StatusUnauthorized, "link_invalid")
}

func TestTenantsMembersAndTokensOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")

	// Unsafe cookie requests need the CSRF token.
	create := call{method: "POST", path: "/v1/tenants", cookie: ada.cookie,
		body: map[string]string{"slug": "acme", "name": "Acme"}, headers: map[string]string{"Idempotency-Key": "k1"}}
	s.do(create).want(t, http.StatusForbidden, "csrf_invalid")
	create.csrf = "not-the-token"
	s.do(create).want(t, http.StatusForbidden, "csrf_invalid")
	create.csrf = ada.csrf
	r := s.do(create)
	r.want(t, http.StatusCreated, "")
	var org struct{ ID, Slug, Kind string }
	r.decode(t, &org)
	if org.Kind != "organization" || r.header.Get("Location") != "/v1/tenants/"+org.ID {
		t.Errorf("org = %+v, Location %q", org, r.header.Get("Location"))
	}
	replay := s.do(create)
	replay.want(t, http.StatusCreated, "")
	if replay.header.Get("Idempotent-Replayed") != "true" || !bytes.Equal(replay.body, r.body) {
		t.Errorf("replay = %s %v", replay.body, replay.header)
	}
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).want(t, http.StatusConflict, "slug_taken")

	// A token for CI, used as a bearer credential.
	r = s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"read", "write"}}})
	r.want(t, http.StatusCreated, "")
	var created struct {
		Secret string `json:"secret"`
		Token  struct{ ID string }
	}
	r.decode(t, &created)
	if !strings.HasPrefix(created.Secret, "glossa_api_") {
		t.Fatalf("secret = %q", created.Secret)
	}
	s.do(call{method: "GET", path: r.header.Get("Location"), bearer: created.Secret}).want(t, http.StatusOK, "")

	r = s.do(call{method: "GET", path: "/v1/tenants/" + org.ID + "/members", bearer: created.Secret})
	r.want(t, http.StatusOK, "")
	var members struct {
		Items []struct {
			ID    string   `json:"id"`
			Email string   `json:"email"`
			Roles []string `json:"roles"`
		} `json:"items"`
	}
	r.decode(t, &members)
	if len(members.Items) != 1 || members.Items[0].Email != "ada@example.com" {
		t.Fatalf("members = %+v", members)
	}

	// Tokens act in their own tenant only, need no CSRF, can't reach
	// session-only routes, and are bound by their scopes.
	var me struct {
		Person struct {
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie}).decode(t, &me)
	s.do(call{method: "GET", path: "/v1/tenants/" + me.Person.IndividualTenantID + "/members", bearer: created.Secret}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "GET", path: "/v1/me", bearer: created.Secret}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/members", bearer: created.Secret,
		body: map[string]any{"email": "x@example.com", "roles": []string{"reviewer"}}}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "GET", path: "/v1/tenants/" + org.ID + "/members", bearer: "glossa_api_" + strings.Repeat("x", 43)}).
		want(t, http.StatusUnauthorized, "unauthenticated")

	// Optimistic concurrency on members.
	r = s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "tr@example.com", "roles": []string{"translator"}, "locales": []string{"de_de"}}})
	r.want(t, http.StatusCreated, "")
	var invite struct {
		ID      string   `json:"id"`
		Locales []string `json:"locales"`
		Status  string   `json:"status"`
	}
	r.decode(t, &invite)
	tag := r.header.Get("ETag")
	if invite.Status != "invited" || invite.Locales[0] != "de-DE" || tag != `"1"` {
		t.Errorf("invite = %+v, ETag %s", invite, tag)
	}
	member := "/v1/tenants/" + org.ID + "/members/" + invite.ID
	patch := call{method: "PATCH", path: member, cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"locales": []string{"fr"}}}
	s.do(patch).want(t, http.StatusPreconditionRequired, "precondition_required")
	patch.headers = map[string]string{"If-Match": `"7"`}
	s.do(patch).want(t, http.StatusPreconditionFailed, "precondition_failed")
	patch.headers = map[string]string{"If-Match": tag}
	r = s.do(patch)
	r.want(t, http.StatusOK, "")
	if r.header.Get("ETag") != `"2"` {
		t.Errorf("ETag after update = %s", r.header.Get("ETag"))
	}

	// Pagination.
	r = s.do(call{method: "GET", path: "/v1/tenants/" + org.ID + "/members?page_size=1", cookie: ada.cookie})
	var page struct {
		Items         []any  `json:"items"`
		NextPageToken string `json:"next_page_token"`
	}
	r.decode(t, &page)
	if len(page.Items) != 1 || page.NextPageToken == "" {
		t.Fatalf("page 1 = %s", r.body)
	}
	r = s.do(call{method: "GET", path: "/v1/tenants/" + org.ID + "/members?page_size=1&page_token=" + page.NextPageToken, cookie: ada.cookie})
	page.NextPageToken = ""
	r.decode(t, &page)
	if len(page.Items) != 1 || page.NextPageToken != "" {
		t.Errorf("page 2 = %s", r.body)
	}
	s.do(call{method: "GET", path: "/v1/tenants/" + org.ID + "/members?page_size=500", cookie: ada.cookie}).
		want(t, http.StatusBadRequest, "invalid_page_size")

	// Someone else can't see the organization at all.
	bob := s.signIn("bob@example.com")
	s.do(call{method: "GET", path: "/v1/tenants/" + org.ID, cookie: bob.cookie}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "GET", path: "/v1/tenants/not-a-tenant", cookie: bob.cookie}).want(t, http.StatusForbidden, "forbidden")

	// Revoking the token ends it at once.
	s.do(call{method: "DELETE", path: "/v1/tenants/" + org.ID + "/tokens/" + created.Token.ID, cookie: ada.cookie, csrf: ada.csrf}).
		want(t, http.StatusNoContent, "")
	s.do(call{method: "GET", path: "/v1/tenants/" + org.ID, bearer: created.Secret}).want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestSignOutOverHTTP(t *testing.T) {
	s := startServer(t)
	laptop := s.signIn("ada@example.com")
	phone := s.signIn("ada@example.com")

	s.do(call{method: "DELETE", path: "/v1/auth/session", cookie: laptop.cookie}).want(t, http.StatusForbidden, "csrf_invalid")
	r := s.do(call{method: "DELETE", path: "/v1/auth/session", cookie: laptop.cookie, csrf: laptop.csrf})
	r.want(t, http.StatusNoContent, "")
	if !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("sign-out doesn't clear the cookie: %q", r.header.Get("Set-Cookie"))
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: laptop.cookie}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "GET", path: "/v1/me", cookie: phone.cookie}).want(t, http.StatusOK, "")

	other := s.signIn("ada@example.com")
	s.do(call{method: "DELETE", path: "/v1/auth/sessions", cookie: phone.cookie, csrf: phone.csrf}).want(t, http.StatusNoContent, "")
	for _, sess := range []session{phone, other} {
		s.do(call{method: "GET", path: "/v1/me", cookie: sess.cookie}).want(t, http.StatusUnauthorized, "unauthenticated")
	}
}

func TestPublicAuthRejectsCrossSiteRequests(t *testing.T) {
	s := startServer(t)
	s.do(call{method: "POST", path: "/v1/auth/magic-links", body: map[string]string{"email": "ada@example.com"},
		headers: map[string]string{"Sec-Fetch-Site": "cross-site"}}).want(t, http.StatusForbidden, "cross_site_request")
	s.do(call{method: "POST", path: "/v1/auth/magic-links", body: map[string]string{"email": "not an email"}}).
		want(t, http.StatusBadRequest, "invalid_request")
	s.do(call{method: "POST", path: "/v1/auth/passkey-challenges", body: map[string]string{"email": "ada@example.com"}}).
		want(t, http.StatusNotFound, "passkeys_disabled")
}
