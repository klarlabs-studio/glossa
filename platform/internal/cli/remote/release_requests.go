package remote

import (
	"context"
	"fmt"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/release"
)

// Release requests and staged rollouts (RFC 0006 §5.1–§5.2) as
// release.Service serves them.

// ── conversions ─────────────────────────────────────────────────────

func toRequest(q apiclient.ReleaseRequest) release.Request {
	out := release.Request{ID: q.Id, Environment: q.Environment, ReleaseID: q.ReleaseId, Action: string(q.Action),
		Requester: q.Requester, Forced: q.Forced, ForceReason: deref(q.ForceReason), State: string(q.State),
		DecidedBy: deref(q.DecidedBy), DecidedAt: q.DecidedAt, Reason: deref(q.Reason), CreatedAt: q.CreatedAt,
		Gate: release.Gate{Met: q.Gate.Met},
		Approval: release.Requirement{N: q.Approval.N, DistinctFromRequester: q.Approval.DistinctFromRequester,
			From: release.Party{Member: deref(q.Approval.From.Member), Group: deref(q.Approval.From.Group)}}}
	if q.Approval.From.Role != nil {
		out.Approval.From.Role = string(*q.Approval.From.Role)
	}
	if q.Gate.Unmet != nil {
		out.Gate.Unmet = *q.Gate.Unmet
	}
	return out
}

// toHeld reads a 202's ReleaseHeld. A 202 without one is the server
// breaking the contract, and is reported as such rather than as a
// deployment.
func toHeld(h *apiclient.ReleaseHeld, method, url string) (*release.Held, error) {
	if h == nil {
		return nil, &APIError{Method: method, URL: url, Status: http.StatusAccepted, Code: "invalid_response",
			Detail: "202 without a release request", Err: fmt.Errorf("202 without a release request")}
	}
	return &release.Held{ReleaseID: h.Id, RequestID: h.ReleaseRequestId, Request: toRequest(h.ReleaseRequest)}, nil
}

func toRollout(r apiclient.Rollout) release.Rollout {
	out := release.Rollout{ID: r.Id, Environment: r.Environment, ReleaseID: r.ReleaseId, StableReleaseID: r.StableReleaseId,
		Percent: r.Percent, Status: string(r.Status), MaxDurationSeconds: r.MaxDurationSeconds, ExpiresAt: r.ExpiresAt,
		Forced: r.Forced, ForceReason: deref(r.ForceReason), StartedBy: r.StartedBy, StartedAt: r.StartedAt,
		UpdatedAt: r.UpdatedAt, EndedBy: deref(r.EndedBy), EndedAt: r.EndedAt}
	if r.End != nil {
		out.End = string(*r.End)
	}
	return out
}

// ── release requests ────────────────────────────────────────────────

// ReleaseRequests implements release.Service.
func (r *ReleaseService) ReleaseRequests(ctx context.Context, s release.Scope, f release.RequestFilter) ([]release.Request, error) {
	size := pageSize
	params := &apiclient.ListReleaseRequestsParams{PageSize: &size, Environment: optional(f.Environment)}
	if f.State != "" {
		st := apiclient.ReleaseRequestState(f.State)
		params.State = &st
	}
	items, err := collect(func(tok *string) ([]apiclient.ReleaseRequest, *string, error) {
		params.PageToken = tok
		resp, err := r.c.api.ListReleaseRequestsWithResponse(ctx, s.Tenant, s.Project, params)
		if err := check(resp, err, http.MethodGet, r.project(s, "/release-requests")); err != nil {
			return nil, nil, err
		}
		return resp.JSON200.Items, resp.JSON200.NextPageToken, nil
	})
	return mapAll(items, toRequest), err
}

// ReleaseRequest implements release.Service.
func (r *ReleaseService) ReleaseRequest(ctx context.Context, s release.Scope, id string) (release.Request, error) {
	resp, err := r.c.api.GetReleaseRequestWithResponse(ctx, s.Tenant, s.Project, id)
	if err := check(resp, err, http.MethodGet, r.project(s, "/release-requests/%s", id)); err != nil {
		return release.Request{}, err
	}
	return toRequest(*resp.JSON200), nil
}

// WithdrawReleaseRequest implements release.Service.
func (r *ReleaseService) WithdrawReleaseRequest(ctx context.Context, s release.Scope, id, reason string) (release.Request, error) {
	resp, err := r.c.api.WithdrawReleaseRequestWithResponse(ctx, s.Tenant, s.Project, id,
		apiclient.ReleaseRequestWithdrawal{Reason: optional(reason)})
	if err := check(resp, err, http.MethodPost, r.project(s, "/release-requests/%s/withdrawal", id)); err != nil {
		return release.Request{}, err
	}
	return toRequest(*resp.JSON200), nil
}

// ── rollouts ────────────────────────────────────────────────────────

func (r *ReleaseService) rolloutPath(s release.Scope, env, id, sub string) string {
	if id == "" {
		return r.project(s, "/environments/%s/rollouts", env)
	}
	return r.project(s, "/environments/%s/rollouts/%s"+sub, env, id)
}

// Rollouts implements release.Service.
func (r *ReleaseService) Rollouts(ctx context.Context, s release.Scope, environment string, limit int) ([]release.Rollout, error) {
	size := pageSize
	if limit > 0 && limit < size {
		size = limit
	}
	var out []release.Rollout
	var tok *string
	for {
		resp, err := r.c.api.ListRolloutsWithResponse(ctx, s.Tenant, s.Project, environment,
			&apiclient.ListRolloutsParams{PageSize: &size, PageToken: tok})
		if err := check(resp, err, http.MethodGet, r.rolloutPath(s, environment, "", "")); err != nil {
			return nil, err
		}
		out = append(out, mapAll(resp.JSON200.Items, toRollout)...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		tok = resp.JSON200.NextPageToken
		if tok == nil || *tok == "" {
			return out, nil
		}
	}
}

// Rollout implements release.Service.
func (r *ReleaseService) Rollout(ctx context.Context, s release.Scope, environment, id string) (release.RolloutVersion, error) {
	resp, err := r.c.api.GetRolloutWithResponse(ctx, s.Tenant, s.Project, environment, id)
	if err := check(resp, err, http.MethodGet, r.rolloutPath(s, environment, id, "")); err != nil {
		return release.RolloutVersion{}, err
	}
	return release.RolloutVersion{Rollout: toRollout(*resp.JSON200), ETag: resp.HTTPResponse.Header.Get("ETag")}, nil
}

// StartRollout implements release.Service. With an idempotency key a
// retry answers the first start, so the request is retried.
func (r *ReleaseService) StartRollout(ctx context.Context, s release.Scope, in release.StartRollout) (release.RolloutVersion, error) {
	params := &apiclient.StartRolloutParams{}
	if in.IdempotencyKey != "" {
		params.IdempotencyKey = &in.IdempotencyKey
		ctx = idempotent(ctx)
	}
	body := apiclient.StartRollout{ReleaseId: in.ReleaseID, Percent: in.Percent, ForceReason: optional(in.ForceReason)}
	if in.MaxDurationSeconds > 0 {
		body.MaxDurationSeconds = &in.MaxDurationSeconds
	}
	if in.Force {
		body.Force = &in.Force
	}
	resp, err := r.c.api.StartRolloutWithResponse(ctx, s.Tenant, s.Project, in.Environment, params, body)
	if err := check(resp, err, http.MethodPost, r.rolloutPath(s, in.Environment, "", "")); err != nil {
		return release.RolloutVersion{}, err
	}
	return release.RolloutVersion{Rollout: toRollout(*resp.JSON201), ETag: resp.HTTPResponse.Header.Get("ETag"),
		Replayed: resp.HTTPResponse.Header.Get("Idempotent-Replayed") == "true"}, nil
}

// AdvanceRollout implements release.Service. It is conditional on
// ifMatch. It is not retried: a retry after a lost answer would find
// its own change and fail with 412, which says the wrong thing.
func (r *ReleaseService) AdvanceRollout(ctx context.Context, s release.Scope, environment, id, ifMatch string, percent int) (release.RolloutVersion, error) {
	resp, err := r.c.api.AdvanceRolloutWithResponse(ctx, s.Tenant, s.Project, environment, id,
		&apiclient.AdvanceRolloutParams{IfMatch: ifMatch}, apiclient.AdvanceRollout{Percent: percent})
	if err := check(resp, err, http.MethodPatch, r.rolloutPath(s, environment, id, "")); err != nil {
		return release.RolloutVersion{}, err
	}
	return release.RolloutVersion{Rollout: toRollout(*resp.JSON200), ETag: resp.HTTPResponse.Header.Get("ETag")}, nil
}

// CompleteRollout implements release.Service.
func (r *ReleaseService) CompleteRollout(ctx context.Context, s release.Scope, environment, id, ifMatch string) (release.RolloutVersion, error) {
	resp, err := r.c.api.CompleteRolloutWithResponse(ctx, s.Tenant, s.Project, environment, id,
		&apiclient.CompleteRolloutParams{IfMatch: optional(ifMatch)})
	if err := check(resp, err, http.MethodPost, r.rolloutPath(s, environment, id, "/completion")); err != nil {
		return release.RolloutVersion{}, err
	}
	return release.RolloutVersion{Rollout: toRollout(*resp.JSON200), ETag: resp.HTTPResponse.Header.Get("ETag")}, nil
}

// AbortRollout implements release.Service. Aborting an aborted rollout
// is refused (rollout_ended), so a lost answer must not be retried.
func (r *ReleaseService) AbortRollout(ctx context.Context, s release.Scope, environment, id, ifMatch string) (release.RolloutVersion, error) {
	resp, err := r.c.api.AbortRolloutWithResponse(ctx, s.Tenant, s.Project, environment, id,
		&apiclient.AbortRolloutParams{IfMatch: optional(ifMatch)})
	if err := check(resp, err, http.MethodPost, r.rolloutPath(s, environment, id, "/abort")); err != nil {
		return release.RolloutVersion{}, err
	}
	return release.RolloutVersion{Rollout: toRollout(*resp.JSON200), ETag: resp.HTTPResponse.Header.Get("ETag")}, nil
}
