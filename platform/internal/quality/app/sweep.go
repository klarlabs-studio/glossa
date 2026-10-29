package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Quality's daily housekeeping (RFC 0005 §2.2, §2.3, §13 wave 7). Two
// things accumulate, and neither of them is a finding:
//
//   - **Waivers that set a date.** `expires_at` stays optional — some
//     waivers are permanent decisions, and forcing a date makes people
//     pick one at random (§15 question 4, decided by the owner) — so
//     the sweep retires the ones that did set a date and leaves the
//     rest to the dashboard's staleness numbers. An expiry is
//     *recorded*, never deleted: the row stays with the moment it was
//     retired, exactly as a revoked waiver stays, because the reason
//     somebody wrote is the whole point of the mechanism.
//
//   - **Check runs past their retention.** They are kept 90 days
//     (domain.RunRetention), except the newest run of a ref, which
//     every dashboard, `listFindings` and the summary read whatever its
//     age. The findings go with their run; the history does not,
//     because the findings-by-day rollup (migration 0035) already holds
//     it a day at a time.
//
// Both halves run per tenant, in that tenant's own scope. Finding
// *which* tenants have work is the one thing that cannot: that is the
// Scanner, in system scope quality.sweep, which sees when a waiver
// expires and when a run started and nothing else at all (§10).

// sweepPrincipal is the background principal of the daily sweep. Its
// name is stable: it appears as the actor in logs.
const sweepPrincipal = "quality.sweep"

// sweepBatch bounds the tenants one run visits, so a database full of
// them doesn't make a single run unbounded; the next run takes the
// rest.
const sweepBatch = 50

// MaxRunsSweptPerTenant bounds the check runs one tenant's sweep
// deletes, so a project that has never been swept doesn't make one
// statement unbounded. The next day takes the rest.
const MaxRunsSweptPerTenant = 5000

// Scanner finds Quality's background work across tenants (system scope
// quality.sweep); the work itself runs in each tenant's scope. The
// daily quality.sweep job drives it.
type Scanner interface {
	// TenantsWithSweepWork lists up to limit tenants holding a waiver
	// that has expired by now and has not been recorded, or a check run
	// that started before cutoff.
	//
	// The second half is an over-approximation: the system scope cannot
	// see which run is a ref's newest, so a tenant whose only old run is
	// one of those is visited and finds nothing to do.
	TenantsWithSweepWork(ctx context.Context, now, cutoff time.Time, limit int) ([]tenancy.ID, error)
}

// WithScanner enables Sweep across tenants (the daily quality.sweep
// job). Without one, Sweep answers an error rather than silently
// sweeping nothing.
func WithScanner(sc Scanner) Option {
	return func(s *Service) {
		if sc != nil {
			s.scanner = sc
		}
	}
}

// Swept says what a sweep did.
type Swept struct {
	// Tenants counts the tenants visited.
	Tenants int
	// WaiversExpired counts the waivers whose expiry was recorded.
	WaiversExpired int
	// RunsDeleted counts the check runs deleted with their findings.
	RunsDeleted int
}

// Add sums another tenant's sweep into s.
func (s *Swept) Add(o Swept) {
	s.Tenants += o.Tenants
	s.WaiversExpired += o.WaiversExpired
	s.RunsDeleted += o.RunsDeleted
}

// SweepTenant records the expiries and applies the retention cutoff for
// the tenant on ctx. It needs `catalog.write`, the permission that
// already carries the authority to change what a project's check
// concludes — an expired waiver stops accepting a finding, which is the
// same kind of change.
//
// Both halves are idempotent: an expiry already recorded is skipped,
// and a deleted run is gone. Running it twice on one day does the work
// once.
func (s *Service) SweepTenant(ctx context.Context) (out Swept, err error) {
	if err := authz.RequireUnscoped(ctx, authz.CatalogWrite); err != nil {
		return Swept{}, err
	}
	now := s.now()
	ctx, end := s.span(ctx, "quality.sweep_tenant",
		attribute.String("glossa.quality.retention_cutoff", now.Add(-domain.RunRetention).UTC().Format(time.RFC3339)))
	defer end(&err)

	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		expired, err := st.ExpireWaivers(ctx, now)
		if err != nil {
			return err
		}
		deleted, err := st.DeleteExpiredCheckRuns(ctx, now.Add(-domain.RunRetention), MaxRunsSweptPerTenant)
		if err != nil {
			return err
		}
		out = Swept{Tenants: 1, WaiversExpired: expired, RunsDeleted: deleted}
		return nil
	})
	if err != nil {
		return Swept{}, err
	}
	// One count per waiver, on the same series a creation, an update, a
	// revocation and a refusal are counted on: `outcome` is what became
	// of the waiver, and expiring is one of the five things that can.
	for range out.WaiversExpired {
		s.metrics.WaiverDecided(WaiverExpired)
	}
	return out, nil
}

// Sweep runs the daily housekeeping over every tenant that has work,
// each in its own scope as the background principal quality.sweep. It
// runs outside any tenant (the daily quality.sweep job, leased by the
// scheduler so one replica leads it) and needs WithScanner. A failing
// tenant doesn't stop the others; their errors are joined.
func (s *Service) Sweep(ctx context.Context) (Swept, error) {
	if s.scanner == nil {
		return Swept{}, errors.New("quality: Sweep needs a Scanner")
	}
	now := s.now()
	tenants, err := s.scanner.TenantsWithSweepWork(ctx, now, now.Add(-domain.RunRetention), sweepBatch)
	if err != nil {
		return Swept{}, err
	}
	var (
		out  Swept
		errs []error
	)
	for _, t := range tenants {
		bg, err := authz.Background(tenancy.ContextWithTenant(ctx, t), sweepPrincipal, authz.CatalogRead, authz.CatalogWrite)
		if err != nil {
			return out, err
		}
		swept, err := s.SweepTenant(bg)
		if err != nil {
			// The tenant id is safe to log and the only thing here that
			// identifies anything: a sweep reads no message text, no
			// translation text and no reason (RFC 0005 §11).
			s.logger.ErrorContext(ctx, "quality: sweep", slog.String("tenant_id", t.String()), slog.Any("error", err))
			errs = append(errs, fmt.Errorf("quality: sweep tenant %s: %w", t, err))
			continue
		}
		out.Add(swept)
	}
	return out, errors.Join(errs...)
}
