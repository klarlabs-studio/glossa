package mcpgo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/mcpgo"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// ── a stand-in for Identity ─────────────────────────────────────────

// tokens resolves the fixture bearers. It refuses the two credential
// kinds MCP does not take, exactly as the real adapter does, so the
// transport is tested against the same answers.
type tokens map[string]app.Caller

func (ts tokens) Authenticate(_ context.Context, bearer string) (app.Caller, error) {
	switch {
	case strings.HasPrefix(bearer, identity.CITokenPrefix):
		return app.Caller{}, fmt.Errorf("%w: a CI token is minted for one workflow run", domain.ErrCredentialNotAccepted)
	case strings.HasPrefix(bearer, identity.InContextGrantPrefix):
		return app.Caller{}, fmt.Errorf("%w: an in-context grant is minted for one origin", domain.ErrCredentialNotAccepted)
	}
	c, ok := ts[bearer]
	if !ok {
		return app.Caller{}, fmt.Errorf("%w: present a tenant API token", domain.ErrUnauthenticated)
	}
	return c, nil
}

// counter counts sessions, so a test can prove that a conversation of
// several requests is one session and not one per call.
type counter struct{ opened int }

func (c *counter) ToolCalled(string, domain.Toolset, domain.Outcome) {}
func (c *counter) SessionOpened(string)                              { c.opened++ }

type ledger struct{ entries []app.AuditEntry }

func (l *ledger) Record(_ context.Context, e app.AuditEntry) error {
	l.entries = append(l.entries, e)
	return nil
}

func caller(t *testing.T, tenant tenancy.ID, scopes ...string) app.Caller {
	t.Helper()
	ss, err := identity.ParseScopes(scopes)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	token := identity.NewTokenID()
	return app.Caller{
		Tenant: tenant, Token: token, Scopes: ss,
		Principal: authz.Principal{
			Actor: identity.TokenActor(token), Tenant: tenant, TokenTenant: tenant,
			Grant: identity.GrantForScopes(ss),
		},
	}
}

// probe answers with the tenant its context was scoped to.
func probe(name string, ts domain.Toolset, perm identity.Permission) app.Tool {
	return app.Tool{
		Name: name, Toolset: ts, Permission: perm, ReadOnly: ts == domain.ToolsetRead,
		Handler: func(ctx context.Context, _ app.Session, _ json.RawMessage) (app.Result, error) {
			tenant, _ := tenancy.FromContext(ctx)
			return app.Result{Explanation: "ok", Data: tenant.String()}, nil
		},
	}
}

// fixture is a server with two tenants' tokens.
type fixture struct {
	url      string
	audit    *ledger
	sessions *counter
	apiA     string
	apiB     string
	readA    string
	// publishA carries read and publish and no write: the two scopes are
	// orthogonal, and every interesting refusal is one where a token has
	// the wrong one rather than none.
	publishA string
	callerA  app.Caller
	callerB  app.Caller
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	tenantA, tenantB := tenancy.NewID(), tenancy.NewID()
	f := fixture{
		audit:    &ledger{},
		sessions: &counter{},
		apiA:     identity.TokenPrefix + strings.Repeat("a", 43),
		apiB:     identity.TokenPrefix + strings.Repeat("b", 43),
		readA:    identity.TokenPrefix + strings.Repeat("r", 43),
		publishA: identity.TokenPrefix + strings.Repeat("p", 43),
	}
	f.callerA = caller(t, tenantA, "read", "write")
	f.callerB = caller(t, tenantB, "read", "write")
	svc, err := app.New(tokens{
		f.apiA:     f.callerA,
		f.apiB:     f.callerB,
		f.readA:    caller(t, tenantA, "read"),
		f.publishA: caller(t, tenantA, "read", "publish"),
	},
		app.WithAudit(f.audit),
		app.WithMetrics(f.sessions),
		app.WithTools(
			probe("catalog_search", domain.ToolsetRead, identity.PermCatalogRead),
			probe("message_upsert", domain.ToolsetWrite, identity.PermCatalogWrite),
			probe("release_publish", domain.ToolsetPublish, identity.PermReleasesPublish),
		),
	)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	h, err := mcpgo.New(svc, mcpgo.WithVersion("test"))
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle(mcpgo.Path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL + mcpgo.Path
	return f
}

// bearer adds an Authorization header to every request.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func connect(t *testing.T, f fixture, token string, query string) (*mcp.ClientSession, error) {
	t.Helper()
	url := f.url
	if query != "" {
		url += "?" + query
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	return client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
	}, nil)
}

// ── who may connect ─────────────────────────────────────────────────

// Every credential that is not a tenant API token is refused at connect,
// before a session exists (RFC 0005 §7.2).
func TestConnectRefusesEveryCredentialButATenantAPIToken(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name       string
		token      string
		query      string
		wantStatus int
		wantIn     string
	}{
		{
			name: "a CI token", token: identity.CITokenPrefix + strings.Repeat("c", 43),
			wantStatus: http.StatusUnauthorized, wantIn: "CI token",
		},
		{
			name: "an in-context grant", token: identity.InContextGrantPrefix + strings.Repeat("g", 43),
			wantStatus: http.StatusUnauthorized, wantIn: "in-context grant",
		},
		{
			name: "an unknown, revoked or expired token", token: identity.TokenPrefix + strings.Repeat("z", 43),
			wantStatus: http.StatusUnauthorized, wantIn: "tenant API token",
		},
		{
			name: "no credential at all", token: "",
			wantStatus: http.StatusUnauthorized, wantIn: "bearer",
		},
		{
			name: "a write session on a read-only token", token: f.readA, query: "toolset=write",
			wantStatus: http.StatusForbidden, wantIn: "write scope",
		},
		{
			name: "a publish session on a read-only token", token: f.readA, query: "toolset=publish",
			wantStatus: http.StatusForbidden, wantIn: "publish scope",
		},
		{
			// `admin` is a token scope but never a toolset: MCP exposes no
			// member, token, connection or tenant management (RFC 0005 §7.2).
			name: "a toolset that does not exist", token: f.apiA, query: "toolset=admin",
			wantStatus: http.StatusBadRequest, wantIn: "read, write or publish",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := initialize(t, f.url, tc.token, tc.query)
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, body, tc.wantStatus)
			}
			if !strings.Contains(body, tc.wantIn) {
				t.Errorf("body = %q, want it to mention %q", body, tc.wantIn)
			}
			// And the SDK client sees it as a failure to connect.
			if sess, err := connect(t, f, tc.token, tc.query); err == nil {
				sess.Close()
				t.Error("the client connected anyway")
			}
		})
	}
	if len(f.audit.entries) != 0 {
		t.Errorf("a refused connect wrote %d audit rows; nothing was called", len(f.audit.entries))
	}
}

// initialize sends a raw MCP initialize and returns the HTTP status, so
// a refusal's status and message can be asserted exactly.
func initialize(t *testing.T, url, token, query string) (int, string) {
	t.Helper()
	if query != "" {
		url += "?" + query
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	return res.StatusCode, string(buf[:n])
}

// ── what a session may call ─────────────────────────────────────────

func TestReadSessionSeesOnlyReadTools(t *testing.T) {
	f := newFixture(t)
	sess, err := connect(t, f, f.apiA, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	names := toolNames(t, sess)
	if !slices.Contains(names, app.WhoAmIName) || !slices.Contains(names, "catalog_search") {
		t.Errorf("tools = %v, want the probe and the read tool", names)
	}
	if slices.Contains(names, "message_upsert") {
		t.Errorf("tools = %v; a read session must not be offered a write tool", names)
	}
	// And asking for it anyway is refused.
	if _, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "message_upsert"}); err == nil {
		t.Error("a read session called a write tool")
	}
}

func TestWriteSessionSeesWriteTools(t *testing.T) {
	f := newFixture(t)
	sess, err := connect(t, f, f.apiA, "toolset=write")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	if names := toolNames(t, sess); !slices.Contains(names, "message_upsert") {
		t.Fatalf("tools = %v, want the write tool", names)
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "message_upsert", Arguments: map[string]any{"key": "checkout.button"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %s", text(res))
	}
	if got := text(res); !strings.Contains(got, f.callerA.Tenant.String()) {
		t.Errorf("the tool ran in the wrong tenant: %q", got)
	}
	if got := auditedTools(f.audit); !slices.Contains(got, "message_upsert") {
		t.Errorf("audited = %v, want the write call", got)
	}
	// A write session is offered no release tool: `publish` is its own
	// scope and its own toolset (RFC 0005 §7.2, §7.3).
	if names := toolNames(t, sess); slices.Contains(names, "release_publish") {
		t.Errorf("tools = %v; a write session must not be offered a release tool", names)
	}
	if res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_publish"}); err == nil && !res.IsError {
		t.Error("a write session published a release")
	}
}

// The publish toolset over the real transport: a publish-scoped token
// asking for ?toolset=publish gets the read tools and the release
// tools, and nothing of the write toolset.
func TestPublishSessionSeesReleaseToolsAndNoWriteTools(t *testing.T) {
	f := newFixture(t)
	sess, err := connect(t, f, f.publishA, "toolset=publish")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	names := toolNames(t, sess)
	if !slices.Contains(names, "release_publish") || !slices.Contains(names, "catalog_search") {
		t.Fatalf("tools = %v, want the release tool and the read tool", names)
	}
	if slices.Contains(names, "message_upsert") {
		t.Errorf("tools = %v; a publish session must not be offered a write tool", names)
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_publish"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %s", text(res))
	}
	if got := text(res); !strings.Contains(got, f.callerA.Tenant.String()) {
		t.Errorf("the tool ran in the wrong tenant: %q", got)
	}
	// Asking for a write tool anyway is refused: the server this session
	// was routed to has no such tool, which is the toolset gate a step
	// earlier than app.Service's.
	if res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "message_upsert"}); err == nil && !res.IsError {
		t.Error("a publish session called a write tool")
	}
	// Everything this session *did* run is on the ledger as a publish
	// session, which is the toolset mcp_tool_calls has to admit.
	if got := auditedTools(f.audit); !slices.Contains(got, "release_publish") {
		t.Errorf("audited = %v, want the release call", got)
	}
	for _, e := range f.audit.entries {
		if e.Toolset != domain.ToolsetPublish {
			t.Errorf("%s was audited in the %s toolset, want publish", e.Tool, e.Toolset)
		}
	}
}

// A publish token cannot open a write session, and a write token cannot
// open a publish one: the scopes are orthogonal and neither implies the
// other (RFC 0005 §7.2).
func TestNeitherScopeStandsInForTheOther(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ name, token, query, wantIn string }{
		{"a publish token asking for write", f.publishA, "toolset=write", "write scope"},
		{"a write token asking for publish", f.apiA, "toolset=publish", "publish scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := initialize(t, f.url, tc.token, tc.query)
			if status != http.StatusForbidden {
				t.Fatalf("status = %d (%s), want 403", status, body)
			}
			if !strings.Contains(body, tc.wantIn) {
				t.Errorf("body = %q, want it to mention %q", body, tc.wantIn)
			}
		})
	}
}

// ── tenant binding ──────────────────────────────────────────────────

// Two tokens, two tenants, one endpoint: each session answers with its
// own tenant and never the other's.
func TestSessionIsBoundToItsTokensTenant(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		token  string
		tenant tenancy.ID
		other  tenancy.ID
	}{
		{f.apiA, f.callerA.Tenant, f.callerB.Tenant},
		{f.apiB, f.callerB.Tenant, f.callerA.Tenant},
	} {
		sess, err := connect(t, f, tc.token, "")
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: app.WhoAmIName})
		if err != nil {
			t.Fatalf("whoami: %v", err)
		}
		got := text(res)
		if !strings.Contains(got, tc.tenant.String()) {
			t.Errorf("whoami = %q, want tenant %s", got, tc.tenant)
		}
		if strings.Contains(got, tc.other.String()) {
			t.Errorf("whoami leaked the other tenant: %q", got)
		}
		sess.Close()
	}
}

// Every call leaves a row naming the actor, the tenant and the tool.
func TestEveryCallIsAudited(t *testing.T) {
	f := newFixture(t)
	sess, err := connect(t, f, f.apiA, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	for _, name := range []string{app.WhoAmIName, "catalog_search"} {
		if _, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if got, want := auditedTools(f.audit), []string{app.WhoAmIName, "catalog_search"}; !slices.Equal(got, want) {
		t.Fatalf("audited = %v, want %v", got, want)
	}
	// One conversation is one session: the initialize, the calls and
	// the standalone stream all audit under the same id, rather than a
	// session being minted per request.
	for _, e := range f.audit.entries {
		if e.Session != f.audit.entries[0].Session {
			t.Error("two calls in one conversation were audited under different sessions")
		}
	}
	if f.sessions.opened == 0 {
		t.Error("no session was counted")
	}
	for _, e := range f.audit.entries {
		switch {
		case e.Actor != f.callerA.Principal.Actor:
			t.Errorf("actor = %v, want %v", e.Actor, f.callerA.Principal.Actor)
		case e.Token != f.callerA.Token:
			t.Errorf("token = %v", e.Token)
		case e.Session == "":
			t.Error("no session id on the row")
		case e.Outcome != domain.OutcomeOK:
			t.Errorf("outcome = %q", e.Outcome)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func toolNames(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func auditedTools(l *ledger) []string {
	out := make([]string, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, e.Tool)
	}
	return out
}
