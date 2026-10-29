//go:build integration

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpBearer struct{ token string }

func (b mcpBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// The M5 MCP read tools (RFC 0006 §8) call the services the API calls,
// so a token's project scope answers as it does over HTTP: a project
// outside it is not found — the same answer as a project that does not
// exist — and an API token, which is no assignee and holds no
// assignments.manage, is told there is no work rather than shown any.
func TestOperationsToolsEnforceScopeOverMCP(t *testing.T) {
	s := startServerWith(t, map[string]string{"GLOSSA_MCP_ENABLED": "true"})
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	project := func(slug string) string {
		var p struct{ ID string }
		r := s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"slug": slug, "name": slug, "source_locale": "en",
				"settings": map[string]any{"default_syntax": "mf1", "review_required": false}}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &p)
		return p.ID
	}
	a, b := project("shop"), project("blog")
	var tok struct {
		Secret string `json:"secret"`
	}
	r := s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "agent", "scopes": []string{"read"}, "projects": []string{a}}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &tok)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "ops-test", Version: "0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: s.base + "/mcp",
			HTTPClient: &http.Client{Transport: mcpBearer{token: tok.Secret}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	run := func(name string, args map[string]any) (string, bool) {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return sb.String(), res.IsError
	}

	// The tools exist, and no decision or write tool beside them.
	listed, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range listed.Tools {
		have[tl.Name] = true
		for _, bad := range []string{"approve", "deny", "decide", "accept", "complete", "assign_"} {
			if strings.Contains(tl.Name, bad) {
				t.Errorf("tool %q looks like a decision or write tool", tl.Name)
			}
		}
	}
	for _, n := range []string{"assignments_list", "assignments_report", "workflow_state", "release_requests_list", "rollouts_list"} {
		if !have[n] {
			t.Errorf("tool %s is not registered", n)
		}
	}

	// Inside the token's project they answer.
	for name, args := range map[string]map[string]any{
		"workflow_state":        {"project": a},
		"release_requests_list": {"project": a},
		"rollouts_list":         {"project": a},
		"assignments_report":    {"project": a},
		"assignments_list":      {"project": a},
	} {
		if out, isErr := run(name, args); isErr {
			t.Errorf("%s inside the token's project was refused: %s", name, out)
		}
	}

	// Outside it, each is told from a project that does not exist by nothing.
	missing := uuid.NewString()
	for _, name := range []string{"workflow_state", "release_requests_list", "rollouts_list", "assignments_report", "assignments_list"} {
		outside, outErr := run(name, map[string]any{"project": b})
		none, noneErr := run(name, map[string]any{"project": missing})
		// A list of the caller's own work is empty for both, as over HTTP.
		if name == "assignments_list" && !outErr && !noneErr {
			if outside != none {
				t.Errorf("assignments_list: outside = %q, missing = %q", outside, none)
			}
			continue
		}
		if !outErr || !noneErr {
			t.Errorf("%s: a project outside the token (refused=%v) or one that does not exist (refused=%v) was answered", name, outErr, noneErr)
			continue
		}
		if strings.ReplaceAll(outside, b, "P") != strings.ReplaceAll(none, missing, "P") {
			t.Errorf("%s: outside = %q, missing = %q: the two differ", name, outside, none)
		}
	}

	// An API token is no assignee: it sees no work, and says so.
	out, _ := run("assignments_list", map[string]any{})
	if !strings.Contains(out, "No assignments are visible") {
		t.Errorf("assignments_list as a token = %s", out)
	}
}
