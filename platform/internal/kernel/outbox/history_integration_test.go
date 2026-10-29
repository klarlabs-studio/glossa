//go:build integration

package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The history reader sees every recorded event, delivered or not, in
// the order they occurred, page by page, within the tenant's scope —
// and reads an event from before migration 0042 as ActorUnknown.
func TestHistoryReadsEveryEventInOrder(t *testing.T) {
	f := setup(t)
	ids := f.publish(t, f.ctx, 5)
	old := uuid.Must(uuid.NewV7())
	if _, err := env.Super.Exec(context.Background(), `INSERT INTO outbox_events
		(id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload, trace_context, occurred_at, status)
		VALUES ($1, $2, 'catalog.source_revised', 'message', 'old', 'unknown', '{}',
		        '{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}', $3, 'delivered')`,
		old, f.tenant.UUID(), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	h := outbox.NewHistory(f.uow)

	tenants, err := h.Tenants(context.Background())
	if err != nil || len(tenants) != 1 || tenants[0] != f.tenant {
		t.Fatalf("tenants %v, %v", tenants, err)
	}
	if n, err := h.Count(f.ctx); err != nil || n != 6 {
		t.Fatalf("count %d, %v", n, err)
	}

	var got []outbox.Delivery
	var cursor outbox.HistoryCursor
	for {
		page, err := h.Page(f.ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		cursor = outbox.CursorAfter(page[len(page)-1])
	}
	if len(got) != 6 || got[0].EventID != old {
		t.Fatalf("read %d events, first %v; want 6, the oldest first", len(got), got)
	}
	if got[0].Actor != outbox.ActorUnknown || got[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("the old event reads as %q, trace %q", got[0].Actor, got[0].TraceID)
	}
	for i, id := range ids {
		if got[i+1].EventID != id || got[i+1].Actor != personActor || got[i+1].TenantID != f.tenant {
			t.Errorf("event %d: %+v", i, got[i+1])
		}
	}

	// Another tenant's history is not this tenant's.
	globex, err := env.SeedTenant(context.Background(), "globex")
	if err != nil {
		t.Fatal(err)
	}
	other := tenancy.ContextWithTenant(context.Background(), globex)
	if page, err := h.Page(other, outbox.HistoryCursor{}, 10); err != nil || len(page) != 0 {
		t.Errorf("another tenant reads %d events, %v", len(page), err)
	}
	if n, err := h.Count(other); err != nil || n != 0 {
		t.Errorf("another tenant counts %d events, %v", n, err)
	}
}
