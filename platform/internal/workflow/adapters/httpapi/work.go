package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Assignments and approvals (RFC 0006 §3.1–3.2, §8): the HTTP edge of
// app.WorkService. Messages are addressed by key, as every other
// surface addresses them, and resolved through Catalog as the caller —
// so a key the caller cannot see resolves to nothing.

// ── conversions ─────────────────────────────────────────────────────

func toAssignee(a domain.Assignee) apiv1.Assignee {
	out := apiv1.Assignee{Kind: apiv1.AssigneeKind(a.Kind)}
	if a.Kind == domain.AssigneeRole {
		out.Role = apiconv.Ptr(apiv1.Role(a.Role))
	} else {
		out.Id = apiconv.Ptr(a.ID.String())
	}
	return out
}

func toAssignment(a domain.Assignment) apiv1.Assignment {
	out := apiv1.Assignment{
		Id: a.ID.String(), ProjectId: a.ProjectID.String(), InstanceId: optionalID(a.InstanceID),
		Units: make([]apiv1.AssignmentUnit, len(a.Units)), Assignee: toAssignee(a.Assignee), Permission: a.Permission,
		DueAt: a.DueAt, State: apiv1.AssignmentState(a.State), CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt, ClosedAt: a.ClosedAt,
	}
	for i, u := range a.Units {
		out.Units[i] = apiv1.AssignmentUnit{MessageId: u.Message.String(), Locale: u.Locale}
	}
	if a.ClosedBy != "" {
		out.ClosedBy = apiconv.Ptr(a.ClosedBy)
	}
	if a.Reason != "" {
		out.Reason = apiconv.Ptr(a.Reason)
	}
	return out
}

func toApproval(a domain.Approval) apiv1.Approval {
	out := apiv1.Approval{
		Id: a.ID.String(), ProjectId: a.ProjectID.String(), InstanceId: optionalID(a.InstanceID),
		Subject: apiv1.WorkflowSubject(a.Subject.Kind), SubjectId: a.Subject.ID.String(), Required: a.Required,
		Eligible: toAssignee(a.Eligible), DistinctFromAuthor: a.DistinctFromAuthor, DueAt: a.DueAt,
		State: apiv1.ApprovalState(a.State), Decisions: make([]apiv1.ApprovalDecision, len(a.Decisions)),
		CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt, ClosedAt: a.ClosedAt,
	}
	if a.Subject.Locale != "" {
		out.Locale = apiconv.Ptr(a.Subject.Locale)
	}
	for i, d := range a.Decisions {
		out.Decisions[i] = apiv1.ApprovalDecision{Principal: d.Principal, Decision: apiv1.ApprovalDecisionDecision(d.Verdict), At: d.At}
		if d.Reason != "" {
			out.Decisions[i].Reason = apiconv.Ptr(d.Reason)
		}
	}
	return out
}

func toParty(p apiv1.Party) domain.Party {
	out := domain.Party{Group: deref(p.Group), Vendor: deref(p.Vendor), Member: deref(p.Member)}
	if p.Role != nil {
		out.Role = string(*p.Role)
	}
	return out
}

func assignmentPath(ctx context.Context, id uuid.UUID) string {
	return tenantPath(ctx, "/assignments/"+id.String())
}

// ── shared reads ────────────────────────────────────────────────────

// unitFilter reads the project, message and locale filters of a list.
// A message is named by key, which needs its project; a key the project
// does not have, or the caller cannot see, matches nothing (none).
func (a *API) unitFilter(ctx context.Context, project, message, locale *string) (p, m uuid.UUID, l string, none bool, err error) {
	if p, err = optionalFilterID("project", project); err != nil {
		return
	}
	if l = deref(locale); l != "" {
		tags, cerr := domain.CanonicalLocales([]string{l})
		if cerr != nil {
			err = mapError(errQuery("locale %q is not a BCP 47 tag", l), nil)
			return
		}
		l = tags[0]
	}
	key := deref(message)
	if key == "" {
		return
	}
	if p == uuid.Nil {
		err = mapError(errQuery("message names a key, which needs project"), nil)
		return
	}
	m, err = a.catalog.MessageID(ctx, p, key)
	if errors.Is(err, app.ErrNotFound) || errors.Is(err, app.ErrProjectNotFound) || errors.Is(err, authz.ErrNotVisible) {
		return p, uuid.Nil, l, true, nil
	}
	return
}

// pageAfter reads a page's cursor, an id this list issued.
func pageAfter(page pagination.Page) (uuid.UUID, error) {
	if page.After == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(page.After)
	if err != nil {
		return uuid.Nil, problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this list issued")
	}
	return id, nil
}

// ── assignments ─────────────────────────────────────────────────────

// ListAssignments lists what the caller may see: everything in their
// project scope for a manager, their own work for everyone else.
func (a *API) ListAssignments(ctx context.Context, req apiv1.ListAssignmentsRequestObject) (apiv1.ListAssignmentsResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	after, err := pageAfter(page)
	if err != nil {
		return nil, err
	}
	project, message, locale, none, err := a.unitFilter(ctx, req.Params.Project, req.Params.Message, req.Params.Locale)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListAssignments200JSONResponse{Items: []apiv1.Assignment{}}
	if none {
		return out, nil
	}
	f := app.AssignmentFilter{Project: project, Message: message, Locale: locale, After: after, Limit: page.Limit()}
	if req.Params.State != nil {
		f.States = []domain.AssignmentState{domain.AssignmentState(*req.Params.State)}
	}
	rows, err := a.work.VisibleAssignments(ctx, f, req.Params.Mine != nil && *req.Params.Mine)
	if err != nil {
		return nil, mapError(err, nil)
	}
	items, next := pagination.Trim(rows, page, func(x domain.Assignment) string { return x.ID.String() })
	out.Items, out.NextPageToken = make([]apiv1.Assignment, len(items)), next
	for i, x := range items {
		out.Items[i] = toAssignment(x)
	}
	return out, nil
}

// GetAssignmentReport serves the per-vendor quality numbers (RFC 0006
// §3.4) from the same service method the assignments_report tool calls;
// the permission, project scope and the refusal of an `assigned` member
// are that method's.
func (a *API) GetAssignmentReport(ctx context.Context, req apiv1.GetAssignmentReportRequestObject) (apiv1.GetAssignmentReportResponseObject, error) {
	q := app.ReportQuery{}
	if req.Params.Project != nil {
		p, err := projectID(*req.Params.Project)
		if err != nil {
			return nil, err
		}
		q.Project = p
	}
	if req.Params.Vendor != nil {
		v, err := uuid.Parse(*req.Params.Vendor)
		if err != nil {
			return nil, mapError(errQuery("vendor %q is not a vendor id", *req.Params.Vendor), nil)
		}
		q.Assignee = domain.Assignee{Kind: domain.AssigneeVendor, ID: v}.String()
	}
	if req.Params.Since != nil {
		q.Since = *req.Params.Since
	}
	r, err := a.work.QualityReport(ctx, q)
	if err != nil {
		return nil, mapError(err, nil)
	}
	return toAssignmentReport(r), nil
}

func toAssignmentReport(r app.QualityReport) apiv1.GetAssignmentReport200JSONResponse {
	out := apiv1.GetAssignmentReport200JSONResponse{
		GeneratedAt: r.GeneratedAt.UTC(), Since: r.Since, Truncated: r.Truncated, Rows: make([]apiv1.AssignmentReportRow, len(r.Rows)),
	}
	for i, x := range r.Rows {
		out.Rows[i] = apiv1.AssignmentReportRow{
			Assignee: x.Assignee, Locale: x.Locale, Assignments: x.Assignments, OnTime: x.OnTime, Late: x.Late,
			NoDue: x.NoDue, Units: x.Units, Unavailable: x.Unavailable, SourceWords: x.SourceWords, TmWords: x.TMWords,
			Approved: x.Approved, Rejected: x.Rejected, NeedsReview: x.InReview, Draft: x.Draft, Unreviewed: x.Unreviewed,
			ChangedAfterDelivery: x.Changed, MeanEditDistance: x.MeanEditDistance, MeanEditRatio: x.MeanEditRatio,
			Findings: x.Findings, FindingsPerUnit: x.FindingsPerUnit, Reworked: x.Reworked, ReworkRate: x.ReworkRate,
			OnTimeRate: x.OnTimeRate,
		}
	}
	return out
}

// CreateAssignment gives a batch of units, named by key, to a party.
func (a *API) CreateAssignment(ctx context.Context, req apiv1.CreateAssignmentRequestObject) (apiv1.CreateAssignmentResponseObject, error) {
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is an assignment")
	}
	b := *req.Body
	project, err := projectID(b.ProjectId)
	if err != nil {
		return nil, err
	}
	if len(b.Units) > domain.MaxAssignmentUnits {
		return nil, mapError(fmt.Errorf("%w: at most %d units per assignment", domain.ErrInvalidAssignment, domain.MaxAssignmentUnits), nil)
	}
	keys := make([]app.KeyedUnit, len(b.Units))
	for i, u := range b.Units {
		keys[i] = app.KeyedUnit{Message: u.Message, Locale: u.Locale}
	}
	x, replayed, err := a.work.Assign(ctx, app.AssignInput{
		ProjectID: project, Keys: keys, To: toParty(b.Assignee), Permission: deref(b.Permission), DueAt: b.DueAt,
		IdempotencyKey: deref(req.Params.IdempotencyKey),
	})
	if err != nil {
		return nil, mapError(err, nil)
	}
	h := apiv1.CreateAssignment201ResponseHeaders{ETag: apiconv.ETag(x.Version), Location: apiconv.Ptr(assignmentPath(ctx, x.ID))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateAssignment201JSONResponse{Body: toAssignment(x), Headers: h}, nil
}

// GetAssignment reads one: any for a manager, one's own otherwise.
func (a *API) GetAssignment(ctx context.Context, req apiv1.GetAssignmentRequestObject) (apiv1.GetAssignmentResponseObject, error) {
	id, err := pathID(req.Assignment, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	x, err := a.work.Assignment(ctx, id)
	if err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.GetAssignment200JSONResponse{Body: toAssignment(x),
		Headers: apiv1.GetAssignment200ResponseHeaders{ETag: apiconv.ETag(x.Version)}}, nil
}

// errAssignmentMoved is a change that lost to a concurrent one.
var errAssignmentMoved = problem.New(http.StatusConflict, problem.CodeConflict, "the assignment changed meanwhile; read it and retry")

// AcceptAssignment takes an open assignment on.
func (a *API) AcceptAssignment(ctx context.Context, req apiv1.AcceptAssignmentRequestObject) (apiv1.AcceptAssignmentResponseObject, error) {
	id, err := pathID(req.Assignment, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	x, err := a.work.Accept(ctx, id)
	if err != nil {
		return nil, mapError(err, errAssignmentMoved)
	}
	return apiv1.AcceptAssignment200JSONResponse{Body: toAssignment(x),
		Headers: apiv1.AcceptAssignment200ResponseHeaders{ETag: apiconv.ETag(x.Version)}}, nil
}

// CompleteAssignment claims an assignment done.
func (a *API) CompleteAssignment(ctx context.Context, req apiv1.CompleteAssignmentRequestObject) (apiv1.CompleteAssignmentResponseObject, error) {
	id, err := pathID(req.Assignment, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	x, err := a.work.Complete(ctx, id)
	if err != nil {
		return nil, mapError(err, errAssignmentMoved)
	}
	return apiv1.CompleteAssignment200JSONResponse{Body: toAssignment(x),
		Headers: apiv1.CompleteAssignment200ResponseHeaders{ETag: apiconv.ETag(x.Version)}}, nil
}

// DeclineAssignment hands an assignment back.
func (a *API) DeclineAssignment(ctx context.Context, req apiv1.DeclineAssignmentRequestObject) (apiv1.DeclineAssignmentResponseObject, error) {
	id, err := pathID(req.Assignment, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	var reason string
	if req.Body != nil {
		reason = deref(req.Body.Reason)
	}
	x, err := a.work.Decline(ctx, id, reason)
	if err != nil {
		return nil, mapError(err, errAssignmentMoved)
	}
	return apiv1.DeclineAssignment200JSONResponse{Body: toAssignment(x),
		Headers: apiv1.DeclineAssignment200ResponseHeaders{ETag: apiconv.ETag(x.Version)}}, nil
}

// ── approvals ───────────────────────────────────────────────────────

// ListApprovals is the approvals inbox.
func (a *API) ListApprovals(ctx context.Context, req apiv1.ListApprovalsRequestObject) (apiv1.ListApprovalsResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	after, err := pageAfter(page)
	if err != nil {
		return nil, err
	}
	project, message, locale, none, err := a.unitFilter(ctx, req.Params.Project, req.Params.Message, req.Params.Locale)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListApprovals200JSONResponse{Items: []apiv1.Approval{}}
	if none {
		return out, nil
	}
	f := app.ApprovalFilter{Project: project, SubjectID: message, Locale: locale, After: after, Limit: page.Limit()}
	if message != uuid.Nil {
		f.Kind = domain.SubjectTranslation
	}
	if req.Params.Subject != nil {
		f.Kind = domain.SubjectKind(*req.Params.Subject)
	}
	if req.Params.State != nil {
		f.States = []domain.ApprovalState{domain.ApprovalState(*req.Params.State)}
	}
	rows, err := a.work.Approvals(ctx, f)
	if err != nil {
		return nil, mapError(err, nil)
	}
	items, next := pagination.Trim(rows, page, func(x domain.Approval) string { return x.ID.String() })
	out.Items, out.NextPageToken = make([]apiv1.Approval, len(items)), next
	for i, x := range items {
		out.Items[i] = toApproval(x)
	}
	return out, nil
}

// CreateApproval asks for a translation unit, named by key, to be
// approved.
func (a *API) CreateApproval(ctx context.Context, req apiv1.CreateApprovalRequestObject) (apiv1.CreateApprovalResponseObject, error) {
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is an approval request")
	}
	b := *req.Body
	project, err := projectID(b.ProjectId)
	if err != nil {
		return nil, err
	}
	x, err := a.work.RequestApproval(ctx, app.ApprovalInput{
		ProjectID: project, N: b.N, From: toParty(b.From), DueAt: b.DueAt, MessageKey: b.Message,
		Subject: domain.ApprovalSubject{Kind: domain.SubjectTranslation, Locale: b.Locale},
	})
	if err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.CreateApproval201JSONResponse{Body: toApproval(x), Headers: apiv1.CreateApproval201ResponseHeaders{
		Location: apiconv.Ptr(tenantPath(ctx, "/approvals/"+x.ID.String())),
	}}, nil
}

// GetApproval reads one approval with its decisions.
func (a *API) GetApproval(ctx context.Context, req apiv1.GetApprovalRequestObject) (apiv1.GetApprovalResponseObject, error) {
	id, err := pathID(req.Approval, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	x, err := a.work.Approval(ctx, id)
	if err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.GetApproval200JSONResponse(toApproval(x)), nil
}

// DecideApproval records a person's decision.
func (a *API) DecideApproval(ctx context.Context, req apiv1.DecideApprovalRequestObject) (apiv1.DecideApprovalResponseObject, error) {
	id, err := pathID(req.Approval, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is a decision")
	}
	x, err := a.work.Decide(ctx, id, domain.Verdict(req.Body.Decision), deref(req.Body.Reason))
	if err != nil {
		return nil, mapError(err, problem.New(http.StatusConflict, problem.CodeConflict, "the approval changed meanwhile; retry"))
	}
	return apiv1.DecideApproval201JSONResponse(toApproval(x)), nil
}

// DecideReleaseRequest is decideApproval reached by the release request
// (RFC 0006 §5.1, §8): the decision is on the request's current
// approval, which Workflow keeps, so the operation is Workflow's even
// though its path is under the project's release requests.
func (a *API) DecideReleaseRequest(ctx context.Context, req apiv1.DecideReleaseRequestRequestObject) (apiv1.DecideReleaseRequestResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	request, err := pathID(req.ReleaseRequest, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is a decision")
	}
	x, err := a.work.DecideReleaseRequest(ctx, project, request, domain.Verdict(req.Body.Decision), deref(req.Body.Reason))
	if err != nil {
		return nil, mapError(err, problem.New(http.StatusConflict, problem.CodeConflict, "the approval changed meanwhile; retry"))
	}
	return apiv1.DecideReleaseRequest201JSONResponse(toApproval(x)), nil
}

// CreateAssignmentPath reports whether a request creates an assignment,
// whose body may carry up to domain.MaxAssignmentUnits units and so
// outgrows the server's default body limit.
func CreateAssignmentPath(method, path string) bool {
	s := strings.Split(path, "/")
	return method == http.MethodPost && len(s) == 5 && s[0] == "" && s[1] == "v1" && s[2] == "tenants" && s[3] != "" &&
		s[4] == "assignments"
}
