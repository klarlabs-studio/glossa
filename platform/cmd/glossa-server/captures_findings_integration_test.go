//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

// The visual findings a capture upload carries, end to end (RFC 0005
// §5, §13 wave 4): CI uploads them with the capture, the server
// completes the fingerprint and the capture the page could not know,
// and a member reads them back by capture and by region.

// probeFinding is one finding as the probe pass writes it: no
// fingerprint, and a region of this capture but no capture.
func probeFinding(code, key, region string) map[string]any {
	locus := map[string]any{"key": key}
	if region != "" {
		locus["region"] = region
	}
	return map[string]any{
		"schema": "glossa.finding/v1", "layer": "visual", "code": code, "severity": "warning",
		"locus": locus, "message": code + " on " + key,
	}
}

// sightings is the count the server recorded on a finding, as JSON
// hands it back.
func sightings(t *testing.T, evidence map[string]any) int {
	t.Helper()
	n, ok := evidence["sightings"].(float64)
	if !ok {
		return 0
	}
	return int(n)
}

func TestCaptureFindingsAPIOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	p := base + "/projects/" + project.ID
	s.do(call{method: "POST", path: p + "/applications", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "web", "name": "Web", "platform": "web"}}).want(t, http.StatusCreated, "")
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"write"}}}).decode(t, &tok)
	s.do(call{method: "POST", path: p + "/message-upserts", bearer: tok.Secret, body: map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay"}, {"key": "checkout.total", "text": "Total"},
	}}}).want(t, http.StatusOK, "")

	commit := strings.Repeat("9f2c1e7a", 5)
	img := testPNG(t, 48, 32, 1)
	manifest := capturesManifest(commit, img, 48, 32)
	shot, _ := manifest["captures"].([]any)[0].(map[string]any)
	shot["findings"] = []any{
		probeFinding("text-clipped", "checkout.pay", "r_0"),
		probeFinding("runtime-missing-message", "checkout.total", ""),
	}
	body, contentType := capturesUpload(t, manifest, img)
	var uploaded struct {
		Captures int `json:"captures"`
		Findings int `json:"findings"`
	}
	r := s.do(call{method: "POST", path: p + "/captures", bearer: tok.Secret, raw: body, contentType: contentType})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &uploaded)
	if uploaded.Captures != 1 || uploaded.Findings != 2 {
		t.Fatalf("upload = %s", r.body)
	}

	// The capture the findings are on, as a member finds it.
	var captures struct {
		Captures []struct {
			ID string `json:"id"`
		} `json:"captures"`
	}
	s.do(call{method: "GET", path: p + "/messages/checkout.pay/captures", cookie: ada.cookie}).decode(t, &captures)
	if len(captures.Captures) != 1 {
		t.Fatalf("captures = %+v", captures)
	}
	id := captures.Captures[0].ID

	var page struct {
		Items []struct {
			Schema      string `json:"schema"`
			Fingerprint string `json:"fingerprint"`
			Layer       string `json:"layer"`
			Code        string `json:"code"`
			Severity    string `json:"severity"`
			Locus       struct {
				Message string `json:"message"`
				Key     string `json:"key"`
				Locale  string `json:"locale"`
				Capture string `json:"capture"`
				Region  string `json:"region"`
			} `json:"locus"`
			Evidence map[string]any `json:"evidence"`
		} `json:"items"`
	}
	r = s.do(call{method: "GET", path: p + "/captures/" + id + "/findings", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &page)
	if len(page.Items) != 2 {
		t.Fatalf("findings = %s", r.body)
	}
	// The order is the run, then the region, then the row: the finding
	// that names no region comes first.
	if page.Items[0].Code != "runtime-missing-message" {
		t.Errorf("order = %s, %s", page.Items[0].Code, page.Items[1].Code)
	}
	f := page.Items[1]
	if f.Schema != "glossa.finding/v1" || f.Layer != "visual" || f.Severity != "warning" || f.Code != "text-clipped" {
		t.Errorf("finding = %+v", f)
	}
	// The two members the page could not write, both completed here.
	if !strings.HasPrefix(f.Fingerprint, "f_") || f.Locus.Capture != id || f.Locus.Region != "r_0" {
		t.Errorf("locus = %+v, fingerprint %q", f.Locus, f.Fingerprint)
	}
	// The key resolved to a catalog message, and the capture lent its
	// locale to a finding that named none.
	if f.Locus.Message == "" || f.Locus.Key != "checkout.pay" || f.Locus.Locale != "de" {
		t.Errorf("locus = %+v", f.Locus)
	}

	// The two-sighting rule, end to end (RFC 0005 §5.2). The first
	// capture of this scope saw everything once; a second capture of the
	// same route, viewport and locale — a different commit, a different
	// image — makes the same fingerprints evidence, and the server says
	// so in the finding's evidence. Nothing in the upload asks for it:
	// the count comes from the findings the first capture left behind.
	if n := sightings(t, page.Items[0].Evidence); n != 1 {
		t.Errorf("first sighting counted %d", n)
	}
	next := capturesManifest(strings.Repeat("a1b2c3d4", 5), testPNG(t, 48, 32, 3), 48, 32)
	shot, _ = next["captures"].([]any)[0].(map[string]any)
	shot["findings"] = []any{probeFinding("text-clipped", "checkout.pay", "r_0")}
	body, contentType = capturesUpload(t, next, testPNG(t, 48, 32, 3))
	s.do(call{method: "POST", path: p + "/captures", bearer: tok.Secret, raw: body, contentType: contentType}).
		want(t, http.StatusCreated, "")
	s.do(call{method: "GET", path: p + "/messages/checkout.pay/captures", cookie: ada.cookie}).decode(t, &captures)
	var promoted int
	for _, c := range captures.Captures {
		r = s.do(call{method: "GET", path: p + "/captures/" + c.ID + "/findings?region=r_0", cookie: ada.cookie})
		r.decode(t, &page)
		for _, f := range page.Items {
			if sightings(t, f.Evidence) == 2 {
				promoted++
			}
		}
	}
	if promoted != 1 {
		t.Errorf("%d findings were seen twice, want the second capture's one", promoted)
	}

	// One region of the screenshot.
	r = s.do(call{method: "GET", path: p + "/captures/" + id + "/findings", cookie: ada.cookie})
	r.decode(t, &page)
	r = s.do(call{method: "GET", path: p + "/captures/" + id + "/findings?region=r_0", cookie: ada.cookie})
	r.decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Code != "text-clipped" {
		t.Errorf("region r_0 = %s", r.body)
	}
	// A capture Quality holds no findings for reads as an empty list; an
	// ID that names no capture at all is a 404.
	r = s.do(call{method: "GET", path: p + "/captures/" + org.ID + "/findings", cookie: ada.cookie})
	r.decode(t, &page)
	if r.status != http.StatusOK || len(page.Items) != 0 {
		t.Errorf("unknown capture = %d %s", r.status, r.body)
	}
	s.do(call{method: "GET", path: p + "/captures/not-a-uuid/findings", cookie: ada.cookie}).
		want(t, http.StatusNotFound, "not_found")
	// Reading a finding needs a principal, like every other read.
	s.do(call{method: "GET", path: p + "/captures/" + id + "/findings"}).want(t, http.StatusUnauthorized, "unauthenticated")
}
