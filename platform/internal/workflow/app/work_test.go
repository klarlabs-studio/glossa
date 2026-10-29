package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// ── fakes ───────────────────────────────────────────────────────────

type fakeWork struct {
	assignments map[uuid.UUID]domain.Assignment
	approvals   map[uuid.UUID]domain.Approval
	order       []uuid.UUID // approvals in creation order
	events      []outbox.Event
}

func newFakeWork() *fakeWork {
	return &fakeWork{assignments: map[uuid.UUID]domain.Assignment{}, approvals: map[uuid.UUID]domain.Approval{}}
}

// InTenant runs fn on a copy and keeps it only if fn succeeds, as a
// transaction would.
func (f *fakeWork) InTenant(ctx context.Context, fn func(context.Context, app.WorkStore) error) error {
	tx := &fakeWork{
		assignments: maps(f.assignments), approvals: maps(f.approvals),
		order: slices.Clone(f.order), events: slices.Clone(f.events),
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	*f = *tx
	return nil
}

func maps[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (f *fakeWork) InsertAssignment(_ context.Context, a domain.Assignment) error {
	f.assignments[a.ID] = a
	return nil
}

func (f *fakeWork) LiveAssignmentsOf(_ context.Context, assignee domain.Assignee) (int, error) {
	n := 0
	for _, a := range f.assignments {
		if a.Assignee == assignee && a.State.Live() {
			n++
		}
	}
	return n, nil
}

func (f *fakeWork) GetAssignment(_ context.Context, id uuid.UUID) (domain.Assignment, error) {
	a, ok := f.assignments[id]
	if !ok {
		return a, app.ErrNotFound
	}
	return a, nil
}

func (f *fakeWork) LockAssignment(ctx context.Context, id uuid.UUID) (domain.Assignment, error) {
	return f.GetAssignment(ctx, id)
}

func (f *fakeWork) UpdateAssignment(_ context.Context, a domain.Assignment) error {
	if f.assignments[a.ID].Version != a.Version-1 {
		return app.ErrConflict
	}
	f.assignments[a.ID] = a
	return nil
}

func (f *fakeWork) ListAssignments(_ context.Context, flt app.AssignmentFilter) ([]domain.Assignment, error) {
	var out []domain.Assignment
	for _, a := range f.assignments {
		if flt.Within != nil && !slices.Contains(*flt.Within, a.ProjectID) {
			continue
		}
		if flt.Assignees == nil || slices.Contains(flt.Assignees, a.Assignee.String()) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeWork) CoveredUnits(context.Context, app.CoverageQuery) ([]domain.Unit, error) {
	return nil, errors.New("not in unit tests")
}

func (f *fakeWork) InsertApproval(_ context.Context, a domain.Approval) error {
	f.approvals[a.ID] = a
	f.order = append(f.order, a.ID)
	return nil
}

func (f *fakeWork) GetApproval(_ context.Context, id uuid.UUID) (domain.Approval, error) {
	a, ok := f.approvals[id]
	if !ok {
		return a, app.ErrNotFound
	}
	a.Decisions = slices.Clone(a.Decisions)
	return a, nil
}

func (f *fakeWork) LockApproval(ctx context.Context, id uuid.UUID) (domain.Approval, error) {
	return f.GetApproval(ctx, id)
}

func (f *fakeWork) LatestApproval(ctx context.Context, project uuid.UUID, s domain.ApprovalSubject) (domain.Approval, error) {
	for i := len(f.order) - 1; i >= 0; i-- {
		if a := f.approvals[f.order[i]]; a.ProjectID == project && a.Subject == s {
			return f.GetApproval(ctx, a.ID)
		}
	}
	return domain.Approval{}, app.ErrNotFound
}

func (f *fakeWork) ListApprovals(_ context.Context, flt app.ApprovalFilter) ([]domain.Approval, error) {
	var out []domain.Approval
	for _, id := range f.order {
		a := f.approvals[id]
		switch {
		case flt.Within != nil && !slices.Contains(*flt.Within, a.ProjectID),
			flt.Project != uuid.Nil && a.ProjectID != flt.Project,
			flt.SubjectID != uuid.Nil && a.Subject.ID != flt.SubjectID,
			flt.Locale != "" && a.Subject.Locale != flt.Locale,
			len(flt.States) > 0 && !slices.Contains(flt.States, a.State):
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeWork) AppendDecision(_ context.Context, id uuid.UUID, seq int, d domain.Decision) error {
	a := f.approvals[id]
	if seq != len(a.Decisions)+1 {
		return app.ErrConflict
	}
	a.Decisions = append(slices.Clone(a.Decisions), d)
	f.approvals[id] = a
	return nil
}

func (f *fakeWork) UpdateApproval(_ context.Context, a domain.Approval) error {
	stored := f.approvals[a.ID]
	if stored.Version != a.Version-1 {
		return app.ErrConflict
	}
	stored.State, stored.Version, stored.ClosedAt = a.State, a.Version, a.ClosedAt
	f.approvals[a.ID] = stored
	return nil
}

func (f *fakeWork) Publish(_ context.Context, e outbox.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	f.events = append(f.events, e)
	return nil
}

func (f *fakeWork) types() []string {
	out := make([]string, len(f.events))
	for i, e := range f.events {
		out[i] = e.Type
	}
	return out
}

type fakeDirectory struct {
	members map[uuid.UUID]app.Affiliation
	groups  map[string]uuid.UUID
	vendors map[string]uuid.UUID
}

func (d *fakeDirectory) Affiliation(_ context.Context, m uuid.UUID) (app.Affiliation, error) {
	a, ok := d.members[m]
	if !ok {
		return a, app.ErrNotFound
	}
	return a, nil
}

func (d *fakeDirectory) Resolve(_ context.Context, kind domain.AssigneeKind, ref string) (uuid.UUID, error) {
	var id uuid.UUID
	switch kind {
	case domain.AssigneeGroup:
		id = d.groups[strings.ToLower(ref)]
	case domain.AssigneeVendor:
		id = d.vendors[strings.ToLower(ref)]
	case domain.AssigneeMember:
		if m, err := uuid.Parse(ref); err == nil {
			if _, ok := d.members[m]; ok {
				id = m
			}
		}
	}
	if id == uuid.Nil {
		return id, app.ErrUnknownParty
	}
	return id, nil
}

type fakeAuthors map[uuid.UUID]string

func (a fakeAuthors) Author(_ context.Context, _ uuid.UUID, s domain.ApprovalSubject) (string, error) {
	return a[s.ID], nil
}

// ── fixture ─────────────────────────────────────────────────────────

type world struct {
	t       *testing.T
	tenant  tenancy.ID
	project uuid.UUID
	work    *fakeWork
	dir     *fakeDirectory
	authors fakeAuthors
	svc     *app.WorkService
	vendor  uuid.UUID
	legal   uuid.UUID
}

func newWorld(t *testing.T) *world {
	w := &world{
		t: t, tenant: tenancy.ID(uuid.New()), project: uuid.New(), work: newFakeWork(),
		dir:     &fakeDirectory{members: map[uuid.UUID]app.Affiliation{}, groups: map[string]uuid.UUID{}, vendors: map[string]uuid.UUID{}},
		authors: fakeAuthors{}, vendor: uuid.New(), legal: uuid.New(),
	}
	w.dir.vendors["lingua-gmbh"] = w.vendor
	w.dir.groups["legal"] = w.legal
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	w.svc = app.NewWorkService(w.work, w.dir, w.authors, app.WithWorkClock(func() time.Time { return clock }))
	return w
}

// person is a signed-in member with roles in locales, registered in the
// directory with groups and a vendor.
func (w *world) person(roles []string, locales []string, groups []uuid.UUID, vendor uuid.UUID) context.Context {
	rs, err := identity.ParseRoles(roles)
	if err != nil {
		w.t.Fatal(err)
	}
	ls, err := identity.ParseLocaleScope(locales)
	if err != nil {
		w.t.Fatal(err)
	}
	person, member := identity.NewPersonID(), identity.NewMemberID()
	w.dir.members[member.UUID()] = app.Affiliation{Member: member.UUID(), Roles: roles, Groups: groups, Vendor: vendor}
	return authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), w.tenant), authz.Principal{
		Actor: identity.PersonActor(person), Person: person, Tenant: w.tenant, Member: member,
		Grant: identity.GrantForMember(rs, ls),
	})
}

func (w *world) token(scopes ...string) context.Context {
	ss, err := identity.ParseScopes(scopes)
	if err != nil {
		w.t.Fatal(err)
	}
	return authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), w.tenant), authz.Principal{
		Actor: identity.TokenActor(identity.NewTokenID()), Tenant: w.tenant, TokenTenant: w.tenant,
		Grant: identity.GrantForScopes(ss),
	})
}

func actorOf(ctx context.Context) string {
	p, _ := authz.From(ctx)
	return p.Actor.String()
}

// ── assignments ─────────────────────────────────────────────────────

func TestAssignForInstanceAndComplete(t *testing.T) {
	w := newWorld(t)
	developer := w.person([]string{"developer"}, nil, nil, uuid.Nil)
	unit := domain.Unit{Message: uuid.New(), Locale: "de"}
	params := domain.Assign{To: domain.Party{Vendor: "Lingua-GmbH"}}

	a, err := w.svc.AssignForInstance(developer, app.WorkflowAssign{
		InstanceID: uuid.New(), ProjectID: w.project, Subject: domain.SubjectTranslation,
		Units: []domain.Unit{unit}, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Assignee != domain.VendorAssignee(w.vendor) || a.Permission != "translations.write" || a.CreatedBy != actorOf(developer) {
		t.Fatalf("a = %+v", a)
	}

	// Someone the assignment is not given to cannot complete it, even a
	// translator for the locale.
	outsider := w.person([]string{"translator"}, []string{"de"}, nil, uuid.Nil)
	// Not found, as reading it is: refusing for permission would say the
	// assignment exists.
	if _, err := w.svc.Complete(outsider, a.ID); !errors.Is(err, app.ErrNotAssignee) || !errors.Is(err, app.ErrNotFound) || errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("an outsider completing: err = %v", err)
	}
	// The vendor's translator for another locale lacks the permission
	// the work takes.
	french := w.person([]string{"translator"}, []string{"fr"}, nil, w.vendor)
	if _, err := w.svc.Complete(french, a.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a vendor translator for fr completing de: err = %v", err)
	}

	vera := w.person([]string{"translator"}, []string{"de"}, nil, w.vendor)
	if _, err := w.svc.Accept(vera, a.ID); err != nil {
		t.Fatal(err)
	}
	done, err := w.svc.Complete(vera, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != domain.AssignmentDone || done.ClosedBy != actorOf(vera) {
		t.Fatalf("done = %+v", done)
	}
	want := []string{domain.EventTypeAssignmentCreated, domain.EventTypeAssignmentAccepted, domain.EventTypeAssignmentCompleted}
	if got := w.work.types(); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	completed := w.work.events[2]
	if string(completed.Actor) != actorOf(vera) {
		t.Fatalf("assignment.completed names %q, want the vendor's translator", completed.Actor)
	}
	if ev, ok := domain.VocabularyEvent(completed.Type); !ok || ev != domain.EventAssignmentCompleted {
		t.Fatalf("%s maps to %q", completed.Type, ev)
	}
	var payload domain.AssignmentEvent
	b, _ := json.Marshal(completed.Payload)
	if err := json.Unmarshal(b, &payload); err != nil || payload.InstanceID == "" || len(payload.Units) != 1 || payload.Units[0].Locale != "de" {
		t.Fatalf("payload = %s, %v", b, err)
	}
}

func TestAssignRefusals(t *testing.T) {
	w := newWorld(t)
	developer := w.person([]string{"developer"}, nil, nil, uuid.Nil)
	unit := []domain.Unit{{Message: uuid.New(), Locale: "de"}}
	in := app.WorkflowAssign{InstanceID: uuid.New(), ProjectID: w.project, Subject: domain.SubjectTranslation, Units: unit}

	in.Params = domain.Assign{To: domain.Party{Vendor: "nobody"}}
	if _, err := w.svc.AssignForInstance(developer, in); !errors.Is(err, app.ErrUnknownParty) {
		t.Errorf("an unknown vendor: err = %v", err)
	}
	in.Params = domain.Assign{To: domain.Party{Group: "legal"}}
	in.Subject = domain.SubjectReleaseRequest
	if _, err := w.svc.AssignForInstance(developer, in); !errors.Is(err, app.ErrUnsupportedSubject) {
		t.Errorf("a release request: err = %v", err)
	}
	// By hand it takes assignments.manage, which a developer lacks.
	if _, _, err := w.svc.Assign(developer, app.AssignInput{ProjectID: w.project, Units: unit, To: domain.Party{Group: "legal"}}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a developer assigning by hand: err = %v", err)
	}
	// Without a principal nothing is assigned.
	if _, err := w.svc.AssignForInstance(tenancy.ContextWithTenant(context.Background(), w.tenant), in); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: err = %v", err)
	}
	if len(w.work.assignments) != 0 || len(w.work.events) != 0 {
		t.Fatalf("a refused assignment was stored: %v", w.work.types())
	}
}

func TestDeclineAndMyWork(t *testing.T) {
	w := newWorld(t)
	admin := w.person([]string{"admin"}, nil, nil, uuid.Nil)
	unit := []domain.Unit{{Message: uuid.New(), Locale: "de"}}
	byGroup, _, err := w.svc.Assign(admin, app.AssignInput{ProjectID: w.project, Units: unit, To: domain.Party{Group: "legal"}})
	if err != nil {
		t.Fatal(err)
	}
	byRole, _, err := w.svc.Assign(admin, app.AssignInput{ProjectID: w.project, Units: unit, To: domain.Party{Role: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}

	lawyer := w.person([]string{"reviewer"}, []string{"de"}, []uuid.UUID{w.legal}, uuid.Nil)
	mine, err := w.svc.MyAssignments(lawyer, app.AssignmentFilter{})
	if err != nil || len(mine) != 2 {
		t.Fatalf("my work = %d assignments, %v; want the group's and the role's", len(mine), err)
	}
	if _, err := w.svc.Assignment(lawyer, byGroup.ID); err != nil {
		t.Fatalf("reading my own assignment: %v", err)
	}
	stranger := w.person([]string{"translator"}, []string{"de"}, nil, uuid.Nil)
	if _, err := w.svc.Assignment(stranger, byGroup.ID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("reading someone else's assignment: err = %v, want ErrNotFound", err)
	}

	declined, err := w.svc.Decline(lawyer, byGroup.ID, "conflict of interest")
	if err != nil || declined.State != domain.AssignmentDeclined || declined.Reason != "conflict of interest" {
		t.Fatalf("decline = %+v, %v", declined, err)
	}
	// A manager takes an assignment back without being its assignee.
	if _, err := w.svc.Decline(admin, byRole.ID, "reassigning"); err != nil {
		t.Fatal(err)
	}
	if last := w.work.events[len(w.work.events)-1]; last.Type != domain.EventTypeAssignmentDeclined || string(last.Actor) != actorOf(admin) {
		t.Fatalf("last event = %s by %s", last.Type, last.Actor)
	}
}

// ── approvals ───────────────────────────────────────────────────────

func (w *world) requestApproval(n int, from domain.Party, author string) (domain.Approval, domain.ApprovalSubject) {
	w.t.Helper()
	subject := domain.ApprovalSubject{Kind: domain.SubjectTranslation, ID: uuid.New(), Locale: "de"}
	w.authors[subject.ID] = author
	developer := w.person([]string{"developer"}, nil, nil, uuid.Nil)
	a, err := w.svc.RequestApprovalForInstance(developer, app.WorkflowApproval{
		InstanceID: uuid.New(), ProjectID: w.project, Subject: subject,
		Params: domain.RequestApproval{N: n, From: from},
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return a, subject
}

func TestTwoDistinctReviewersApprove(t *testing.T) {
	w := newWorld(t)
	vera := w.person([]string{"translator"}, []string{"de"}, nil, w.vendor)
	a, subject := w.requestApproval(2, domain.Party{Role: "reviewer"}, actorOf(vera))
	if !a.DistinctFromAuthor {
		t.Fatal("four-eyes must apply to a requested approval")
	}

	first := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	second := w.person([]string{"reviewer"}, []string{"de-AT", "de"}, nil, uuid.Nil)
	got, err := w.svc.Decide(first, a.ID, domain.VerdictGranted, "")
	if err != nil || got.State != domain.ApprovalPending {
		t.Fatalf("first grant: %+v, %v", got.State, err)
	}
	// A second click by the same reviewer counts once and raises nothing.
	before := len(w.work.events)
	if got, err = w.svc.Decide(first, a.ID, domain.VerdictGranted, ""); err != nil || len(got.Decisions) != 1 || len(w.work.events) != before {
		t.Fatalf("repeat grant: %d decisions, %v, events %v", len(got.Decisions), err, w.work.types())
	}
	approvers, err := w.svc.Approvers(first, w.project, subject)
	if err != nil || len(approvers) != 1 || approvers[0] != actorOf(first) {
		t.Fatalf("approvers = %v, %v", approvers, err)
	}

	got, err = w.svc.Decide(second, a.ID, domain.VerdictGranted, "looks right")
	if err != nil || got.State != domain.ApprovalGranted {
		t.Fatalf("second grant: %s, %v", got.State, err)
	}
	want := []string{domain.EventTypeApprovalRequested, domain.EventTypeApprovalGranted, domain.EventTypeApprovalGranted}
	if !slices.Equal(w.work.types(), want) {
		t.Fatalf("events = %v, want %v", w.work.types(), want)
	}
	for i, e := range w.work.events[1:] {
		p := e.Payload.(domain.ApprovalEvent)
		if p.Satisfied != (i == 1) || p.Principal != string(e.Actor) {
			t.Errorf("grant %d: satisfied %v, principal %s, actor %s", i+1, p.Satisfied, p.Principal, e.Actor)
		}
	}
	if _, err := w.svc.Decide(w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil), a.ID, domain.VerdictDenied, ""); !errors.Is(err, domain.ErrApprovalClosed) {
		t.Fatalf("deciding a granted approval: err = %v", err)
	}
}

func TestWhoCannotDecide(t *testing.T) {
	w := newWorld(t)
	author := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	a, _ := w.requestApproval(1, domain.Party{Role: "reviewer"}, actorOf(author))
	cases := map[string]struct {
		ctx  context.Context
		want error
	}{
		"the author (four-eyes)": {author, domain.ErrOwnText},
		"an API token":           {w.token("read", "write", "publish", "admin"), domain.ErrNotHuman},
		"a token somehow holding approvals.decide": {authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), w.tenant), authz.Principal{
			Actor: identity.TokenActor(identity.NewTokenID()), Tenant: w.tenant, TokenTenant: w.tenant,
			Grant: identity.GrantOf(authz.ApprovalsDecide, authz.WorkflowsRead),
		}), domain.ErrNotHuman},
		"a background process":    {background(t, w.tenant), domain.ErrNotHuman},
		"a reviewer for fr":       {w.person([]string{"reviewer"}, []string{"fr"}, nil, uuid.Nil), authz.ErrForbidden},
		"a translator for de":     {w.person([]string{"translator"}, []string{"de"}, nil, uuid.Nil), authz.ErrForbidden},
		"an owner not asked to":   {w.person([]string{"owner"}, nil, nil, uuid.Nil), domain.ErrNotEligible},
		"the vendor's translator": {w.person([]string{"translator"}, []string{"de"}, nil, w.vendor), authz.ErrForbidden},
	}
	for name, c := range cases {
		for _, v := range []domain.Verdict{domain.VerdictGranted, domain.VerdictDenied} {
			if _, err := w.svc.Decide(c.ctx, a.ID, v, ""); !errors.Is(err, c.want) || !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("%s %s: err = %v, want %v (and forbidden)", name, v, err, c.want)
			}
		}
	}
	if stored := w.work.approvals[a.ID]; len(stored.Decisions) != 0 || stored.State != domain.ApprovalPending {
		t.Fatalf("a refused decision was recorded: %+v", stored)
	}
	if len(w.work.events) != 1 {
		t.Fatalf("a refused decision raised events: %v", w.work.types())
	}
}

func background(t *testing.T, tenant tenancy.ID) context.Context {
	t.Helper()
	ctx, err := authz.Background(tenancy.ContextWithTenant(context.Background(), tenant), "workflow.runner",
		authz.TranslationsRead, authz.TranslationsWrite)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestAGroupDecides(t *testing.T) {
	w := newWorld(t)
	a, _ := w.requestApproval(1, domain.Party{Group: "Legal"}, "person:"+uuid.NewString())
	notLegal := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	if _, err := w.svc.Decide(notLegal, a.ID, domain.VerdictGranted, ""); !errors.Is(err, domain.ErrNotEligible) {
		t.Fatalf("a reviewer outside the group: err = %v", err)
	}
	lawyer := w.person([]string{"reviewer"}, []string{"de"}, []uuid.UUID{w.legal}, uuid.Nil)
	if got, err := w.svc.Decide(lawyer, a.ID, domain.VerdictGranted, ""); err != nil || got.State != domain.ApprovalGranted {
		t.Fatalf("the group's reviewer: %s, %v", got.State, err)
	}
}

func TestADenialEndsItAndClearsTheGuardInput(t *testing.T) {
	w := newWorld(t)
	a, subject := w.requestApproval(2, domain.Party{Role: "reviewer"}, "person:"+uuid.NewString())
	first := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	if _, err := w.svc.Decide(first, a.ID, domain.VerdictGranted, ""); err != nil {
		t.Fatal(err)
	}
	denier := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	got, err := w.svc.Decide(denier, a.ID, domain.VerdictDenied, "wrong formality")
	if err != nil || got.State != domain.ApprovalDenied {
		t.Fatalf("denial: %s, %v", got.State, err)
	}
	last := w.work.events[len(w.work.events)-1]
	if last.Type != domain.EventTypeApprovalDenied || last.Payload.(domain.ApprovalEvent).Reason != "wrong formality" {
		t.Fatalf("last event = %+v", last)
	}
	if approvers, err := w.svc.Approvers(first, w.project, subject); err != nil || len(approvers) != 0 {
		t.Fatalf("approvers after a denial = %v, %v", approvers, err)
	}
}

func TestANewRequestSupersedesTheOld(t *testing.T) {
	w := newWorld(t)
	old, subject := w.requestApproval(1, domain.Party{Role: "reviewer"}, "person:"+uuid.NewString())
	developer := w.person([]string{"developer"}, nil, nil, uuid.Nil)
	// Asking the same of the same party again returns the pending one.
	again, err := w.svc.RequestApprovalForInstance(developer, app.WorkflowApproval{
		InstanceID: old.InstanceID, ProjectID: w.project, Subject: subject,
		Params: domain.RequestApproval{N: 1, From: domain.Party{Role: "reviewer"}},
	})
	if err != nil || again.ID != old.ID {
		t.Fatalf("a repeated request made a new approval: %v, %v", again.ID, err)
	}
	newer, err := w.svc.RequestApprovalForInstance(developer, app.WorkflowApproval{
		InstanceID: old.InstanceID, ProjectID: w.project, Subject: subject,
		Params: domain.RequestApproval{N: 2, From: domain.Party{Role: "reviewer"}},
	})
	if err != nil || newer.ID == old.ID {
		t.Fatalf("a different request: %v, %v", newer.ID, err)
	}
	reviewer := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	if _, err := w.svc.Decide(reviewer, old.ID, domain.VerdictGranted, ""); !errors.Is(err, app.ErrSuperseded) {
		t.Fatalf("deciding a superseded approval: err = %v", err)
	}
	if _, err := w.svc.RequestApprovalForInstance(developer, app.WorkflowApproval{
		InstanceID: old.InstanceID, ProjectID: w.project, Subject: subject,
		Params: domain.RequestApproval{N: 1, From: domain.Party{Vendor: "lingua-gmbh"}},
	}); !errors.Is(err, domain.ErrInvalidApproval) {
		t.Fatalf("asking a vendor to approve: err = %v", err)
	}
}
