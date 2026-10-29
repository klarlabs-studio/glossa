package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	mcpgo "go.klarlabs.de/glossa/platform/internal/mcp/adapters/mcpgo"
	mcpapp "go.klarlabs.de/glossa/platform/internal/mcp/app"
	mcpdomain "go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// These tests run the proxy against a *real* glossa-server MCP
// endpoint, driven by a real MCP client on the other side of real
// pipes. That is the only test worth writing for it: the proxy's whole
// claim is that it cannot diverge from the endpoint, and a fake on
// either side would be a place for a divergence to hide.

// ── a real /mcp endpoint ────────────────────────────────────────────

// mcpTokens resolves the fixture's bearers, refusing the two credential
// kinds MCP never takes.
type mcpTokens map[string]mcpapp.Caller

func (ts mcpTokens) Authenticate(_ context.Context, bearer string) (mcpapp.Caller, error) {
	c, ok := ts[bearer]
	if !ok {
		return mcpapp.Caller{}, mcpdomain.ErrUnauthenticated
	}
	return c, nil
}

func mcpCaller(t *testing.T, scopes ...string) mcpapp.Caller {
	t.Helper()
	ss, err := identity.ParseScopes(scopes)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	tenant, token := tenancy.NewID(), identity.NewTokenID()
	return mcpapp.Caller{
		Tenant: tenant, Token: token, Scopes: ss,
		Principal: authz.Principal{
			Actor: identity.TokenActor(token), Tenant: tenant, TokenTenant: tenant,
			Grant: identity.GrantForScopes(ss),
		},
	}
}

// mcpProbe answers with the session's toolset, which is what the proxy
// tests need to see: the flag's only job is to ask for the right one.
func mcpProbe(name string, ts mcpdomain.Toolset, perm identity.Permission) mcpapp.Tool {
	return mcpapp.Tool{
		Name: name, Toolset: ts, Permission: perm, ReadOnly: ts == mcpdomain.ToolsetRead,
		Handler: func(_ context.Context, sess mcpapp.Session, _ json.RawMessage) (mcpapp.Result, error) {
			return mcpapp.Result{Explanation: sess.Toolset.String(), Data: sess.Toolset.String()}, nil
		},
	}
}

// mcpServer is a real /mcp endpoint with three tokens: one that only
// reads, one that also writes and one that also publishes.
type mcpServer struct {
	url                       string
	readToken, writeToken     string
	publishToken              string
	audit                     *mcpLedger
	readCaller, publishCaller mcpapp.Caller
	writeCaller               mcpapp.Caller
}

type mcpLedger struct {
	mu      sync.Mutex
	entries []mcpapp.AuditEntry
}

func (l *mcpLedger) Record(_ context.Context, e mcpapp.AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	return nil
}

func (l *mcpLedger) toolsets() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, e := range l.entries {
		out = append(out, e.Toolset.String())
	}
	return out
}

func newMCPServer(t *testing.T) *mcpServer {
	t.Helper()
	s := &mcpServer{
		audit:        &mcpLedger{},
		readToken:    identity.TokenPrefix + strings.Repeat("r", 43),
		writeToken:   identity.TokenPrefix + strings.Repeat("w", 43),
		publishToken: identity.TokenPrefix + strings.Repeat("p", 43),
	}
	s.readCaller = mcpCaller(t, "read")
	s.writeCaller = mcpCaller(t, "read", "write")
	s.publishCaller = mcpCaller(t, "read", "publish")
	svc, err := mcpapp.New(mcpTokens{
		s.readToken:    s.readCaller,
		s.writeToken:   s.writeCaller,
		s.publishToken: s.publishCaller,
	},
		mcpapp.WithAudit(s.audit),
		mcpapp.WithTools(
			mcpProbe("catalog_search", mcpdomain.ToolsetRead, identity.PermCatalogRead),
			mcpProbe("message_upsert", mcpdomain.ToolsetWrite, identity.PermCatalogWrite),
			mcpProbe("release_publish", mcpdomain.ToolsetPublish, identity.PermReleasesPublish),
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
	s.url = srv.URL
	return s
}

// ── the proxy, end to end ───────────────────────────────────────────

// proxied runs `glossa mcp` in a goroutine over real pipes and connects
// an MCP client to the other ends, exactly as an editor would.
func proxied(t *testing.T, srv *mcpServer, token string, args ...string) (*mcp.ClientSession, <-chan int) {
	t.Helper()
	toProxy, fromClient := io.Pipe()
	toClient, fromProxy := io.Pipe()

	w := newWorkspace(t)
	w.env["GLOSSA_TOKEN"] = token
	done := make(chan int, 1)
	go func() {
		done <- Main(context.Background(), append([]string{"mcp", "--server", srv.url}, args...), Env{
			Stdin: toProxy, Stdout: fromProxy, Stderr: io.Discard,
			Getenv: func(k string) string { return w.env[k] }, Dir: w.dir,
			Credentials: w.store, Version: "test",
		})
		_ = fromProxy.Close()
	}()
	t.Cleanup(func() { _ = fromClient.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "editor", Version: "0"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.IOTransport{
		Reader: toClient, Writer: fromClient,
	}, nil)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess, done
}

func proxiedToolNames(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	out := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		out[i] = tool.Name
	}
	return out
}

func proxiedText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Without a flag the proxy opens a read session, and the tools it
// offers are the endpoint's read tools — not a list this command keeps.
func TestMCPProxyOpensAReadSessionByDefault(t *testing.T) {
	srv := newMCPServer(t)
	sess, _ := proxied(t, srv, srv.publishToken)

	names := proxiedToolNames(t, sess)
	if !slices.Contains(names, "catalog_search") {
		t.Fatalf("tools = %v, want the endpoint's read tool", names)
	}
	for _, unwanted := range []string{"message_upsert", "release_publish"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("tools = %v; a session opened without a flag must not offer %s", names, unwanted)
		}
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "catalog_search"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := proxiedText(t, res); !strings.Contains(got, "read") {
		t.Errorf("the endpoint ran the call in %q, want a read session", got)
	}
}

// --allow-publish opens a publish session on a publish-scoped token,
// and the release tools arrive because the *endpoint* advertises them.
func TestMCPProxyAllowPublishOpensAPublishSession(t *testing.T) {
	srv := newMCPServer(t)
	sess, _ := proxied(t, srv, srv.publishToken, "--allow-publish")

	names := proxiedToolNames(t, sess)
	if !slices.Contains(names, "release_publish") {
		t.Fatalf("tools = %v, want the endpoint's release tool", names)
	}
	if slices.Contains(names, "message_upsert") {
		t.Errorf("tools = %v; a publish session must not offer a write tool", names)
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_publish"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %s", proxiedText(t, res))
	}
	if got := proxiedText(t, res); !strings.Contains(got, "publish") {
		t.Errorf("the endpoint ran the call in %q, want a publish session", got)
	}
	// The call is on the server's ledger as a publish session: the proxy
	// adds nothing to the audit trail and hides nothing from it.
	if got := srv.audit.toolsets(); !slices.Contains(got, "publish") {
		t.Errorf("audited toolsets = %v, want publish", got)
	}
}

// --allow-write is the write toolset and nothing else: a write session
// is offered no release tool, through the proxy as at the endpoint.
func TestMCPProxyAllowWriteOpensAWriteSession(t *testing.T) {
	srv := newMCPServer(t)
	sess, _ := proxied(t, srv, srv.writeToken, "--allow-write")

	names := proxiedToolNames(t, sess)
	if !slices.Contains(names, "message_upsert") {
		t.Fatalf("tools = %v, want the endpoint's write tool", names)
	}
	if slices.Contains(names, "release_publish") {
		t.Errorf("tools = %v; a write session must not offer a release tool", names)
	}
}

// Asking is not getting. The flag is a request; the server still checks
// the token's scopes, and a write-only token asking to publish is
// refused at connect — by the endpoint, not by the proxy.
func TestMCPProxyCannotWidenWhatTheTokenCarries(t *testing.T) {
	srv := newMCPServer(t)
	toProxy, fromClient := io.Pipe()
	toClient, fromProxy := io.Pipe()
	t.Cleanup(func() { _ = fromClient.Close() })

	w := newWorkspace(t)
	w.env["GLOSSA_TOKEN"] = srv.writeToken
	done := make(chan int, 1)
	go func() {
		done <- Main(context.Background(), []string{"mcp", "--server", srv.url, "--allow-publish"}, Env{
			Stdin: toProxy, Stdout: fromProxy, Stderr: io.Discard,
			Getenv: func(k string) string { return w.env[k] }, Dir: w.dir,
			Credentials: w.store, Version: "test",
		})
		_ = fromProxy.Close()
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "editor", Version: "0"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	if err == nil {
		defer sess.Close()
		if names := proxiedToolNames(t, sess); slices.Contains(names, "release_publish") {
			t.Fatalf("a write-only token published through the proxy: %v", names)
		}
		t.Fatal("a write-only token opened a publish session")
	}
	if code := <-done; code != int(ExitNetwork) {
		t.Errorf("exit = %d, want %d", code, ExitNetwork)
	}
	if len(srv.audit.entries) != 0 {
		t.Errorf("a refused connect was audited as a tool call: %+v", srv.audit.entries)
	}
}

// ── the flags ───────────────────────────────────────────────────────

// Write and publish are separate scopes and separate sessions, so one
// process speaks one of them. Asking for both is a usage error rather
// than a silent choice.
func TestMCPProxyRefusesBothToolsetFlags(t *testing.T) {
	srv := newMCPServer(t)
	w := newWorkspace(t)
	w.env["GLOSSA_TOKEN"] = srv.publishToken

	r := w.run("mcp", "--server", srv.url, "--allow-write", "--allow-publish")
	r.want(t, ExitUsage)
	if !strings.Contains(r.stderr, "separate") && !strings.Contains(r.stdout, "separate") {
		t.Errorf("output = %q / %q, want it to say the two are separate", r.stdout, r.stderr)
	}
}

func TestMCPProxyNeedsAServerAndAToken(t *testing.T) {
	srv := newMCPServer(t)

	// No token stored and none in the environment.
	w := newWorkspace(t)
	w.run("mcp", "--server", srv.url).want(t, ExitNetwork)

	// No server anywhere: no flag, no GLOSSA_SERVER, no glossa.yaml.
	w2 := newWorkspace(t)
	w2.env["GLOSSA_TOKEN"] = srv.readToken
	w2.run("mcp").want(t, ExitUsage)
}

// The endpoint the proxy dials is the server's /mcp with the toolset
// appended — the same path and the same query parameter the transport
// declares, because both read them from one place.
func TestMCPEndpointIsTheServersMCPPath(t *testing.T) {
	tests := []struct {
		server  string
		toolset mcpdomain.Toolset
		want    string
	}{
		{"https://glossa.example.com", mcpdomain.ToolsetRead, "https://glossa.example.com/mcp"},
		{"https://glossa.example.com/", mcpdomain.ToolsetWrite, "https://glossa.example.com/mcp?toolset=write"},
		{"http://localhost:8080", mcpdomain.ToolsetPublish, "http://localhost:8080/mcp?toolset=publish"},
		// A server behind a path prefix keeps it.
		{"https://example.com/glossa", mcpdomain.ToolsetRead, "https://example.com/glossa/mcp"},
	}
	for _, tc := range tests {
		got, err := mcpEndpoint(tc.server, tc.toolset)
		if err != nil {
			t.Fatalf("%s: %v", tc.server, err)
		}
		if got != tc.want {
			t.Errorf("endpoint(%q, %s) = %q, want %q", tc.server, tc.toolset, got, tc.want)
		}
	}
	if _, err := mcpEndpoint("not a url", mcpdomain.ToolsetRead); err == nil {
		t.Error("a server that is not a URL was accepted")
	}
}

// The proxy asks for a toolset and decides nothing else. There is no
// flag that could make it offer a tool, refuse one, or change what a
// tool does: those are the endpoint's, which is what keeps the two from
// diverging (RFC 0005 §7.1).
func TestToolsetForIsTheOnlyChoiceTheProxyMakes(t *testing.T) {
	tests := []struct {
		write, publish bool
		want           mcpdomain.Toolset
		wantErr        bool
	}{
		{want: mcpdomain.ToolsetRead},
		{write: true, want: mcpdomain.ToolsetWrite},
		{publish: true, want: mcpdomain.ToolsetPublish},
		{write: true, publish: true, wantErr: true},
	}
	for _, tc := range tests {
		got, err := toolsetFor(tc.write, tc.publish)
		if tc.wantErr {
			if err == nil {
				t.Errorf("write=%t publish=%t was accepted", tc.write, tc.publish)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("toolsetFor(%t, %t) = %q, %v; want %q", tc.write, tc.publish, got, err, tc.want)
		}
	}
}
