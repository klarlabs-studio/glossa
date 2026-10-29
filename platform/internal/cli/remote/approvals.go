package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Approvals (RFC 0006 §3.2, §5.1): a person's grant or denial, on a
// translation unit or on a release request. Deciding is human-only;
// the server refuses an API token with `person_required`.
type (
	Approval         = apiclient.Approval
	ApprovalDecision = apiclient.ApprovalDecision
)

// ApprovalFilter narrows the approvals inbox (empty: any).
type ApprovalFilter struct {
	Project, Message, Locale, Subject, State string
}

// Approvals lists approvals with their decisions, oldest first, every
// page.
func (c *Client) Approvals(ctx context.Context, tenant string, f ApprovalFilter) ([]Approval, error) {
	size := pageSize
	params := &apiclient.ListApprovalsParams{PageSize: &size, Project: optional(f.Project),
		Message: optional(f.Message), Locale: optional(f.Locale)}
	if f.Subject != "" {
		s := apiclient.WorkflowSubject(f.Subject)
		params.Subject = &s
	}
	if f.State != "" {
		s := apiclient.ApprovalState(f.State)
		params.State = &s
	}
	return collect(func(tok *string) ([]Approval, *string, error) {
		params.PageToken = tok
		r, err := c.api.ListApprovalsWithResponse(ctx, tenant, params)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/approvals")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// Approval reads one approval.
func (c *Client) Approval(ctx context.Context, tenant, id string) (Approval, error) {
	r, err := c.api.GetApprovalWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/approvals/%s", id)); err != nil {
		return Approval{}, err
	}
	return *r.JSON200, nil
}

func decisionBody(granted bool, reason string) apiclient.CreateApprovalDecision {
	d := apiclient.CreateApprovalDecisionDecisionDenied
	if granted {
		d = apiclient.CreateApprovalDecisionDecisionGranted
	}
	return apiclient.CreateApprovalDecision{Decision: d, Reason: optional(reason)}
}

// retryGrant marks a grant safe to repeat: the contract records one
// person's grant once. A denial is not repeated, because a repeated
// one finds the approval closed and would say so.
func retryGrant(ctx context.Context, granted bool) context.Context {
	if granted {
		return idempotent(ctx)
	}
	return ctx
}

// DecideApproval grants or denies a translation unit's approval.
func (c *Client) DecideApproval(ctx context.Context, tenant, id string, granted bool, reason string) (Approval, error) {
	r, err := c.api.DecideApprovalWithResponse(retryGrant(ctx, granted), tenant, id, decisionBody(granted, reason))
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/approvals/%s/decisions", id)); err != nil {
		return Approval{}, err
	}
	return *r.JSON201, nil
}

// DecideReleaseRequest grants or denies a release request's current
// approval: the one its release-approval workflow asked for.
func (c *Client) DecideReleaseRequest(ctx context.Context, s Scope, id string, granted bool, reason string) (Approval, error) {
	r, err := c.api.DecideReleaseRequestWithResponse(retryGrant(ctx, granted), s.Tenant, s.Project, id, decisionBody(granted, reason))
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/release-requests/%s/approvals", s.Tenant, s.Project, id)); err != nil {
		return Approval{}, err
	}
	return *r.JSON201, nil
}
