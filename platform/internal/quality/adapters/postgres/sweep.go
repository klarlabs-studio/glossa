package postgres

import (
	"context"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres/qualitysql"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

// Quality's daily housekeeping, in storage (migration 0039): the
// tenant-scoped half of the sweep, and the cross-tenant Scanner that
// finds which tenants have any.

// ExpireWaivers implements app.Store.
func (s *store) ExpireWaivers(ctx context.Context, now time.Time) (int, error) {
	n, err := s.q.ExpireWaivers(ctx, now)
	if err != nil {
		return 0, storeError(err)
	}
	return int(n), nil
}

// DeleteExpiredCheckRuns implements app.Store.
func (s *store) DeleteExpiredCheckRuns(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	n, err := s.q.DeleteExpiredCheckRuns(ctx, qualitysql.DeleteExpiredCheckRunsParams{
		Cutoff: cutoff, MaxRows: int32Of(limit),
	})
	if err != nil {
		return 0, storeError(err)
	}
	return int(n), nil
}

// ── system scope ────────────────────────────────────────────────────

// Scanner implements app.Scanner in the system scope quality.sweep,
// which migration 0039 opens to reading quality_waivers' expiry columns
// and quality_check_runs' started_at, and nothing else.
type Scanner struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewScanner returns a scanner on uow.
func NewScanner(uow *db.UnitOfWork) *Scanner {
	return &Scanner{uow: uow, scope: db.NewSystemScope("quality.sweep")}
}

var _ app.Scanner = (*Scanner)(nil)

// TenantsWithSweepWork implements app.Scanner.
func (s *Scanner) TenantsWithSweepWork(
	ctx context.Context, now, cutoff time.Time, limit int,
) ([]tenancy.ID, error) {
	var out []tenancy.ID
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := qualitysql.New(tx).ListTenantsWithSweepWork(ctx, qualitysql.ListTenantsWithSweepWorkParams{
			Now: now, Cutoff: cutoff, MaxRows: int32Of(limit),
		})
		for _, r := range rows {
			out = append(out, tenancy.ID(r))
		}
		return err
	})
	return out, err
}
