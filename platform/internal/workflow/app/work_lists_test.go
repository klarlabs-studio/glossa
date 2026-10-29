package app_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The API's list of assignments answers what the caller may see: a
// manager every assignment (or, asking for it, their own), everyone
// else their own — a vendor's member included.
func TestVisibleAssignments(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	units := []domain.Unit{{Message: uuid.New(), Locale: "de"}}
	toVendor, _, err := w.svc.Assign(admin, app.AssignInput{ProjectID: w.project, Units: units, To: domain.Party{Vendor: "lingua-gmbh"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Assign(admin, app.AssignInput{ProjectID: w.project, Units: units, To: domain.Party{Group: "legal"}}); err != nil {
		t.Fatal(err)
	}

	all, err := w.svc.VisibleAssignments(admin, app.AssignmentFilter{}, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("a manager sees %d, %v; want both", len(all), err)
	}
	mine, err := w.svc.VisibleAssignments(admin, app.AssignmentFilter{}, true)
	if err != nil || len(mine) != 0 {
		t.Fatalf("a manager's own work = %d, %v; want none", len(mine), err)
	}
	vera := w.person([]string{"translator"}, []string{"de"}, nil, w.vendor)
	for _, own := range []bool{false, true} {
		got, err := w.svc.VisibleAssignments(vera, app.AssignmentFilter{}, own)
		if err != nil || len(got) != 1 || got[0].ID != toVendor.ID {
			t.Fatalf("the vendor's member (mine=%v) sees %+v, %v; want only the vendor's assignment", own, got, err)
		}
	}
	// The same with visibility `assigned`, which holds no tenant-wide
	// permission at all.
	assigned := w.vendorMember(&authztest.Coverage{}, "de")
	if got, err := w.svc.VisibleAssignments(assigned, app.AssignmentFilter{}, false); err != nil || len(got) != 1 || got[0].ID != toVendor.ID {
		t.Fatalf("an assigned vendor member sees %+v, %v; want only the vendor's assignment", got, err)
	}
	// A token has no work of its own, and reads no one else's without
	// assignments.manage.
	if got, err := w.svc.VisibleAssignments(w.token("read"), app.AssignmentFilter{}, false); err != nil || len(got) != 0 {
		t.Fatalf("a read token sees %d, %v", len(got), err)
	}
}

// Approvals are listed with workflows.read and filtered by subject.
func TestApprovalsList(t *testing.T) {
	w := newWorld(t)
	vera := w.person([]string{"translator"}, []string{"de"}, nil, w.vendor)
	a, subject := w.requestApproval(2, domain.Party{Role: "reviewer"}, actorOf(vera))
	w.requestApproval(1, domain.Party{Role: "reviewer"}, actorOf(vera))
	reviewer := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	if _, err := w.svc.Decide(reviewer, a.ID, domain.VerdictGranted, "fine"); err != nil {
		t.Fatal(err)
	}

	all, err := w.svc.Approvals(reviewer, app.ApprovalFilter{Project: w.project})
	if err != nil || len(all) != 2 {
		t.Fatalf("approvals = %d, %v; want 2", len(all), err)
	}
	one, err := w.svc.Approvals(reviewer, app.ApprovalFilter{SubjectID: subject.ID, Locale: "de"})
	if err != nil || len(one) != 1 || one[0].ID != a.ID || len(one[0].Decisions) != 1 {
		t.Fatalf("approvals of one unit = %+v, %v", one, err)
	}
	// A vendor's member is refused: a vendor does not sign work off.
	assigned := w.vendorMember(&authztest.Coverage{}, "de")
	if _, err := w.svc.Approvals(assigned, app.ApprovalFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("an assigned member listing approvals: err = %v, want forbidden", err)
	}
}

// An Idempotency-Key makes a retried assignment the first one.
func TestAssignIsIdempotentOnItsKey(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	in := app.AssignInput{ProjectID: w.project, Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}},
		To: domain.Party{Vendor: "lingua-gmbh"}, IdempotencyKey: "job-1"}
	first, replayed, err := w.svc.Assign(admin, in)
	if err != nil || replayed {
		t.Fatalf("first = %v, replayed %v", err, replayed)
	}
	again, replayed, err := w.svc.Assign(admin, in)
	if err != nil || !replayed || again.ID != first.ID || len(w.work.assignments) != 1 {
		t.Fatalf("retry = %s (replayed %v), %v; %d assignments", again.ID, replayed, err, len(w.work.assignments))
	}
	in.ProjectID = uuid.New()
	if _, _, err := w.svc.Assign(admin, in); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Fatalf("the key for another project: err = %v", err)
	}
	in.IdempotencyKey = "not printable\n"
	if _, _, err := w.svc.Assign(admin, in); err == nil {
		t.Fatal("a malformed key was accepted")
	}
}
