//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

// The in-product editor's authorization through the real server
// (RFC 0004 §5.2): registering where the editor may run, minting a grant
// from Studio's session, using it from the bound origin and only from
// there, and the CORS that lets a browser make those calls at all.

const previewOrigin = "https://preview.example.com"

// missingMessage is a well-formed message id nothing ever has, so a
// request that authenticates ends in 404 rather than a parse error.
const missingMessage = "01a0be1c-0000-7000-8000-00000000dead"

type grantFixture struct {
	s               *server
	ada             session
	tenant, project string
	other           string // a second project in the same tenant
}

func setUpGrants(t *testing.T) *grantFixture {
	t.Helper()
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID

	project := func(slug string) string {
		var p struct{ ID string }
		r := s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"slug": slug, "name": slug, "source_locale": "en"}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &p)
		return p.ID
	}
	return &grantFixture{s: s, ada: ada, tenant: org.ID, project: project("shop"), other: project("docs")}
}

func (f *grantFixture) originsPath() string {
	return "/v1/tenants/" + f.tenant + "/projects/" + f.project + "/preview-origins"
}

func (f *grantFixture) grantsPath(project string) string {
	return "/v1/tenants/" + f.tenant + "/projects/" + project + "/in-context-grants"
}

// register adds a preview origin and returns its id.
func (f *grantFixture) register(t *testing.T, origin string) string {
	t.Helper()
	r := f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": origin, "label": "shared preview"}})
	r.want(t, http.StatusCreated, "")
	var o struct{ ID string }
	r.decode(t, &o)
	return o.ID
}

// mint asks Studio's popup endpoint for a grant and returns its secret.
func (f *grantFixture) mint(t *testing.T, project, origin string) string {
	t.Helper()
	r := f.s.do(call{method: "POST", path: f.grantsPath(project), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": origin}})
	r.want(t, http.StatusCreated, "")
	var g struct {
		Token string `json:"token"`
	}
	r.decode(t, &g)
	return g.Token
}

func TestPreviewOriginsAreRegisteredAndCanonicalized(t *testing.T) {
	f := setUpGrants(t)

	r := f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": "HTTPS://Preview.Example.com:443", "label": "shared preview"}})
	r.want(t, http.StatusCreated, "")
	var o struct {
		ID          string `json:"id"`
		Origin      string `json:"origin"`
		Label       string `json:"label"`
		Development bool   `json:"development"`
	}
	r.decode(t, &o)
	if o.Origin != previewOrigin || o.Label != "shared preview" || o.Development {
		t.Fatalf("origin = %+v, want the canonical form of a shared https deployment", o)
	}

	// A developer's own run is registered too, and marked as such so
	// Studio can say it is for development.
	r = f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": "http://localhost:5173"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &o)
	if !o.Development {
		t.Error("a loopback http origin should be marked as development")
	}

	// http anywhere else is not an origin the editor may run on.
	f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": "http://preview.example.com"}}).
		want(t, http.StatusBadRequest, "invalid_origin")
	// Neither is a wildcard.
	f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": "*"}}).
		want(t, http.StatusBadRequest, "invalid_origin")
	// The same origin twice is a conflict, not a second row.
	f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": previewOrigin}}).
		want(t, http.StatusConflict, "origin_registered")

	r = f.s.do(call{method: "GET", path: f.originsPath(), cookie: f.ada.cookie})
	r.want(t, http.StatusOK, "")
	var list struct {
		Items []struct{ Origin string }
	}
	r.decode(t, &list)
	if len(list.Items) != 2 || list.Items[0].Origin != "http://localhost:5173" ||
		list.Items[1].Origin != previewOrigin {
		t.Errorf("origins = %+v, want both in origin order", list.Items)
	}
}

func TestInContextGrantIsBoundToItsOriginAndProject(t *testing.T) {
	f := setUpGrants(t)
	f.register(t, previewOrigin)
	token := f.mint(t, f.project, previewOrigin)

	if !strings.HasPrefix(token, "glossa_ctx_") {
		t.Fatalf("token %q is not an in-context grant", token)
	}

	message := "/v1/tenants/" + f.tenant + "/projects/" + f.project + "/messages/" + missingMessage
	// From the bound origin the grant authenticates: the message is
	// missing, which is a 404 and not a 401.
	f.s.do(call{method: "GET", path: message, bearer: token,
		headers: map[string]string{"Origin": previewOrigin}}).
		want(t, http.StatusNotFound, "not_found")

	// From anywhere else it opens nothing, however valid the secret.
	f.s.do(call{method: "GET", path: message, bearer: token,
		headers: map[string]string{"Origin": "https://evil.example.com"}}).
		want(t, http.StatusUnauthorized, "origin_not_bound")
	f.s.do(call{method: "GET", path: message, bearer: token}).
		want(t, http.StatusUnauthorized, "origin_not_bound")

	// And it reaches only the project it was minted for, even though
	// both projects are the same person's, in the same tenant.
	other := "/v1/tenants/" + f.tenant + "/projects/" + f.other + "/messages/" + missingMessage
	f.s.do(call{method: "GET", path: other, bearer: token,
		headers: map[string]string{"Origin": previewOrigin}}).
		want(t, http.StatusForbidden, "grant_project_mismatch")
}

func TestInContextGrantReachesOnlyTheEditorsEndpoints(t *testing.T) {
	f := setUpGrants(t)
	f.register(t, previewOrigin)
	token := f.mint(t, f.project, previewOrigin)
	from := map[string]string{"Origin": previewOrigin}

	// Endpoints the overlay never calls refuse the credential itself,
	// before any permission is considered.
	for _, c := range []call{
		{method: "GET", path: "/v1/tenants/" + f.tenant + "/tokens"},
		{method: "GET", path: "/v1/me"},
		{method: "GET", path: f.originsPath()},
		{method: "POST", path: "/v1/tenants/" + f.tenant + "/projects/" + f.project + "/releases",
			body: map[string]string{"environment": "preview"}},
	} {
		c.bearer, c.headers = token, from
		f.s.do(c).want(t, http.StatusUnauthorized, "unauthenticated")
	}

	// A grant never mints another one: the popup on Studio is the only
	// way in, so a leaked grant can't extend itself.
	f.s.do(call{method: "POST", path: f.grantsPath(f.project), bearer: token, headers: from,
		body: map[string]string{"origin": previewOrigin}}).
		want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestMintingRefusesAnUnregisteredOriginAndAPITokens(t *testing.T) {
	f := setUpGrants(t)
	f.register(t, previewOrigin)

	// An origin nobody registered for this project mints nothing, even
	// for an owner.
	f.s.do(call{method: "POST", path: f.grantsPath(f.project), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": "https://evil.example.com"}}).
		want(t, http.StatusForbidden, "origin_not_registered")
	// Nor does the right origin on the wrong project.
	f.s.do(call{method: "POST", path: f.grantsPath(f.other), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": previewOrigin}}).
		want(t, http.StatusForbidden, "origin_not_registered")

	// An API token may not launder itself into a person's credential.
	var tok struct {
		Secret string `json:"secret"`
	}
	r := f.s.do(call{method: "POST", path: "/v1/tenants/" + f.tenant + "/tokens", cookie: f.ada.cookie,
		csrf: f.ada.csrf, body: map[string]any{"name": "ci", "scopes": []string{"read", "write"}}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &tok)
	f.s.do(call{method: "POST", path: f.grantsPath(f.project), bearer: tok.Secret,
		body: map[string]string{"origin": previewOrigin}}).
		want(t, http.StatusUnauthorized, "unauthenticated")
}

// Unregistering an origin ends the sessions on it now, not when the last
// fifteen-minute grant runs out.
func TestUnregisteringAnOriginEndsItsGrants(t *testing.T) {
	f := setUpGrants(t)
	id := f.register(t, previewOrigin)
	token := f.mint(t, f.project, previewOrigin)
	message := "/v1/tenants/" + f.tenant + "/projects/" + f.project + "/messages/" + missingMessage
	from := map[string]string{"Origin": previewOrigin}

	f.s.do(call{method: "GET", path: message, bearer: token, headers: from}).
		want(t, http.StatusNotFound, "not_found")

	f.s.do(call{method: "DELETE", path: f.originsPath() + "/" + id, cookie: f.ada.cookie, csrf: f.ada.csrf}).
		want(t, http.StatusNoContent, "")

	f.s.do(call{method: "GET", path: message, bearer: token, headers: from}).
		want(t, http.StatusUnauthorized, "unauthenticated")
	// And CORS stops answering the origin with the same request.
	r := f.s.do(call{method: "OPTIONS", path: message,
		headers: map[string]string{"Origin": previewOrigin, "Access-Control-Request-Method": "GET"}})
	if got := r.header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q after unregistering", got)
	}
}

func TestCORSAnswersRegisteredOriginsOnTheEditorsSurfaceOnly(t *testing.T) {
	f := setUpGrants(t)
	f.register(t, previewOrigin)

	preflight := func(origin, method, path string) reply {
		return f.s.do(call{method: "OPTIONS", path: path, headers: map[string]string{
			"Origin": origin, "Access-Control-Request-Method": method,
		}})
	}
	message := "/v1/tenants/" + f.tenant + "/projects/" + f.project + "/messages/" + missingMessage
	translation := message + "/translations/de"

	r := preflight(previewOrigin, "PUT", translation)
	if r.status != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", r.status)
	}
	if got := r.header.Get("Access-Control-Allow-Origin"); got != previewOrigin {
		t.Errorf("Allow-Origin = %q, want %q", got, previewOrigin)
	}
	if got := r.header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q; the editor sends no cookies and this is never sent", got)
	}
	headers := r.header.Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "If-Match", "Idempotency-Key"} {
		if !strings.Contains(headers, h) {
			t.Errorf("Allow-Headers %q lacks %s", headers, h)
		}
	}
	if got := r.header.Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("Max-Age = %q, want a short preflight cache", got)
	}

	// An unregistered origin gets nothing at all.
	if got := preflight("https://evil.example.com", "PUT", translation).
		header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("an unregistered origin got Allow-Origin %q", got)
	}
	// A registered one gets nothing off the editor's surface.
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/tenants/" + f.tenant + "/tokens"},
		{"GET", "/v1/me"},
		{"GET", f.originsPath()},
		{"DELETE", message},
	} {
		if got := preflight(previewOrigin, c.method, c.path).
			header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s %s answered a preview origin with %q", c.method, c.path, got)
		}
	}

	// A real cross-origin call exposes ETag, without which the editor
	// could never send If-Match.
	token := f.mint(t, f.project, previewOrigin)
	got := f.s.do(call{method: "GET", path: message, bearer: token,
		headers: map[string]string{"Origin": previewOrigin}})
	if v := got.header.Get("Access-Control-Allow-Origin"); v != previewOrigin {
		t.Errorf("Allow-Origin on the real request = %q", v)
	}
	if v := got.header.Get("Access-Control-Expose-Headers"); !strings.Contains(v, "ETag") {
		t.Errorf("Expose-Headers = %q, want it to name ETag", v)
	}
}

// Studio is served beside /v1, so its own requests are same-origin and
// must be untouched by any of this.
func TestStudiosOwnOriginKeepsWorking(t *testing.T) {
	f := setUpGrants(t)
	f.register(t, previewOrigin)

	// The test server is reached over http on 127.0.0.1, so its own
	// origin is exactly that.
	own := strings.TrimSuffix(f.s.base, "/")
	r := f.s.do(call{method: "GET", path: f.originsPath(), cookie: f.ada.cookie,
		headers: map[string]string{"Origin": own}})
	r.want(t, http.StatusOK, "")
	if got := r.header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a same-origin request got Allow-Origin %q; it needs none", got)
	}

	// Writing still works, cookie and CSRF token as before.
	f.s.do(call{method: "POST", path: f.originsPath(), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": "https://staging.example.com"}, headers: map[string]string{"Origin": own}}).
		want(t, http.StatusCreated, "")
}

// The grant's permissions are the person's, cut to the editor's ceiling
// and no further — and a translator's locale scope survives the cut.
func TestGrantPermissionsAreTheCutDownPersons(t *testing.T) {
	f := setUpGrants(t)
	f.register(t, previewOrigin)

	r := f.s.do(call{method: "POST", path: f.grantsPath(f.project), cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"origin": previewOrigin}})
	r.want(t, http.StatusCreated, "")
	var g struct {
		Token       string `json:"token"`
		ExpiresAt   string `json:"expires_at"`
		ProjectID   string `json:"project_id"`
		Origin      string `json:"origin"`
		PersonID    string `json:"person_id"`
		Permissions []struct {
			Permission string   `json:"permission"`
			Locales    []string `json:"locales"`
		} `json:"permissions"`
	}
	r.decode(t, &g)

	if g.ProjectID != f.project || g.Origin != previewOrigin || g.PersonID == "" {
		t.Errorf("grant = %+v, want it bound to the project and origin and named for the person", g)
	}
	var names []string
	for _, p := range g.Permissions {
		names = append(names, p.Permission)
		if len(p.Locales) != 0 {
			t.Errorf("%s is locale-scoped for an owner: %v", p.Permission, p.Locales)
		}
	}
	want := []string{
		"catalog.read", "intelligence.read", "intelligence.translate",
		"knowledge.read", "translations.read", "translations.write",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("permissions = %v, want %v — an owner's grant is cut to the same six", names, want)
	}
}
