//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	mcppg "go.klarlabs.de/glossa/platform/internal/mcp/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/postgres/mcpsql"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

var env *dbtest.Env

func TestMain(m *testing.M) {
	var err error
	env, err = dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	env.Close()
	os.Exit(code)
}

func entry(tool string, outcome domain.Outcome, token identity.TokenID) app.AuditEntry {
	return app.AuditEntry{
		ID:      uuid.Must(uuid.NewV7()),
		Session: "session-" + tool,
		Actor:   identity.TokenActor(token),
		Token:   token,
		Toolset: domain.ToolsetWrite,
		Tool:    tool,
		Outcome: outcome,
		// The shape, never the content: a translation's length, a
		// locale verbatim.
		Arguments: map[string]string{"locale": "de", "translation": "string(len=42)"},
		Affected:  []string{"msg-1", "msg-2"},
		Duration:  37 * time.Millisecond,
		At:        time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
	}
}

// The ledger is tenant-owned under forced RLS: one tenant's agents
// never appear in another's trail, and the transaction's tenant — not
// the caller's claim — decides where a row lands.
func TestAuditIsTenantIsolated(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := env.SeedTenant(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.SeedTenant(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	audit := mcppg.NewAudit(uow)

	tokenA, tokenB := identity.NewTokenID(), identity.NewTokenID()
	if err := audit.Record(tenancy.ContextWithTenant(ctx, first), entry("message_upsert", domain.OutcomeOK, tokenA)); err != nil {
		t.Fatalf("record for the first tenant: %v", err)
	}
	if err := audit.Record(tenancy.ContextWithTenant(ctx, first), entry("whoami", domain.OutcomeDenied, tokenA)); err != nil {
		t.Fatalf("record a refusal: %v", err)
	}
	if err := audit.Record(tenancy.ContextWithTenant(ctx, second), entry("catalog_search", domain.OutcomeOK, tokenB)); err != nil {
		t.Fatalf("record for the second tenant: %v", err)
	}

	rows := read(t, uow, first)
	if len(rows) != 2 {
		t.Fatalf("the first tenant sees %d rows, want its own 2", len(rows))
	}
	for _, r := range rows {
		if r.Tool == "catalog_search" {
			t.Error("the first tenant sees the second's tool call")
		}
	}
	// Newest first, so the refusal is the head.
	if rows[0].Outcome != string(domain.OutcomeDenied) && rows[1].Outcome != string(domain.OutcomeDenied) {
		t.Errorf("no refusal was recorded: %+v", rows)
	}
	if got := read(t, uow, second); len(got) != 1 || got[0].Tool != "catalog_search" {
		t.Errorf("the second tenant sees %+v, want only its own call", got)
	}
}

// What the row keeps is the shape, and it survives the round trip.
func TestAuditRowKeepsTheShape(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "shapes")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	token := identity.NewTokenID()
	if err := mcppg.NewAudit(uow).Record(tenancy.ContextWithTenant(ctx, tenant),
		entry("translation_propose", domain.OutcomeOK, token)); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows := read(t, uow, tenant)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	switch {
	case r.Actor != identity.TokenActor(token).String():
		t.Errorf("actor = %q", r.Actor)
	case r.TokenID != token.UUID():
		t.Errorf("token = %v", r.TokenID)
	case r.Toolset != string(domain.ToolsetWrite):
		t.Errorf("toolset = %q", r.Toolset)
	case r.DurationMs != 37:
		t.Errorf("duration = %d", r.DurationMs)
	case string(r.Arguments) == "":
		t.Error("no argument shape stored")
	}
	args := string(r.Arguments)
	if !strings.Contains(args, `"de"`) || !strings.Contains(args, "string(len=42)") {
		t.Errorf("arguments = %s, want the locale verbatim and the text as a length", args)
	}
}

// Every toolset the domain knows is a toolset the column admits. The
// CHECK on mcp_tool_calls.toolset is a second, independent statement of
// domain.Toolsets(), and a value added on one side without the other
// would lose an audit row rather than fail a tool call — the one
// failure mode an append-only ledger cannot recover from.
func TestEveryToolsetIsAdmittedByTheLedger(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "toolsets")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	audit := mcppg.NewAudit(uow)
	for _, ts := range domain.Toolsets() {
		e := entry("release_publish", domain.OutcomeOK, identity.NewTokenID())
		e.Toolset = ts
		if err := audit.Record(tenancy.ContextWithTenant(ctx, tenant), e); err != nil {
			t.Errorf("the ledger refused the %s toolset: %v", ts, err)
		}
	}
	rows := read(t, uow, tenant)
	if len(rows) != len(domain.Toolsets()) {
		t.Fatalf("rows = %d, want %d", len(rows), len(domain.Toolsets()))
	}
	var stored []string
	for _, r := range rows {
		stored = append(stored, r.Toolset)
	}
	for _, ts := range domain.Toolsets() {
		if !slices.Contains(stored, ts.String()) {
			t.Errorf("the %s toolset is not on the ledger: %v", ts, stored)
		}
	}
	// And a toolset nothing mints is refused, so the CHECK is a real
	// constraint and not a column that takes anything.
	e := entry("member_remove", domain.OutcomeOK, identity.NewTokenID())
	e.Toolset = "admin"
	if err := audit.Record(tenancy.ContextWithTenant(ctx, tenant), e); err == nil {
		t.Error("the ledger admitted an `admin` toolset; MCP exposes none")
	}
}

// glossa_app may append to the ledger and read it, and nothing else:
// no code path — and no agent — can rewrite or erase its own trail.
func TestAuditLedgerIsAppendOnly(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "append-only")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	if err := mcppg.NewAudit(uow).Record(tenancy.ContextWithTenant(ctx, tenant),
		entry("message_upsert", domain.OutcomeOK, identity.NewTokenID())); err != nil {
		t.Fatalf("record: %v", err)
	}
	for _, stmt := range []string{
		"UPDATE mcp_tool_calls SET outcome = 'ok'",
		"DELETE FROM mcp_tool_calls",
	} {
		err := uow.InTenantTx(tenancy.ContextWithTenant(ctx, tenant),
			func(ctx context.Context, tx *db.TenantTx) error {
				_, err := tx.Exec(ctx, stmt)
				return err
			})
		if err == nil {
			t.Errorf("%q was allowed; the ledger must be append-only", stmt)
		}
	}
}

func read(t *testing.T, uow *db.UnitOfWork, tenant tenancy.ID) []mcpsql.ToolCallsByTenantRow {
	t.Helper()
	var rows []mcpsql.ToolCallsByTenantRow
	err := uow.InTenantTx(tenancy.ContextWithTenant(t.Context(), tenant),
		func(ctx context.Context, tx *db.TenantTx) error {
			var err error
			rows, err = mcpsql.New(tx).ToolCallsByTenant(ctx, mcpsql.ToolCallsByTenantParams{
				TenantID: tenant.UUID(), Limit: 50,
			})
			return err
		})
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	return rows
}
