//go:build integration || system

// Package dbtest boots a disposable Postgres 16 for integration tests,
// provisioned the way production is:
//
//   - the container superuser only creates the schema owner, a
//     NOSUPERUSER CREATEROLE role (the CNPG database owner in prod);
//   - migrations run as that owner and create glossa_app / glossa_system;
//   - the harness then grants glossa_app LOGIN + a password, as the
//     deployment does out of band, and the app pool connects as it.
//
// So every test runs with row-level security genuinely enforced.
package dbtest

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

const (
	image         = "postgres:16-alpine"
	database      = "glossa"
	ownerRole     = "glossa_owner"
	ownerPassword = "owner-test-password"
	appPassword   = "app-test-password"
)

// Env is a migrated database plus pools for the roles tests need.
type Env struct {
	// SuperDSN is the container superuser; use only for assertions that
	// need to see past RLS (it bypasses it).
	SuperDSN string
	// OwnerDSN is the schema owner; migrations run as it.
	OwnerDSN string
	// AppDSN is glossa_app, NOSUPERUSER NOBYPASSRLS.
	AppDSN string
	// App is a pool on AppDSN.
	App *pgxpool.Pool
	// Super is a pool on SuperDSN.
	Super *pgxpool.Pool

	container *tcpostgres.PostgresContainer
}

// Start boots Postgres, provisions roles and applies all migrations.
func Start(ctx context.Context) (*Env, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	ctr, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase(database),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("dbtest: start postgres: %w", err)
	}
	env := &Env{container: ctr}
	if err := env.provision(ctx); err != nil {
		env.Close()
		return nil, err
	}
	return env, nil
}

func (e *Env) provision(ctx context.Context) error {
	superDSN, err := e.container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("dbtest: dsn: %w", err)
	}
	e.SuperDSN = superDSN
	if e.Super, err = pgxpool.New(ctx, superDSN); err != nil {
		return fmt.Errorf("dbtest: superuser pool: %w", err)
	}
	if err := e.createOwner(ctx); err != nil {
		return err
	}
	e.OwnerDSN = withUser(superDSN, ownerRole, ownerPassword)
	if err := e.migrate(ctx); err != nil {
		return err
	}
	if err := e.enableAppLogin(ctx); err != nil {
		return err
	}
	e.AppDSN = withUser(superDSN, "glossa_app", appPassword)
	if e.App, err = db.OpenPool(ctx, e.AppDSN, "glossa-test"); err != nil {
		return fmt.Errorf("dbtest: app pool: %w", err)
	}
	return nil
}

func (e *Env) createOwner(ctx context.Context) error {
	stmts := []string{
		fmt.Sprintf("CREATE ROLE %s LOGIN CREATEROLE NOSUPERUSER NOBYPASSRLS PASSWORD '%s'", ownerRole, ownerPassword),
		fmt.Sprintf("ALTER DATABASE %s OWNER TO %s", database, ownerRole),
		// PG15+: public belongs to pg_database_owner, i.e. the new owner.
	}
	for _, s := range stmts {
		if _, err := e.Super.Exec(ctx, s); err != nil {
			return fmt.Errorf("dbtest: %s: %w", s, err)
		}
	}
	return nil
}

func (e *Env) migrate(ctx context.Context) error {
	m, err := db.NewMigrator(e.OwnerDSN, slog.New(slog.DiscardHandler))
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	return m.Up(ctx)
}

// enableAppLogin is the out-of-band step a deployment performs.
func (e *Env) enableAppLogin(ctx context.Context) error {
	owner, err := pgxpool.New(ctx, e.OwnerDSN)
	if err != nil {
		return fmt.Errorf("dbtest: owner pool: %w", err)
	}
	defer owner.Close()
	stmt := fmt.Sprintf("ALTER ROLE glossa_app LOGIN PASSWORD '%s'", appPassword)
	if _, err := owner.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("dbtest: enable glossa_app login: %w", err)
	}
	return nil
}

// Reset deletes all rows of every table except golang-migrate's, so
// tests can share one container and every bounded context's tables are
// covered without registering them here. TRUNCATE is not subject to RLS.
func (e *Env) Reset(ctx context.Context) error {
	var tables string
	err := e.Super.QueryRow(ctx, `
		SELECT coalesce(string_agg(format('%I', c.relname), ', '), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		  AND c.relname <> 'schema_migrations'`).Scan(&tables)
	if err != nil || tables == "" {
		return err
	}
	_, err = e.Super.Exec(ctx, "TRUNCATE "+tables+" CASCADE")
	return err
}

// SeedTenant creates an organization tenant the way Identity does: in
// a transaction scoped to the new tenant's own id.
func (e *Env) SeedTenant(ctx context.Context, slug string) (tenancy.ID, error) {
	tn, err := tenancy.NewTenant(tenancy.KindOrganization, slug, "Tenant "+slug)
	if err != nil {
		return tenancy.ID{}, err
	}
	scoped := tenancy.ContextWithTenant(ctx, tn.ID)
	err = db.NewUnitOfWork(e.App).InTenantTx(scoped, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx, "INSERT INTO tenants (id, kind, slug, name) VALUES ($1, $2, $3, $4)",
			tn.ID.UUID(), string(tn.Kind), string(tn.Slug), tn.Name)
		return err
	})
	return tn.ID, err
}

// Close stops the pools and the container.
func (e *Env) Close() {
	if e.App != nil {
		e.App.Close()
	}
	if e.Super != nil {
		e.Super.Close()
	}
	if e.container != nil {
		_ = testcontainers.TerminateContainer(e.container)
	}
}

func withUser(dsn, user, password string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(fmt.Sprintf("dbtest: parse dsn: %v", err))
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
