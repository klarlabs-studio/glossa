// Package mcpgo serves the MCP context over streamable HTTP, at /mcp on
// glossa-server, using the Go MCP SDK.
//
// # Why this SDK
//
// RFC 0002 §9 and RFC 0005 §7.1 name "mcp-go" without pinning a module,
// and two candidates answer to that name today:
// github.com/mark3labs/mcp-go and the official
// github.com/modelcontextprotocol/go-sdk. We pin the official one, at a
// released v1, for three reasons that are about the build rather than
// taste:
//
//  1. It is already in glossa-server's module graph. The Anthropic SDK
//     we use for AI fills depends on it, so choosing the other library
//     would link *two* MCP implementations into one binary, with two
//     views of the protocol's version negotiation.
//  2. It is the reference implementation the specification is tracked
//     against, maintained with the Go team, and it is where new spec
//     revisions (streamable HTTP, protocol-version headers, the session
//     hijacking rule we lean on below) land first.
//  3. It hands us the pieces this slice needs — a streamable HTTP
//     handler with a per-request `getServer` hook, bearer-token
//     middleware that binds a session to the credential that opened it —
//     so the adapter stays a translation.
//
// The bet is hedged rather than taken: nothing outside this package
// imports the SDK. The application service speaks Go functions over
// app.Session and json.RawMessage, so swapping the library is a rewrite
// of this file and nothing else.
//
// # How a session is bound
//
// The SDK creates a session from the *initialize* request and reuses it
// for every later message on the same Mcp-Session-Id, running handlers
// on that first request's (detached) context. That is exactly the
// binding RFC 0005 §7.2 asks for: the session's tenant is the tenant of
// the token that opened it, no tool takes a tenant argument, and a
// second token cannot steer an existing session — the SDK refuses a
// request whose bearer resolves to a different user than the session's.
// Every HTTP request is re-authenticated all the same, so revoking a
// token ends its agent's session at the next call.
package mcpgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Path is where glossa-server serves MCP, and ToolsetParam how a
// client asks for a toolset beyond read (`/mcp?toolset=publish`). Both
// are the domain's, because the stdio proxy of RFC 0005 §7.1 dials the
// same endpoint and must spell it the same way.
const (
	Path         = domain.EndpointPath
	ToolsetParam = domain.ToolsetParam
)

// Transport labels the metrics' transport dimension.
const Transport = "streamable-http"

// sessionIDHeader is the streamable-HTTP session header. Its presence
// is how the gate tells a request that will create a session from one
// that continues an open one; the SDK spells it the same way.
const sessionIDHeader = "Mcp-Session-Id"

// serverName is the implementation name clients see.
const serverName = "glossa"

// instructions tell a connected agent how this server behaves. They are
// part of the contract: an agent that knows writes go to review does not
// waste a turn discovering it.
const instructions = "Glossa is localization infrastructure. This session is bound to one tenant — " +
	"the tenant of the API token it presented — and no tool takes a tenant argument. " +
	"Call whoami first to see the tenant, the token's scopes and the tools this session may call. " +
	"Read sessions are the default; a write session needs a token with the write scope and " +
	"?toolset=write on the endpoint, and a session that may publish, promote or roll back " +
	"releases needs the publish scope and ?toolset=publish. Write and publish are separate: a " +
	"session opens one or the other. Translations written through MCP always enter review, a " +
	"publish that does not meet its environment's policy is refused rather than forced, and " +
	"nothing here deletes data or reveals an AI provider key."

// Handler serves /mcp.
type Handler struct {
	svc     *app.Service
	servers map[domain.Toolset]*mcp.Server
	http    http.Handler
	logger  *slog.Logger
}

// Option configures the handler.
type Option func(*options)

type options struct {
	logger         *slog.Logger
	version        string
	sessionTimeout time.Duration
}

// WithLogger sets the logger the transport and its sessions use.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithVersion reports glossa-server's version to clients.
func WithVersion(v string) Option {
	return func(o *options) {
		if v != "" {
			o.version = v
		}
	}
}

// WithSessionTimeout closes a session that has been idle this long. An
// agent that walks away should not hold a session open forever.
func WithSessionTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.sessionTimeout = d
		}
	}
}

// DefaultSessionTimeout is how long an idle MCP session survives.
const DefaultSessionTimeout = 30 * time.Minute

// New builds the handler. One mcp.Server is built per toolset and held
// for the process's life: the servers differ only in which tools are
// registered, and everything that varies per session — the tenant, the
// grant, the token — travels on the session's context.
func New(svc *app.Service, opts ...Option) (*Handler, error) {
	if svc == nil {
		return nil, errors.New("mcp: a service is required")
	}
	o := options{logger: slog.New(slog.DiscardHandler), version: "dev", sessionTimeout: DefaultSessionTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	h := &Handler{svc: svc, servers: map[domain.Toolset]*mcp.Server{}, logger: o.logger}
	for _, ts := range domain.Toolsets() {
		h.servers[ts] = h.newServer(ts, o.version)
	}
	streamable := mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{
		Logger:         o.logger,
		SessionTimeout: o.sessionTimeout,
	})
	// Order, outermost first: the bearer middleware authenticates and
	// puts the SDK's TokenInfo on the context (which is what makes it
	// refuse a second token on an open session), then the gate decides
	// the toolset and opens Glossa's own session.
	h.http = auth.RequireBearerToken(h.verify, &auth.RequireBearerTokenOptions{
		// A tenant API token need not expire — expiry and revocation are
		// Identity's, checked on every request by AuthenticateToken — so
		// the middleware must not insist on an `exp` of its own.
		AllowMissingExpiration: true,
	})(h.gate(streamable))
	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.http.ServeHTTP(w, r) }

// OpenSessions counts the sessions currently connected, for the
// glossa_mcp_sessions_open gauge. The SDK owns session lifetime, so
// asking it is exact where our own bookkeeping would drift.
func (h *Handler) OpenSessions() int {
	n := 0
	for _, s := range h.servers {
		for range s.Sessions() {
			n++
		}
	}
	return n
}

// callerExtra is where the bearer middleware leaves the authenticated
// caller for the gate: auth.TokenInfo.Extra is the SDK's own channel
// from a verifier to the handlers behind it.
const callerExtra = "glossa.caller"

// sessionKey carries the opened session from the gate into the MCP
// session's context, and so into every tool handler.
type sessionKey struct{}

// verify implements auth.TokenVerifier. It authenticates the bearer and
// nothing more: the toolset decision needs the caller, so it happens in
// the gate, where a refusal can be a proper 403.
func (h *Handler) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	caller, err := h.svc.Authenticate(ctx, token)
	switch {
	case errors.Is(err, domain.ErrCredentialNotAccepted), errors.Is(err, domain.ErrUnauthenticated):
		// The message comes from the authenticator, which knows which
		// credential was presented. Naming it is the difference between
		// an agent that fixes its configuration and one that retries
		// forever.
		return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, err)
	case err != nil:
		h.logger.ErrorContext(ctx, "mcp: authentication failed", slog.Any("error", err))
		return nil, err
	}
	return &auth.TokenInfo{
		// UserID is what the SDK compares on later requests, so a session
		// opened by one token cannot be driven by another.
		UserID: caller.Principal.Actor.String(),
		Scopes: caller.Scopes.Strings(),
		Extra:  map[string]any{callerExtra: caller},
	}, nil
}

// gate decides the session's toolset and opens the session. It runs
// after the bearer middleware, so a refusal here is about what the
// caller asked for rather than who they are, and gets a 403.
func (h *Handler) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := callerFrom(r.Context())
		if !ok {
			problem.Write(w, http.StatusUnauthorized, "present a tenant API token as the bearer token")
			return
		}
		want, err := domain.ParseToolset(r.URL.Query().Get(ToolsetParam))
		if err != nil {
			problem.WriteDetails(w, problem.New(http.StatusBadRequest, "invalid_toolset",
				"toolset must be read, write or publish"))
			return
		}
		if err := h.svc.AllowToolset(caller, want); err != nil {
			// The code names the scope that is missing, not the toolset
			// that was asked for: what a person has to change is the
			// token.
			problem.WriteDetails(w, problem.New(http.StatusForbidden, problem.Code(string(want.Scope())+"_scope_required"),
				fmt.Sprintf("a %s session needs an API token with the %s scope; this token has %s",
					want, want.Scope(), joinScopes(caller.Scopes.Strings()))))
			return
		}
		// A request that carries no Mcp-Session-Id is the one that will
		// create a session, so that is the only place a session is
		// counted and its id minted. Later requests on the same session
		// are re-authenticated above — a revoked token stops working at
		// the next call — but run on the context the SDK kept from this
		// one, which is what keeps the session bound to the tenant that
		// opened it.
		if r.Header.Get(sessionIDHeader) == "" {
			sess := h.svc.Open(Transport, caller, want)
			r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess))
		}
		next.ServeHTTP(w, r)
	})
}

// callerFrom reads the caller the bearer middleware authenticated.
func callerFrom(ctx context.Context) (app.Caller, bool) {
	info := auth.TokenInfoFromContext(ctx)
	if info == nil {
		return app.Caller{}, false
	}
	c, ok := info.Extra[callerExtra].(app.Caller)
	return c, ok
}

func joinScopes(ss []string) string {
	if len(ss) == 0 {
		return "none"
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += ", " + s
	}
	return out
}

// serverFor picks the server whose toolset this request asked for. The
// SDK calls it on every request, so it must be cheap and must not mint
// anything.
func (h *Handler) serverFor(r *http.Request) *mcp.Server {
	sess, ok := r.Context().Value(sessionKey{}).(app.Session)
	if !ok {
		return nil
	}
	return h.servers[sess.Toolset]
}

// newServer registers one toolset's tools on a fresh MCP server.
func (h *Handler) newServer(ts domain.Toolset, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: version, Title: "Glossa"},
		&mcp.ServerOptions{Instructions: instructions, Logger: h.logger})
	for _, t := range h.svc.Tools(ts) {
		srv.AddTool(toolOf(t), h.handle(ts, t.Name))
	}
	return srv
}

// toolOf translates a declared tool into the SDK's shape.
func toolOf(t app.Tool) *mcp.Tool {
	schema := t.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return &mcp.Tool{
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: t.ReadOnly,
			// No MCP tool deletes anything (RFC 0005 §7.2), so nothing is
			// ever destructive. The hint is stated rather than defaulted
			// because clients use it to decide what to confirm.
			DestructiveHint: boolOf(false),
		},
	}
}

func boolOf(b bool) *bool { return &b }

// handle runs one tool through the application service. The session
// comes from the context the MCP session was created with, so it is the
// session's own tenant and grant — not whatever a later request claimed.
func (h *Handler) handle(ts domain.Toolset, name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sess, ok := ctx.Value(sessionKey{}).(app.Session)
		if !ok {
			// The session lost its binding: refuse rather than guess.
			return nil, errors.New("mcp: this session is not bound to a tenant; reconnect")
		}
		// Belt and braces: the server this handler belongs to decides the
		// toolset, so a context from elsewhere cannot widen the session.
		sess.Toolset = ts
		res, err := h.svc.Call(ctx, sess, name, req.Params.Arguments)
		if err != nil {
			return refusal(err), nil
		}
		return result(res), nil
	}
}

// result renders a tool's answer: the human explanation first, then the
// structured payload, which the client also gets as structuredContent.
func result(res app.Result) *mcp.CallToolResult {
	out := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: res.Explanation}}}
	if res.Data == nil {
		return out
	}
	if payload, err := json.Marshal(res.Data); err == nil {
		out.Content = append(out.Content, &mcp.TextContent{Text: string(payload)})
		out.StructuredContent = res.Data
	}
	return out
}

// refusal renders a failed call. Tool-level failures belong in the
// result with isError set, not as a protocol error, so the model can
// read what went wrong and correct itself rather than seeing the
// connection fault.
func refusal(err error) *mcp.CallToolResult {
	msg := err.Error()
	switch {
	case errors.Is(err, domain.ErrToolNotInSession):
		// The error carries the toolset the tool needs, which is the one
		// thing that makes this message actionable: reconnecting to the
		// wrong toolset is the mistake it exists to prevent.
		msg = err.Error() + ": reconnect to /mcp?toolset=<that toolset> with a token that carries the matching scope"
	case errors.Is(err, authz.ErrForbidden):
		msg = "this API token does not carry the permission this tool needs: " + err.Error()
	case errors.Is(err, app.ErrRateLimited):
		msg = "too many tool calls for this tenant; slow down and retry"
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
