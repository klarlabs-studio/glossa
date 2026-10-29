//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// metrics is the server's /metrics page.
func (s *server) metrics() string {
	s.t.Helper()
	resp, err := http.Get(s.base + "/metrics")
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// usagesDoc is a glossa.usages/v1 document of the web application.
func usagesDoc(commit, branch string, usages ...map[string]any) map[string]any {
	return map[string]any{
		"schema": "glossa.usages/v1", "application": "web", "commit": commit, "branch": branch,
		"tool": map[string]any{"name": "@klarlabs-studio/glossa", "version": "0.1.0"}, "usages": usages,
		"future_field": "ignored within v1",
	}
}

func usageAt(key, file string, line int, component, route string) map[string]any {
	u := map[string]any{"key": key, "file": file, "line": line, "column": 5, "kind": "t"}
	if component != "" {
		u["component"] = component
	}
	if route != "" {
		u["route"] = route
	}
	return u
}

// The Context API (RFC 0004 §9): CI uploads a build's usages with a
// write token; members read where messages appear.
func TestContextAPIOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	var project struct {
		ID       string
		Settings struct {
			DefaultBranch string `json:"default_branch"`
		}
	}
	s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	if project.Settings.DefaultBranch != "main" {
		t.Errorf("default branch of a new project = %q", project.Settings.DefaultBranch)
	}
	p := base + "/projects/" + project.ID
	s.do(call{method: "POST", path: p + "/applications", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "web", "name": "Web", "platform": "web"}}).want(t, http.StatusCreated, "")
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"write"}}}).decode(t, &tok)
	s.do(call{method: "POST", path: p + "/message-upserts", bearer: tok.Secret, body: map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay"}, {"key": "checkout.total", "text": "Total"}, {"key": "nav.home", "text": "Home"},
	}}}).want(t, http.StatusOK, "")

	// The project's default branch decides, not the document.
	r := s.do(call{method: "PATCH", path: p, cookie: ada.cookie, csrf: ada.csrf, headers: map[string]string{"If-Match": `"1"`},
		body: map[string]any{"settings": map[string]any{"default_syntax": "mf1", "review_required": true, "default_branch": "x..y"}}})
	r.want(t, http.StatusBadRequest, "invalid_branch")

	commit := strings.Repeat("9f2c1e7a", 5)
	doc := usagesDoc(commit, "main",
		usageAt("checkout.pay", "src/Payment.vue", 9, "Payment", "/checkout"),
		usageAt("checkout.total", "src/Payment.vue", 12, "Payment", "/checkout"),
		usageAt("gone.key", "src/Old.vue", 1, "", ""))
	upload := call{method: "POST", path: p + "/context-builds?source=plugin", bearer: tok.Secret, body: doc}
	var build struct {
		ID              string `json:"id"`
		OnDefaultBranch bool   `json:"on_default_branch"`
		Usages          int    `json:"usages"`
		UnknownKeys     int    `json:"unknown_keys"`
		Digest          string `json:"digest"`
		Source          string `json:"source"`
	}
	r = s.do(upload)
	r.want(t, http.StatusCreated, "")
	r.decode(t, &build)
	if !build.OnDefaultBranch || build.Usages != 3 || build.UnknownKeys != 1 || build.Source != "plugin" || len(build.Digest) != 64 {
		t.Fatalf("build = %s", r.body)
	}
	// The same document again is a replay.
	r = s.do(upload)
	r.want(t, http.StatusOK, "")
	if r.header.Get("Idempotent-Replayed") != "true" || !strings.Contains(string(r.body), build.ID) {
		t.Errorf("replay = %d %v %s", r.status, r.header, r.body)
	}

	bad := usagesDoc(commit[:7], "main", usageAt("checkout.pay", "src/a.vue", 1, "", ""))
	s.do(call{method: "POST", path: p + "/context-builds?source=plugin", bearer: tok.Secret, body: bad}).
		want(t, http.StatusBadRequest, "invalid_usages")
	s.do(call{method: "POST", path: p + "/context-builds?source=bundler", bearer: tok.Secret, body: doc}).
		want(t, http.StatusBadRequest, "invalid_source")
	other := usagesDoc(commit, "main", usageAt("checkout.pay", "src/a.swift", 1, "", ""))
	other["application"] = "ios"
	s.do(call{method: "POST", path: p + "/context-builds?source=plugin", bearer: tok.Secret, body: other}).
		want(t, http.StatusBadRequest, "unknown_application")

	var builds struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/context-builds?application=web", cookie: ada.cookie}).decode(t, &builds)
	if len(builds.Items) != 1 || builds.Items[0].ID != build.ID {
		t.Errorf("builds = %+v", builds)
	}

	var usages struct {
		MessageID string `json:"message_id"`
		Truncated bool   `json:"truncated"`
		Usages    []struct {
			File      string `json:"file"`
			Line      int    `json:"line"`
			Component string `json:"component"`
			Route     string `json:"route"`
		} `json:"usages"`
	}
	s.do(call{method: "GET", path: p + "/messages/checkout.pay/usages", cookie: ada.cookie}).decode(t, &usages)
	if len(usages.Usages) != 1 || usages.Usages[0].File != "src/Payment.vue" || usages.Usages[0].Route != "/checkout" ||
		usages.Truncated || usages.MessageID == "" {
		t.Errorf("usages of checkout.pay = %+v", usages)
	}
	s.do(call{method: "GET", path: p + "/messages/nope.none/usages", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: p + "/messages/checkout.pay/usages?branch=a..b", cookie: ada.cookie}).
		want(t, http.StatusBadRequest, "invalid_branch")

	var onRoute struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
		NextPageToken string `json:"next_page_token"`
	}
	s.do(call{method: "GET", path: p + "/usages?route=/checkout&page_size=1", cookie: ada.cookie}).decode(t, &onRoute)
	if len(onRoute.Items) != 1 || onRoute.Items[0].Key != "checkout.pay" || onRoute.NextPageToken == "" {
		t.Errorf("first page on /checkout = %+v", onRoute)
	}

	var unused struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
		CurrentBuilds  int `json:"current_builds"`
		ActiveMessages int `json:"active_messages"`
		UnusedMessages int `json:"unused_messages"`
	}
	s.do(call{method: "GET", path: p + "/unused-messages", cookie: ada.cookie}).decode(t, &unused)
	if len(unused.Items) != 1 || unused.Items[0].Key != "nav.home" || unused.CurrentBuilds != 1 ||
		unused.ActiveMessages != 3 || unused.UnusedMessages != 1 {
		t.Errorf("unused = %+v", unused)
	}

	// Coverage is measured after the default-branch build.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.metrics(), `glossa_context_coverage_ratio{project="`+project.ID+`",tenant="`+org.ID+`"} 0.6666`) {
		if time.Now().After(deadline) {
			t.Fatalf("no coverage metric:\n%s", s.metrics())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(s.metrics(), `glossa_context_usages_ingested_total{source="plugin"} 3`) {
		t.Error("usages ingested not counted")
	}
}

// The Context context (RFC 0004) is composed into the server: deleting
// a project erases its builds through Context's outbox subscriber (the
// build is seeded in SQL).
func TestContextIsComposed(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	var application struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects/" + project.ID + "/applications", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "web", "name": "Web", "platform": "web"}}).decode(t, &application)

	ctx := context.Background()
	if _, err := s.db.Super.Exec(ctx, `
		INSERT INTO context_builds (id, tenant_id, project_id, application_id, commit_sha, branch, on_default_branch,
		                            source, tool_name, digest, usage_count, created_by, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, 'abcdef1', 'main', true, 'plugin', '@klarlabs-studio/glossa/unplugin', $4, 0, 'test', now())`,
		org.ID, project.ID, application.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}

	s.do(call{method: "DELETE", path: base + "/projects/" + project.ID, cookie: ada.cookie, csrf: ada.csrf}).
		want(t, http.StatusNoContent, "")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		if err := s.db.Super.QueryRow(ctx, "SELECT count(*) FROM context_builds WHERE project_id = $1", project.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the deleted project's builds were never erased")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestThePurgeJobRunsInTheServer boots the real composition root with a
// one-second purge interval and watches the daily retention job
// (RFC 0004 §2.3) delete the builds beyond the three-build window, with
// its deletions and its run on /metrics (§11).
func TestThePurgeJobRunsInTheServer(t *testing.T) {
	s := startServerWith(t, map[string]string{
		"GLOSSA_PURGE_INTERVAL":      "1s",
		"GLOSSA_PURGE_TIMEOUT":       "1s",
		"GLOSSA_PURGE_LEASE":         "2s",
		"GLOSSA_PURGE_POLL_INTERVAL": "200ms",
		"GLOSSA_PURGE_JITTER":        "0",
	})
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
	s.do(call{method: "POST", path: p + "/message-upserts", bearer: tok.Secret,
		body: map[string]any{"items": []map[string]any{{"key": "checkout.pay", "text": "Pay"}}}}).want(t, http.StatusOK, "")

	// Five builds of one application on the default branch: retention
	// keeps three.
	for i := range 5 {
		doc := usagesDoc(strings.Repeat(fmt.Sprintf("%08x", 0x9f2c1e00+i), 5), "main",
			usageAt("checkout.pay", "src/Payment.vue", i+1, "Payment", "/checkout"))
		s.do(call{method: "POST", path: p + "/context-builds?source=plugin", bearer: tok.Secret, body: doc}).
			want(t, http.StatusCreated, "")
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		var n int
		if err := s.db.Super.QueryRow(context.Background(),
			"SELECT count(*) FROM context_builds WHERE project_id = $1", project.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the purge left %d builds, want 3\nlogs:\n%s", n, s.logs)
		}
		time.Sleep(100 * time.Millisecond)
	}

	page := s.metrics()
	for _, want := range []string{
		`glossa_context_purge_deletions_total{kind="build"} 2`,
		`glossa_scheduler_runs_total{job="catalog.proposals",outcome="ok"}`,
		`glossa_scheduler_runs_total{job="context.purge",outcome="ok"}`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
}
