//go:build integration

package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	idpostgres "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	widentity "go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// workHarness is a tenant with Identity's real members, groups and
// vendors behind Workflow's Directory, and assignments and approvals in
// Postgres.
type workHarness struct {
	t        *testing.T
	tenant   tenancy.ID
	uow      *db.UnitOfWork
	ids      *idpostgres.Transactor
	svc      *app.WorkService
	coverage *app.Coverage
	authors  fakeAuthors
	clock    time.Time
	now      time.Time // what Coverage reads; moved by tests
}

func newWorkHarness(t *testing.T, tenant tenancy.ID) *workHarness {
	t.Helper()
	uow := db.NewUnitOfWork(env.App)
	h := &workHarness{t: t, tenant: tenant, uow: uow, ids: idpostgres.NewTransactor(uow, nil), authors: fakeAuthors{},
		clock: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	h.now = h.clock
	dir := widentity.NewDirectory(h.ids)
	tx := postgres.NewWorkTransactor(uow)
	h.svc = app.NewWorkService(tx, dir, h.authors, app.WithWorkClock(func() time.Time { return h.clock }))
	h.coverage = app.NewCoverage(tx, dir, func() time.Time { return h.now })
	return h
}

func (h *workHarness) inTenant() context.Context {
	return tenancy.ContextWithTenant(context.Background(), h.tenant)
}

func (h *workHarness) identity(fn func(context.Context, identityapp.TenantStore) error) {
	h.t.Helper()
	if err := h.ids.InTenant(h.inTenant(), fn); err != nil {
		h.t.Fatal(err)
	}
}

func (h *workHarness) vendor(name string) identity.VendorID {
	h.t.Helper()
	v, err := identity.NewVendor(h.tenant, tenancy.KindOrganization, name, "", []string{"de"}, h.clock)
	if err != nil {
		h.t.Fatal(err)
	}
	h.identity(func(ctx context.Context, st identityapp.TenantStore) error {
		_, err := st.InsertVendor(ctx, v, identity.SystemActor("test"))
		return err
	})
	return v.ID
}

func (h *workHarness) group(name string, members ...identity.Member) identity.GroupID {
	h.t.Helper()
	g, err := identity.NewGroup(h.tenant, tenancy.KindOrganization, name, h.clock)
	if err != nil {
		h.t.Fatal(err)
	}
	h.identity(func(ctx context.Context, st identityapp.TenantStore) error {
		if _, err := st.InsertGroup(ctx, g, identity.SystemActor("test")); err != nil {
			return err
		}
		for _, m := range members {
			if err := g.AddMember(m, h.clock); err != nil {
				return err
			}
			if err := st.AddGroupMember(ctx, g, m.ID, identity.SystemActor("test")); err != nil {
				return err
			}
		}
		return nil
	})
	return g.ID
}

func (h *workHarness) member(email string, roles []string, locales []string, vendor identity.VendorID) identity.Member {
	h.t.Helper()
	rs, err := identity.ParseRoles(roles)
	if err != nil {
		h.t.Fatal(err)
	}
	ls, err := identity.ParseLocaleScope(locales)
	if err != nil {
		h.t.Fatal(err)
	}
	addr, err := authgo.NewEmail(email)
	if err != nil {
		h.t.Fatal(err)
	}
	var r identity.Restriction
	if !vendor.IsZero() {
		r = identity.Restriction{Vendor: vendor, Visibility: identity.VisibilityAssigned}
	}
	m, err := identity.InviteWith(h.tenant, tenancy.KindOrganization, addr, rs, ls, r,
		identity.GrantForMember(identity.Roles{identity.RoleOwner}, identity.LocaleScope{}), h.clock)
	if err != nil {
		h.t.Fatal(err)
	}
	h.identity(func(ctx context.Context, st identityapp.TenantStore) error {
		_, err := st.InsertMember(ctx, m, identity.SystemActor("test"))
		return err
	})
	return m
}

// as acts as m, signed in.
func (h *workHarness) as(m identity.Member) context.Context {
	person := identity.NewPersonID()
	return authz.WithPrincipal(h.inTenant(), authz.Principal{
		Actor: identity.PersonActor(person), Person: person, Tenant: h.tenant, Member: m.ID,
		Grant: identity.GrantForMember(m.Roles, m.Locales),
	})
}

func (h *workHarness) manager() context.Context {
	h.t.Helper()
	ctx, err := authz.Background(h.inTenant(), "test.manager", app.PermAssignmentsManage, app.PermAssignmentsRead, app.PermWorkflowsRead)
	if err != nil {
		h.t.Fatal(err)
	}
	return ctx
}

func (h *workHarness) assign(project uuid.UUID, to domain.Party, units ...domain.Unit) domain.Assignment {
	h.t.Helper()
	a, _, err := h.svc.Assign(h.manager(), app.AssignInput{ProjectID: project, Units: units, To: to})
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

func (h *workHarness) covers(m identity.Member, project uuid.UUID, u domain.Unit) bool {
	h.t.Helper()
	ok, err := h.coverage.Covers(h.inTenant(), m.ID, project, u.Message, u.Locale)
	if err != nil {
		h.t.Fatal(err)
	}
	return ok
}

func unitIn(locale string) domain.Unit { return domain.Unit{Message: uuid.New(), Locale: locale} }

func TestCoverageDirectGroupAndVendor(t *testing.T) {
	newHarness(t)
	h := newWorkHarness(t, mustTenant(t, "work"))
	project := uuid.New()

	lingua := h.vendor("Lingua GmbH")
	vera := h.member("vera@lingua.example", []string{"translator"}, []string{"de"}, lingua)
	lena := h.member("lena@acme.example", []string{"reviewer"}, []string{"de"}, identity.VendorID{})
	max := h.member("max@acme.example", []string{"translator"}, []string{"de"}, identity.VendorID{})
	tom := h.member("tom@acme.example", []string{"translator"}, []string{"de"}, identity.VendorID{})
	h.group("Legal", lena)

	byVendor := []domain.Unit{unitIn("de"), unitIn("de")}
	byGroup, direct, byRole := unitIn("de"), unitIn("de-AT"), unitIn("de")
	vendorWork := h.assign(project, domain.Party{Vendor: "lingua gmbh"}, byVendor...)
	groupWork := h.assign(project, domain.Party{Group: "legal"}, byGroup)
	h.assign(project, domain.Party{Member: max.ID.String()}, direct)
	h.assign(project, domain.Party{Role: "translator"}, byRole)

	cases := []struct {
		who  identity.Member
		unit domain.Unit
		want bool
	}{
		{vera, byVendor[0], true}, {vera, byVendor[1], true}, {vera, byGroup, false}, {vera, direct, false},
		{lena, byGroup, true}, {lena, byVendor[0], false},
		{max, direct, true}, {max, byGroup, false},
		// A role names everyone holding it; it makes no one see the work.
		{tom, byRole, false}, {vera, byRole, false}, {max, byRole, false},
	}
	for _, c := range cases {
		if got := h.covers(c.who, project, c.unit); got != c.want {
			t.Errorf("%s covers %s/%s = %v, want %v", c.who.Email, c.unit.Message, c.unit.Locale, got, c.want)
		}
	}
	if h.covers(vera, uuid.New(), byVendor[0]) {
		t.Error("coverage leaked into another project")
	}
	if h.covers(vera, project, domain.Unit{Message: byVendor[0].Message, Locale: "fr"}) {
		t.Error("coverage leaked into another locale")
	}
	set, err := h.coverage.Covered(h.inTenant(), vera.ID, project)
	if err != nil || set.Len() != 2 || !set.Has(byVendor[1].Message, "de") || !set.HasMessage(byVendor[0].Message) {
		t.Fatalf("vera's covered set: %d units, %v", set.Len(), err)
	}

	// Read paths may ask from inside their own transaction.
	err = h.uow.InTenantTx(h.inTenant(), func(ctx context.Context, _ *db.TenantTx) error {
		ok, err := h.coverage.Covers(ctx, vera.ID, project, byVendor[0].Message, "de")
		if err == nil && !ok {
			err = errors.New("not covered inside a transaction")
		}
		return err
	})
	if err != nil {
		t.Fatalf("coverage inside a read path's transaction: %v", err)
	}

	// Work given to vera's vendor is hers to complete; done, it stays
	// visible for 30 days and then goes.
	if _, err := h.svc.Complete(h.as(tom), vendorWork.ID); !errors.Is(err, app.ErrNotAssignee) {
		t.Fatalf("tom completing the vendor's work: err = %v", err)
	}
	if _, err := h.svc.Accept(h.as(vera), vendorWork.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Complete(h.as(vera), vendorWork.ID); err != nil {
		t.Fatal(err)
	}
	h.now = h.clock.Add(29 * 24 * time.Hour)
	if !h.covers(vera, project, byVendor[0]) {
		t.Error("a completed assignment stopped covering within 30 days")
	}
	h.now = h.clock.Add(31 * 24 * time.Hour)
	if h.covers(vera, project, byVendor[0]) {
		t.Error("a completed assignment still covers after 30 days")
	}
	h.now = h.clock

	// A declined assignment covers nothing.
	if _, err := h.svc.Decline(h.as(lena), groupWork.ID, "not mine"); err != nil {
		t.Fatal(err)
	}
	if h.covers(lena, project, byGroup) {
		t.Error("a declined assignment still covers")
	}

	// Lists filter by unit in the query: a message, a locale, or both.
	for _, c := range []struct {
		f    app.AssignmentFilter
		want int
	}{
		{app.AssignmentFilter{Message: byVendor[0].Message}, 1},
		{app.AssignmentFilter{Message: byGroup.Message, Locale: "de"}, 1},
		{app.AssignmentFilter{Message: byGroup.Message, Locale: "fr"}, 0},
		{app.AssignmentFilter{Locale: "de-AT"}, 1},
		{app.AssignmentFilter{Locale: "de"}, 3},
	} {
		got, err := h.svc.VisibleAssignments(h.manager(), c.f, false)
		if err != nil || len(got) != c.want {
			t.Errorf("assignments for %+v = %d, %v; want %d", c.f, len(got), err, c.want)
		}
	}
	// As a person, the vendor's translator may pick up work given to
	// every translator; with visibility `assigned`, only the vendor's.
	if got, err := h.svc.VisibleAssignments(h.as(vera), app.AssignmentFilter{Locale: "de"}, false); err != nil || len(got) != 2 {
		t.Errorf("the vendor's translator's work = %d, %v; want the vendor's and the role's", len(got), err)
	}
	p, _ := authz.From(h.as(vera))
	p.Visibility, p.Coverage = identity.VisibilityAssigned, h.coverage
	if got, err := h.svc.VisibleAssignments(authz.WithPrincipal(h.inTenant(), p), app.AssignmentFilter{Locale: "de"}, false); err != nil ||
		len(got) != 1 || got[0].ID != vendorWork.ID {
		t.Errorf("the assigned vendor member's work = %+v, %v; want only the vendor's", got, err)
	}

	mine, err := h.svc.MyAssignments(h.as(vera), app.AssignmentFilter{Project: project})
	if err != nil || len(mine) != 2 {
		t.Fatalf("vera's work = %d, %v; want her vendor's and the translators'", len(mine), err)
	}
	stored, err := h.svc.Assignment(h.manager(), vendorWork.ID)
	if err != nil || stored.State != domain.AssignmentDone || len(stored.Units) != 2 || stored.ClosedBy == "" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	// Another tenant sees none of it, whoever it asks about.
	other := newWorkHarness(t, mustTenant(t, "other"))
	if other.covers(vera, project, byVendor[0]) {
		t.Error("another tenant's coverage sees this tenant's assignment")
	}
	if list, err := other.svc.Assignments(other.manager(), app.AssignmentFilter{}); err != nil || len(list) != 0 {
		t.Errorf("another tenant lists %d assignments, %v", len(list), err)
	}
	if _, err := other.svc.Assignment(other.manager(), vendorWork.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant reads this tenant's assignment: err = %v", err)
	}
}

func TestApprovalsInPostgres(t *testing.T) {
	newHarness(t)
	h := newWorkHarness(t, mustTenant(t, "approvals"))
	project := uuid.New()
	vera := h.member("vera@lingua.example", []string{"translator"}, []string{"de"}, h.vendor("Lingua GmbH"))
	first := h.member("ann@acme.example", []string{"reviewer"}, []string{"de"}, identity.VendorID{})
	second := h.member("ben@acme.example", []string{"reviewer"}, []string{"de"}, identity.VendorID{})

	subject := domain.ApprovalSubject{Kind: domain.SubjectTranslation, ID: uuid.New(), Locale: "de"}
	veraCtx := h.as(vera)
	h.authors[subject.ID] = actorOf(veraCtx)
	a, err := h.svc.RequestApprovalForInstance(h.as(first), app.WorkflowApproval{
		InstanceID: uuid.New(), ProjectID: project, Subject: subject,
		Params: domain.RequestApproval{N: 2, From: domain.Party{Role: "reviewer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Decide(veraCtx, a.ID, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("the author approving: err = %v", err)
	}
	firstCtx := h.as(first)
	for range 2 {
		if _, err := h.svc.Decide(firstCtx, a.ID, domain.VerdictGranted, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.svc.Decide(h.as(second), a.ID, domain.VerdictGranted, "fine")
	if err != nil || got.State != domain.ApprovalGranted || len(got.Decisions) != 2 {
		t.Fatalf("after two distinct grants: %s with %d decisions, %v", got.State, len(got.Decisions), err)
	}
	stored, err := h.svc.Approval(h.manager(), a.ID)
	if err != nil || stored.State != domain.ApprovalGranted || len(stored.Decisions) != 2 || stored.Decisions[1].Reason != "fine" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	approvers, err := h.svc.Approvers(firstCtx, project, subject)
	if err != nil || len(approvers) != 2 {
		t.Fatalf("approvers = %v, %v", approvers, err)
	}

	// The inbox lists by unit and state, with every decision, in one page
	// or several.
	if _, err := h.svc.RequestApprovalForInstance(h.as(first), app.WorkflowApproval{
		InstanceID: uuid.New(), ProjectID: project, Subject: domain.ApprovalSubject{Kind: domain.SubjectTranslation, ID: uuid.New(), Locale: "fr"},
		Params: domain.RequestApproval{N: 1, From: domain.Party{Role: "reviewer"}},
	}); err != nil {
		t.Fatal(err)
	}
	listed := func(f app.ApprovalFilter) []domain.Approval {
		t.Helper()
		out, err := h.svc.Approvals(firstCtx, f)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := listed(app.ApprovalFilter{Project: project}); len(got) != 2 {
		t.Errorf("the project's approvals = %d, want 2", len(got))
	}
	unit := listed(app.ApprovalFilter{Project: project, Kind: domain.SubjectTranslation, SubjectID: subject.ID, Locale: "de"})
	if len(unit) != 1 || unit[0].ID != a.ID || len(unit[0].Decisions) != 2 || unit[0].Decisions[1].Reason != "fine" {
		t.Errorf("the unit's approvals = %+v", unit)
	}
	if got := listed(app.ApprovalFilter{States: []domain.ApprovalState{domain.ApprovalPending}}); len(got) != 1 || got[0].Subject.Locale != "fr" {
		t.Errorf("pending = %+v", got)
	}
	page := listed(app.ApprovalFilter{Limit: 1})
	if len(page) != 1 {
		t.Fatalf("a page of one = %d", len(page))
	}
	if rest := listed(app.ApprovalFilter{After: page[0].ID}); len(rest) != 1 || rest[0].ID == page[0].ID {
		t.Errorf("the page after = %+v", rest)
	}
	if got := listed(app.ApprovalFilter{Project: uuid.New()}); len(got) != 0 {
		t.Errorf("another project's approvals = %d", len(got))
	}

	// Decisions and units are append-only for the application role,
	// whatever the code above it does.
	for _, stmt := range []string{
		`UPDATE workflow_approval_decisions SET verdict = 'denied'`,
		`DELETE FROM workflow_approval_decisions`,
		`UPDATE workflow_assignment_units SET locale = 'fr'`,
		`DELETE FROM workflow_assignment_units`,
		`UPDATE workflow_approvals SET required = 1`,
		`UPDATE workflow_assignments SET assignee = 'role:owner'`,
	} {
		err := h.uow.InTenantTx(h.inTenant(), func(ctx context.Context, tx *db.TenantTx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "42501" {
			t.Errorf("%s: err = %v, want permission denied (42501)", stmt, err)
		}
	}

	// The outbox holds every event with its actor.
	var n int
	err = h.uow.InTenantTx(h.inTenant(), func(ctx context.Context, tx *db.TenantTx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox_events
			WHERE event_type IN ('workflow.approval.requested', 'workflow.approval.granted') AND actor LIKE 'person:%'`).Scan(&n)
	})
	// Two requests (the second for the inbox above) and two grants.
	if err != nil || n != 4 {
		t.Fatalf("approval events with a person as actor = %d, %v; want 4", n, err)
	}

	// Another tenant sees no approval.
	other := newWorkHarness(t, mustTenant(t, "elsewhere"))
	if _, err := other.svc.Approval(other.manager(), a.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant reads this tenant's approval: err = %v", err)
	}
}

var tenantSeq int

func mustTenant(t *testing.T, slug string) tenancy.ID {
	t.Helper()
	tenantSeq++
	id, err := env.SeedTenant(context.Background(), fmt.Sprintf("%s-%d", slug, tenantSeq))
	if err != nil {
		t.Fatal(err)
	}
	return id
}
