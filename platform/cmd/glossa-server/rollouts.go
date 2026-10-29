package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
)

// The rollout sweep's cadence (RFC 0006 §5.2). A rollout's max_duration
// is an hour at the least and fourteen days by default, so five minutes
// late is as late as one is aborted.
const (
	rolloutSweepInterval = 5 * time.Minute
	rolloutSweepTimeout  = 2 * time.Minute
	rolloutSweepLease    = 3 * time.Minute
	rolloutSweepPoll     = time.Minute
)

// newRolloutSweep schedules the max_duration sweep on the Postgres lease
// so one replica aborts each expired rollout. It runs where the leased
// periodic jobs run (GLOSSA_PURGE_ENABLED); nil means it does not run
// here.
func newRolloutSweep(
	cfg config.Purge, logger *slog.Logger, reg prometheus.Registerer, pool *pgxpool.Pool, sweeper *releaseapp.RolloutSweeper,
) (*scheduler.Scheduler, error) {
	if !cfg.Enabled || sweeper == nil {
		return nil, nil
	}
	s, err := scheduler.New(scheduler.NewPostgresLease(db.NewUnitOfWork(pool)), scheduler.Config{
		Interval: rolloutSweepInterval, Timeout: rolloutSweepTimeout, Lease: rolloutSweepLease,
		Poll: rolloutSweepPoll, Jitter: cfg.Jitter,
	}, scheduler.WithLogger(logger), scheduler.WithMetrics(scheduler.NewMetrics(reg)))
	if err != nil {
		return nil, err
	}
	s.Add(scheduler.Job{Name: "release.rollouts", Run: func(ctx context.Context) error {
		n, err := sweeper.Sweep(ctx)
		if n > 0 {
			logger.InfoContext(ctx, "release: expired rollouts aborted", slog.Int("rollouts", n))
		}
		return err
	}})
	return s, nil
}
