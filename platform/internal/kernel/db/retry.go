package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Backoff bounds for the startup connection retry. A k3s pod's first
// connections are refused for about a second while its network policy
// is programmed (#66), so the first retry comes quickly.
const (
	retryInitialDelay = 250 * time.Millisecond
	retryMaxDelay     = 5 * time.Second
)

// Retry retries the first connection to the database with exponential
// backoff until Deadline passes, logging each failed attempt. Server
// startup and `-migrate=only` both go through it, so a database that is
// not reachable yet (still starting, or its network policy not yet
// programmed) delays startup instead of failing it.
type Retry struct {
	// Deadline is the total time allowed for connecting. Zero means a
	// single attempt.
	Deadline time.Duration
	Logger   *slog.Logger
	// Sleep waits between attempts; nil means a context-aware timer.
	// Tests replace it.
	Sleep func(context.Context, time.Duration) error
}

// permanentError marks a failure no retry can fix, such as a DSN that
// does not parse.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

func permanent(err error) error { return permanentError{err} }

// Do calls attempt until it succeeds, fails permanently, or the deadline
// or ctx ends; then it returns the last error. what names the
// connection in logs ("application pool", "migrations").
func (r Retry) Do(ctx context.Context, what string, attempt func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, max(r.Deadline, time.Nanosecond))
	defer cancel()
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	delay := retryInitialDelay
	for n := 1; ; n++ {
		err := attempt(ctx)
		if err == nil {
			if n > 1 {
				r.log(ctx, slog.LevelInfo, "database connected", what, n, nil, 0)
			}
			return nil
		}
		var p permanentError
		if errors.As(err, &p) {
			return p.err
		}
		if r.Deadline <= 0 {
			return err
		}
		r.log(ctx, slog.LevelWarn, "database not reachable, retrying", what, n, err, delay)
		if serr := sleep(ctx, delay); serr != nil {
			return fmt.Errorf("db: %s: gave up after %d attempts in %s: %w", what, n, r.Deadline, err)
		}
		delay = min(delay*2, retryMaxDelay)
	}
}

func (r Retry) log(ctx context.Context, level slog.Level, msg, what string, n int, err error, delay time.Duration) {
	if r.Logger == nil {
		return
	}
	attrs := []slog.Attr{slog.String("connection", what), slog.Int("attempt", n)}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()), slog.Duration("retry_in", delay))
	}
	r.Logger.LogAttrs(context.WithoutCancel(ctx), level, msg, attrs...)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// OpenPool opens the pool through r (see the package-level OpenPool).
func (r Retry) OpenPool(ctx context.Context, dsn, applicationName string) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	err := r.Do(ctx, "application pool", func(ctx context.Context) (err error) {
		pool, err = OpenPool(ctx, dsn, applicationName)
		return err
	})
	return pool, err
}

// NewMigrator connects the migrator through r (see the package-level
// NewMigrator).
func (r Retry) NewMigrator(ctx context.Context, dsn string, logger *slog.Logger) (*Migrator, error) {
	var m *Migrator
	err := r.Do(ctx, "migrations", func(context.Context) (err error) {
		m, err = NewMigrator(dsn, logger)
		return err
	})
	return m, err
}
