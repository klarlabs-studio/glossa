package app

import (
	"context"
	"errors"
	"time"
)

// Workflow's operational numbers (RFC 0006 §10.1). The ports name what
// is measured; the Prometheus series and their allowlisted labels are
// adapters/metrics'. Nothing here carries a tenant, a definition name, a
// vendor or any text — only the closed vocabularies the series are
// labelled by.

// RunnerMetrics records what the instance runner and its timer sweep
// observe.
type RunnerMetrics interface {
	// Transition counts one recorded transition by its outcome:
	// TransitionApplied, TransitionIgnored or TransitionRefused. It is
	// called once the step's transaction committed, so a step the outbox
	// delivers again is counted once.
	Transition(outcome string)
	// Instances reports how many instances have status (active,
	// finished) across the deployment, as the sweep last counted them.
	Instances(status string, n int)
	// OpenAssignments reports how many live (open or accepted)
	// assignments are past their due date (overdue) or not.
	OpenAssignments(overdue bool, n int)
}

// WorkloadScanner counts Workflow's rows across tenants, in the system
// scope the timer sweep already runs in: statuses and due dates only.
type WorkloadScanner interface {
	// InstancesByStatus counts instances by status.
	InstancesByStatus(ctx context.Context) (map[string]int, error)
	// LiveAssignments counts open and accepted assignments due before
	// now (overdue) and the rest.
	LiveAssignments(ctx context.Context, now time.Time) (overdue, onTime int, err error)
}

// DecisionMetrics records approval decisions (RFC 0006 §10.1).
type DecisionMetrics interface {
	// Decision counts one recorded decision on an approval of subject
	// (translation, release_request): granted or denied.
	Decision(subject, verdict string)
}

// WithWorkMetrics records the decisions the service takes.
func WithWorkMetrics(m DecisionMetrics) WorkOption {
	return func(s *WorkService) { s.metrics = m }
}

// instanceStatuses are the statuses the gauge always publishes, at zero
// when none is counted, so a dashboard shows "none" rather than a gap.
var instanceStatuses = []string{"active", "finished"}

// CountWork reports the instance and assignment gauges. It runs beside
// the timer sweep, on its lease, so one replica counts. A deployment
// without metrics or a scanner counts nothing.
func (r *Runner) CountWork(ctx context.Context) error {
	if r.d.Metrics == nil || r.d.Workload == nil {
		return nil
	}
	var errs []error
	if byStatus, err := r.d.Workload.InstancesByStatus(ctx); err != nil {
		errs = append(errs, err)
	} else {
		for _, s := range instanceStatuses {
			r.d.Metrics.Instances(s, byStatus[s])
		}
	}
	if overdue, onTime, err := r.d.Workload.LiveAssignments(ctx, r.now()); err != nil {
		errs = append(errs, err)
	} else {
		r.d.Metrics.OpenAssignments(true, overdue)
		r.d.Metrics.OpenAssignments(false, onTime)
	}
	return errors.Join(errs...)
}

// tallyKey carries the outcomes of the transitions one transaction
// records, so they are counted only once it commits.
type tallyKey struct{}

type tally struct{ outcomes []string }

// counted records t's outcome on the tally ctx carries, if any.
func counted(ctx context.Context, outcome string) {
	if t, ok := ctx.Value(tallyKey{}).(*tally); ok {
		t.outcomes = append(t.outcomes, outcome)
	}
}

// inTenant runs fn in a tenant transaction and, once it commits, counts
// the transitions it recorded. fn's outer context carries the tally.
func (r *Runner) inTenant(ctx context.Context, fn func(txCtx, outer context.Context, st InstanceStore) error) error {
	t := &tally{}
	outer := context.WithValue(ctx, tallyKey{}, t)
	err := r.d.Tx.InTenant(outer, func(txCtx context.Context, st InstanceStore) error {
		return fn(txCtx, outer, st)
	})
	if err == nil && r.d.Metrics != nil {
		for _, o := range t.outcomes {
			r.d.Metrics.Transition(o)
		}
	}
	return err
}
