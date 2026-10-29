package app_test

import (
	"context"
	"errors"
	stdmaps "maps"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

type recordingRunnerMetrics struct {
	transitions map[string]int
	instances   map[string]int
	open        map[bool]int
}

func newRecordingRunnerMetrics() *recordingRunnerMetrics {
	return &recordingRunnerMetrics{transitions: map[string]int{}, instances: map[string]int{}, open: map[bool]int{}}
}

func (r *recordingRunnerMetrics) Transition(o string)            { r.transitions[o]++ }
func (r *recordingRunnerMetrics) Instances(s string, n int)      { r.instances[s] = n }
func (r *recordingRunnerMetrics) OpenAssignments(od bool, n int) { r.open[od] = n }
func (w *runWorld) withMetrics(m app.RunnerMetrics, tx app.InstanceTransactor) {
	w.runner = app.NewRunner(app.RunnerDeps{
		Tx: tx, Definitions: w.bindings, Translations: w.tr, Assignments: w.as, Actors: w.actors,
		Timers: w.store, Now: func() time.Time { return w.now }, Metrics: m, Workload: fakeWorkload{},
	})
}

// glossa_workflow_transitions_total{outcome} moves once per recorded
// transition, by its outcome, and not for a replay (RFC 0006 §10.1).
func TestTransitionsAreCountedByOutcome(t *testing.T) {
	w := newRunWorld(t, approveOnGrant)
	m := newRecordingRunnerMetrics()
	w.withMetrics(m, w.store)
	revised := w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	w.sendID(revised, domain.EventTranslationRevised, w.person([]string{"translator"}, "de")) // a replay
	w.send(domain.EventTranslationOutdated, system)                                           // no longer applies
	w.send(domain.EventApprovalGranted, system)                                               // refused: Workflow never approves
	want := map[string]int{app.TransitionApplied: 1, app.TransitionIgnored: 1, app.TransitionRefused: 1}
	if !stdmaps.Equal(m.transitions, want) {
		t.Errorf("transitions = %v, want %v", m.transitions, want)
	}
}

// failingCommit runs the step and then fails the commit, as a database
// would: nothing it recorded happened, so nothing is counted.
type failingCommit struct{ *memStore }

var errCommit = errors.New("commit failed")

func (f failingCommit) InTenant(ctx context.Context, fn func(context.Context, app.InstanceStore) error) error {
	if err := f.memStore.InTenant(ctx, fn); err != nil {
		return err
	}
	return errCommit
}

func TestAStepThatDoesNotCommitIsNotCounted(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	m := newRecordingRunnerMetrics()
	w.withMetrics(m, failingCommit{w.store})
	ev := app.Event{ID: [16]byte{1}, Name: domain.EventTranslationRevised,
		Actor: w.person([]string{"translator"}, "de"), Subjects: []app.SubjectRef{w.unit()}}
	if err := w.runner.Handle(w.ctx, ev); !errors.Is(err, errCommit) {
		t.Fatalf("Handle = %v, want the commit's error", err)
	}
	if len(m.transitions) != 0 {
		t.Errorf("a rolled-back step was counted: %v", m.transitions)
	}
}

type fakeWorkload struct{}

func (fakeWorkload) InstancesByStatus(context.Context) (map[string]int, error) {
	return map[string]int{"active": 3}, nil
}

func (fakeWorkload) LiveAssignments(context.Context, time.Time) (int, int, error) { return 2, 5, nil }

// The gauges publish both statuses — finished at zero when none is
// counted — and both sides of overdue.
func TestCountWorkSetsTheGauges(t *testing.T) {
	w := newRunWorld(t, "")
	m := newRecordingRunnerMetrics()
	w.withMetrics(m, w.store)
	if err := w.runner.CountWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stdmaps.Equal(m.instances, map[string]int{"active": 3, "finished": 0}) {
		t.Errorf("instances = %v", m.instances)
	}
	if !stdmaps.Equal(m.open, map[bool]int{true: 2, false: 5}) {
		t.Errorf("open assignments = %v", m.open)
	}
}
