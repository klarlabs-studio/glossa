package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
)

// The in-context surface is the contract's, not a second list kept
// alongside it. This pins what is on it, so widening the editor's reach
// — and with it what CORS answers a preview origin on — is a deliberate
// edit to this test as well as to api/openapi.yaml.
func TestInContextSurfaceIsTheOverlaysEndpoints(t *testing.T) {
	routes, err := apiv1.InContextRoutes()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"/v1/message-previews":                                                                       {"POST"},
		"/v1/tenants/{tenant}/ai-suggestions":                                                        {"GET"},
		"/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/acceptance":                             {"POST"},
		"/v1/tenants/{tenant}/projects/{project}/ai-fill-previews":                                   {"POST"},
		"/v1/tenants/{tenant}/projects/{project}/ai-fills":                                           {"POST"},
		"/v1/tenants/{tenant}/projects/{project}/messages/{message}":                                 {"GET"},
		"/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}":           {"GET", "PUT"},
		"/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/revisions": {"GET"},
		"/v1/tenants/{tenant}/projects/{project}/terminology-findings":                               {"GET"},
	}
	for path, methods := range want {
		got, ok := routes[path]
		if !ok {
			t.Errorf("%s accepts no in-context grant; the overlay needs it", path)
			continue
		}
		if !slices.Equal(got, methods) {
			t.Errorf("%s allows %v, want %v", path, got, methods)
		}
	}
	for path := range routes {
		if _, ok := want[path]; !ok {
			t.Errorf("%s accepts an in-context grant but the overlay does not call it", path)
		}
	}
	// Nothing that writes beyond a translation, publishes, reviews or
	// administers is ever reachable with a grant.
	reqs, err := apiv1.Requirements()
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{
		"POST /v1/tenants/{tenant}/tokens",
		"POST /v1/tenants/{tenant}/projects/{project}/releases",
		"POST /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/reviews",
		"POST /v1/tenants/{tenant}/projects/{project}/preview-origins",
		"POST /v1/tenants/{tenant}/projects/{project}/in-context-grants",
	} {
		if reqs[pattern].InContext {
			t.Errorf("%s must not accept an in-context grant", pattern)
		}
	}
}

// An in-context grant never mints another one: the popup on Studio, with
// the person's session, is the only way in, so a leaked grant cannot
// extend or widen itself.
func TestGrantsAreMintedBySessionOnly(t *testing.T) {
	reqs, err := apiv1.Requirements()
	if err != nil {
		t.Fatal(err)
	}
	req := reqs["POST /v1/tenants/{tenant}/projects/{project}/in-context-grants"]
	if !req.Session || !req.CSRF {
		t.Error("minting a grant needs Studio's session and a CSRF token")
	}
	if req.Bearer || req.InContext {
		t.Error("no bearer credential may mint a grant")
	}
}

func TestCORSSurfaceMatchesOnlyItsRoutes(t *testing.T) {
	s, err := newCORSSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/v1/tenants/t1/projects/p1/messages/m1", true},
		{"PUT", "/v1/tenants/t1/projects/p1/messages/m1/translations/de", true},
		{"POST", "/v1/message-previews", true},
		// The right path with a method the overlay never uses.
		{"DELETE", "/v1/tenants/t1/projects/p1/messages/m1", false},
		// Real endpoints that are not the editor's.
		{"POST", "/v1/tenants/t1/tokens", false},
		{"GET", "/v1/me", false},
		{"POST", "/v1/tenants/t1/projects/p1/releases", false},
		{"GET", "/v1/tenants/t1/projects/p1/preview-origins", false},
		{"GET", "/nope", false},
	} {
		r := httptest.NewRequest(http.MethodOptions, tc.path, nil)
		if _, ok := s.match(r, tc.method); ok != tc.want {
			t.Errorf("match(%s %s) = %v, want %v", tc.method, tc.path, ok, tc.want)
		}
	}
}

func TestPreflightAnswersOnlyRegisteredOrigins(t *testing.T) {
	const registered = "https://preview.example.com"
	a := &API{cors: mustSurface(t), logger: discardLogger()}
	a.originLookup = func(_ context.Context, origin string) (bool, error) { return origin == registered, nil }

	call := func(origin, method, path string) *http.Response {
		r := httptest.NewRequest(http.MethodOptions, path, nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if method != "" {
			r.Header.Set("Access-Control-Request-Method", method)
		}
		w := httptest.NewRecorder()
		a.Preflight(w, r)
		return w.Result()
	}

	const overlayPath = "/v1/tenants/t1/projects/p1/messages/m1/translations/de"

	res := call(registered, "PUT", overlayPath)
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != registered {
		t.Errorf("Allow-Origin = %q, want %q", got, registered)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("the API never answers a wildcard origin")
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q; it is never sent", got)
	}
	headers := res.Header.Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "If-Match", "Idempotency-Key"} {
		if !strings.Contains(headers, h) {
			t.Errorf("Allow-Headers %q lacks %s", headers, h)
		}
	}
	if got := res.Header.Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("Max-Age = %q, want 600", got)
	}
	if !strings.Contains(res.Header.Get("Vary"), "Origin") {
		t.Errorf("Vary = %q, want it to name Origin", res.Header.Get("Vary"))
	}

	// An origin nobody registered gets no headers at all, so the browser
	// blocks the real request.
	if got := call("https://evil.example.com", "PUT", overlayPath).
		Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("an unregistered origin got Allow-Origin %q", got)
	}
	// A registered origin still gets nothing off the editor's surface.
	if got := call(registered, "POST", "/v1/tenants/t1/tokens").
		Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a registered origin got Allow-Origin %q on an endpoint the overlay never calls", got)
	}
	// A preflight without the CORS headers is not a preflight.
	if got := call("", "PUT", overlayPath).Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a request with no Origin got Allow-Origin %q", got)
	}
}

func TestCORSLeavesSameOriginAlone(t *testing.T) {
	a := &API{cors: mustSurface(t), logger: discardLogger()}
	a.originLookup = func(context.Context, string) (bool, error) {
		t.Error("a same-origin request should never look an origin up")
		return false, nil
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// Studio is served beside /v1, so its own requests carry an Origin
	// naming this very server. They must pass through untouched.
	r := httptest.NewRequest(http.MethodGet, "/v1/tenants/t1/projects/p1/messages/m1", nil)
	r.Host = "studio.example.com"
	r.Header.Set("Origin", "https://studio.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Pattern = "GET /v1/tenants/{tenant}/projects/{project}/messages/{message}"
	w := httptest.NewRecorder()
	a.CORS(next).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a same-origin request got Allow-Origin %q", got)
	}
}

func TestCORSExposesETagToRegisteredOrigins(t *testing.T) {
	const registered = "https://preview.example.com"
	a := &API{cors: mustSurface(t), logger: discardLogger()}
	a.originLookup = func(_ context.Context, origin string) (bool, error) { return origin == registered, nil }
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	call := func(origin, pattern, path string) http.Header {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "api.example.com"
		r.Header.Set("Origin", origin)
		r.Pattern = pattern
		w := httptest.NewRecorder()
		a.CORS(next).ServeHTTP(w, r)
		return w.Header()
	}

	const pattern = "GET /v1/tenants/{tenant}/projects/{project}/messages/{message}"
	h := call(registered, pattern, "/v1/tenants/t1/projects/p1/messages/m1")
	if got := h.Get("Access-Control-Allow-Origin"); got != registered {
		t.Errorf("Allow-Origin = %q, want %q", got, registered)
	}
	if got := h.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "ETag") {
		t.Errorf("Expose-Headers = %q; the editor needs ETag to send If-Match", got)
	}
	if got := h.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q; it is never sent", got)
	}

	if got := call("https://evil.example.com", pattern, "/v1/tenants/t1/projects/p1/messages/m1").
		Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("an unregistered origin got Allow-Origin %q", got)
	}
	if got := call(registered, "POST /v1/tenants/{tenant}/tokens", "/v1/tenants/t1/tokens").
		Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a registered origin got Allow-Origin %q off the editor's surface", got)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func mustSurface(t *testing.T) *corsSurface {
	t.Helper()
	s, err := newCORSSurface()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
