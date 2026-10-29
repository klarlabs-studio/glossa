package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Handler runs one tool. It is an ordinary Go function over the
// session and the raw arguments: no MCP type reaches it, so the tools
// are testable without a transport and the SDK stays replaceable.
type Handler func(ctx context.Context, sess Session, args json.RawMessage) (Result, error)

// Result is a tool's answer. Every result carries a short human
// explanation beside the structured payload (intent §47: "structured,
// explainable answers").
type Result struct {
	// Explanation is one sentence a person or a model can read.
	Explanation string
	// Data is the structured payload, marshalled to JSON by the
	// transport.
	Data any
	// Affected are the ids this call created or changed; they go to the
	// audit ledger. Empty for a read.
	Affected []string
}

// Tool is one capability, declared as data so the transport adapter is
// a translation and nothing more.
type Tool struct {
	// Name is the wire name ("catalog_search"), stable once released.
	Name string
	// Title is the display name.
	Title string
	// Description tells a model what the tool is for.
	Description string
	// Toolset is which session may call it: ToolsetRead for a read tool,
	// ToolsetWrite for one that changes something.
	Toolset domain.Toolset
	// Permission is checked before the handler runs, through the
	// session's grant — the same check the REST endpoint makes. Empty
	// means the session itself is enough.
	Permission identity.Permission
	// Selectors names the arguments the audit ledger may record
	// verbatim: identifiers and enumerations, never anything that can
	// carry message text (see domain.Shape).
	Selectors []string
	// InputSchema is the arguments' JSON Schema (2020-12).
	InputSchema json.RawMessage
	// ReadOnly marks a tool that changes nothing, for the client's
	// annotations.
	ReadOnly bool
	Handler  Handler
}

// Registry holds the tools a deployment serves, in registration order
// so a client's tool list is stable.
type Registry struct {
	tools []Tool
}

// Add registers a tool, refusing a duplicate name (a programming error:
// two tools answering to one name would silently shadow each other).
func (r *Registry) Add(t Tool) error {
	if t.Name == "" || t.Handler == nil {
		return fmt.Errorf("mcp: a tool needs a name and a handler (%q)", t.Name)
	}
	if _, ok := r.Lookup(t.Name); ok {
		return fmt.Errorf("mcp: tool %q is registered twice", t.Name)
	}
	r.tools = append(r.tools, t)
	return nil
}

// Lookup returns the tool of that name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	i := slices.IndexFunc(r.tools, func(t Tool) bool { return t.Name == name })
	if i < 0 {
		return Tool{}, false
	}
	return r.tools[i], true
}

// For returns the tools a session with this toolset may call.
func (r *Registry) For(ts domain.Toolset) []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if ts.Includes(t.Toolset) {
			out = append(out, t)
		}
	}
	return out
}

// names returns the tool names a toolset exposes.
func (r *Registry) names(ts domain.Toolset) []string {
	tools := r.For(ts)
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// WhoAmIName is the capability probe every session has.
const WhoAmIName = "whoami"

// noArguments is the JSON Schema of a tool that takes none.
var noArguments = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// Identity is what the capability probe answers: who the session acts
// as, where, and what it may do. An agent's first question is always
// "what am I allowed to do here?", and answering it wrongly is how an
// agent wastes a hundred calls discovering a permission it never had.
type Identity struct {
	Tenant      string   `json:"tenant"`
	Actor       string   `json:"actor"`
	Token       string   `json:"token"`
	Scopes      []string `json:"scopes"`
	Toolset     string   `json:"toolset"`
	Permissions []string `json:"permissions"`
	Tools       []string `json:"tools"`
	// WriteAvailable reports whether this token *could* open a write
	// session, so a client knows whether reconnecting with
	// ?toolset=write would help. PublishAvailable says the same of the
	// release tools. The two are separate because the scopes are: a
	// token may carry either, both or neither.
	WriteAvailable   bool `json:"write_available"`
	PublishAvailable bool `json:"publish_available"`
}

// whoAmI builds the capability probe over reg.
func whoAmI(reg *Registry) Tool {
	return Tool{
		Name:  WhoAmIName,
		Title: "Who am I",
		Description: "Report the tenant this session is bound to, the API token it acts as, " +
			"the scopes and permissions that token carries, the toolset the session opened " +
			"and the tools it may call.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermTenantRead,
		InputSchema: noArguments,
		ReadOnly:    true,
		Handler: func(_ context.Context, sess Session, _ json.RawMessage) (Result, error) {
			perms := sess.Principal.Grant.Permissions()
			names := make([]string, len(perms))
			for i, p := range perms {
				names[i] = string(p)
			}
			id := Identity{
				Tenant:           sess.Tenant.String(),
				Actor:            sess.Actor().String(),
				Token:            sess.Token.String(),
				Scopes:           sess.Scopes.Strings(),
				Toolset:          sess.Toolset.String(),
				Permissions:      names,
				Tools:            reg.names(sess.Toolset),
				WriteAvailable:   slices.Contains(sess.Scopes, identity.ScopeWrite),
				PublishAvailable: slices.Contains(sess.Scopes, identity.ScopePublish),
			}
			return Result{
				Explanation: fmt.Sprintf(
					"This %s session acts as %s in tenant %s and may call %d tool(s).",
					id.Toolset, id.Actor, id.Tenant, len(id.Tools)),
				Data: id,
			}, nil
		},
	}
}
