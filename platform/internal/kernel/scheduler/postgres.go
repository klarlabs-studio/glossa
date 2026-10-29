package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler/schedulersql"
)

// systemScope is the tenantless scope the lease runs in: migration 0017
// opens system_leases to it and to nothing else.
var systemScope = db.NewSystemScope("scheduler.lease")

// PostgresLease implements Lease on the system_leases table. Both
// conditions — nothing holds the lease, and the job is due — are
// checked inside one conditional upsert, so two replicas racing for the
// same job can't both win.
type PostgresLease struct{ uow *db.UnitOfWork }

// NewPostgresLease returns the lease store on uow.
func NewPostgresLease(uow *db.UnitOfWork) *PostgresLease { return &PostgresLease{uow: uow} }

var _ Lease = (*PostgresLease)(nil)

func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// Acquire implements Lease.
func (l *PostgresLease) Acquire(ctx context.Context, name, holder string, interval, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	var taken bool
	err := l.uow.InSystemTx(ctx, systemScope, func(ctx context.Context, tx *db.SystemTx) error {
		n, err := schedulersql.New(tx).AcquireLease(ctx, schedulersql.AcquireLeaseParams{
			Name: name, Holder: holder, Now: stamp(now), ExpiresAt: stamp(now.Add(ttl)),
			DueBefore: stamp(now.Add(-interval)),
		})
		taken = n == 1
		return err
	})
	if err != nil {
		return false, fmt.Errorf("scheduler: acquire lease %s: %w", name, err)
	}
	return taken, nil
}

// Release implements Lease.
func (l *PostgresLease) Release(ctx context.Context, name, holder string) error {
	err := l.uow.InSystemTx(ctx, systemScope, func(ctx context.Context, tx *db.SystemTx) error {
		return schedulersql.New(tx).ReleaseLease(ctx, schedulersql.ReleaseLeaseParams{
			Name: name, Holder: holder, RanAt: stamp(time.Now().UTC()),
		})
	})
	if err != nil {
		return fmt.Errorf("scheduler: release lease %s: %w", name, err)
	}
	return nil
}
