package app

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Caller is what an accepted tenant API token resolves to. The
// principal is the one Identity would hand a REST request on the same
// token — the same Scope → GrantForScopes → permissions — so a tool and
// an endpoint can never disagree about what a token may do.
type Caller struct {
	Tenant    tenancy.ID
	Token     identity.TokenID
	Scopes    identity.Scopes
	Principal authz.Principal
}

// Session is one MCP connection. It is bound to exactly one tenant —
// the token's own — and no tool takes a tenant argument, so there is
// nothing to confuse and nothing to escalate (RFC 0005 §7.2).
type Session struct {
	// ID is Glossa's own id for the session, minted when it opens and
	// written to every audit row it produces.
	ID string
	// Transport is how the session is carried ("streamable-http"), the
	// label of glossa_mcp_sessions_total.
	Transport string
	Caller
	Toolset domain.Toolset
}

// Context returns ctx scoped to the session's tenant and carrying its
// principal, exactly as Identity's guard and tenancy middleware leave a
// REST request. Every tool runs under it, so row-level security and
// authz.Require behave identically on both façades.
func (s Session) Context(ctx context.Context) context.Context {
	return authz.WithPrincipal(tenancy.ContextWithTenant(ctx, s.Tenant), s.Principal)
}

// Actor is who the session acts as: the API token, "token:<uuid>".
func (s Session) Actor() identity.Actor { return s.Principal.Actor }
