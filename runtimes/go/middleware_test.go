package glossa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

type userKey struct{}

func userLocale(r *http.Request) []string {
	if l, ok := r.Context().Value(userKey{}).(string); ok {
		return []string{l}
	}
	return nil
}

func TestResolverChainOrder(t *testing.T) {
	chain := ResolverChain{
		Explicit: QueryParam("lang"),
		User:     userLocale,
		Org:      Header("X-Org-Locale"),
		Metadata: Cookie("locale"),
	}
	available := []string{"de", "en", "fr", "ja"}
	cases := []struct {
		name, query, user, org, cookie, accept string
		wantLocale, wantBy                     string
		wantRequested                          []string
	}{
		{"explicit wins", "fr", "de", "ja", "en", "en", "fr", ByExplicit, []string{"fr", "de", "ja", "en"}},
		{"unavailable explicit falls to user", "pt", "de_AT", "", "", "", "de", ByUser, []string{"pt", "de-AT"}},
		{"org", "", "", "ja", "", "fr", "ja", ByOrg, []string{"ja", "fr"}},
		{"metadata", "", "", "", "en", "fr", "en", ByMetadata, []string{"en", "fr"}},
		{"accept-language by quality", "", "", "", "", "pt;q=0.9, fr-CA;q=0.8, *;q=0.5, de;q=0", "fr", ByAcceptLanguage, []string{"pt", "fr-CA"}},
		{"nothing matches: source", "xx-invalid!", "", "", "", "pt", "de", BySource, []string{"pt"}},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/?lang="+tc.query, nil)
		if tc.user != "" {
			r = r.WithContext(context.WithValue(r.Context(), userKey{}, tc.user))
		}
		setHeader(r, "X-Org-Locale", tc.org)
		setHeader(r, "Accept-Language", tc.accept)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "locale", Value: tc.cookie})
		}
		n := chain.Negotiate(r, available, "de")
		if n.Locale != tc.wantLocale || n.By != tc.wantBy || !slices.Equal(n.Requested, tc.wantRequested) {
			t.Errorf("%s: %+v; want %s by %s from %v", tc.name, n, tc.wantLocale, tc.wantBy, tc.wantRequested)
		}
	}
}

func setHeader(r *http.Request, name, value string) {
	if value != "" {
		r.Header.Set(name, value)
	}
}

func TestResolverChainWithoutRelease(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "ar-EG, en")
	n := ResolverChain{}.Negotiate(r, nil, "")
	if n.Locale != "ar-EG" || n.By != ByAcceptLanguage {
		t.Fatalf("%+v", n)
	}
	n = ResolverChain{IgnoreAcceptLanguage: true}.Negotiate(r, nil, "")
	if n.Locale != "" || n.By != BySource || len(n.Requested) != 0 {
		t.Fatalf("%+v", n)
	}
}

func TestAcceptLanguageLimits(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", strings.Repeat("en-US,", 5000))
	if got := AcceptLanguage(r); len(got) > maxAcceptLanguageTags {
		t.Fatalf("%d tags", len(got))
	}
	r.Header.Set("Accept-Language", "garbage;;;q=x")
	_ = AcceptLanguage(r) // must not panic
}

func TestPathValue(t *testing.T) {
	mux := http.NewServeMux()
	var got []string
	mux.HandleFunc("GET /{lang}/hello", func(w http.ResponseWriter, r *http.Request) { got = PathValue("lang")(r) })
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/de/hello", nil))
	if !slices.Equal(got, []string{"de"}) {
		t.Fatalf("PathValue = %v", got)
	}
	if v := QueryParam("lang")(httptest.NewRequest(http.MethodGet, "/", nil)); v != nil {
		t.Fatalf("absent query parameter = %v, want nil", v)
	}
	if v := Cookie("locale")(httptest.NewRequest(http.MethodGet, "/", nil)); v != nil {
		t.Fatalf("absent cookie = %v, want nil", v)
	}
}

func TestMiddleware(t *testing.T) {
	rel := buildRelease(t, "rel_1", 1, twoLocales, "en", "de")
	c := newTestClient(t, Config{Bundled: rel.fs(), DisableBidiIsolation: true})
	h := c.Middleware(ResolverChain{Explicit: QueryParam("lang")})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := NegotiationFrom(r.Context())
		_, _ = w.Write([]byte(n.Locale + ":" + n.By + ":" + c.T(r.Context(), "hello", Args{"name": "Ada"})))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?lang=ja", nil)
	req.Header.Set("Accept-Language", "de-CH, en;q=0.5")
	h.ServeHTTP(rec, req)
	if got := rec.Body.String(); got != "de:accept-language:Hallo Ada!" {
		t.Fatalf("body = %q", got)
	}
	if vary := rec.Header().Values("Vary"); !slices.Contains(vary, "Accept-Language") {
		t.Fatalf("Vary = %v", vary)
	}
	if _, ok := NegotiationFrom(context.Background()); ok {
		t.Fatal("no negotiation on a bare context")
	}
}
