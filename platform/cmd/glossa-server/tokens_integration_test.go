//go:build integration

package main

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// API tokens' project scope and the opt-in `workflows` scope (RFC 0006
// §4.1, §4.2) through HTTP against Postgres: the token remembers its
// projects, acts inside them, answers outside them as for a project
// that does not exist, and the workflows scope saves definitions while
// `read` cannot.
func TestTokenProjectScopeAndWorkflowsScopeOverHTTP(t *testing.T) {
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
	a, b := project("shop"), project("blog")

	type token struct {
		Token struct {
			ID       string   `json:"id"`
			Scopes   []string `json:"scopes"`
			Projects []string `json:"projects"`
		} `json:"token"`
		Secret string `json:"secret"`
	}
	create := func(body map[string]any) reply {
		return s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf, body: body})
	}
	var wf token
	r := create(map[string]any{"name": "workflow-push", "scopes": []string{"workflows"}, "projects": []string{a}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &wf)
	if !slices.Equal(wf.Token.Scopes, []string{"workflows"}) || !slices.Equal(wf.Token.Projects, []string{a}) {
		t.Fatalf("created token = %+v", wf.Token)
	}
	var got struct {
		Projects []string `json:"projects"`
	}
	s.do(call{method: "GET", path: base + "/tokens/" + wf.Token.ID, cookie: ada.cookie}).decode(t, &got)
	if !slices.Equal(got.Projects, []string{a}) {
		t.Errorf("read back, the token's projects are %v, want [%s]", got.Projects, a)
	}

	doc := workflowDoc(t, "vendor-then-four-eyes.json")
	s.do(call{method: "POST", path: base + "/workflow-definitions?project=" + a, bearer: wf.Secret, body: doc}).
		want(t, http.StatusCreated, "")
	outside := s.do(call{method: "POST", path: base + "/workflow-definitions?project=" + b, bearer: wf.Secret, body: doc})
	missing := s.do(call{method: "POST", path: base + "/workflow-definitions?project=" + uuid.NewString(), bearer: wf.Secret, body: doc})
	outside.want(t, http.StatusNotFound, "not_found")
	if outside.status != missing.status || outside.problem.Code != missing.problem.Code {
		t.Errorf("a project outside the token (%d %s) is told from one that does not exist (%d %s)",
			outside.status, outside.problem.Code, missing.status, missing.problem.Code)
	}
	s.do(call{method: "GET", path: base + "/projects/" + b, bearer: wf.Secret}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: base + "/projects/" + a, bearer: wf.Secret}).want(t, http.StatusOK, "")

	// `read` does not save definitions; the workflows scope is opt-in.
	var ro token
	create(map[string]any{"name": "reader", "scopes": []string{"read", "write", "publish", "admin"}}).decode(t, &ro)
	s.do(call{method: "POST", path: base + "/workflow-definitions?project=" + a, bearer: ro.Secret, body: doc}).
		want(t, http.StatusForbidden, "forbidden")

	create(map[string]any{"name": "bad", "scopes": []string{"read"}, "projects": []string{"not-an-id"}}).
		want(t, http.StatusBadRequest, "invalid_project_scope")
	create(map[string]any{"name": "bad", "scopes": []string{"approve"}}).want(t, http.StatusBadRequest, "invalid_scope")

	// A token a project-scoped token creates is cut to that scope, and
	// naming a project outside it is refused.
	var admin token
	create(map[string]any{"name": "scoped-admin", "scopes": []string{"admin"}, "projects": []string{a}}).decode(t, &admin)
	var child token
	r = s.do(call{method: "POST", path: base + "/tokens", bearer: admin.Secret, body: map[string]any{"name": "child", "scopes": []string{"read"}}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &child)
	if !slices.Equal(child.Token.Projects, []string{a}) {
		t.Errorf("a scoped token's child has projects %v, want [%s]", child.Token.Projects, a)
	}
	s.do(call{method: "POST", path: base + "/tokens", bearer: admin.Secret,
		body: map[string]any{"name": "wider", "scopes": []string{"read"}, "projects": []string{b}}}).
		want(t, http.StatusForbidden, "scope_exceeds_grant")
}
