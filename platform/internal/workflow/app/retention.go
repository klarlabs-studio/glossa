package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Retention of finished instances (RFC 0006 §2.5, §13 wave 6). An
// instance exists while work is in flight; once it finishes it keeps
// its transition log — the answer to "why is this translation where it
// is?" — for a configurable age (GLOSSA_WORKFLOW_INSTANCE_RETENTION),
// and the daily workflow.retention job then deletes it with its log.
// A running instance is never deleted: the query asks for finished ones
// only, and migration 0056's restrictive policy refuses the application
// role any other delete. Who did what stays in the audit trail, which
// the events feed, not these rows.

// Retention sweep bounds: a run visits at most this many tenants and
// deletes at most this many instances in each, so a backlog never makes
// one statement unbounded; the next run takes the rest.
const (
	maxRetentionTenants   = 200
	MaxRetentionPerTenant = 5000
)

// MinInstanceRetention is the shortest retention the server accepts: a
// finished instance's log is kept at least a day, so the person who
// finished it can still read why.
const MinInstanceRetention = 24 * time.Hour

// SweepRetention deletes, tenant by tenant, the instances that finished
// more than keep ago. It runs outside any tenant (a leased scheduler
// job) and needs RunnerDeps.Retention. A failing tenant doesn't stop
// the others.
func (r *Runner) SweepRetention(ctx context.Context, keep time.Duration) (int, error) {
	if r.d.Retention == nil {
		return 0, errors.New("workflow: SweepRetention needs a RetentionScanner")
	}
	if keep < MinInstanceRetention {
		return 0, fmt.Errorf("workflow: instance retention %s is shorter than %s", keep, MinInstanceRetention)
	}
	cutoff := r.now().Add(-keep)
	tenants, err := r.d.Retention.TenantsWithExpiredInstances(ctx, cutoff, maxRetentionTenants)
	if err != nil {
		return 0, err
	}
	var (
		deleted int
		errs    []error
	)
	for _, t := range tenants {
		n, err := r.DeleteExpired(tenancy.ContextWithTenant(ctx, t), cutoff)
		deleted += n
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return deleted, errors.Join(errs...)
}

// DeleteExpired deletes the tenant's instances on ctx that finished
// before cutoff, with their transition logs, in one transaction.
func (r *Runner) DeleteExpired(ctx context.Context, cutoff time.Time) (int, error) {
	deleted := 0
	err := r.d.Tx.InTenant(ctx, func(ctx context.Context, st InstanceStore) (err error) {
		deleted, err = st.DeleteFinished(ctx, cutoff, MaxRetentionPerTenant)
		return err
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
