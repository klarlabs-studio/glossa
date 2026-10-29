// Package audit writes each MCP tool call into the tenant's audit trail
// (RFC 0006 §6.1: "MCP tool calls, projected from mcp_tool_calls"),
// beside the MCP ledger row it points at. It implements MCP's own Audit
// port, so the service records a call to the ledger and to the trail
// through the same seam, with the same content-free entry: the tool, the
// toolset, the outcome, the arguments' shape and how many rows the call
// affected. The session id and the token itself never leave MCP.
package audit

import (
	"context"

	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
)

// Trail implements app.Audit over the Audit context's Recorder.
type Trail struct{ rec auditapp.Recorder }

// New returns the adapter over rec.
func New(rec auditapp.Recorder) *Trail { return &Trail{rec: rec} }

var _ app.Audit = (*Trail)(nil)

// Record implements app.Audit. The ledger row's id is the entry's event
// id, so the trail and the ledger name the same call.
func (t *Trail) Record(ctx context.Context, e app.AuditEntry) error {
	return t.rec.RecordToolCall(ctx, auditdomain.ToolCall{
		Call: e.ID, Actor: e.Actor.String(), Toolset: e.Toolset.String(), Tool: e.Tool,
		Outcome: e.Outcome.String(), Arguments: e.Arguments, Affected: len(e.Affected),
		DurationMs: e.Duration.Milliseconds(), At: e.At,
	})
}
