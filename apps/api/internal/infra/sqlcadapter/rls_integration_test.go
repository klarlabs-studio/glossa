//go:build integration

// Package sqlcadapter integration test: spins up real Postgres via
// testcontainers, applies the 0001 schema, then verifies the
// Row-Level Security policies actually keep one tenant's reads /
// writes out of another's data. A buggy handler that forgets the
// tenant filter must still hit zero rows at the DB layer; that's
// the contract this file pins down.
//
// Build tag keeps it out of `go test ./...` — runs only via
// `go test -tags=integration ./...` (Docker required).
package sqlcadapter_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translationkey"
	"go.klarlabs.de/glossa/apps/api/internal/infra/sqlcadapter"
)

type fixture struct {
	pool     *pgxpool.Pool
	tenantA  uuid.UUID
	tenantB  uuid.UUID
	projectA project.Project
	projectB project.Project
	cleanup  func()
}

// setupPg boots Postgres in a container, applies the 0001 migration,
// turns on FORCE ROW LEVEL SECURITY so even the table owner is bound
// by RLS, then seeds two isolated tenants — each with one project
// and one "fr-FR" locale. Seed inserts run inside a tx with the
// target tenant active so the RLS policies admit them.
func setupPg(t *testing.T) *fixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Apply every .up.sql migration in numeric order so the test
	// schema matches what production runs. Previously only 0001 was
	// loaded; once 0003 dropped projects.api_key_hash the seed helper
	// fell behind the live schema and every test that called
	// seedProject failed on the now-missing NOT NULL column.
	migrationDir, err := filepath.Abs("../../../db/migrations")
	if err != nil {
		t.Fatalf("locate migration dir: %v", err)
	}
	upScripts, err := filepath.Glob(filepath.Join(migrationDir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	sort.Strings(upScripts)
	if len(upScripts) == 0 {
		t.Fatalf("no migrations found under %s", migrationDir)
	}

	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("glossa_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.WithInitScripts(upScripts...),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	// Bootstrap pool as superuser to create a non-superuser app role.
	// RLS is bypassed by superusers and the postgres bootstrap user
	// is one; the real app connects as glossa_app which has
	// NOBYPASSRLS, so RLS actually fires.
	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()
	bootstrap := []string{
		"CREATE ROLE glossa_app LOGIN PASSWORD 'glossa_app' NOSUPERUSER NOBYPASSRLS",
		"GRANT ALL PRIVILEGES ON DATABASE glossa_test TO glossa_app",
		"GRANT ALL PRIVILEGES ON SCHEMA public TO glossa_app",
		"GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO glossa_app",
		"GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO glossa_app",
	}
	for _, stmt := range bootstrap {
		if _, err := adminPool.Exec(ctx, stmt); err != nil {
			t.Fatalf("bootstrap (%s): %v", stmt, err)
		}
	}

	host, _ := pgC.Host(ctx)
	port, _ := pgC.MappedPort(ctx, "5432")
	appDSN := fmt.Sprintf("postgres://glossa_app:glossa_app@%s:%s/glossa_test?sslmode=disable", host, port.Port())
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}

	f := &fixture{pool: pool}
	f.tenantA = seedTenant(ctx, t, pool, "tenant-a", "Tenant A")
	f.tenantB = seedTenant(ctx, t, pool, "tenant-b", "Tenant B")
	f.projectA = seedProject(ctx, t, pool, f.tenantA, "proj-a", "Project A")
	f.projectB = seedProject(ctx, t, pool, f.tenantB, "proj-b", "Project B")
	seedLocale(ctx, t, pool, f.tenantA, f.projectA.ID, "fr-FR", "French")
	seedLocale(ctx, t, pool, f.tenantB, f.projectB.ID, "fr-FR", "French")

	f.cleanup = func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	}
	return f
}

// runAsTenant wraps fn in `BEGIN; SET LOCAL app.current_tenant = …;`
// — same shape as rlsTxMiddleware in production — and commits if fn
// returns nil. Returns fn's error verbatim (or the begin/commit error
// if the tx machinery itself failed) so callers can assert on
// RLS-driven failure modes.
func runAsTenant(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID, fn func(context.Context, *db.Queries) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant.String()+"'"); err != nil {
		return err
	}
	q := db.New(tx)
	tenantCtx := db.WithQueries(ctx, q)
	if err := fn(tenantCtx, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func seedTenant(ctx context.Context, t *testing.T, pool *pgxpool.Pool, slug, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+id.String()+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO tenants (id, slug, name) VALUES ($1, $2, $3)", id, slug, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id
}

func seedProject(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID, slug, name string) project.Project {
	t.Helper()
	id := uuid.New()
	keyHash := sha256.Sum256([]byte(slug + "-key"))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant.String()+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO projects (id, tenant_id, slug, name) VALUES ($1, $2, $3, $4)`,
		id, tenant, slug, name); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Migration 0003 moved keys to their own table. Seed one write-
	// scope key so the legacy key-resolution test paths still pass.
	if _, err := tx.Exec(ctx,
		`INSERT INTO project_api_keys (project_id, hash, scope, label) VALUES ($1, $2, 'write', 'legacy')`,
		id, keyHash[:]); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	pSlug, _ := project.NewSlug(slug)
	pName, _ := project.NewName(name)
	return project.Project{
		ID: id, TenantID: tenant, Slug: pSlug, Name: pName,
		DefaultLocale: "de",
	}
}

func seedLocale(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenant, projectID uuid.UUID, code, label string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant.String()+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO locales (project_id, code, label, enabled) VALUES ($1, $2, $3, true)`,
		projectID, code, label); err != nil {
		t.Fatalf("seed locale: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestRLS_LocalesNotVisibleAcrossTenants is the core guarantee:
// Tenant A sees its own locale and zero of Tenant B's, even when
// it explicitly queries by Tenant B's project ID.
func TestRLS_LocalesNotVisibleAcrossTenants(t *testing.T) {
	f := setupPg(t)
	defer f.cleanup()

	repo := sqlcadapter.NewLocaleRepo(db.New(f.pool))

	var ownCount, crossCount int
	if err := runAsTenant(t, f.pool, f.tenantA, func(ctx context.Context, _ *db.Queries) error {
		own, err := repo.ListForProject(ctx, f.projectA.ID)
		if err != nil {
			return err
		}
		cross, err := repo.ListForProject(ctx, f.projectB.ID)
		if err != nil {
			return err
		}
		ownCount, crossCount = len(own), len(cross)
		return nil
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}

	if ownCount != 1 {
		t.Fatalf("own locales: want 1, got %d", ownCount)
	}
	if crossCount != 0 {
		t.Fatalf("cross-tenant locales leaked: want 0, got %d", crossCount)
	}
}

// TestRLS_ProjectNotFoundAcrossTenants — Find must fail when the
// caller is operating under a different tenant, even though the row
// exists.
func TestRLS_ProjectNotFoundAcrossTenants(t *testing.T) {
	f := setupPg(t)
	defer f.cleanup()

	repo := sqlcadapter.NewProjectRepo(db.New(f.pool))

	if err := runAsTenant(t, f.pool, f.tenantA, func(ctx context.Context, _ *db.Queries) error {
		_, err := repo.Find(ctx, f.tenantA, f.projectA.Slug)
		return err
	}); err != nil {
		t.Fatalf("own project lookup failed: %v", err)
	}

	gotErr := runAsTenant(t, f.pool, f.tenantA, func(ctx context.Context, _ *db.Queries) error {
		_, err := repo.Find(ctx, f.tenantB, f.projectB.Slug)
		return err
	})
	if !errors.Is(gotErr, sqlcadapter.ErrNotFound) {
		t.Fatalf("cross-tenant Find: want ErrNotFound, got %v", gotErr)
	}
}

// TestRLS_KeyUpsertBlockedAcrossTenants — INSERT must be RLS-blocked
// when the caller's tenant doesn't own the parent project. The keys
// policy traverses keys.project_id → projects.tenant_id; with FORCE
// RLS active the foreign project is invisible, so the EXISTS subquery
// fails and the INSERT is rejected.
func TestRLS_KeyUpsertBlockedAcrossTenants(t *testing.T) {
	f := setupPg(t)
	defer f.cleanup()

	repo := sqlcadapter.NewKeyRepo(db.New(f.pool))
	name, _ := translationkey.NewName("cross.tenant.attempt")

	err := runAsTenant(t, f.pool, f.tenantA, func(ctx context.Context, _ *db.Queries) error {
		_, err := repo.Upsert(ctx, translationkey.Key{
			ID:        uuid.New(),
			ProjectID: f.projectB.ID,
			Name:      name,
		})
		return err
	})
	if err == nil {
		t.Fatalf("cross-tenant Upsert: expected RLS denial, got nil")
	}
}
