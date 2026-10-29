package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
)

// ── assignments ─────────────────────────────────────────────────────

func (f *fixture) assign(t *testing.T, ctx context.Context, key string, to apiv1.Party, idem string) response {
	t.Helper()
	body := apiv1.CreateAssignment{ProjectId: f.project.String(), Assignee: to}
	body.Units = append(body.Units, struct {
		Locale  apiv1.Locale     `json:"locale"`
		Message apiv1.MessageKey `json:"message"`
	}{Locale: "de", Message: key})
	params := apiv1.CreateAssignmentParams{}
	if idem != "" {
		params.IdempotencyKey = &idem
	}
	resp, err := f.api.CreateAssignment(ctx, apiv1.CreateAssignmentRequestObject{Tenant: f.tenant.String(), Params: params, Body: &body})
	return render(t, resp, err)
}

func (f *fixture) listAssignments(t *testing.T, ctx context.Context, p apiv1.ListAssignmentsParams) response {
	t.Helper()
	resp, err := f.api.ListAssignments(ctx, apiv1.ListAssignmentsRequestObject{Tenant: f.tenant.String(), Params: p})
	return render(t, resp, err)
}

func ids(t *testing.T, r response) []string {
	t.Helper()
	var page struct{ Items []struct{ ID string } }
	r.decode(t, &page)
	out := make([]string, len(page.Items))
	for i, it := range page.Items {
		out[i] = it.ID
	}
	return out
}

func TestAssignmentsOverTheAPI(t *testing.T) {
	f := newFixture(t, false)
	vendor := apiv1.Party{Vendor: apiconv.Ptr("Lingua")}

	f.assign(t, f.anonymous, "shared.welcome", vendor, "").want(t, http.StatusUnauthorized, "unauthenticated")
	f.assign(t, f.reader, "shared.welcome", vendor, "").want(t, http.StatusForbidden, "forbidden")
	f.assign(t, f.owner, "no.such.key", vendor, "").want(t, http.StatusUnprocessableEntity, "invalid_assignment")
	f.assign(t, f.owner, "shared.welcome", apiv1.Party{Vendor: apiconv.Ptr("nobody")}, "").want(t, http.StatusUnprocessableEntity, "unknown_party")
	f.assign(t, f.owner, "shared.welcome", apiv1.Party{}, "").want(t, http.StatusUnprocessableEntity, "invalid_assignment")

	r := f.assign(t, f.owner, "shared.welcome", vendor, "job-1").want(t, http.StatusCreated, "")
	var a apiv1.Assignment
	r.decode(t, &a)
	if a.State != "open" || a.Assignee.Kind != "vendor" || *a.Assignee.Id != f.vendor.String() || len(a.Units) != 1 ||
		a.Units[0].MessageId != f.message.String() || a.Permission != "translations.write" ||
		r.header.Get("ETag") != `"1"` || r.header.Get("Location") != "/v1/tenants/"+f.tenant.String()+"/assignments/"+a.Id {
		t.Fatalf("created = %s (%v)", r.body, r.header)
	}
	replay := f.assign(t, f.owner, "shared.welcome", vendor, "job-1").want(t, http.StatusCreated, "")
	if replay.header.Get("Idempotent-Replayed") != "true" || ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{}))[0] != a.Id {
		t.Fatalf("a retried create was not a replay: %v", replay.header)
	}
	f.assign(t, f.owner, "shared.welcome", vendor, "bad key\n").want(t, http.StatusBadRequest, "invalid_idempotency_key")
	other := f.assign(t, f.owner, "shared.welcome", apiv1.Party{Role: apiconv.Ptr(apiv1.Role("reviewer"))}, "")
	var byRole apiv1.Assignment
	other.want(t, http.StatusCreated, "").decode(t, &byRole)

	// The vendor's member sees their work, and only theirs, whatever
	// they ask — as a person or with visibility `assigned`.
	vera := f.person([]string{"translator"}, []string{"de"}, f.vendor)
	if got := ids(t, f.listAssignments(t, vera, apiv1.ListAssignmentsParams{}).want(t, http.StatusOK, "")); len(got) != 1 || got[0] != a.Id {
		t.Fatalf("the vendor's member lists %v, want only %s", got, a.Id)
	}
	assigned, member := authztest.Assigned(context.Background(), f.tenant, &authztest.Coverage{}, "de")
	f.dir.members[member.UUID()] = f.dir.members[mustMember(t, vera)]
	if got := ids(t, f.listAssignments(t, assigned, apiv1.ListAssignmentsParams{})); len(got) != 1 || got[0] != a.Id {
		t.Fatalf("an assigned vendor member lists %v, want only %s", got, a.Id)
	}
	// The manager sees both, filters, and their own work is none.
	if got := ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{})); len(got) != 2 {
		t.Fatalf("the owner lists %v", got)
	}
	if got := ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{Mine: apiconv.Ptr(true)})); len(got) != 0 {
		t.Fatalf("the owner's own work = %v", got)
	}
	project, key := f.project.String(), "shared.welcome"
	if got := ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{Project: &project, Message: &key, Locale: apiconv.Ptr("DE")})); len(got) != 2 {
		t.Fatalf("by unit = %v", got)
	}
	if got := ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{Locale: apiconv.Ptr("fr")})); len(got) != 0 {
		t.Fatalf("fr = %v", got)
	}
	unknown := "no.such.key"
	if got := ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{Project: &project, Message: &unknown})); len(got) != 0 {
		t.Fatalf("an unknown key = %v", got)
	}
	f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{Message: &key}).want(t, http.StatusBadRequest, "invalid_query")
	f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{Locale: apiconv.Ptr("not a tag")}).want(t, http.StatusBadRequest, "invalid_query")
	f.listAssignments(t, f.anonymous, apiv1.ListAssignmentsParams{}).want(t, http.StatusUnauthorized, "unauthenticated")
	// Paging.
	first := f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{PageSize: apiconv.Ptr(1)})
	var page apiv1.AssignmentList
	first.decode(t, &page)
	if len(page.Items) != 1 || page.NextPageToken == nil {
		t.Fatalf("first page = %s", first.body)
	}
	if got := ids(t, f.listAssignments(t, f.owner, apiv1.ListAssignmentsParams{PageSize: apiconv.Ptr(1), PageToken: page.NextPageToken})); len(got) != 1 || got[0] == page.Items[0].Id {
		t.Fatalf("second page = %v", got)
	}

	get := func(ctx context.Context, id string) response {
		resp, err := f.api.GetAssignment(ctx, apiv1.GetAssignmentRequestObject{Tenant: f.tenant.String(), Assignment: id})
		return render(t, resp, err)
	}
	get(vera, a.Id).want(t, http.StatusOK, "")
	get(vera, byRole.Id).want(t, http.StatusNotFound, "not_found")
	get(f.owner, uuid.NewString()).want(t, http.StatusNotFound, "not_found")
	get(f.owner, "not-an-id").want(t, http.StatusNotFound, "not_found")

	accept := func(ctx context.Context, id string) response {
		resp, err := f.api.AcceptAssignment(ctx, apiv1.AcceptAssignmentRequestObject{Tenant: f.tenant.String(), Assignment: id})
		return render(t, resp, err)
	}
	complete := func(ctx context.Context, id string) response {
		resp, err := f.api.CompleteAssignment(ctx, apiv1.CompleteAssignmentRequestObject{Tenant: f.tenant.String(), Assignment: id})
		return render(t, resp, err)
	}
	// Someone else's assignment is not found to act on, as it is to
	// read: a 403 here would say it exists.
	stranger := f.person([]string{"translator"}, []string{"de"}, uuid.Nil)
	complete(stranger, a.Id).want(t, http.StatusNotFound, "not_found")
	resp, err := f.api.GetAssignment(stranger, apiv1.GetAssignmentRequestObject{Tenant: f.tenant.String(), Assignment: a.Id})
	render(t, resp, err).want(t, http.StatusNotFound, "not_found")
	complete(f.anonymous, a.Id).want(t, http.StatusUnauthorized, "unauthenticated")
	accept(vera, a.Id).want(t, http.StatusOK, "")
	done := complete(vera, a.Id).want(t, http.StatusOK, "")
	var closed apiv1.Assignment
	done.decode(t, &closed)
	if closed.State != "done" || closed.ClosedBy == nil || done.header.Get("ETag") != `"3"` {
		t.Fatalf("completed = %s", done.body)
	}
	complete(vera, a.Id).want(t, http.StatusConflict, "assignment_state")
	accept(vera, a.Id).want(t, http.StatusConflict, "assignment_state")
	complete(vera, uuid.NewString()).want(t, http.StatusNotFound, "not_found")

	decline := func(ctx context.Context, id, reason string) response {
		resp, err := f.api.DeclineAssignment(ctx, apiv1.DeclineAssignmentRequestObject{Tenant: f.tenant.String(), Assignment: id,
			Body: &apiv1.AssignmentDecline{Reason: &reason}})
		return render(t, resp, err)
	}
	decline(stranger, byRole.Id, "no").want(t, http.StatusNotFound, "not_found")
	var declined apiv1.Assignment
	decline(f.owner, byRole.Id, "reassigning").want(t, http.StatusOK, "").decode(t, &declined)
	if declined.State != "declined" || declined.Reason == nil || *declined.Reason != "reassigning" {
		t.Fatalf("declined = %+v", declined)
	}
	if n := len(f.work.events); n != 5 {
		t.Errorf("%d events; want created ×2, accepted, completed, declined", n)
	}
}

func mustMember(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	p, ok := authz.From(ctx)
	if !ok {
		t.Fatal("no principal")
	}
	return p.Member.UUID()
}

// ── approvals ───────────────────────────────────────────────────────

func TestApprovalsOverTheAPI(t *testing.T) {
	f := newFixture(t, false)
	reviewers := apiv1.Party{Role: apiconv.Ptr(apiv1.Role("reviewer"))}
	ask := func(ctx context.Context, key string, n int, from apiv1.Party) response {
		resp, err := f.api.CreateApproval(ctx, apiv1.CreateApprovalRequestObject{Tenant: f.tenant.String(), Body: &apiv1.CreateApproval{
			ProjectId: f.project.String(), Message: key, Locale: "de", N: n, From: from,
		}})
		return render(t, resp, err)
	}
	ask(f.anonymous, "shared.welcome", 2, reviewers).want(t, http.StatusUnauthorized, "unauthenticated")
	ask(f.reader, "shared.welcome", 2, reviewers).want(t, http.StatusForbidden, "forbidden")
	ask(f.owner, "no.such.key", 2, reviewers).want(t, http.StatusUnprocessableEntity, "invalid_approval")
	ask(f.owner, "shared.welcome", 2, apiv1.Party{Vendor: apiconv.Ptr("lingua")}).want(t, http.StatusUnprocessableEntity, "invalid_approval")
	ask(f.owner, "shared.welcome", 0, reviewers).want(t, http.StatusUnprocessableEntity, "invalid_approval")
	var a apiv1.Approval
	r := ask(f.owner, "shared.welcome", 2, reviewers).want(t, http.StatusCreated, "")
	r.decode(t, &a)
	if a.State != "pending" || a.Required != 2 || !a.DistinctFromAuthor || a.SubjectId != f.message.String() || *a.Locale != "de" ||
		a.Subject != "translation" || r.header.Get("Location") == "" {
		t.Fatalf("asked = %s", r.body)
	}

	author := f.person([]string{"reviewer"}, []string{"de"}, uuid.Nil)
	p, _ := authz.From(author)
	f.authors[f.message] = p.Actor.String()

	list := func(ctx context.Context, params apiv1.ListApprovalsParams) response {
		resp, err := f.api.ListApprovals(ctx, apiv1.ListApprovalsRequestObject{Tenant: f.tenant.String(), Params: params})
		return render(t, resp, err)
	}
	project, key := f.project.String(), "shared.welcome"
	if got := ids(t, list(author, apiv1.ListApprovalsParams{Project: &project, Message: &key, Locale: apiconv.Ptr("de")}).want(t, http.StatusOK, "")); len(got) != 1 || got[0] != a.Id {
		t.Fatalf("a reviewer lists %v", got)
	}
	if got := ids(t, list(f.owner, apiv1.ListApprovalsParams{State: apiconv.Ptr(apiv1.ApprovalState("granted"))})); len(got) != 0 {
		t.Fatalf("granted = %v", got)
	}
	list(f.anonymous, apiv1.ListApprovalsParams{}).want(t, http.StatusUnauthorized, "unauthenticated")
	assigned, _ := authztest.Assigned(context.Background(), f.tenant, &authztest.Coverage{}, "de")
	list(assigned, apiv1.ListApprovalsParams{}).want(t, http.StatusForbidden, "forbidden")
	list(f.owner, apiv1.ListApprovalsParams{Message: &key}).want(t, http.StatusBadRequest, "invalid_query")

	decide := func(ctx context.Context, id, decision string) response {
		resp, err := f.api.DecideApproval(ctx, apiv1.DecideApprovalRequestObject{Tenant: f.tenant.String(), Approval: id,
			Body: &apiv1.CreateApprovalDecision{Decision: apiv1.CreateApprovalDecisionDecision(decision), Reason: apiconv.Ptr("reads well")}})
		return render(t, resp, err)
	}
	decide(f.anonymous, a.Id, "granted").want(t, http.StatusUnauthorized, "unauthenticated")
	// No token decides: no scope grants approvals.decide, and a token is
	// not a person (§3.2, §9.3).
	decide(authztest.Token(context.Background(), f.tenant, "read", "write", "publish", "admin", "workflows"), a.Id, "granted").
		want(t, http.StatusForbidden, "person_required")
	decide(author, a.Id, "granted").want(t, http.StatusForbidden, "own_text")
	decide(f.owner, a.Id, "granted").want(t, http.StatusForbidden, "not_eligible")
	decide(f.reader, a.Id, "granted").want(t, http.StatusForbidden, "forbidden")
	first := f.person([]string{"reviewer"}, []string{"de"}, uuid.Nil)
	second := f.person([]string{"reviewer"}, []string{"de"}, uuid.Nil)
	decide(first, a.Id, "maybe").want(t, http.StatusUnprocessableEntity, "invalid_approval")
	decide(first, uuid.NewString(), "granted").want(t, http.StatusNotFound, "not_found")
	var got apiv1.Approval
	decide(first, a.Id, "granted").want(t, http.StatusCreated, "").decode(t, &got)
	if got.State != "pending" || len(got.Decisions) != 1 {
		t.Fatalf("after one grant = %+v", got)
	}
	decide(first, a.Id, "granted").want(t, http.StatusCreated, "").decode(t, &got) // counts once
	if len(got.Decisions) != 1 {
		t.Fatalf("a repeated grant was recorded: %+v", got.Decisions)
	}
	decide(second, a.Id, "granted").want(t, http.StatusCreated, "").decode(t, &got)
	if got.State != "granted" || len(got.Decisions) != 2 || got.Decisions[1].Decision != "granted" || *got.Decisions[1].Reason != "reads well" {
		t.Fatalf("after two grants = %+v", got)
	}
	decide(f.person([]string{"reviewer"}, []string{"de"}, uuid.Nil), a.Id, "denied").want(t, http.StatusConflict, "approval_closed")

	getA := func(ctx context.Context, id string) response {
		resp, err := f.api.GetApproval(ctx, apiv1.GetApprovalRequestObject{Tenant: f.tenant.String(), Approval: id})
		return render(t, resp, err)
	}
	getA(f.reader, a.Id).want(t, http.StatusOK, "").decode(t, &got)
	if len(got.Decisions) != 2 {
		t.Fatalf("read back = %+v", got)
	}
	getA(f.owner, uuid.NewString()).want(t, http.StatusNotFound, "not_found")
	getA(f.anonymous, a.Id).want(t, http.StatusUnauthorized, "unauthenticated")

	// Asking the same twice asks once; a different request supersedes
	// the pending one, which then takes no decisions.
	var pending apiv1.Approval
	ask(f.owner, "shared.welcome", 1, reviewers).want(t, http.StatusCreated, "").decode(t, &pending)
	ask(f.owner, "shared.welcome", 1, reviewers).want(t, http.StatusCreated, "")
	if got := ids(t, list(f.owner, apiv1.ListApprovalsParams{State: apiconv.Ptr(apiv1.ApprovalState("pending"))})); len(got) != 1 {
		t.Fatalf("asking the same twice made %d pending approvals", len(got))
	}
	ask(f.owner, "shared.welcome", 2, reviewers).want(t, http.StatusCreated, "")
	decide(first, pending.Id, "granted").want(t, http.StatusConflict, "approval_superseded")
}

func (f *fixture) assignmentReport(t *testing.T, ctx context.Context, p apiv1.GetAssignmentReportParams) response {
	t.Helper()
	resp, err := f.api.GetAssignmentReport(ctx, apiv1.GetAssignmentReportRequestObject{Tenant: f.tenant.String(), Params: p})
	return render(t, resp, err)
}

// The report is the service's answer, so what it refuses and how it
// fails are the service's: the HTTP layer adds the filters and the
// problem codes (RFC 0006 §3.4).
func TestAssignmentReportOverTheAPI(t *testing.T) {
	f := newFixture(t, false)
	f.assignmentReport(t, f.anonymous, apiv1.GetAssignmentReportParams{}).want(t, http.StatusUnauthorized, "unauthenticated")
	f.assignmentReport(t, f.owner, apiv1.GetAssignmentReportParams{Vendor: apiconv.Ptr("not-a-uuid")}).want(t, http.StatusBadRequest, "invalid_query")
	// This fixture is built without the contexts a report reads.
	f.assignmentReport(t, f.owner, apiv1.GetAssignmentReportParams{Vendor: apiconv.Ptr(f.vendor.String())}).
		want(t, http.StatusServiceUnavailable, "workflow_instances_unavailable")
	assigned, _ := authztest.Assigned(context.Background(), f.tenant, &authztest.Coverage{}, "de")
	f.assignmentReport(t, assigned, apiv1.GetAssignmentReportParams{}).want(t, http.StatusForbidden, "forbidden")
}
