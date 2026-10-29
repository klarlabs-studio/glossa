package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

type countingMetrics struct {
	appended, failures int
	exports            []string
}

func (c *countingMetrics) Appended(n int)           { c.appended += n }
func (c *countingMetrics) VerifyFailed()            { c.failures++ }
func (c *countingMetrics) ExportJob(outcome string) { c.exports = append(c.exports, outcome) }

func toolCall(id uuid.UUID) domain.Draft {
	return domain.ToolCall{
		Call: id, Actor: "token:" + uuid.NewString(), Toolset: "read", Tool: "assignments_list", Outcome: "ok",
		Arguments: map[string]string{"project": uuid.NewString()}, At: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
	}.Draft()
}

// glossa_audit_entries_total moves by what was appended, never by a
// redelivery that appended nothing; glossa_audit_chain_verify_failures_total
// moves when a stored entry no longer follows the one before it (RFC
// 0006 §10.1).
func TestAppendsAndBrokenChainsAreCounted(t *testing.T) {
	tenant := tenancy.ID(uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"))
	store := &memStore{tenant: tenant.UUID()}
	m := &countingMetrics{}
	svc := app.New(store, app.WithMetrics(m))
	if svc.Metrics() != m {
		t.Fatal("Metrics() is not the recorder the service was given")
	}
	ctx := tenancy.ContextWithTenant(t.Context(), tenant)
	first, second := uuid.New(), uuid.New()
	if _, err := svc.Append(ctx, toolCall(first), toolCall(second)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Append(ctx, toolCall(first)); err != nil { // a redelivery
		t.Fatal(err)
	}
	if m.appended != 2 {
		t.Errorf("appended = %d, want 2", m.appended)
	}
	if _, err := svc.Verify(ctx); err != nil || m.failures != 0 {
		t.Fatalf("an intact chain: err %v, failures %d", err, m.failures)
	}
	store.entries[1].Actor = "person:" + uuid.NewString() // tampered
	_, err := svc.Verify(ctx)
	var ce *domain.ChainError
	if !errors.As(err, &ce) || m.failures != 1 {
		t.Errorf("a tampered chain: err %v, failures %d, want a ChainError counted once", err, m.failures)
	}
}
