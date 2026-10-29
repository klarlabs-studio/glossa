package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Rebase and retention (RFC 0006 §2.3, §2.5, §13 wave 6), with every
// port faked.

// assignWithDueV2 is assignWithDue with a review after translating:
// translating keeps its name and its entry action.
const assignWithDueV2 = `{
  "schema": "glossa.workflow/v1", "name": "assign-with-due", "subject": "translation",
  "chart": { "id": "assign-with-due", "initial": "translating", "states": {
    "translating": { "type": "atomic", "entry": ["assign_vendor"], "transitions": [
      { "event": "assignment.completed", "target": "reviewing" },
      { "event": "timer.due", "target": "escalated" } ] },
    "escalated": { "type": "atomic", "transitions": [ { "event": "assignment.completed", "target": "reviewing" } ] },
    "reviewing": { "type": "atomic", "transitions": [ { "event": "translation.reviewed", "target": "done" } ] },
    "done": { "type": "final" } } },
  "guards": {},
  "actions": { "assign_vendor": { "use": "assign", "to": { "vendor": "lingua-gmbh" }, "due": "P1D" } }
}`

// assignWithDueV3 has no translating state.
const assignWithDueV3 = `{
  "schema": "glossa.workflow/v1", "name": "assign-with-due", "subject": "translation",
  "chart": { "id": "assign-with-due", "initial": "reviewing", "states": {
    "reviewing": { "type": "atomic", "transitions": [ { "event": "translation.reviewed", "target": "done" } ] },
    "done": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

// addVersion stores doc as version n of the bound definition.
func (w *runWorld) addVersion(n int, doc string) {
	w.t.Helper()
	d, err := domain.Compile([]byte(doc))
	if err != nil {
		w.t.Fatalf("compile: %v", err)
	}
	def := w.bindings.res.Version.DefinitionID
	if w.store.later[def] == nil {
		w.store.later[def] = map[int]domain.Version{}
	}
	w.store.later[def][n] = domain.Version{ID: uuid.New(), DefinitionID: def, Number: n, Schema: d.Schema,
		Document: d.Document, CreatedBy: "test", CreatedAt: w.now}
}

// started starts the instance in translating: its entry assigned the
// vendor and set a due date.
func startedInTranslating(t *testing.T) (*runWorld, domain.Instance) {
	w := newRunWorld(t, assignWithDue)
	w.send(domain.EventTranslationRevised, w.person([]string{"developer"}))
	inst, _ := w.only()
	if inst.State != "translating" || len(w.as.assigned) != 1 || inst.Timer == nil {
		t.Fatalf("start: %+v, assigned %d", inst, len(w.as.assigned))
	}
	return w, inst
}

// manager makes w.ctx a workflow manager's and returns their actor.
func (w *runWorld) manager() outbox.Actor {
	w.ctx = authztest.Member(w.ctx, w.tenant, []string{"admin"})
	p, _ := authz.From(w.ctx)
	return outbox.Actor(p.Actor.String())
}

func TestARebaseMovesTheInstanceAndRunsNothingAgain(t *testing.T) {
	w, inst := startedInTranslating(t)
	w.addVersion(2, assignWithDueV2)
	timer := *inst.Timer.DueAt
	worker := w.ctx
	actor := w.manager()
	w.now = w.now.Add(time.Hour)

	got, err := w.runner.Rebase(w.ctx, app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.State != "translating" || got.Status != domain.StatusActive {
		t.Fatalf("rebased %+v", got)
	}
	inst, log := w.only()
	if inst.Version != 2 || inst.Timer == nil || !inst.Timer.DueAt.Equal(timer) {
		t.Errorf("stored %+v: the version moved and the timer kept its due date?", inst)
	}
	// Nothing ran again: the vendor was asked once.
	if len(w.as.assigned) != 1 {
		t.Errorf("assigned %d times; a rebase runs no entry action", len(w.as.assigned))
	}
	last := log[len(log)-1]
	if last.Event != app.TransitionRebase || last.From != "translating" || last.To != "translating" ||
		last.Outcome != app.TransitionApplied || last.Actor != actor || len(last.Actions) != 1 ||
		last.Actions[0].Detail != "version 1 → 2" {
		t.Errorf("log entry %+v", last)
	}
	e := w.store.published[len(w.store.published)-1]
	rebased, ok := e.Payload.(domain.InstanceRebased)
	if e.Type != domain.EventInstanceRebased || e.Actor != actor || e.ID != last.EventID || !ok ||
		rebased.FromVersion != 1 || rebased.ToVersion != 2 || rebased.State != "translating" {
		t.Errorf("published %+v", e)
	}

	// The instance runs on version 2: completing the assignment now
	// moves it to v2's review, which v1 did not have.
	w.ctx = worker
	if err := w.runner.Handle(w.ctx, app.Event{ID: uuid.New(), Name: domain.EventAssignmentCompleted,
		Actor: actor, Instance: inst.ID}); err != nil {
		t.Fatal(err)
	}
	if inst, _ = w.only(); inst.State != "reviewing" {
		t.Errorf("after assignment.completed on v2: %q", inst.State)
	}
}

func TestARebaseIsRefusedAndChangesNothing(t *testing.T) {
	w, inst := startedInTranslating(t)
	w.addVersion(2, assignWithDueV2)
	w.addVersion(3, assignWithDueV3)
	worker := w.ctx
	w.manager()
	manager := w.ctx
	_, before := w.only()

	for name, c := range map[string]struct {
		in   app.RebaseInput
		want error
	}{
		"onto a version without its state": {app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1, Version: 3}, domain.ErrRebaseStateMissing},
		"the latest lacks its state":       {app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1}, domain.ErrRebaseStateMissing},
		"a stale If-Match":                 {app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 2, Version: 2}, app.ErrConflict},
		"onto its own version":             {app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1, Version: 1}, domain.ErrRebaseNotNewer},
		"onto a version that isn't there":  {app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1, Version: 9}, app.ErrRebaseVersion},
		"in another project":               {app.RebaseInput{Project: uuid.New(), Instance: inst.ID, IfVersion: 1, Version: 2}, app.ErrNotFound},
		"an instance that isn't there":     {app.RebaseInput{Project: w.project, Instance: uuid.New(), IfVersion: 1, Version: 2}, app.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := w.runner.Rebase(manager, c.in); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	t.Run("by someone without workflows.manage", func(t *testing.T) {
		translator := authztest.Member(worker, w.tenant, []string{"translator"}, "de")
		_, err := w.runner.Rebase(translator, app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1, Version: 2})
		if !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("err = %v, want forbidden", err)
		}
	})
	after, log := w.only()
	if after.Version != 1 || len(log) != len(before) {
		t.Errorf("a refused rebase changed the instance: version %d, %d log entries (was %d)", after.Version, len(log), len(before))
	}
}

func TestAFinishedInstanceIsNotRebased(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	w.send(domain.EventTranslationReviewed, w.person([]string{"reviewer"}, "de"))
	inst, _ := w.only()
	if inst.Status != domain.StatusFinished {
		t.Fatalf("not finished: %+v", inst)
	}
	w.addVersion(2, waitForReview)
	w.manager()
	if _, err := w.runner.Rebase(w.ctx, app.RebaseInput{Project: w.project, Instance: inst.ID, IfVersion: 1}); !errors.Is(err, domain.ErrInstanceFinished) {
		t.Fatalf("err = %v, want ErrInstanceFinished", err)
	}
}

func TestRetentionDeletesOnlyFinishedInstancesPastTheirAge(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	w.send(domain.EventTranslationReviewed, w.person([]string{"reviewer"}, "de"))
	finished, _ := w.only()
	// A second subject, still running.
	w.message = uuid.New()
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	w.runner = app.NewRunner(app.RunnerDeps{Tx: w.store, Definitions: w.bindings, Retention: w.store,
		Now: func() time.Time { return w.now }})

	if _, err := w.runner.SweepRetention(t.Context(), time.Hour); err == nil {
		t.Error("a retention under a day was accepted")
	}
	keep := 30 * 24 * time.Hour
	w.now = w.now.Add(keep - time.Minute)
	if n, err := w.runner.SweepRetention(t.Context(), keep); err != nil || n != 0 {
		t.Fatalf("swept %d before its age (%v)", n, err)
	}
	w.now = w.now.Add(2 * time.Minute)
	if n, err := w.runner.SweepRetention(t.Context(), keep); err != nil || n != 1 {
		t.Fatalf("swept %d, want 1 (%v)", n, err)
	}
	if _, ok := w.store.instances[finished.ID]; ok {
		t.Error("the finished instance is still there")
	}
	if _, ok := w.store.transitions[finished.ID]; ok {
		t.Error("its transition log is still there")
	}
	if len(w.store.instances) != 1 {
		t.Fatalf("instances = %d; the running one must stay", len(w.store.instances))
	}
	for _, i := range w.store.instances {
		if i.Status != domain.StatusActive {
			t.Errorf("left %+v", i)
		}
	}
}

func (m *memStore) TenantsWithExpiredInstances(_ context.Context, cutoff time.Time, _ int) ([]tenancy.ID, error) {
	for _, i := range m.instances {
		if i.Status == domain.StatusFinished && i.FinishedAt.Before(cutoff) {
			return []tenancy.ID{tenantOfTest}, nil
		}
	}
	return nil, nil
}
