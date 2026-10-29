//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
)

// Release approvals and staged rollouts end to end (RFC 0006 §5, §12.3,
// §12.4): over HTTP against Postgres and MinIO, read back from a real
// glossa-edge, with the release-approval workflow running in the
// server's own outbox — no step of it is called by the test.

type edgeView struct {
	Release struct {
		ID string `json:"id"`
	} `json:"release"`
	Rollout *struct {
		ID        string `json:"id"`
		Percent   int    `json:"percent"`
		Candidate struct {
			Release struct {
				ID string `json:"id"`
			} `json:"release"`
		} `json:"candidate"`
	} `json:"rollout"`
}

// edgeOf reads what the edge serves now.
func edgeOf(t *testing.T, url string) edgeView {
	t.Helper()
	resp, body := get(t, url)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edge %d: %s", resp.StatusCode, body)
	}
	var v edgeView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// until polls the edge until ok holds, and fails with what it served.
func until(t *testing.T, url, what string, ok func(edgeView) bool) edgeView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		v := edgeOf(t, url)
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("the edge never served %s: it serves %+v", what, v)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stays asserts the edge keeps serving release for a while — longer than
// the outbox, the workflow and the edge's manifest TTL need to change it.
func stays(t *testing.T, url, release, when string) {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	if v := edgeOf(t, url); v.Release.ID != release {
		t.Fatalf("%s the edge serves %s, not %s: a pointer moved", when, v.Release.ID, release)
	}
}

type approvalsWorld struct {
	s           *server
	ada         session
	base, p     string
	manifestURL string
}

func (w approvalsWorld) as(who session, method, path string, body any, headers ...string) reply {
	c := call{method: method, path: path, cookie: who.cookie, csrf: who.csrf, body: body, headers: map[string]string{}}
	for i := 0; i+1 < len(headers); i += 2 {
		c.headers[headers[i]] = headers[i+1]
	}
	return w.s.do(c)
}

// revise changes the German text, so the next release differs.
func (w approvalsWorld) revise(t *testing.T, text string) {
	t.Helper()
	path := w.p + "/messages/checkout.pay/translations/de"
	var headers []string
	if cur := w.as(w.ada, "GET", path, nil); cur.status == http.StatusOK {
		headers = []string{"If-Match", cur.header.Get("ETag")}
	}
	r := w.as(w.ada, "PUT", path, map[string]string{"text": text, "state": "approved", "origin": "human"}, headers...)
	if r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("revise: %d %s", r.status, r.body)
	}
}

func newApprovalsWorld(t *testing.T) approvalsWorld {
	t.Helper()
	ctx := context.Background()
	minio, err := s3test.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(minio.Close)
	if _, err := minio.Store(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	vars := s3Vars(minio)
	vars["GLOSSA_RELEASE_SIGNING_KEYS"] = "e2e-2026=" + testSigningSeed
	s := startServerWith(t, vars)
	edgeURL := startEdge(t, minio)

	w := approvalsWorld{s: s, ada: s.signIn("ada@example.com")}
	var org struct{ ID string }
	w.as(w.ada, "POST", "/v1/tenants", map[string]string{"slug": "acme", "name": "Acme"}).decode(t, &org)
	w.base = "/v1/tenants/" + org.ID
	var project struct{ ID string }
	w.as(w.ada, "POST", w.base+"/projects", map[string]any{"slug": "web", "name": "Web", "source_locale": "en"}).decode(t, &project)
	w.p = w.base + "/projects/" + project.ID
	w.as(w.ada, "POST", w.p+"/locales", map[string]string{"code": "de"}).want(t, http.StatusCreated, "")
	w.as(w.ada, "POST", w.p+"/message-upserts", map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay {amount, number}"},
	}}).want(t, http.StatusOK, "")
	w.revise(t, "{amount, number} bezahlen")
	var key struct{ Key string }
	w.as(w.ada, "POST", w.p+"/delivery-keys", map[string]any{"name": "web"}, "Idempotency-Key", "web").decode(t, &key)
	w.manifestURL = edgeURL + "/v1/" + key.Key + "/production/manifest.json"
	return w
}

func (w approvalsWorld) publish(t *testing.T, want int) (release, request string) {
	t.Helper()
	r := w.as(w.ada, "POST", w.p+"/releases", map[string]string{"environment": "production"})
	r.want(t, want, "")
	var out struct {
		ID        string `json:"id"`
		RequestID string `json:"release_request_id"`
	}
	r.decode(t, &out)
	return out.ID, out.RequestID
}

func TestReleaseApprovalsOverHTTP(t *testing.T) {
	w := newApprovalsWorld(t)
	stable, _ := w.publish(t, http.StatusCreated)
	until(t, w.manifestURL, "the first release", func(v edgeView) bool { return v.Release.ID == stable })

	// Two reviewers, distinct from the requester.
	reviewers := make([]session, 2)
	for i, email := range []string{"rita@example.com", "rolf@example.com"} {
		w.as(w.ada, "POST", w.base+"/members", map[string]any{"email": email, "roles": []string{"reviewer"}, "locales": []string{"de"}}).
			want(t, http.StatusCreated, "")
		reviewers[i] = w.s.signIn(email)
	}
	env := w.as(w.ada, "GET", w.p+"/environments/production", nil)
	env.want(t, http.StatusOK, "")
	var e struct {
		Policy   map[string]any `json:"policy"`
		Approval *struct{ N int }
	}
	env.decode(t, &e)
	saved := w.as(w.ada, "PATCH", w.p+"/environments/production", map[string]any{"policy": e.Policy, "approval": map[string]any{
		"n": 2, "from": map[string]any{"role": "reviewer"}, "distinct_from_requester": true,
	}}, "If-Match", env.header.Get("ETag"))
	saved.want(t, http.StatusOK, "")
	saved.decode(t, &e)
	if e.Approval == nil || e.Approval.N != 2 {
		t.Fatalf("the environment was saved without its approval: %s", saved.body)
	}

	// A publish is held: 202, a request, and the edge unchanged.
	w.revise(t, "Jetzt {amount, number} zahlen")
	candidate, request := w.publish(t, http.StatusAccepted)
	if candidate == "" || request == "" || candidate == stable {
		t.Fatalf("held publish: release %q request %q", candidate, request)
	}
	stays(t, w.manifestURL, stable, "right after a held publish")

	approve := func(who session) reply {
		return w.as(who, "POST", w.p+"/release-requests/"+request+"/approvals", map[string]string{"decision": "granted"})
	}
	// The workflow asks for the approval from the request's event; wait
	// for it, so the refusals below are the decision's and not timing.
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := approve(w.ada)
		if r.problem.Code != "approval_not_requested" {
			// The requester never approves their own release. Ada is an
			// owner, not a reviewer, so the approval does not ask her
			// either; the requester-as-reviewer case (own_text) is
			// Workflow's handler test.
			if r.status != http.StatusForbidden || (r.problem.Code != "own_text" && r.problem.Code != "not_eligible") {
				t.Fatalf("the requester's own approval: %d %s", r.status, r.body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the release-approval workflow never asked for approval")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Nor does a token, whatever its scopes: approving is a person's.
	var tok struct {
		Secret string `json:"secret"`
	}
	w.as(w.ada, "POST", w.base+"/tokens", map[string]any{"name": "ci", "scopes": []string{"read", "write", "publish", "admin", "workflows"}}).
		decode(t, &tok)
	w.s.do(call{method: "POST", path: w.p + "/release-requests/" + request + "/approvals", bearer: tok.Secret,
		body: map[string]string{"decision": "granted"}}).want(t, http.StatusForbidden, "person_required")

	approve(reviewers[0]).want(t, http.StatusCreated, "")
	stays(t, w.manifestURL, stable, "after one approval of two")

	approve(reviewers[1]).want(t, http.StatusCreated, "")
	until(t, w.manifestURL, "the approved release", func(v edgeView) bool { return v.Release.ID == candidate })
	var req struct {
		State     string `json:"state"`
		DecidedBy string `json:"decided_by"`
	}
	w.as(reviewers[0], "GET", w.p+"/release-requests/"+request, nil).decode(t, &req)
	var rolf struct {
		Person struct {
			ID string `json:"id"`
		} `json:"person"`
	}
	w.as(reviewers[1], "GET", "/v1/me", nil).decode(t, &rolf)
	if req.State != "deployed" || rolf.Person.ID == "" || req.DecidedBy != "person:"+rolf.Person.ID {
		t.Fatalf("request = %+v, want deployed by the last approver (person:%s)", req, rolf.Person.ID)
	}

	// Rollback is never held, and takes effect at once.
	w.as(w.ada, "POST", w.p+"/environments/production/rollbacks", map[string]string{"release_id": stable}).want(t, http.StatusOK, "")
	until(t, w.manifestURL, "the rollback target", func(v edgeView) bool { return v.Release.ID == stable })

	// A rollout into an environment that requires approval is refused.
	w.as(w.ada, "POST", w.p+"/environments/production/rollouts", map[string]any{"release_id": candidate, "percent": 10}).
		want(t, http.StatusConflict, "rollout_needs_approval")
}

func TestRolloutsOverHTTP(t *testing.T) {
	w := newApprovalsWorld(t)
	stable, _ := w.publish(t, http.StatusCreated)
	w.revise(t, "Jetzt {amount, number} zahlen")
	var staged struct{ ID string }
	w.as(w.ada, "POST", w.p+"/releases", map[string]string{"environment": "staging"}).decode(t, &staged)
	until(t, w.manifestURL, "the stable release", func(v edgeView) bool { return v.Release.ID == stable })

	rollouts := w.p + "/environments/production/rollouts"
	start := func() (string, string) {
		r := w.as(w.ada, "POST", rollouts, map[string]any{"release_id": staged.ID, "percent": 10})
		r.want(t, http.StatusCreated, "")
		var ro struct{ ID string }
		r.decode(t, &ro)
		return ro.ID, r.header.Get("ETag")
	}
	id, etag := start()
	until(t, w.manifestURL, "the rollout at 10 %", func(v edgeView) bool {
		return v.Release.ID == stable && v.Rollout != nil && v.Rollout.Percent == 10 && v.Rollout.Candidate.Release.ID == staged.ID
	})
	// While it runs, a publish waits; a PATCH needs the ETag.
	w.as(w.ada, "POST", w.p+"/releases", map[string]string{"environment": "production"}).want(t, http.StatusConflict, "rollout_active")
	w.as(w.ada, "PATCH", rollouts+"/"+id, map[string]any{"percent": 50}).want(t, http.StatusPreconditionRequired, "precondition_required")
	w.as(w.ada, "PATCH", rollouts+"/"+id, map[string]any{"percent": 50}, "If-Match", etag).want(t, http.StatusOK, "")
	until(t, w.manifestURL, "the rollout at 50 %", func(v edgeView) bool { return v.Rollout != nil && v.Rollout.Percent == 50 })

	w.as(w.ada, "POST", rollouts+"/"+id+"/abort", nil).want(t, http.StatusOK, "")
	until(t, w.manifestURL, "no rollout after the abort", func(v edgeView) bool { return v.Rollout == nil && v.Release.ID == stable })
	w.as(w.ada, "POST", rollouts+"/"+id+"/abort", nil).want(t, http.StatusConflict, "rollout_ended")

	again, _ := start()
	until(t, w.manifestURL, "the second rollout", func(v edgeView) bool { return v.Rollout != nil && v.Rollout.ID == again })
	w.as(w.ada, "POST", rollouts+"/"+again+"/completion", nil).want(t, http.StatusOK, "")
	until(t, w.manifestURL, "the candidate, completed", func(v edgeView) bool { return v.Release.ID == staged.ID && v.Rollout == nil })

	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	w.as(w.ada, "GET", rollouts, nil).decode(t, &page)
	if len(page.Items) != 2 || page.Items[0].ID != again || page.Items[0].Status != "completed" || page.Items[1].Status != "aborted" {
		t.Fatalf("rollouts = %+v", page)
	}
}
