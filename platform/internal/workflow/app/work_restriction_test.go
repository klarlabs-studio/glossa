package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Assignments and approvals under project scope and `assigned`
// visibility (RFC 0006 §3.3, §4.1). Work is the one thing a vendor's
// member must be able to reach, and everything else about a project
// outside someone's scope must look like it doesn't exist.

// vendorMember is a vendor's translator with visibility `assigned`,
// registered in the directory as the vendor's, seeing what cov covers.
func (w *world) vendorMember(cov authz.Coverage, locales ...string) context.Context {
	ctx, member := authztest.Assigned(tenancy.ContextWithTenant(context.Background(), w.tenant), w.tenant, cov, locales...)
	p, _ := authz.From(ctx)
	person := identity.NewPersonID()
	p.Person, p.Actor = person, identity.PersonActor(person)
	w.dir.members[member.UUID()] = app.Affiliation{Member: member.UUID(), Roles: []string{"translator"}, Vendor: w.vendor}
	return authz.WithPrincipal(ctx, p)
}

// scoped is a person with roles limited to projects.
func (w *world) scoped(projects []uuid.UUID, roles []string, locales ...string) context.Context {
	ctx := authztest.ScopedMember(tenancy.ContextWithTenant(context.Background(), w.tenant), w.tenant, projects, roles, locales...)
	p, _ := authz.From(ctx)
	person := identity.NewPersonID()
	p.Person, p.Actor = person, identity.PersonActor(person)
	w.dir.members[p.Member.UUID()] = app.Affiliation{Member: p.Member.UUID(), Roles: roles}
	return authz.WithPrincipal(ctx, p)
}

func TestAVendorMemberDoesTheWorkTheirAssignmentCovers(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	unit := domain.Unit{Message: uuid.New(), Locale: "de"}
	a, _, err := w.svc.Assign(admin, app.AssignInput{
		ProjectID: w.project, Units: []domain.Unit{unit}, To: domain.Party{Vendor: "lingua-gmbh"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cov := &authztest.Coverage{}
	vera := w.vendorMember(cov, "de")
	p, _ := authz.From(vera)
	cov.Assign(p.Member, w.project, unit.Message, "de")

	// Their own work is theirs to read, though authz.Require refuses an
	// assigned member every tenant-wide permission.
	if got, err := w.svc.Assignment(vera, a.ID); err != nil || got.ID != a.ID {
		t.Fatalf("reading their assignment: %+v, %v", got, err)
	}
	mine, err := w.svc.MyAssignments(vera, app.AssignmentFilter{})
	if err != nil || len(mine) != 1 {
		t.Fatalf("my work = %d, %v; want their one assignment", len(mine), err)
	}
	if _, err := w.svc.Accept(vera, a.ID); err != nil {
		t.Fatalf("accepting covered work: %v", err)
	}
	if _, err := w.svc.Complete(vera, a.ID); err != nil {
		t.Fatalf("completing covered work: %v", err)
	}
	// Managing is never theirs.
	if _, err := w.svc.Assignments(vera, app.AssignmentFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("listing the tenant's assignments: err = %v", err)
	}
}

func TestAVendorMemberWithoutCoverageCannotDoTheWork(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	a, _, err := w.svc.Assign(admin, app.AssignInput{
		ProjectID: w.project, Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}},
		To: domain.Party{Vendor: "lingua-gmbh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Coverage says nothing is theirs: the assignment row alone must not
	// let them act, since coverage is what every other read path trusts.
	vera := w.vendorMember(&authztest.Coverage{}, "de")
	if _, err := w.svc.Accept(vera, a.ID); !errors.Is(err, authz.ErrNotVisible) {
		t.Fatalf("accepting uncovered work: err = %v, want not visible", err)
	}
}

func TestAProjectScopedManagerSeesOnlyTheirProjects(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	other := uuid.New()
	theirs, _, err := w.svc.Assign(admin, app.AssignInput{
		ProjectID: other, Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}}, To: domain.Party{Vendor: "lingua-gmbh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	outside, _, err := w.svc.Assign(admin, app.AssignInput{
		ProjectID: w.project, Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}}, To: domain.Party{Vendor: "lingua-gmbh"},
	})
	if err != nil {
		t.Fatal(err)
	}

	manager := w.scoped([]uuid.UUID{other}, []string{"admin"})
	list, err := w.svc.Assignments(manager, app.AssignmentFilter{})
	if err != nil || len(list) != 1 || list[0].ID != theirs.ID {
		t.Fatalf("list = %+v, %v; want only their project's", list, err)
	}
	for name, call := range map[string]func() error{
		"read":    func() error { _, err := w.svc.Assignment(manager, outside.ID); return err },
		"decline": func() error { _, err := w.svc.Decline(manager, outside.ID, "no"); return err },
		"assign": func() error {
			_, _, err := w.svc.Assign(manager, app.AssignInput{
				ProjectID: w.project, Units: []domain.Unit{{Message: uuid.New(), Locale: "de"}}, To: domain.Party{Vendor: "lingua-gmbh"},
			})
			return err
		},
	} {
		if err := call(); !errors.Is(err, authz.ErrNotVisible) {
			t.Errorf("%s outside their scope: err = %v, want not visible", name, err)
		}
	}
}

func TestAProjectScopedReviewerCannotDecideElsewhere(t *testing.T) {
	w := newWorld(t)
	ap, _ := w.requestApproval(1, domain.Party{Role: "reviewer"}, "person:"+uuid.NewString())
	reviewer := w.scoped([]uuid.UUID{uuid.New()}, []string{"reviewer"}, "de")
	if _, err := w.svc.Decide(reviewer, ap.ID, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrNotVisible) {
		t.Fatalf("deciding outside their scope: err = %v, want not visible", err)
	}
	if _, err := w.svc.Approval(reviewer, ap.ID); !errors.Is(err, authz.ErrNotVisible) {
		t.Fatalf("reading an approval outside their scope: err = %v", err)
	}
}
