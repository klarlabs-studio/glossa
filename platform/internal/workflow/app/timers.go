package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Timers (RFC 0006 §2.3): no timer lives in a process. An action with a
// due period stores due_at on the instance; this sweep, a job on the
// kernel scheduler, raises timer.due and timer.overdue from it as
// outbox events whose actor is the scheduler, and the runner handles
// those like any other event. A restart loses nothing, and the outbox's
// idempotency covers a timer exactly as it covers a review.

// The timer events Workflow publishes, and their aggregate.
const (
	AggregateInstance         = "workflow_instance"
	EventInstanceTimerDue     = "workflow.instance.timer_due"
	EventInstanceTimerOverdue = "workflow.instance.timer_overdue"
)

// TimerRaised is the payload of the timer events.
type TimerRaised struct {
	InstanceID string `json:"instance_id"`
	ProjectID  string `json:"project_id"`
	// State is the state that set the timer: an instance that has left
	// it ignores the event.
	State string `json:"state"`
}

// Sweep bounds.
const (
	maxTimerTenants    = 200
	maxTimersPerTenant = 500
)

// SweepTimers raises every timer that has fallen due, tenant by tenant.
// It runs outside any tenant (a leased scheduler job) and needs
// RunnerDeps.Timers. A failing tenant doesn't stop the others.
func (r *Runner) SweepTimers(ctx context.Context) (int, error) {
	if r.d.Timers == nil {
		return 0, errors.New("workflow: SweepTimers needs a TimerScanner")
	}
	tenants, err := r.d.Timers.TenantsWithDueTimers(ctx, r.now(), maxTimerTenants)
	if err != nil {
		return 0, err
	}
	var (
		raised int
		errs   []error
	)
	for _, t := range tenants {
		n, err := r.RaiseDueTimers(tenancy.ContextWithTenant(ctx, t))
		raised += n
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return raised, errors.Join(errs...)
}

// RaiseDueTimers publishes the due timer events of the tenant on ctx
// and clears what it raised, in one transaction.
func (r *Runner) RaiseDueTimers(ctx context.Context) (int, error) {
	now := r.now()
	raised := 0
	err := r.d.Tx.InTenant(ctx, func(ctx context.Context, st InstanceStore) error {
		instances, err := st.LockDueTimers(ctx, now, maxTimersPerTenant)
		if err != nil {
			return err
		}
		for _, inst := range instances {
			n, err := r.raise(ctx, st, inst, now)
			if err != nil {
				return err
			}
			raised += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return raised, nil
}

func (r *Runner) raise(ctx context.Context, st InstanceStore, inst domain.Instance, now time.Time) (int, error) {
	n := 0
	state := ""
	if inst.Timer != nil {
		state = inst.Timer.State
	}
	for {
		ev, rest, ok := inst.Timer.Due(now)
		if !ok {
			break
		}
		typ := EventInstanceTimerDue
		if ev == domain.EventTimerOverdue {
			typ = EventInstanceTimerOverdue
		}
		if err := st.Publish(ctx, outbox.Event{
			Type: typ, AggregateType: AggregateInstance, AggregateID: inst.ID.String(),
			Actor:   authz.SystemEventActor(PrincipalScheduler),
			Payload: TimerRaised{InstanceID: inst.ID.String(), ProjectID: inst.Project.String(), State: state},
		}); err != nil {
			return 0, err
		}
		inst.Timer = rest
		n++
	}
	if n == 0 {
		return 0, nil
	}
	snapshot, err := st.Snapshot(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	return n, st.SaveInstance(ctx, inst, snapshot)
}
