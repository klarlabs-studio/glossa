//go:build integration

package db_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
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

func reset(t *testing.T) {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

// seedTenant creates a tenant the way Identity will: by scoping the
// transaction to the new tenant's own id.
func seedTenant(t *testing.T, slug string) tenancy.ID {
	t.Helper()
	tn, err := tenancy.NewTenant(tenancy.KindOrganization, slug, "Tenant "+slug)
	if err != nil {
		t.Fatalf("new tenant: %v", err)
	}
	ctx := tenancy.ContextWithTenant(context.Background(), tn.ID)
	err = db.NewUnitOfWork(env.App).InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO tenants (id, kind, slug, name) VALUES ($1, $2, $3, $4)",
			tn.ID.UUID(), string(tn.Kind), string(tn.Slug), tn.Name)
		return err
	})
	if err != nil {
		t.Fatalf("seed tenant %s: %v", slug, err)
	}
	return tn.ID
}

// seedEvent writes an outbox row as tenant.
func seedEvent(t *testing.T, tenant tenancy.ID) {
	t.Helper()
	ctx := tenancy.ContextWithTenant(context.Background(), tenant)
	err := db.NewUnitOfWork(env.App).InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return insertEvent(ctx, tx, tenant)
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func insertEvent(ctx context.Context, q db.Querier, tenant tenancy.ID) error {
	_, err := q.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload)
		VALUES (gen_random_uuid(), $1, 'test.happened', 'test', 'a1', 'system:5b8f1c2e-4d3a-5e6f-8a9b-0c1d2e3f4a5b', '{}')`, tenant.UUID())
	return err
}

func countRows(ctx context.Context, q db.Querier, table string) (int, error) {
	var n int
	err := q.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n)
	return n, err
}
