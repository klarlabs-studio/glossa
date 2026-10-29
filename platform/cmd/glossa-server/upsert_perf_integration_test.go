//go:build integration

package main

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// upsertBudget is how long a full 500-item bulk write may take over
// HTTP against a local Postgres. The CLI's client timeout is 30 s and
// the batched writes finish in well under a second here; the bound is
// generous for slow CI runners but fails a return to per-item round
// trips (#77: ~140 ms per message, a 500-item batch past 30 s).
// GLOSSA_UPSERT_BUDGET overrides it.
func upsertBudget(t *testing.T) time.Duration {
	if v := os.Getenv("GLOSSA_UPSERT_BUDGET"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("GLOSSA_UPSERT_BUDGET: %v", err)
		}
		return d
	}
	return 5 * time.Second
}

// TestBulkUpsertPerformance pushes a full batch of messages (create,
// then revise every source) and imports a full batch of translations
// (create, then revise), through the real server, and checks each
// request finishes within the budget and did the work.
func TestBulkUpsertPerformance(t *testing.T) {
	s := startServer(t)
	p, secret := perfProject(t, s)

	const n = 500
	budget := upsertBudget(t)
	messages := func(round int) []map[string]any { return perfMessages(n, round) }
	translations := func(round int) []map[string]any { return perfTranslations(n, round) }
	type results struct {
		Results []struct {
			Status string `json:"status"`
			Error  *struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			} `json:"error"`
		} `json:"results"`
	}
	timed := func(name, path string, items []map[string]any, wantStatus string) {
		t.Helper()
		start := time.Now()
		r := s.do(call{method: "POST", path: path, bearer: secret, body: map[string]any{"items": items}})
		took := time.Since(start)
		r.want(t, http.StatusOK, "")
		var out results
		r.decode(t, &out)
		if len(out.Results) != n {
			t.Fatalf("%s: %d results, want %d", name, len(out.Results), n)
		}
		for i, res := range out.Results {
			if res.Status != wantStatus {
				t.Fatalf("%s: item %d = %s %+v, want %s", name, i, res.Status, res.Error, wantStatus)
			}
		}
		t.Logf("%s: %d items in %v (%.2f ms/item)", name, n, took, float64(took.Microseconds())/1000/n)
		if took > budget {
			t.Errorf("%s: %d items took %v, budget %v", name, n, took, budget)
		}
	}
	timed("message-upserts create", p+"/message-upserts", messages(1), "created")
	timed("message-upserts revise", p+"/message-upserts", messages(2), "revised")
	timed("message-upserts unchanged", p+"/message-upserts", messages(2), "unchanged")
	timed("translation-imports create", p+"/translation-imports", translations(1), "created")
	timed("message-upserts revise (translations go outdated)", p+"/message-upserts", messages(3), "revised")
	timed("translation-imports revise", p+"/translation-imports", translations(2), "revised")
}

// perfMessages is a full batch of message upserts; round changes every
// source text.
func perfMessages(n, round int) []map[string]any {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{
			"key":         fmt.Sprintf("screen%02d.label%03d", i%20, i),
			"text":        fmt.Sprintf("{count, plural, one {# item %d} other {# items %d}} r%d", i, i, round),
			"description": "perf",
		}
	}
	return items
}

// perfTranslations is a full batch of "de" translation imports for
// perfMessages' keys; round changes every text.
func perfTranslations(n, round int) []map[string]any {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{
			"key":    fmt.Sprintf("screen%02d.label%03d", i%20, i),
			"locale": "de",
			"text":   fmt.Sprintf("{count, plural, one {# Artikel %d} other {# Artikel %d}} r%d", i, i, round),
		}
	}
	return items
}

// perfProject creates a tenant with a project in "en" with "de" added,
// and a write token. It returns the project's path and the token.
func perfProject(t *testing.T, s *server) (string, string) {
	t.Helper()
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	p := base + "/projects/" + project.ID
	s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": "de"}}).
		want(t, http.StatusCreated, "")
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "cli", "scopes": []string{"write"}}}).decode(t, &tok)

	return p, tok.Secret
}
