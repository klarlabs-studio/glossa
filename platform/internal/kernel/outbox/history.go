package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox/outboxsql"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// HistoryCursor is a position in a tenant's event history: after the
// event that occurred at OccurredAt with ID. The zero cursor is the
// start.
type HistoryCursor struct {
	OccurredAt time.Time
	ID         uuid.UUID
}

// History reads events already recorded, whatever their delivery state,
// for a projection built after them — the audit backfill (RFC 0006
// §6.1). It is read-only: nothing here claims, settles or edits an
// event, and a delivery read from it is never handed to the dispatcher.
type History struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewHistory returns the reader on uow.
func NewHistory(uow *db.UnitOfWork) *History {
	return &History{uow: uow, scope: db.NewSystemScope("outbox.history")}
}

// Tenants lists the tenants with at least one recorded event, in id
// order. It runs in system scope, so ctx must not carry a tenant.
func (h *History) Tenants(ctx context.Context) ([]tenancy.ID, error) {
	var out []tenancy.ID
	err := h.uow.InSystemTx(ctx, h.scope, func(ctx context.Context, tx *db.SystemTx) error {
		ids, err := outboxsql.New(tx).OutboxHistoryTenants(ctx)
		for _, id := range ids {
			out = append(out, tenancy.ID(id))
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: history tenants: %w", err)
	}
	return out, nil
}

// Count is how many events ctx's tenant has recorded.
func (h *History) Count(ctx context.Context) (int64, error) {
	var n int64
	err := h.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		var err error
		n, err = outboxsql.New(tx).OutboxHistoryCount(ctx, tx.Tenant().UUID())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("outbox: history count: %w", err)
	}
	return n, nil
}

// Page reads up to limit of the tenant's events after cursor, in the
// order they occurred. The tenant is ctx's. Actor and TraceID are read
// as the dispatcher reads them: an event from before migration 0042
// reports ActorUnknown. Attempt is zero: a history read is not a
// delivery.
func (h *History) Page(ctx context.Context, after HistoryCursor, limit int) ([]Delivery, error) {
	var out []Delivery
	err := h.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		rows, err := outboxsql.New(tx).OutboxHistoryPage(ctx, outboxsql.OutboxHistoryPageParams{
			TenantID: tx.Tenant().UUID(),
			AfterAt:  after.OccurredAt,
			AfterID:  after.ID,
			PageSize: int32(min(max(limit, 1), 10_000)),
		})
		if err != nil {
			return err
		}
		out = make([]Delivery, 0, len(rows))
		for _, r := range rows {
			var carrier map[string]string
			_ = json.Unmarshal(r.TraceContext, &carrier) // best effort, as at claim
			out = append(out, Delivery{
				EventID: r.ID, TenantID: tx.Tenant(), Type: r.EventType,
				AggregateType: r.AggregateType, AggregateID: r.AggregateID,
				Actor: actorRead(r.Actor), Payload: r.Payload, OccurredAt: r.OccurredAt,
				TraceID: traceIDOf(carrier),
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: history page: %w", err)
	}
	return out, nil
}

// CursorAfter is the cursor just past d.
func CursorAfter(d Delivery) HistoryCursor {
	return HistoryCursor{OccurredAt: d.OccurredAt, ID: d.EventID}
}
