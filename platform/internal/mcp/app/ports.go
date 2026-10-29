package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Authenticator resolves a bearer credential to the caller MCP will act
// as. It is Identity's job, behind a port, so this context never learns
// how a token is stored or how a grant is derived: MCP mints no
// credential and re-implements no authorization (RFC 0005 §7.2).
type Authenticator interface {
	// Authenticate returns the caller a tenant API token names. It
	// returns domain.ErrCredentialNotAccepted for a CI token or an
	// in-context grant, and domain.ErrUnauthenticated for anything
	// unknown, revoked, expired or malformed.
	Authenticate(ctx context.Context, bearer string) (Caller, error)
}

// Audit writes the ledger row every tool call leaves behind.
type Audit interface {
	// Record appends one call. It runs in the tenant's own scope, so the
	// row lands under the same row-level security as everything else.
	Record(ctx context.Context, e AuditEntry) error
}

// AuditEntry is one tool call as the ledger keeps it. Arguments hold
// the call's *shape*, never its content (see domain.Shape).
type AuditEntry struct {
	ID        uuid.UUID
	Session   string
	Actor     identity.Actor
	Token     identity.TokenID
	Toolset   domain.Toolset
	Tool      string
	Outcome   domain.Outcome
	Arguments map[string]string
	Affected  []string
	Duration  time.Duration
	At        time.Time
}

// Metrics are the RFC 0005 §11 series MCP owns.
type Metrics interface {
	// ToolCalled counts one call by tool, the session's toolset and how
	// it ended (glossa_mcp_tool_calls_total{tool,scope,outcome}).
	ToolCalled(tool string, toolset domain.Toolset, outcome domain.Outcome)
	// SessionOpened counts a session
	// (glossa_mcp_sessions_total{transport}). How many are open at once
	// is a gauge the transport answers, since the transport owns session
	// lifetime; nothing here has to track a close it might miss.
	SessionOpened(transport string)
}

// NoMetrics is the default: MCP is measured only where a registry is
// wired in.
type NoMetrics struct{}

// ToolCalled implements Metrics.
func (NoMetrics) ToolCalled(string, domain.Toolset, domain.Outcome) {}

// SessionOpened implements Metrics.
func (NoMetrics) SessionOpened(string) {}

var _ Metrics = NoMetrics{}
