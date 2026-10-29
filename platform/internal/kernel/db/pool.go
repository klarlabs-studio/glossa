// Package db is the kernel's Postgres layer: the pgx pool, embedded
// migrations, the startup check that row-level security actually binds
// the application role, and the tenant- and system-scoped unit of work
// every bounded context writes through.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRLSBypassed means the application connected as a role that row-level
// security does not bind (a superuser or a BYPASSRLS role).
var ErrRLSBypassed = errors.New("db: application role bypasses row-level security")

// OpenPool opens and pings a pgx pool, once. Pool sizing comes from the
// DSN (pool_max_conns, pool_min_conns, …) so it stays 12-factor.
// Retry.OpenPool retries it.
func OpenPool(ctx context.Context, dsn, applicationName string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, permanent(fmt.Errorf("db: parse DSN: %w", err))
	}
	if applicationName != "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// VerifyAppRole refuses a pool whose role escapes row-level security or
// cannot enter system scope. glossa-server calls it at startup, so a
// DATABASE_URL pointing at the owner or a superuser fails fast instead
// of silently voiding tenant isolation.
func VerifyAppRole(ctx context.Context, pool *pgxpool.Pool) error {
	var (
		role                 string
		super, bypass, canSU bool
	)
	err := pool.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls,
		       pg_has_role(r.rolname, $1, 'SET')
		FROM pg_roles r WHERE r.rolname = current_user`, systemRole,
	).Scan(&role, &super, &bypass, &canSU)
	if err != nil {
		return fmt.Errorf("db: inspect application role: %w", err)
	}
	if super || bypass {
		return fmt.Errorf("%w: role %q (superuser=%t, bypassrls=%t)", ErrRLSBypassed, role, super, bypass)
	}
	if !canSU {
		return fmt.Errorf("db: role %q cannot SET ROLE %s; run migrations and grant it", role, systemRole)
	}
	return nil
}
