//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestReleasePreviewOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	var project struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	p := "/v1/tenants/" + org.ID + "/projects/" + project.ID
	write := func(method, path string, body any) reply {
		t.Helper()
		return s.do(call{method: method, path: p + path, cookie: ada.cookie, csrf: ada.csrf, body: body})
	}
	write("POST", "/locales", map[string]string{"code": "de"}).want(t, http.StatusCreated, "")
	write("POST", "/message-upserts", map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay {amount, number}"},
		{"key": "home.title", "text": "Welcome"},
	}}).want(t, http.StatusOK, "")
	write("PUT", "/messages/checkout.pay/translations/de", map[string]string{"text": "{amount, number} bezahlen"}).
		want(t, http.StatusCreated, "")

	type preview struct {
		Environment   string `json:"environment"`
		BaseReleaseID string `json:"base_release_id"`
		Releasable    bool   `json:"releasable"`
		Problems      []any  `json:"problems"`
		Counts        struct {
			Messages     int `json:"messages"`
			NewArtifacts int `json:"new_artifacts"`
			Locales      map[string]struct {
				Messages int `json:"messages"`
			} `json:"locales"`
		} `json:"counts"`
		Changes []struct {
			Locale string   `json:"locale"`
			Added  []string `json:"added"`
		} `json:"changes"`
	}
	// The session needs its CSRF token, as for any POST.
	s.do(call{method: "POST", path: p + "/environments/development/release-previews", cookie: ada.cookie}).
		want(t, http.StatusForbidden, "csrf_invalid")
	r := write("POST", "/environments/development/release-previews", nil)
	r.want(t, http.StatusOK, "")
	var pv preview
	r.decode(t, &pv)
	if !pv.Releasable || pv.Problems == nil || pv.Environment != "development" || pv.BaseReleaseID != "" ||
		pv.Counts.Messages != 2 || pv.Counts.Locales["de"].Messages != 1 || pv.Counts.NewArtifacts == 0 ||
		len(pv.Changes) != 2 || len(pv.Changes[1].Added) != 1 {
		t.Fatalf("preview %s", r.body)
	}
	// It stored nothing: no release exists yet.
	var rels struct{ Items []any }
	s.do(call{method: "GET", path: p + "/releases", cookie: ada.cookie}).decode(t, &rels)
	if len(rels.Items) != 0 {
		t.Errorf("a preview recorded a release: %+v", rels)
	}
	write("POST", "/environments/nope/release-previews", nil).want(t, http.StatusNotFound, "not_found")

	// A read token may preview; the development release can't reach
	// production, and the refusal says why and what to do.
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"read"}}}).decode(t, &tok)
	s.do(call{method: "POST", path: p + "/environments/production/release-previews", bearer: tok.Secret}).
		want(t, http.StatusOK, "")
	var rel releaseBody
	r = write("POST", "/releases", map[string]string{"environment": "development"})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &rel)
	r = write("POST", "/environments/production/promotions", map[string]string{"release_id": rel.ID})
	r.want(t, http.StatusConflict, "release_ineligible")
	var prob struct{ Detail string }
	r.decode(t, &prob)
	for _, want := range []string{"v1", "development", "draft, needs_review", "staging"} {
		if !strings.Contains(prob.Detail, want) {
			t.Errorf("detail %q lacks %q", prob.Detail, want)
		}
	}
}
