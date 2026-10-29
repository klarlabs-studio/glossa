package httpapi

import (
	"context"
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Release approvals (RFC 0006 §5.1): an environment's `approval`, the
// 202 a held publish or promote answers, and the release requests it
// leaves. Granting a request is a decision on its Workflow approval, so
// that operation is Workflow's (decideReleaseRequest); everything here
// is Release's.

func toApprovalPolicy(a domain.ApprovalPolicy) apiv1.EnvironmentApproval {
	from := apiv1.EnvironmentApprovalParty{}
	switch {
	case a.From.Member != "":
		from.Member = apiconv.Ptr(a.From.Member)
	case a.From.Role != "":
		from.Role = apiconv.Ptr(apiv1.Role(a.From.Role))
	case a.From.Group != "":
		from.Group = apiconv.Ptr(a.From.Group)
	}
	return apiv1.EnvironmentApproval{N: a.N, From: from, DistinctFromRequester: a.DistinctFromRequester}
}

func fromApprovalPolicy(a apiv1.EnvironmentApproval) domain.ApprovalPolicy {
	from := domain.ApprovalParty{}
	if a.From.Member != nil {
		from.Member = *a.From.Member
	}
	if a.From.Role != nil {
		from.Role = string(*a.From.Role)
	}
	if a.From.Group != nil {
		from.Group = *a.From.Group
	}
	return domain.ApprovalPolicy{N: a.N, From: from, DistinctFromRequester: a.DistinctFromRequester}
}

// approvalChange reads what an environment update asks of the approval
// requirement: nil keeps it, and asking to set and clear it at once is
// not a request.
func approvalChange(body apiv1.UpdateEnvironment) (*app.ApprovalChange, error) {
	clear := body.ClearApproval != nil && *body.ClearApproval
	switch {
	case clear && body.Approval != nil:
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest,
			"send `approval` to set the requirement or `clear_approval` to switch it off, not both")
	case clear:
		return &app.ApprovalChange{}, nil
	case body.Approval != nil:
		a := fromApprovalPolicy(*body.Approval)
		return &app.ApprovalChange{Approval: &a}, nil
	}
	return nil, nil
}

func toReleaseRequest(r domain.ReleaseRequest) apiv1.ReleaseRequest {
	out := apiv1.ReleaseRequest{
		Id: r.ID.String(), Environment: r.Environment, ReleaseId: r.ReleaseID.String(),
		Action: apiv1.ReleaseRequestAction(r.Action), Requester: r.Requester, Approval: toApprovalPolicy(r.Approval),
		Gate: apiv1.GateVerdict{Met: r.Verdict.Met}, Forced: r.Override.Forced,
		State: apiv1.ReleaseRequestState(r.State), CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt,
		DecidedBy: optional(r.DecidedBy), Reason: optional(r.Reason),
	}
	if len(r.Verdict.Unmet) > 0 {
		out.Gate.Unmet = apiconv.Ptr(r.Verdict.Unmet)
	}
	// The approvers see that it was forced and why (§5.1): the reason is
	// present exactly when the request was forced.
	if r.Override.Forced {
		out.ForceReason = apiconv.Ptr(r.Override.Reason)
	}
	return out
}

func requestPath(ctx context.Context, r domain.ReleaseRequest) string {
	return projectPath(ctx, r.ProjectID, "/release-requests/"+r.ID.String())
}

// heldRequest reports whether err is a publish or a promote that became
// a release request, and returns the request.
func heldRequest(err error) (domain.ReleaseRequest, bool) {
	var held *domain.HeldError
	if errors.As(err, &held) {
		return held.Request, true
	}
	return domain.ReleaseRequest{}, false
}

func releaseHeld(r domain.ReleaseRequest) apiv1.ReleaseHeld {
	return apiv1.ReleaseHeld{Id: r.ReleaseID.String(), ReleaseRequestId: r.ID.String(), ReleaseRequest: toReleaseRequest(r)}
}

func (a *API) ListReleaseRequests(ctx context.Context, req apiv1.ListReleaseRequestsRequestObject) (apiv1.ListReleaseRequestsResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var environment string
	if req.Params.Environment != nil {
		environment = *req.Params.Environment
	}
	var state domain.RequestState
	if req.Params.State != nil {
		state = domain.RequestState(*req.Params.State)
	}
	rows, next, err := a.svc.ListReleaseRequests(ctx, project, environment, state, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListReleaseRequests200JSONResponse{Items: make([]apiv1.ReleaseRequest, len(rows)), NextPageToken: next}
	for i, r := range rows {
		out.Items[i] = toReleaseRequest(r)
	}
	return out, nil
}

func (a *API) GetReleaseRequest(ctx context.Context, req apiv1.GetReleaseRequestRequestObject) (apiv1.GetReleaseRequestResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.ReleaseRequest)
	if err != nil {
		return nil, err
	}
	r, err := a.svc.GetReleaseRequest(ctx, project, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetReleaseRequest200JSONResponse(toReleaseRequest(r)), nil
}

func (a *API) WithdrawReleaseRequest(ctx context.Context, req apiv1.WithdrawReleaseRequestRequestObject) (apiv1.WithdrawReleaseRequestResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.ReleaseRequest)
	if err != nil {
		return nil, err
	}
	var reason string
	if req.Body != nil && req.Body.Reason != nil {
		reason = *req.Body.Reason
	}
	r, err := a.svc.WithdrawRequest(ctx, project, id, reason)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.WithdrawReleaseRequest200JSONResponse(toReleaseRequest(r)), nil
}
