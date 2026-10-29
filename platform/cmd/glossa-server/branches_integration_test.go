//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

type branch struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	PRNumber   int    `json:"pr_number"`
	HeadCommit string `json:"head_commit"`
	PreviewURL string `json:"preview_url"`
	ClosedAt   string `json:"closed_at"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type branchStatus struct {
	Branch          branch         `json:"branch"`
	NewKeys         []string       `json:"new_keys"`
	SourceProposals []string       `json:"source_proposals"`
	Removed         []string       `json:"removed"`
	Outdated        map[string]int `json:"outdated"`
	Conflicts       []struct {
		Key      string   `json:"key"`
		Branches []string `json:"branches"`
	} `json:"conflicts"`
	Items []struct {
		Key    string `json:"key"`
		Status string `json:"status"`
	} `json:"items"`
}

// The Branches API and the branch environments it drives (RFC 0004 §4,
// §9): CI pushes a branch with a write token, the status report says
// what merging it would do, its preview environment appears with
// kind branch, a preview key reads it and a production key doesn't, and
// closing the branch takes the environment away.
func TestBranchesAPIOverHTTP(t *testing.T) {
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
	s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"code": "de"}}).want(t, http.StatusCreated, "")
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"write"}}}).decode(t, &tok)

	// The default branch's push, and a translation of one of its messages.
	s.do(call{method: "POST", path: p + "/message-upserts", bearer: tok.Secret, body: map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay now"}, {"key": "home.title", "text": "Welcome"},
	}}}).want(t, http.StatusOK, "")
	s.do(call{method: "PUT", path: p + "/messages/checkout.pay/translations/de", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"text": "Jetzt bezahlen", "state": "approved"}}).want(t, http.StatusCreated, "")

	// A branch push from CI: a new key, a changed source, a removed key.
	commit := strings.Repeat("9f2c1e7a", 5)
	r := s.do(call{method: "POST", path: p + "/branch-pushes", bearer: tok.Secret, body: map[string]any{
		"branch": "feature/checkout-copy", "pr_number": 42, "head_commit": commit, "complete": true,
		"items": []map[string]any{
			{"key": "checkout.pay", "text": "Pay securely"},
			{"key": "checkout.tip", "text": "Add a tip"},
		},
	}})
	r.want(t, http.StatusOK, "")
	var pushed branchStatus
	r.decode(t, &pushed)
	if pushed.Branch.Name != "feature/checkout-copy" || pushed.Branch.State != "open" || pushed.Branch.PRNumber != 42 ||
		pushed.Branch.HeadCommit != commit {
		t.Fatalf("branch = %s", r.body)
	}
	if len(pushed.NewKeys) != 1 || pushed.NewKeys[0] != "checkout.tip" ||
		len(pushed.SourceProposals) != 1 || pushed.SourceProposals[0] != "checkout.pay" ||
		len(pushed.Removed) != 1 || pushed.Removed[0] != "home.title" || pushed.Outdated["de"] != 1 {
		t.Errorf("report = %s", r.body)
	}
	if len(pushed.Items) != 2 || pushed.Items[0].Status != "source_proposal" || pushed.Items[1].Status != "new_key" {
		t.Errorf("items = %s", r.body)
	}
	// Nothing live changed: the branch's source is a proposal.
	var live message
	s.do(call{method: "GET", path: p + "/messages/checkout.pay", bearer: tok.Secret}).decode(t, &live)
	if live.Source.Text != "Pay now" || live.SourceRevision != 1 {
		t.Errorf("live message = %+v", live)
	}
	var proposed message
	s.do(call{method: "GET", path: p + "/messages/checkout.tip", bearer: tok.Secret}).decode(t, &proposed)
	if proposed.State != "proposed" {
		t.Errorf("new key = %+v", proposed)
	}

	// Branches are listed by name and filtered by state; URLs use the ID.
	var list struct{ Items []branch }
	s.do(call{method: "GET", path: p + "/branches?state=open", bearer: tok.Secret}).decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].ID != pushed.Branch.ID {
		t.Fatalf("list = %+v", list.Items)
	}
	s.do(call{method: "GET", path: p + "/branches?name=feature%2Fcheckout-copy", bearer: tok.Secret}).decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "feature/checkout-copy" {
		t.Errorf("by name = %+v", list.Items)
	}
	s.do(call{method: "GET", path: p + "/branches?state=merged", bearer: tok.Secret}).decode(t, &list)
	if len(list.Items) != 0 {
		t.Errorf("merged = %+v", list.Items)
	}
	s.do(call{method: "GET", path: p + "/branches?state=dangling", bearer: tok.Secret}).want(t, http.StatusBadRequest, "invalid_branch_state")
	b := p + "/branches/" + pushed.Branch.ID

	// The status report again, and the proposals behind it.
	var status branchStatus
	s.do(call{method: "GET", path: b, bearer: tok.Secret}).decode(t, &status)
	if len(status.NewKeys) != 1 || len(status.SourceProposals) != 1 || status.Outdated["de"] != 1 {
		t.Errorf("status = %+v", status)
	}
	var proposals struct {
		Items []struct {
			Key          string `json:"key"`
			Kind         string `json:"kind"`
			BaseRevision int    `json:"base_revision"`
			Source       struct {
				Text string `json:"text"`
			} `json:"source"`
		}
	}
	s.do(call{method: "GET", path: b + "/proposals", bearer: tok.Secret}).decode(t, &proposals)
	if len(proposals.Items) != 2 || proposals.Items[0].Kind != "source_change" || proposals.Items[0].BaseRevision != 1 ||
		proposals.Items[0].Source.Text != "Pay securely" || proposals.Items[1].Kind != "new_key" {
		t.Errorf("proposals = %+v", proposals.Items)
	}

	// CI registers the preview deployment.
	r = s.do(call{method: "PUT", path: b + "/preview", bearer: tok.Secret,
		body: map[string]string{"url": "https://pr-42.preview.example.com"}})
	r.want(t, http.StatusOK, "")
	var withPreview branch
	r.decode(t, &withPreview)
	if withPreview.PreviewURL != "https://pr-42.preview.example.com" {
		t.Errorf("preview = %+v", withPreview)
	}
	s.do(call{method: "PUT", path: b + "/preview", bearer: tok.Secret, body: map[string]string{"url": "javascript:alert(1)"}}).
		want(t, http.StatusBadRequest, "invalid_preview_url")

	// The push opened the branch's environment, with kind and branch on it.
	env := awaitBranchEnvironment(t, s, p, tok.Secret, "pr-42")
	if env.Kind != "branch" || env.Branch != "feature/checkout-copy" {
		t.Fatalf("branch environment = %+v", env)
	}

	// Delivery keys carry their scope; a new key reads production only.
	var prod, preview struct {
		ID    string `json:"id"`
		Key   string `json:"key"`
		Scope struct {
			Environments []string `json:"environments"`
			Branches     bool     `json:"branches"`
		} `json:"scope"`
	}
	r = s.do(call{method: "POST", path: p + "/delivery-keys", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{"name": "web"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &prod)
	if len(prod.Scope.Environments) != 1 || prod.Scope.Environments[0] != "production" || prod.Scope.Branches {
		t.Errorf("a new key reads %+v", prod.Scope)
	}
	r = s.do(call{method: "POST", path: p + "/delivery-keys", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"name": "previews", "scope": map[string]any{"environments": []string{"preview"}, "branches": true},
	}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &preview)
	if !preview.Scope.Branches || len(preview.Scope.Environments) != 1 {
		t.Errorf("preview key reads %+v", preview.Scope)
	}
	s.do(call{method: "POST", path: p + "/delivery-keys", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"name": "bad", "scope": map[string]any{"environments": []string{"pr-42"}, "branches": false},
	}}).want(t, http.StatusBadRequest, "invalid_key_scope")

	r = s.do(call{method: "PUT", path: p + "/delivery-keys/" + prod.ID + "/scope", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"environments": []string{"production", "staging"}, "branches": false}})
	r.want(t, http.StatusOK, "")
	r.decode(t, &prod)
	if len(prod.Scope.Environments) != 2 {
		t.Errorf("after the scope change: %+v", prod.Scope)
	}

	// Closing the branch destroys its environment.
	r = s.do(call{method: "POST", path: b + "/closure", bearer: tok.Secret})
	r.want(t, http.StatusOK, "")
	var closed branch
	r.decode(t, &closed)
	if closed.State != "closed" || closed.ClosedAt == "" {
		t.Errorf("closed = %+v", closed)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		reply := s.do(call{method: "GET", path: p + "/environments/pr-42", bearer: tok.Secret})
		if reply.status == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the closed branch's environment is still there: %d %s", reply.status, reply.body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// A merged branch takes no more pushes.
	s.do(call{method: "POST", path: b + "/merge", bearer: tok.Secret}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: p + "/branch-pushes", bearer: tok.Secret, body: map[string]any{
		"branch": "feature/checkout-copy", "items": []map[string]any{{"key": "checkout.tip", "text": "Add a tip"}},
	}}).want(t, http.StatusConflict, "branch_merged")
}

type environmentJSON struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Branch string `json:"branch"`
}

// awaitBranchEnvironment waits for the outbox to open the environment
// the branch's push asked for.
func awaitBranchEnvironment(t *testing.T, s *server, project, bearer, name string) environmentJSON {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := s.do(call{method: "GET", path: project + "/environments/" + name, bearer: bearer})
		if r.status == http.StatusOK {
			var env environmentJSON
			r.decode(t, &env)
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never opened: %d %s", name, r.status, r.body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
