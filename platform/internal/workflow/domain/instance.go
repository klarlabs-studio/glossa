package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.klarlabs.de/statekit"
)

// Instance statuses (RFC 0006 §2.5).
const (
	StatusActive   = "active"
	StatusFinished = "finished"
)

// NotStarted is the state of an instance whose initial state's entry
// actions were refused: it never entered the chart, and the next event
// tries again.
const NotStarted = ""

// Instance is one workflow instance: a subject running on one version
// of one definition. It exists only while work is in flight; a finished
// instance keeps its transition log and drops its snapshot.
type Instance struct {
	ID         uuid.UUID
	Project    uuid.UUID
	Definition uuid.UUID
	Version    int
	Kind       SubjectKind
	// SubjectID is the message of a translation unit, the release
	// request otherwise.
	SubjectID uuid.UUID
	// Locale is a translation unit's canonical BCP 47 tag; empty for a
	// release request.
	Locale string
	State  string
	Status string
	// Timer is the instance's pending due date, if an action set one.
	Timer      *Timer
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
}

// Started reports whether the instance has entered its chart.
func (i Instance) Started() bool { return i.State != NotStarted }

// Rebase errors (RFC 0006 §2.3): moving a running instance to a newer
// version of its definition is explicit, and refused for any instance
// whose state the new version lacks.
var (
	// ErrInstanceFinished is a rebase of an instance that has finished:
	// nothing is left to run on another version.
	ErrInstanceFinished = errors.New("workflow: the instance has finished")
	// ErrRebaseNotNewer is a rebase onto the version the instance runs
	// on, or an older one. A rebase only moves forward; the way back is
	// saving the older document again as the next version.
	ErrRebaseNotNewer = errors.New("workflow: a rebase moves an instance to a newer version")
	// ErrRebaseStateMissing is a rebase onto a version whose chart has
	// no state of the instance's state's name that an instance can wait
	// in (an atomic one).
	ErrRebaseStateMissing = errors.New("workflow: the target version has no such state")
	// ErrRebaseStateFinal is a rebase onto a version in which the
	// instance's state is final: the instance would be active in a state
	// that never moves, and would never finish.
	ErrRebaseStateFinal = errors.New("workflow: the state is final in the target version")
)

// Rebase moves the instance onto version of target — a newer version of
// the same definition — keeping its state by name. It returns the
// instance as rebased and the snapshot to store: nil for an instance
// that has not started, which the next event starts on target.
//
// A rebase changes what happens next, never what already happened: the
// state's entry actions are not run again, so the assignments and
// approvals they asked for stand and nothing is asked twice; and a
// pending timer keeps its due date, because the state that set it is
// the state the instance stays in.
func (i Instance) Rebase(target *Definition, version int, now time.Time) (Instance, json.RawMessage, error) {
	if i.Status != StatusActive {
		return Instance{}, nil, ErrInstanceFinished
	}
	if version <= i.Version {
		return Instance{}, nil, fmt.Errorf("%w: it runs on version %d, and %d is not newer", ErrRebaseNotNewer, i.Version, version)
	}
	var snapshot json.RawMessage
	if i.Started() {
		s := target.machine.GetState(statekit.StateID(i.State))
		switch {
		case s != nil && s.IsFinal():
			return Instance{}, nil, fmt.Errorf("%w: %q is final in version %d", ErrRebaseStateFinal, i.State, version)
		case s == nil || !s.IsAtomic():
			return Instance{}, nil, fmt.Errorf("%w: version %d has no state %q to wait in (its states: %s)",
				ErrRebaseStateMissing, version, i.State, strings.Join(target.States(), ", "))
		}
		snapshot = target.Snapshot(i.State)
	}
	out := i
	out.Version, out.UpdatedAt = version, now
	return out, snapshot, nil
}

// Timer is when timer.due and timer.overdue fall due for an instance,
// and the state that set them (RFC 0006 §2.3: a stored due_at, never a
// timer in the chart). Leaving that state cancels them.
type Timer struct {
	State string
	// DueAt and OverdueAt are nil once raised.
	DueAt     *time.Time
	OverdueAt *time.Time
}

// NewTimer is the timer an action with a due period sets in state at
// now: timer.due when the period has passed, and timer.overdue when it
// has passed again — the escalation a definition can react to.
func NewTimer(state string, now time.Time, due Duration) *Timer {
	d, o := now.Add(due.Std()), now.Add(2*due.Std())
	return &Timer{State: state, DueAt: &d, OverdueAt: &o}
}

// Due returns the timer event that has fallen due at now, if any, and
// the timer that remains once it is raised (nil when nothing remains).
// timer.due is raised before timer.overdue.
func (t *Timer) Due(now time.Time) (EventName, *Timer, bool) {
	if t == nil {
		return "", nil, false
	}
	rest := *t
	switch {
	case t.DueAt != nil && !t.DueAt.After(now):
		rest.DueAt = nil
		return EventTimerDue, rest.orNil(), true
	case t.DueAt == nil && t.OverdueAt != nil && !t.OverdueAt.After(now):
		rest.OverdueAt = nil
		return EventTimerOverdue, rest.orNil(), true
	}
	return "", t, false
}

func (t Timer) orNil() *Timer {
	if t.DueAt == nil && t.OverdueAt == nil {
		return nil
	}
	return &t
}

// snapshot is what an instance stores to Restore from: the machine and
// the state. A definition's chart has no history or parallel states and
// no delayed transitions (§2.3), so the state is the whole of
// statekit's snapshot; the step context is loaded afresh for every
// transition and never stored, so no subject data is kept with it.
type snapshot struct {
	MachineID string `json:"machine_id"`
	State     string `json:"state"`
}

// Snapshot is what an instance in state stores to Restore from.
func (d *Definition) Snapshot(state string) json.RawMessage {
	b, _ := json.Marshal(snapshot{MachineID: d.machine.ID, State: state})
	return b
}

// RestoreState reads a stored snapshot back into the state to Advance
// from, refusing one taken on another chart.
func (d *Definition) RestoreState(raw []byte) (string, error) {
	var s snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("workflow: snapshot: %w", err)
	}
	if s.MachineID != d.machine.ID {
		return "", fmt.Errorf("workflow: snapshot of %q restored on %q", s.MachineID, d.machine.ID)
	}
	if d.machine.GetState(statekit.StateID(s.State)) == nil {
		return "", fmt.Errorf("workflow: snapshot state %q is not in %q", s.State, d.machine.ID)
	}
	return s.State, nil
}

// GuardOutcome is one guard evaluated for a transition.
type GuardOutcome struct {
	Guard  string
	Passed bool
}

// GuardOutcomes evaluates every guard on a transition out of state (or
// out of one of its ancestors) for step's event, in the order statekit
// would consider them — what the transition log records beside the
// transition statekit took, so "why didn't it move?" has an answer.
func (d *Definition) GuardOutcomes(state string, step Step) []GuardOutcome {
	var out []GuardOutcome
	for id := statekit.StateID(state); id != ""; {
		s := d.machine.GetState(id)
		if s == nil {
			break
		}
		for _, t := range s.Transitions {
			if string(t.Event) != string(step.Trigger.Event) || t.Guard == "" {
				continue
			}
			fn, ok := d.guardFns[string(t.Guard)]
			out = append(out, GuardOutcome{Guard: string(t.Guard), Passed: ok && fn(step)})
		}
		id = s.Parent
	}
	return out
}
