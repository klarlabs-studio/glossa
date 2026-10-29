//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

// The Quality API (RFC 0005 §9, wave 2): check runs, the findings list
// with its filters, and the waivers that accept a finding. Nothing
// records a run through the API yet — `glossa check` and the
// pull-request check do that in later slices — so this covers what the
// endpoints promise about routing, permissions and the waiver rules.
func TestQualityAPIOverHTTP(t *testing.T) {
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
	fingerprint := "f_0123456789abcdef"

	// Nothing has been checked yet: an empty list, not a 404.
	var findings struct {
		Items  []map[string]any
		Run    *map[string]any
		Counts struct{ Errors, Warnings, Waived int }
	}
	r := s.do(call{method: "GET", path: p + "/findings", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &findings)
	if len(findings.Items) != 0 || findings.Run != nil || findings.Counts.Errors != 0 {
		t.Errorf("findings of an unchecked project = %s", r.body)
	}
	var runs struct{ Items []map[string]any }
	r = s.do(call{method: "GET", path: p + "/check-runs?branch=main&conclusion=success", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &runs)
	if len(runs.Items) != 0 {
		t.Errorf("check runs = %s", r.body)
	}
	// A filter outside the vocabulary is a 400, not a silent empty page.
	s.do(call{method: "GET", path: p + "/check-runs?conclusion=maybe", cookie: ada.cookie}).
		want(t, http.StatusBadRequest, "invalid_query")
	s.do(call{method: "GET", path: p + "/findings?layer=spelling", cookie: ada.cookie}).
		want(t, http.StatusBadRequest, "invalid_query")
	// A run that isn't there is a 404, whether it is named or malformed.
	s.do(call{method: "GET", path: p + "/check-runs/0192f5a1-0000-7000-8000-000000000001", cookie: ada.cookie}).
		want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: p + "/findings?run=not-a-uuid", cookie: ada.cookie}).
		want(t, http.StatusNotFound, "not_found")

	// A waiver needs a reason. There is no way around it.
	for _, reason := range []string{"", "   "} {
		s.do(call{method: "POST", path: p + "/waivers", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"fingerprint": fingerprint, "reason": reason}}).
			want(t, http.StatusBadRequest, "waiver_reason_required")
	}
	s.do(call{method: "POST", path: p + "/waivers", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"fingerprint": "nonsense", "reason": "because"}}).
		want(t, http.StatusBadRequest, "invalid_fingerprint")
	s.do(call{method: "POST", path: p + "/waivers", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"fingerprint": fingerprint, "reason": "because", "scope": "branch"}}).
		want(t, http.StatusBadRequest, "waiver_branch_required")

	var waiver struct {
		ID             string
		Reason, Scope  string
		Active         bool
		SourceRevision int `json:"source_revision"`
	}
	r = s.do(call{method: "POST", path: p + "/waivers", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"fingerprint": fingerprint, "reason": "Login is the German term"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &waiver)
	if !waiver.Active || waiver.Scope != "project" || waiver.Reason != "Login is the German term" {
		t.Fatalf("waiver = %s", r.body)
	}
	if loc := r.header.Get("Location"); !strings.HasSuffix(loc, "/waivers/"+waiver.ID) {
		t.Errorf("Location = %q", loc)
	}
	// The same finding again restates the waiver rather than adding one.
	r = s.do(call{method: "POST", path: p + "/waivers", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"fingerprint": fingerprint, "reason": "still the German term"}})
	r.want(t, http.StatusOK, "")
	var again struct{ ID, Reason string }
	r.decode(t, &again)
	if again.ID != waiver.ID || again.Reason != "still the German term" {
		t.Errorf("repeat = %s", r.body)
	}

	var waivers struct {
		Items []struct {
			ID     string
			Active bool
		}
	}
	r = s.do(call{method: "GET", path: p + "/waivers?active=true", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &waivers)
	if len(waivers.Items) != 1 || waivers.Items[0].ID != waiver.ID || !waivers.Items[0].Active {
		t.Fatalf("waivers = %s", r.body)
	}

	// A translator may read, and may not waive or revoke.
	s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "bob@example.com", "roles": []string{"translator"}, "locales": []string{"de"}}}).
		want(t, http.StatusCreated, "")
	bob := s.signIn("bob@example.com") // accepts the invitation
	s.do(call{method: "GET", path: p + "/findings", cookie: bob.cookie}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: p + "/waivers", cookie: bob.cookie, csrf: bob.csrf,
		body: map[string]any{"fingerprint": "f_fedcba9876543210", "reason": "no"}}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "DELETE", path: p + "/waivers/" + waiver.ID, cookie: bob.cookie, csrf: bob.csrf}).
		want(t, http.StatusForbidden, "forbidden")

	// Revoking is idempotent, and the revoked waiver stays as history.
	s.do(call{method: "DELETE", path: p + "/waivers/" + waiver.ID, cookie: ada.cookie, csrf: ada.csrf}).
		want(t, http.StatusNoContent, "")
	s.do(call{method: "DELETE", path: p + "/waivers/" + waiver.ID, cookie: ada.cookie, csrf: ada.csrf}).
		want(t, http.StatusNoContent, "")
	s.do(call{method: "DELETE", path: p + "/waivers/0192f5a1-0000-7000-8000-000000000002", cookie: ada.cookie, csrf: ada.csrf}).
		want(t, http.StatusNotFound, "not_found")
	r = s.do(call{method: "GET", path: p + "/waivers?active=false", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &waivers)
	if len(waivers.Items) != 1 || waivers.Items[0].Active {
		t.Errorf("revoked waivers = %s", r.body)
	}

	// The session cookie alone cannot write.
	s.do(call{method: "POST", path: p + "/waivers", cookie: ada.cookie,
		body: map[string]any{"fingerprint": fingerprint, "reason": "no csrf"}}).
		want(t, http.StatusForbidden, "csrf_invalid")
	// A read token may read and may not waive.
	var readToken struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "reader", "scopes": []string{"read"}}}).decode(t, &readToken)
	s.do(call{method: "GET", path: p + "/findings", bearer: readToken.Secret}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: p + "/waivers", bearer: readToken.Secret,
		body: map[string]any{"fingerprint": fingerprint, "reason": "no"}}).
		want(t, http.StatusForbidden, "forbidden")
}
