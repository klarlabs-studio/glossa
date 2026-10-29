//go:build integration

package db_test

import (
	"context"
	"log/slog"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

func TestMigrationsAreReversibleAndIdempotent(t *testing.T) {
	ctx := context.Background()
	m, err := db.NewMigrator(env.OwnerDSN, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}
	defer func() { _ = m.Close() }()

	v, dirty, err := m.Version()
	if err != nil || dirty || v < 1 {
		t.Fatalf("after Start: version=%d dirty=%t err=%v", v, dirty, err)
	}

	// Up again is a no-op, not an error.
	if err := m.Up(ctx); err != nil {
		t.Fatalf("second Up: %v", err)
	}

	if err := m.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var tables int
	if err := env.Super.QueryRow(ctx,
		`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'schema_migrations'`,
	).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tables != 0 {
		t.Errorf("%d tables left after Down", tables)
	}

	// Re-applying over existing roles must work (roles survive Down).
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}
	if v2, _, _ := m.Version(); v2 != v {
		t.Errorf("version after re-Up = %d, want %d", v2, v)
	}
	// Tables were recreated; drop connections holding cached plans.
	env.App.Reset()
}

func TestVerifyAppRole(t *testing.T) {
	ctx := context.Background()
	if err := db.VerifyAppRole(ctx, env.App); err != nil {
		t.Errorf("glossa_app rejected: %v", err)
	}

	super, err := db.OpenPool(ctx, env.SuperDSN, "glossa-test-super")
	if err != nil {
		t.Fatalf("super pool: %v", err)
	}
	defer super.Close()
	if err := db.VerifyAppRole(ctx, super); err == nil {
		t.Error("superuser accepted as the application role")
	}

	owner, err := db.OpenPool(ctx, env.OwnerDSN, "glossa-test-owner")
	if err != nil {
		t.Fatalf("owner pool: %v", err)
	}
	defer owner.Close()
	if err := db.VerifyAppRole(ctx, owner); err == nil {
		t.Error("schema owner accepted as the application role")
	}
}
