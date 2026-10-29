// Package postgres implements MCP's audit port on the kernel's unit of
// work: one append-only row per tool call in mcp_tool_calls, written in
// the session's own tenant scope and therefore under the same forced
// row-level security as everything else a tenant owns.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/postgres/mcpsql"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
)

// maxAffected bounds the ids one row records. A write tool that touches
// more than this says how many, not which: the ledger is an account of
// what happened, not a copy of the result.
const maxAffected = 100

// Audit implements app.Audit.
type Audit struct{ uow *db.UnitOfWork }

// NewAudit returns the ledger on uow.
func NewAudit(uow *db.UnitOfWork) *Audit { return &Audit{uow: uow} }

var _ app.Audit = (*Audit)(nil)

// Record implements app.Audit. It opens its own tenant transaction, so
// a refusal that never reached a database is audited exactly like a
// call that did.
func (a *Audit) Record(ctx context.Context, e app.AuditEntry) error {
	args, err := json.Marshal(shapeOrEmpty(e.Arguments))
	if err != nil {
		return fmt.Errorf("mcp: encode the argument shape: %w", err)
	}
	affected, err := json.Marshal(clip(e.Affected))
	if err != nil {
		return fmt.Errorf("mcp: encode the affected ids: %w", err)
	}
	return a.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return mcpsql.New(tx).InsertToolCall(ctx, mcpsql.InsertToolCallParams{
			ID:         e.ID,
			TenantID:   tx.Tenant().UUID(),
			SessionID:  e.Session,
			Actor:      e.Actor.String(),
			TokenID:    e.Token.UUID(),
			Toolset:    e.Toolset.String(),
			Tool:       e.Tool,
			Outcome:    e.Outcome.String(),
			Arguments:  args,
			Affected:   affected,
			DurationMs: millis(e.Duration),
			CalledAt:   e.At,
		})
	})
}

// shapeOrEmpty keeps the column's object invariant: a nil map would
// marshal to null, and the table only admits an object.
func shapeOrEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// clip bounds the affected ids and never returns nil (the column only
// admits an array).
func clip(ids []string) []string {
	if len(ids) > maxAffected {
		ids = ids[:maxAffected]
	}
	if ids == nil {
		return []string{}
	}
	return ids
}

// millis renders a duration for the column, clamped to non-negative.
func millis(d time.Duration) int32 {
	ms := d.Milliseconds()
	if ms < 0 {
		return 0
	}
	if ms > int64(^uint32(0)>>1) {
		return int32(^uint32(0) >> 1)
	}
	return int32(ms)
}
