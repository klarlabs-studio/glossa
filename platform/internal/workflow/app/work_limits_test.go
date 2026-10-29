package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// RFC 0006 §9.6: at most 1,000 open assignments per assignee, at the
// boundary, by hand and from a workflow.

// fill gives the vendor n live assignments.
func (w *world) fill(n int) {
	vendor := domain.Assignee{Kind: domain.AssigneeVendor, ID: w.vendor}
	for range n {
		a := domain.Assignment{ID: uuid.New(), ProjectID: w.project, Assignee: vendor, State: domain.AssignmentOpen,
			Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}}}
		w.work.assignments[a.ID] = a
	}
}

func TestAnAssigneeHoldsAtMost1000OpenAssignments(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	in := app.AssignInput{ProjectID: w.project, Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}},
		To: domain.Party{Vendor: "lingua-gmbh"}}
	w.fill(domain.MaxOpenAssignments - 1)

	last, _, err := w.svc.Assign(admin, in)
	if err != nil {
		t.Fatalf("the %dth: %v", domain.MaxOpenAssignments, err)
	}
	_, _, err = w.svc.Assign(admin, in)
	if !errors.Is(err, app.ErrLimit) {
		t.Fatalf("the %dth: err = %v, want ErrLimit", domain.MaxOpenAssignments+1, err)
	}
	want := fmt.Sprintf("already holds %d open assignments", domain.MaxOpenAssignments)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal says %q", err)
	}
	// From a workflow too: the runner records the action as failed.
	if _, err := w.svc.AssignForInstance(admin, app.WorkflowAssign{InstanceID: uuid.New(), ProjectID: w.project,
		Subject: domain.SubjectTranslation, Units: in.Units, Params: domain.Assign{To: in.To}}); !errors.Is(err, app.ErrLimit) {
		t.Errorf("from a workflow: err = %v, want ErrLimit", err)
	}

	// A closed assignment frees its place; other assignees are not
	// counted against this one.
	if _, err := w.svc.Decline(w.person([]string{"translator"}, []string{"de"}, nil, w.vendor), last.ID, "no capacity"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if _, _, err := w.svc.Assign(admin, in); err != nil {
		t.Errorf("after one closed: %v", err)
	}
	if _, _, err := w.svc.Assign(admin, app.AssignInput{ProjectID: w.project, Units: in.Units,
		To: domain.Party{Group: "legal"}}); err != nil {
		t.Errorf("another assignee: %v", err)
	}
}

func TestAWorkflowAtTheLimitStaysWhereItWas(t *testing.T) {
	w := newRunWorld(t, assignWithDue)
	w.as.err = fmt.Errorf("%w: lingua-gmbh already holds 1000 open assignments", app.ErrLimit)
	w.send(domain.EventTranslationRevised, w.person([]string{"developer"}))
	inst, log := w.only()
	if inst.Started() || len(log) != 1 || log[0].Outcome != app.TransitionRefused ||
		len(log[0].Actions) != 1 || log[0].Actions[0].Outcome != app.ActionFailed {
		t.Fatalf("instance %+v, log %+v", inst, log)
	}
}
