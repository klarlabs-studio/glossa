package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// `glossa mcp` is a stdio proxy to glossa-server's /mcp endpoint
// (RFC 0005 §7.1). Editors that speak only stdio work without a second
// server, and a self-hosted or air-gapped user gets the same tools.
//
// It is **framing only**, and that is the entire point. The proxy moves
// JSON-RPC messages between two transports — the editor's stdio on one
// side, streamable HTTP on the other — and never looks inside one. It
// registers no tool, declares no schema, validates no argument, knows
// no tool name and holds no MCP session of its own: the SDK's
// Connection interface is the level it works at, so a message is read
// and written as a jsonrpc.Message and nothing more.
//
// That is why it cannot diverge from the hosted endpoint. A tool added
// to the server appears here the moment the server advertises it; a
// rule tightened there is tightened here, because there is nothing here
// to tighten. The two transports are the only thing this file knows
// about, which is the trade-off RFC 0005 §7.1 names and accepts.
//
// What the proxy *does* decide is one thing: which toolset to ask the
// server for. That is a flag, it is off by default, and it is a request
// — the server still checks the token's scope and refuses a session it
// may not open. A flag here can only ever narrow what a session does,
// never widen it.

const mcpUsage = `mcp [--server URL] [--allow-write] [--allow-publish]

Speaks MCP on stdin and stdout, proxying to glossa-server's /mcp
endpoint with the token ` + "`glossa login`" + ` stored (or GLOSSA_TOKEN).

Configure an editor to run this command; it needs no port and no second
server. The tools, their rules and their audit trail are the server's —
this is a pipe.

Sessions are read-only unless you ask for more, and asking is not
getting: the server refuses a toolset the token's scopes do not carry.

  --allow-write     also offer the write tools (message_upsert,
                    translation_propose, locale_add, translate).
                    Needs a token with the write scope.
  --allow-publish   instead offer the release tools (release_publish,
                    release_promote, release_rollback). Needs a token
                    with the publish scope.

--allow-write and --allow-publish are alternatives, not a pair: write
and publish are separate scopes and separate sessions, so one process
speaks one of them. Run a second ` + "`glossa mcp`" + ` for the other.`

func runMCP(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(mcpUsage)
	server := fs.String("server", "", "glossa-server URL (default: glossa.yaml's server)")
	allowWrite := fs.Bool("allow-write", false, "offer the write tools (needs a token with the write scope)")
	allowPublish := fs.Bool("allow-publish", false, "offer the release tools (needs a token with the publish scope)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	toolset, err := toolsetFor(*allowWrite, *allowPublish)
	if err != nil {
		return err
	}
	srv, err := inv.serverFor(*server)
	if err != nil {
		return err
	}
	endpoint, err := mcpEndpoint(srv, toolset)
	if err != nil {
		return err
	}
	token, _, err := inv.token(srv)
	if err != nil {
		return err
	}
	return inv.proxy(ctx, endpoint, token)
}

// toolsetFor turns the flags into the toolset to ask for. The two
// flags are alternatives rather than a pair, because the toolsets are:
// a session opens one.
func toolsetFor(write, publish bool) (domain.Toolset, error) {
	switch {
	case write && publish:
		return "", &Error{Exit: ExitUsage, Code: "one_toolset",
			What: "--allow-write and --allow-publish together",
			Why: "write and publish are separate token scopes and separate MCP sessions, " +
				"so one process speaks one of them",
			Fix: "pick one, and run a second `glossa mcp` for the other"}
	case publish:
		return domain.ToolsetPublish, nil
	case write:
		return domain.ToolsetWrite, nil
	}
	return domain.ToolsetRead, nil
}

// mcpEndpoint is the server's /mcp URL with the toolset asked for. A
// read session says nothing, which is what a client that was given no
// flag should send.
func mcpEndpoint(server string, toolset domain.Toolset) (string, error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", &Error{Exit: ExitUsage, Code: "invalid_server",
			What: "invalid server URL", Where: server,
			Fix: "pass --server https://glossa.example.com, or set server in glossa.yaml"}
	}
	u.Path = strings.TrimRight(u.Path, "/") + domain.EndpointPath
	if toolset != domain.ToolsetRead {
		q := u.Query()
		q.Set(domain.ToolsetParam, toolset.String())
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// bearer adds the stored credential to every request to /mcp. It is the
// only thing the proxy adds to a message's journey, and it adds it to
// the HTTP request rather than to the message: the JSON-RPC body is
// forwarded byte for byte.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	// A RoundTripper must not modify the request it was given.
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	next := b.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(clone)
}

// proxy connects both transports and pumps messages between them until
// one side closes.
func (inv *invocation) proxy(ctx context.Context, endpoint, token string) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	client := &http.Client{Transport: bearer{token: token, next: transportOf(inv.env.HTTP)}}
	up, err := (&mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client}).Connect(ctx)
	if err != nil {
		return &Error{Exit: ExitNetwork, Code: "mcp_unreachable",
			What: "can't reach the MCP endpoint", Where: endpoint, Why: err.Error(),
			Fix: "check that glossa-server is running and serves /mcp, and that `glossa login` stored a token for it",
			Err: err}
	}
	defer up.Close()

	// The editor's side. Env.Stdin and Env.Stdout are what every command
	// here writes through, so the proxy is testable in-process — and a
	// reader the CLI does not own is not closed by closing the transport.
	down, err := (&mcp.IOTransport{
		Reader: io.NopCloser(inv.env.Stdin),
		Writer: nopWriteCloser{inv.env.Stdout},
	}).Connect(ctx)
	if err != nil {
		return err
	}
	defer down.Close()

	return pump(ctx, down, up)
}

// transportOf is the HTTP transport under the bearer; tests wire their
// own client in through Env.HTTP.
func transportOf(c *http.Client) http.RoundTripper {
	if c == nil || c.Transport == nil {
		return http.DefaultTransport
	}
	return c.Transport
}

// nopWriteCloser adapts a writer the CLI does not own.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// pump moves every message from each connection to the other, verbatim,
// until one side stops. Nothing here reads a method name, an id or a
// parameter: a jsonrpc.Message goes in and the same one comes out, so
// there is no rule the proxy could get wrong and none it could enforce
// differently from the server.
//
// The first side to fail ends both, because a half-open MCP session is
// worse than a closed one: an editor waiting forever for a response
// that can no longer arrive looks like a hung server.
func pump(ctx context.Context, down, up mcp.Connection) error {
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	copyOne := func(from, to mcp.Connection) {
		defer wg.Done()
		defer stop()
		for {
			msg, err := from.Read(ctx)
			if err != nil {
				once.Do(func() { first = err })
				return
			}
			if err := to.Write(ctx, msg); err != nil {
				once.Do(func() { first = err })
				return
			}
		}
	}
	wg.Add(2)
	go copyOne(down, up)
	go copyOne(up, down)
	wg.Wait()

	// An editor that closed its side, or a context the shell cancelled,
	// is how this command normally ends. Anything else is a real
	// failure worth an exit code.
	switch {
	case first == nil, errors.Is(first, io.EOF), errors.Is(first, context.Canceled):
		return nil
	}
	return &Error{Exit: ExitNetwork, Code: "mcp_disconnected",
		What: "the MCP connection ended", Why: first.Error(),
		Fix: "restart the editor's MCP server; if it keeps happening, check glossa-server's logs",
		Err: first}
}
