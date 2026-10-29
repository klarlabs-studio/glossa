package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Approvals of a release request (RFC 0006 §3.2, §4.2, §5.1): human
// only, four-eyes against the requester, and approvals.decide checked
// in the request's environment — not in a locale.

func newReleaseWorkWorld(t *testing.T) (*world, *fakeReleases, context.Context) {
	t.Helper()
	w := newWorld(t)
	requester := w.person([]string{"developer"}, nil, nil, uuid.Nil)
	releases := &fakeReleases{facts: app.ReleaseRequestFacts{
		Environment: "production", Requester: actorOf(requester), State: "pending",
		Required: 2, From: domain.Party{Role: "reviewer"},
	}}
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	w.svc = app.NewWorkService(w.work, w.dir, w.authors, app.WithWorkClock(func() time.Time { return clock }),
		app.WithReleaseRequests(releases))
	return w, releases, requester
}

func TestReleaseApprovalsAreEnvironmentScopedHumanAndFourEyes(t *testing.T) {
	w, _, requester := newReleaseWorkWorld(t)
	request := uuid.New()
	a, err := w.svc.RequestApprovalForInstance(requester, app.WorkflowApproval{
		InstanceID: uuid.New(), ProjectID: w.project,
		Subject: domain.ApprovalSubject{Kind: domain.SubjectReleaseRequest, ID: request},
		Params:  domain.RequestApproval{N: 2, From: domain.Party{Role: "reviewer"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// An owner who requested is refused as the requester even if they
	// were eligible: give the requester the reviewer role too.
	selfReviewer := w.person([]string{"reviewer", "developer"}, nil, nil, uuid.Nil)
	w.svc = app.NewWorkService(w.work, w.dir, w.authors, app.WithReleaseRequests(&fakeReleases{facts: app.ReleaseRequestFacts{
		Environment: "production", Requester: actorOf(selfReviewer), Required: 2, From: domain.Party{Role: "reviewer"},
	}}))
	if _, err := w.svc.Decide(selfReviewer, a.ID, domain.VerdictGranted, ""); !errors.Is(err, domain.ErrOwnText) ||
		!errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("the requester approving their own release: err = %v", err)
	}

	// A token never decides, whatever its scopes.
	if _, err := w.svc.Decide(w.token("read", "write", "publish", "admin"), a.ID, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a token approving: err = %v", err)
	}

	// A reviewer limited to de decides production: the locale scope does
	// not limit a release.
	deReviewer := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	got, err := w.svc.Decide(deReviewer, a.ID, domain.VerdictGranted, "")
	if err != nil {
		t.Fatalf("a de reviewer approving production: %v", err)
	}
	if got.State != domain.ApprovalPending {
		t.Fatalf("one approval of two closed it: %s", got.State)
	}
	// The same reviewer again counts once.
	if got, err = w.svc.Decide(deReviewer, a.ID, domain.VerdictGranted, ""); err != nil || len(got.Granters(actorOf(selfReviewer))) != 1 {
		t.Fatalf("a repeated grant: %+v, %v", got, err)
	}

	// A reviewer whose environment scope leaves production out cannot.
	stagingOnly := authztest.InEnvironments(w.person([]string{"reviewer"}, nil, nil, uuid.Nil), "staging")
	if _, err := w.svc.Decide(stagingOnly, a.ID, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a reviewer scoped to staging approving production: err = %v", err)
	}
	// Nor can someone without approvals.decide.
	if _, err := w.svc.Decide(w.person([]string{"developer"}, nil, nil, uuid.Nil), a.ID, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a developer approving: err = %v", err)
	}

	// A second, distinct reviewer completes it.
	other := w.person([]string{"reviewer"}, nil, nil, uuid.Nil)
	if got, err = w.svc.Decide(other, a.ID, domain.VerdictGranted, ""); err != nil || got.State != domain.ApprovalGranted {
		t.Fatalf("the second approval: %+v, %v", got, err)
	}
}

// Without Release wired, no release request can be decided: the
// environment and the requester cannot be known, so the decision fails
// closed.
func TestReleaseApprovalsFailClosedWithoutRelease(t *testing.T) {
	w := newWorld(t)
	requester := w.person([]string{"developer"}, nil, nil, uuid.Nil)
	a, err := w.svc.RequestApprovalForInstance(requester, app.WorkflowApproval{
		InstanceID: uuid.New(), ProjectID: w.project,
		Subject: domain.ApprovalSubject{Kind: domain.SubjectReleaseRequest, ID: uuid.New()},
		Params:  domain.RequestApproval{N: 1, From: domain.Party{Role: "reviewer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := w.person([]string{"reviewer"}, nil, nil, uuid.Nil)
	if _, err := w.svc.Decide(reviewer, a.ID, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("deciding with no Release wired: err = %v", err)
	}
}
