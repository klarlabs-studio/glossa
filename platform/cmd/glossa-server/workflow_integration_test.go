//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowDoc reads one of the M5 exit test's workflow documents, the
// same files §12.1 saves, so this test and the exit test agree on what
// the API accepts.
func workflowDoc(t *testing.T, file string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "systemtest", "m5", "testdata", "workflows", file))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// The Workflow API (RFC 0006 §8, wave 2) through HTTP against Postgres:
// definition → version → lint → bind → resolve, the three refusals of
// §12.1, the permission split, and another tenant seeing none of it.
func TestWorkflowAPIOverHTTP(t *testing.T) {
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
	write := func(method, path string, body any, headers ...string) reply {
		c := call{method: method, path: path, cookie: ada.cookie, csrf: ada.csrf, body: body}
		if len(headers) == 2 {
			c.headers = map[string]string{headers[0]: headers[1]}
		}
		return s.do(c)
	}

	// §12.1's three refusals: 422 `invalid_workflow`, every finding in
	// the problem, nothing stored.
	for _, file := range []string{"invalid-unknown-guard.json", "invalid-unreachable-state.json", "invalid-delayed-transition.json"} {
		r := write("POST", base+"/workflow-definitions", workflowDoc(t, file))
		r.want(t, http.StatusUnprocessableEntity, "invalid_workflow")
		var prob struct {
			Findings []struct{ Rule, Severity, Path, Message string }
		}
		r.decode(t, &prob)
		if len(prob.Findings) == 0 || prob.Findings[0].Rule == "" || prob.Findings[0].Message == "" {
			t.Errorf("%s: 422 without findings: %s", file, r.body)
		}
	}

	// Lint says the same without storing anything.
	var lint struct {
		Valid    bool
		Findings []struct{ Rule string }
	}
	write("POST", base+"/workflow-definition-lints", workflowDoc(t, "invalid-unreachable-state.json")).decode(t, &lint)
	if lint.Valid || len(lint.Findings) == 0 {
		t.Errorf("lint of an unreachable state = %+v", lint)
	}
	r := write("POST", base+"/workflow-definition-lints", workflowDoc(t, "vendor-then-four-eyes.json"))
	r.want(t, http.StatusOK, "")
	r.decode(t, &lint)
	if !lint.Valid {
		t.Errorf("lint of vendor-then-four-eyes = %s", r.body)
	}

	// Create.
	var def struct {
		ID      string
		Name    string
		Version int
	}
	r = write("POST", base+"/workflow-definitions", workflowDoc(t, "vendor-then-four-eyes.json"))
	r.want(t, http.StatusCreated, "")
	r.decode(t, &def)
	if def.Version != 1 || def.Name != "vendor-then-four-eyes" || r.header.Get("ETag") != `"1"` {
		t.Fatalf("created = %s %v", r.body, r.header)
	}
	write("POST", base+"/workflow-definitions", workflowDoc(t, "vendor-then-four-eyes.json")).
		want(t, http.StatusConflict, "workflow_definition_exists")
	var defs struct {
		Items []struct{ ID, Name string }
	}
	s.do(call{method: "GET", path: base + "/workflow-definitions?page_size=100", cookie: ada.cookie}).decode(t, &defs)
	// The tenant's seeded default ("review", RFC 0006 §2.3) may be
	// listed beside it: the instance runner seeds it on the tenant's
	// creation event, asynchronously.
	var mine []string
	for _, it := range defs.Items {
		if it.Name != "review" {
			mine = append(mine, it.ID)
		}
	}
	if len(mine) != 1 || mine[0] != def.ID {
		t.Fatalf("definitions = %+v", defs)
	}

	// Version: If-Match is the version the author edited.
	d := def.ID
	write("POST", base+"/workflow-definitions/"+d+"/versions", workflowDoc(t, "vendor-then-four-eyes.json")).
		want(t, http.StatusPreconditionRequired, "precondition_required")
	r = write("POST", base+"/workflow-definitions/"+d+"/versions", workflowDoc(t, "vendor-then-four-eyes.json"), "If-Match", `"1"`)
	r.want(t, http.StatusCreated, "")
	r.decode(t, &def)
	if def.Version != 2 || r.header.Get("ETag") != `"2"` {
		t.Fatalf("v2 = %s", r.body)
	}
	write("POST", base+"/workflow-definitions/"+d+"/versions", workflowDoc(t, "vendor-then-four-eyes.json"), "If-Match", `"1"`).
		want(t, http.StatusPreconditionFailed, "precondition_failed")
	var versions struct {
		Items []struct {
			Version  int
			Document map[string]any
		}
	}
	s.do(call{method: "GET", path: base + "/workflow-definitions/" + d + "/versions", cookie: ada.cookie}).decode(t, &versions)
	if len(versions.Items) != 2 || versions.Items[0].Version != 2 || versions.Items[1].Document["name"] != "vendor-then-four-eyes" {
		t.Errorf("versions = %+v", versions)
	}
	s.do(call{method: "GET", path: base + "/workflow-definitions/" + d + "/versions/1", cookie: ada.cookie}).want(t, http.StatusOK, "")
	s.do(call{method: "GET", path: base + "/workflow-definitions/" + d + "/versions/3", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")

	// Bind, for `de`, and resolve.
	var binding struct{ ID string }
	r = write("POST", p+"/workflow-bindings", map[string]any{"definition_id": d, "locales": []string{"de"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &binding)
	write("POST", p+"/workflow-bindings", map[string]any{"definition_id": d, "locales": []string{"de"}}).
		want(t, http.StatusConflict, "workflow_binding_exists")
	var res struct {
		Bound          bool
		DefinitionName string `json:"definition_name"`
		Version        int
		Binding        struct{ ID string }
	}
	s.do(call{method: "GET", path: p + "/workflow-resolution?locale=de", cookie: ada.cookie}).decode(t, &res)
	if !res.Bound || res.Binding.ID != binding.ID || res.DefinitionName != "vendor-then-four-eyes" || res.Version != 2 {
		t.Errorf("de resolves to %+v", res)
	}
	s.do(call{method: "GET", path: p + "/workflow-resolution?locale=fr", cookie: ada.cookie}).decode(t, &res)
	if res.Bound {
		t.Errorf("fr resolves to %+v; only de is bound", res)
	}

	// Instances are the runner's, and nothing has been translated: the
	// list is empty, and answered.
	var instances struct{ Items []any }
	r = s.do(call{method: "GET", path: p + "/workflow-instances", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &instances)
	if len(instances.Items) != 0 {
		t.Errorf("instances = %s", r.body)
	}

	// The writes were announced with their actor, in their transactions.
	var events []struct{ Type, Actor string }
	rows, err := s.db.Super.Query(t.Context(), `SELECT event_type, actor FROM outbox_events WHERE event_type LIKE 'workflow.%' ORDER BY occurred_at`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var e struct{ Type, Actor string }
		if err := rows.Scan(&e.Type, &e.Actor); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	rows.Close()
	if len(events) != 3 || events[0].Type != "workflow.definition_saved" || events[2].Type != "workflow.binding_changed" {
		t.Errorf("workflow events = %+v", events)
	}
	for _, e := range events {
		if !strings.HasPrefix(e.Actor, "person:") {
			t.Errorf("%s names actor %q", e.Type, e.Actor)
		}
	}

	// A translator reads, and may not write.
	s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "bob@example.com", "roles": []string{"translator"}, "locales": []string{"de"}}}).
		want(t, http.StatusCreated, "")
	bob := s.signIn("bob@example.com")
	s.do(call{method: "GET", path: p + "/workflow-bindings", cookie: bob.cookie}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: base + "/workflow-definitions", cookie: bob.cookie, csrf: bob.csrf,
		body: workflowDoc(t, "vendor-then-four-eyes.json")}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "DELETE", path: p + "/workflow-bindings/" + binding.ID, cookie: bob.cookie, csrf: bob.csrf}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "GET", path: base + "/workflow-definitions"}).want(t, http.StatusUnauthorized, "unauthenticated")

	// Another tenant — even one ada also owns — sees none of it.
	var other struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "beta", "name": "Beta"}}).decode(t, &other)
	ob := "/v1/tenants/" + other.ID
	var otherProject struct{ ID string }
	s.do(call{method: "POST", path: ob + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "blog", "name": "Blog", "source_locale": "en"}}).decode(t, &otherProject)
	s.do(call{method: "GET", path: ob + "/workflow-definitions/" + d, cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: ob + "/workflow-definitions/" + d + "/versions", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "POST", path: ob + "/projects/" + otherProject.ID + "/workflow-bindings", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"definition_id": d}}).want(t, http.StatusNotFound, "not_found")
	// Acme's project, addressed under beta, is not beta's.
	s.do(call{method: "GET", path: ob + "/projects/" + project.ID + "/workflow-bindings", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "DELETE", path: ob + "/projects/" + project.ID + "/workflow-bindings/" + binding.ID, cookie: ada.cookie, csrf: ada.csrf}).
		want(t, http.StatusNotFound, "not_found")
	var none struct{ Items []struct{ ID, Name string } }
	s.do(call{method: "GET", path: ob + "/workflow-definitions", cookie: ada.cookie}).decode(t, &none)
	for _, it := range none.Items {
		if it.Name != "review" {
			t.Errorf("beta lists a definition it did not make: %+v", it)
		}
	}

	// Unbind and delete.
	write("DELETE", p+"/workflow-bindings/"+binding.ID, nil).want(t, http.StatusNoContent, "")
	write("DELETE", base+"/workflow-definitions/"+d, nil).want(t, http.StatusNoContent, "")
	write("DELETE", base+"/workflow-definitions/"+d, nil).want(t, http.StatusNotFound, "not_found")
}
