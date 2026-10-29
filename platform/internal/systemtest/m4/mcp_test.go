//go:build system

package m4_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// MCP (RFC 0005 §12.6), against the same glossa-server, over the same
// streamable-HTTP endpoint an editor would use. The client is the
// `go-sdk` one, not a hand-rolled JSON-RPC pump.

// mcpRow is one observation for the report.
type mcpRow struct {
	Credential, Session, Tool, Outcome string
	OK                                 bool
}

// bearerRT adds the Authorization header to every request.
type bearerRT struct {
	token string
	next  http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(r)
}

func (s *scenario) mcpURL(toolset string) string {
	u := s.d.base + "/mcp"
	if toolset != "" {
		u += "?toolset=" + toolset
	}
	return u
}

func (s *scenario) connect(token, toolset string) (*mcp.ClientSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "glossa-m4-exit", Version: "0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   s.mcpURL(toolset),
		HTTPClient: &http.Client{Transport: bearerRT{token: token, next: http.DefaultTransport}, Timeout: 60 * time.Second},
	}, nil)
}

// call runs a tool and reports whether it was refused, and with what.
func call(sess *mcp.ClientSession, name string, args map[string]any) (text string, refused bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true, err
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), res.IsError, nil
}

func (s *scenario) mcp() {
	// 1. The two credentials MCP does not take, refused at connect.
	//
	// The refusal is by credential kind, in
	// mcp/adapters/identity.Authenticate, before any lookup — so the CI
	// token below is a well-formed CI secret rather than one minted
	// through the GitHub OIDC exchange, and the in-context grant is a
	// real one this test mints through the API.
	ciToken := identity.CITokenPrefix + strings.Repeat("c", 43)
	s.refusedAtConnect("a CI token (`glossa_ci_…`)", ciToken, "CI token")

	origin := strings.TrimSuffix(s.appURL, "/")
	var grant struct {
		Token  string `json:"token"`
		Origin string `json:"origin"`
	}
	s.owner.do(http.MethodPost, s.projectPath("/preview-origins"),
		map[string]string{"origin": origin, "label": "m4 preview deployment"}, http.StatusCreated, nil)
	s.owner.do(http.MethodPost, s.projectPath("/in-context-grants"),
		map[string]string{"origin": origin}, http.StatusCreated, &grant)
	if !strings.HasPrefix(grant.Token, identity.InContextGrantPrefix) {
		s.gap("12.6", "the in-context grant does not look like one: %q", grant.Token)
	}
	s.refusedAtConnect("a live in-context grant (`glossa_ctx_…`)", grant.Token, "in-context grant")

	// 2. A read-only token: it reads, and it is refused on a write.
	read, err := s.connect(s.readToken, "")
	if err != nil {
		s.gap("12.6", "a read-only token could not open a read session: %v", err)
		return
	}
	defer read.Close()
	tools, err := read.ListTools(context.Background(), nil)
	if err != nil {
		s.gap("12.6", "the read session could not list its tools: %v", err)
		return
	}
	names := map[string]bool{}
	for _, t := range tools.Tools {
		names[t.Name] = true
	}
	for _, want := range []string{"catalog_search", "message_get", "usages_get", "findings_list", "check_run"} {
		if !names[want] {
			s.gap("12.6", "the read session does not offer `%s`", want)
		}
	}
	if names["translation_propose"] {
		s.gap("12.6", "the read session offers `translation_propose`")
	}
	s.mcpRows = append(s.mcpRows, mcpRow{
		Credential: "read-only API token", Session: "read", Tool: "tools/list",
		Outcome: fmt.Sprintf("%d tools, none of them a write tool", len(tools.Tools)), OK: true})

	if out, refused, _ := call(read, "catalog_search", map[string]any{
		"project": s.project, "key_prefix": "checkout.", "limit": 10,
	}); refused || !strings.Contains(out, "checkout.") {
		s.gap("12.6", "`catalog_search` did not search the catalog: %s", firstLine(out))
	} else {
		s.mcpRows = append(s.mcpRows, mcpRow{Credential: "read-only API token", Session: "read",
			Tool: "catalog_search", Outcome: "the project's `checkout.` messages", OK: true})
	}
	if out, refused, _ := call(read, "message_get", map[string]any{
		"project": s.project, "key": keyButton,
	}); refused || !strings.Contains(out, keyButton) {
		s.gap("12.6", "`message_get` did not read `%s` with its usages: %s", keyButton, firstLine(out))
	} else {
		s.mcpRows = append(s.mcpRows, mcpRow{Credential: "read-only API token", Session: "read",
			Tool: "message_get", Outcome: "the message with its usages and capture count", OK: true})
	}
	if out, refused, _ := call(read, "check_run", map[string]any{"project": s.project}); refused {
		s.gap("12.6", "`check_run` was refused on a read token: %s", firstLine(out))
	} else {
		s.mcpRows = append(s.mcpRows, mcpRow{Credential: "read-only API token", Session: "read",
			Tool: "check_run", Outcome: "findings and the policy verdict, computed and stored nowhere", OK: true})
	}

	out, refused, _ := call(read, "translation_propose", map[string]any{
		"project": s.project, "key": keyButton, "locale": "fr", "text": "croûte : tiède",
	})
	if !refused {
		s.gap("12.6", "a read-only token proposed a translation: %s", firstLine(out))
	} else {
		s.mcpRows = append(s.mcpRows, mcpRow{Credential: "read-only API token", Session: "read",
			Tool: "translation_propose", Outcome: "**refused** — " + firstLine(out), OK: true})
	}
	// The second lock: the scope alone is not enough.
	if _, err := s.connect(s.readToken, "write"); err == nil {
		s.gap("12.6", "a read-only token opened a write session")
	} else {
		s.mcpRows = append(s.mcpRows, mcpRow{Credential: "read-only API token", Session: "write (asked for)",
			Tool: "connect", Outcome: "**refused** — a write session needs the `write` scope", OK: true})
	}

	// 3. A write token in a write session: it proposes, and what it
	// wrote is in review.
	write, err := s.connect(s.writeToken, "write")
	if err != nil {
		s.gap("12.6", "a write token could not open a write session: %v", err)
		return
	}
	defer write.Close()
	const proposed = "croûte : tiède"
	out, refused, _ = call(write, "translation_propose", map[string]any{
		"project": s.project, "key": keyButton, "locale": "fr", "text": proposed,
	})
	if refused {
		s.gap("12.6", "`translation_propose` was refused in a write session: %s", firstLine(out))
	} else {
		var tr struct {
			State  string `json:"state"`
			Origin string `json:"origin"`
			Text   string `json:"text"`
		}
		s.owner.do(http.MethodGet, s.projectPath("/messages/"+keyButton+"/translations/fr"),
			nil, http.StatusOK, &tr)
		switch {
		case tr.State == "approved":
			s.gap("12.6", "the agent's translation was approved: an agent's write always enters review")
		case tr.Text != proposed:
			s.gap("12.6", "the project's French is %q, want the proposed %q", tr.Text, proposed)
		default:
			s.mcpRows = append(s.mcpRows, mcpRow{Credential: "write API token", Session: "write",
				Tool: "translation_propose",
				Outcome: fmt.Sprintf("written, state `%s`, origin `%s` — in review, not an approved revision",
					tr.State, tr.Origin), OK: true})
		}
	}

	// 4. A second tenant's token sees nothing of the first's project.
	other, err := s.connect(s.otherToken, "")
	if err != nil {
		s.gap("12.6", "the second tenant's token could not open a session: %v", err)
		return
	}
	defer other.Close()
	out, refused, _ = call(other, "catalog_search", map[string]any{"project": s.project, "limit": 5})
	switch {
	case !refused && strings.Contains(out, "checkout."):
		s.gap("12.6", "the second tenant's token read the first tenant's catalog: %s", firstLine(out))
	default:
		s.mcpRows = append(s.mcpRows, mcpRow{Credential: "another tenant's API token", Session: "read",
			Tool:    "catalog_search (the first tenant's project)",
			Outcome: "**refused** — " + firstLine(out), OK: true})
	}
	out, refused, _ = call(other, "findings_list", map[string]any{"project": s.project, "limit": 5})
	if !refused && strings.Contains(out, "term_") {
		s.gap("12.6", "the second tenant's token listed the first tenant's findings: %s", firstLine(out))
	}
}

// refusedAtConnect asserts a credential cannot open a session at all.
func (s *scenario) refusedAtConnect(what, token, wantIn string) {
	sess, err := s.connect(token, "")
	if err == nil {
		sess.Close()
		s.gap("12.6", "%s opened an MCP session", what)
		return
	}
	body := err.Error()
	// The refusal has to say which credential kind it was, or the
	// person holding it cannot tell what to present instead.
	if !strings.Contains(body, wantIn) {
		body = s.probeConnect(token)
	}
	ok := strings.Contains(body, wantIn)
	if !ok {
		s.gap("12.6", "%s was refused, but the refusal does not say %q: %s", what, wantIn, firstLine(body))
	}
	s.mcpRows = append(s.mcpRows, mcpRow{Credential: what, Session: "read (asked for)", Tool: "connect",
		Outcome: "**refused at connect** — " + firstLine(body), OK: ok})
}

// probeConnect does the raw initialize the SDK hides, so the report can
// quote what the server actually said.
func (s *scenario) probeConnect(token string) string {
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"m4","version":"0"}}}`)
	req, err := http.NewRequest(http.MethodPost, s.mcpURL(""), body)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	raw := make([]byte, 4096)
	n, _ := resp.Body.Read(raw)
	var p struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(raw[:n], &p)
	if p.Detail != "" {
		return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, p.Detail)
	}
	return fmt.Sprintf("HTTP %d: %s %s", resp.StatusCode, resp.Header.Get("WWW-Authenticate"), strings.TrimSpace(string(raw[:n])))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 220 {
		s = s[:220] + "…"
	}
	return s
}
