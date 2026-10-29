//go:build integration

package main

import (
	"net/http"
	"testing"
)

// The linguistic-QA jobs API over HTTP (RFC 0005 §3.8, §9, wave 6):
// routing, permissions, and what a tenant that never consented gets.
//
// The composition root now wires the whole seam — the preflight, the
// batcher that expands a scope into translations, and the layer that
// reviews one — so a review here is not `linguistic_unavailable`. It is
// a job, on the record, `failed` with `provider_consent`: this tenant
// has not enabled sending text to AI providers, and that is the first
// of the three gates M2 already owns (RFC 0003 §7).
//
// **Nothing here reaches a provider**, and not because a reviewer is
// missing: the refusal happens in the preflight, before any text is
// selected, and the tenant has no provider configured to reach even if
// it had consented. The seam's own tests (internal/quality/adapters/review)
// and the service's (internal/quality/app) drive the whole lifecycle
// against a faked layer.
func TestLinguisticJobsAPIOverHTTP(t *testing.T) {
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
	body := map[string]any{"ref": "main", "scope": map[string]any{"locales": []string{"de"}}}

	// Nothing has been asked for yet: an empty list, not a 404.
	var jobs struct{ Items []map[string]any }
	r := s.do(call{method: "GET", path: p + "/linguistic-jobs", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &jobs)
	if len(jobs.Items) != 0 {
		t.Errorf("jobs of a project nobody reviewed = %s", r.body)
	}
	// A state outside the vocabulary is a 400, not a silent empty page.
	s.do(call{method: "GET", path: p + "/linguistic-jobs?state=pondering", cookie: ada.cookie}).
		want(t, http.StatusBadRequest, "invalid_query")
	// A job that isn't there is a 404, named or malformed.
	s.do(call{method: "GET", path: p + "/linguistic-jobs/0192f5a1-0000-7000-8000-000000000001", cookie: ada.cookie}).
		want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: p + "/linguistic-jobs/not-a-uuid", cookie: ada.cookie}).
		want(t, http.StatusNotFound, "not_found")

	// A review is asked for, and refused by the tenant's own settings.
	// The refusal is a job — 201, `failed`, with the code that says what
	// to change — because a refusal on the record is worth more than a
	// 4xx nobody kept.
	var refused struct {
		State       string `json:"state"`
		FailureCode string `json:"failure_code"`
	}
	created := s.do(call{method: "POST", path: p + "/linguistic-jobs", cookie: ada.cookie, csrf: ada.csrf, body: body})
	created.want(t, http.StatusCreated, "")
	created.decode(t, &refused)
	if refused.State != "failed" || refused.FailureCode != "provider_consent" {
		t.Errorf("job = %s/%s, want failed/provider_consent: %s", refused.State, refused.FailureCode, created.body)
	}
	// And it is on the record, where a list can find it.
	r = s.do(call{method: "GET", path: p + "/linguistic-jobs?state=failed", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &jobs)
	if len(jobs.Items) != 1 {
		t.Errorf("failed jobs = %s, want the one that was refused", r.body)
	}

	// The session cookie alone cannot ask for one, and a read token may
	// look but not spend the tenant's AI budget.
	s.do(call{method: "POST", path: p + "/linguistic-jobs", cookie: ada.cookie, body: body}).
		want(t, http.StatusForbidden, "csrf_invalid")
	var readToken struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "reader", "scopes": []string{"read"}}}).decode(t, &readToken)
	s.do(call{method: "GET", path: p + "/linguistic-jobs", bearer: readToken.Secret}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: p + "/linguistic-jobs", bearer: readToken.Secret, body: body}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{
		method: "POST", bearer: readToken.Secret,
		path: p + "/linguistic-jobs/0192f5a1-0000-7000-8000-000000000001/cancellation",
	}).want(t, http.StatusForbidden, "forbidden")
}
