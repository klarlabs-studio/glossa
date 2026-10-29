//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// Release approvals and staged rollouts through Release's HTTP edge
// (RFC 0006 §5.1, §5.2, §8): each operation over the service the
// app-level tests drive, so what those prove reaches the contract.

func (h *harness) environmentETag(t *testing.T, project uuid.UUID, environment string) string {
	t.Helper()
	resp, err := h.api().GetEnvironment(h.owner(), apiv1.GetEnvironmentRequestObject{
		Tenant: h.tenant.String(), Project: project.String(), Environment: environment,
	})
	if err != nil {
		t.Fatal(err)
	}
	return *resp.(apiv1.GetEnvironment200JSONResponse).Headers.ETag
}

func (h *harness) patchEnvironment(ctx context.Context, project uuid.UUID, environment, ifMatch string, body apiv1.UpdateEnvironment) (apiv1.UpdateEnvironmentResponseObject, error) {
	return h.api().UpdateEnvironment(ctx, apiv1.UpdateEnvironmentRequestObject{
		Tenant: h.tenant.String(), Project: project.String(), Environment: environment,
		Params: apiv1.UpdateEnvironmentParams{IfMatch: ifMatch}, Body: &body,
	})
}

var approvedOnly = apiv1.EnvironmentPolicy{States: []apiv1.EnvironmentPolicyStates{"approved"}}

func twoReviewers() *apiv1.EnvironmentApproval {
	return &apiv1.EnvironmentApproval{N: 2, From: apiv1.EnvironmentApprovalParty{Role: apiconv.Ptr(apiv1.Role("reviewer"))},
		DistinctFromRequester: true}
}

func TestEnvironmentApprovalOverTheAPI(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	etag := h.environmentETag(t, p, "production")

	// Who must approve is governance: a developer may change the policy
	// but not the requirement.
	if _, err := h.patchEnvironment(h.as("developer"), p, "production", etag,
		apiv1.UpdateEnvironment{Policy: approvedOnly, Approval: twoReviewers()}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a developer setting the approval: %v", err)
	}
	bad := twoReviewers()
	bad.DistinctFromRequester = false
	if _, err := h.patchEnvironment(h.owner(), p, "production", etag, apiv1.UpdateEnvironment{Policy: approvedOnly, Approval: bad}); problemCode(t, err) != "invalid_approval" {
		t.Fatalf("self-approval offered: %v", err)
	}
	if _, err := h.patchEnvironment(h.owner(), p, "production", etag,
		apiv1.UpdateEnvironment{Policy: approvedOnly, Approval: twoReviewers(), ClearApproval: ptr(true)}); problemCode(t, err) != "invalid_request" {
		t.Fatalf("set and clear at once: %v", err)
	}
	if _, err := h.patchEnvironment(h.owner(), p, "production", `"99"`, apiv1.UpdateEnvironment{Policy: approvedOnly, Approval: twoReviewers()}); problemCode(t, err) != "precondition_failed" {
		t.Fatalf("a stale If-Match: %v", err)
	}
	// Policy and approval change together, one version.
	both := apiv1.EnvironmentPolicy{States: []apiv1.EnvironmentPolicyStates{"approved"}, IncludeOutdated: true}
	resp, err := h.patchEnvironment(h.owner(), p, "production", etag, apiv1.UpdateEnvironment{Policy: both, Approval: twoReviewers()})
	if err != nil {
		t.Fatal(err)
	}
	saved := resp.(apiv1.UpdateEnvironment200JSONResponse)
	if saved.Body.Approval == nil || saved.Body.Approval.N != 2 || *saved.Body.Approval.From.Role != "reviewer" ||
		!saved.Body.Policy.IncludeOutdated || *saved.Headers.ETag == etag {
		t.Fatalf("saved = %+v (%v)", saved.Body, *saved.Headers.ETag)
	}
	got, _ := h.svc.GetEnvironment(h.owner(), p, "production")
	if apiconv.ETag(got.Version) == nil || *apiconv.ETag(got.Version) != *saved.Headers.ETag {
		t.Fatalf("version %d, ETag %s", got.Version, *saved.Headers.ETag)
	}
	// Restating it unchanged is a policy edit, which a developer may make.
	if _, err := h.patchEnvironment(h.as("developer"), p, "production", *saved.Headers.ETag,
		apiv1.UpdateEnvironment{Policy: approvedOnly, Approval: twoReviewers()}); err != nil {
		t.Fatalf("a developer restating the requirement: %v", err)
	}
	// clear_approval switches it off.
	etag = h.environmentETag(t, p, "production")
	if _, err := h.patchEnvironment(h.owner(), p, "production", etag, apiv1.UpdateEnvironment{Policy: approvedOnly, ClearApproval: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if e, _ := h.svc.GetEnvironment(h.owner(), p, "production"); e.Approval != nil {
		t.Fatalf("clear_approval left %+v", e.Approval)
	}
}

func TestReleaseRequestsOverTheAPI(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	stable, err := h.publish(t, p, apiv1.PublishRelease{Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	staged, err := h.publish(t, p, apiv1.PublishRelease{Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	h.requireApproval(t, p, "production", 2)
	// From now on production's gate refuses: German is one message
	// short. A forced publish still waits for approvals (§5.1).
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})

	// A publish answers 202 with the recorded release and its request,
	// and moves nothing.
	const reason = "the campaign starts at nine"
	owner := h.owner() // idempotency keys are the caller's
	publish := func(key string) apiv1.PublishReleaseResponseObject {
		t.Helper()
		resp, err := h.api().PublishRelease(owner, apiv1.PublishReleaseRequestObject{
			Tenant: h.tenant.String(), Project: p.String(), Params: apiv1.PublishReleaseParams{IdempotencyKey: &key},
			Body: &apiv1.PublishRelease{Environment: "production", Force: ptr(true), ForceReason: ptr(reason)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	held, ok := publish("held-1").(apiv1.PublishRelease202JSONResponse)
	if !ok {
		t.Fatalf("a publish into an approval environment did not answer 202")
	}
	req := held.Body.ReleaseRequest
	if held.Body.Id == stable.Id || held.Body.ReleaseRequestId != req.Id || req.ReleaseId != held.Body.Id ||
		req.State != "pending" || req.Action != "publish" || req.Approval.N != 2 || !req.Forced || *req.ForceReason != reason ||
		req.Gate.Met || req.Gate.Unmet == nil || *held.Headers.Location != "/v1/tenants/"+h.tenant.String()+"/projects/"+p.String()+"/release-requests/"+req.Id {
		t.Fatalf("held = %+v (%v)", held.Body, *held.Headers.Location)
	}
	if h.current(t, p, "production").String() != stable.Id {
		t.Fatal("the held publish moved the pointer")
	}
	replay, ok := publish("held-1").(apiv1.PublishRelease202JSONResponse)
	if !ok || replay.Body.ReleaseRequestId != req.Id || replay.Headers.IdempotentReplayed == nil {
		t.Fatalf("a replayed held publish = %+v", replay)
	}

	// Approvers read it, with the force and its reason.
	get := func(ctx context.Context, id string) (apiv1.GetReleaseRequestResponseObject, error) {
		return h.api().GetReleaseRequest(ctx, apiv1.GetReleaseRequestRequestObject{Tenant: h.tenant.String(), Project: p.String(), ReleaseRequest: id})
	}
	r, err := get(h.as("reviewer"), req.Id)
	if err != nil || *r.(apiv1.GetReleaseRequest200JSONResponse).ForceReason != reason {
		t.Fatalf("a reviewer reading the request: %+v %v", r, err)
	}
	if _, err := get(h.owner(), uuid.NewString()); problemCode(t, err) != "not_found" {
		t.Fatalf("an unknown request: %v", err)
	}
	if _, err := get(h.owner(), "nope"); problemCode(t, err) != "not_found" {
		t.Fatalf("a malformed id: %v", err)
	}

	// A promote is held the same way; the newer request withdraws the
	// pending one.
	presp, err := h.api().PromoteRelease(h.owner(), apiv1.PromoteReleaseRequestObject{
		Tenant: h.tenant.String(), Project: p.String(), Environment: "production",
		Body: &apiv1.Promotion{ReleaseId: staged.Id, Force: ptr(true), ForceReason: ptr(reason)},
	})
	if err != nil {
		t.Fatal(err)
	}
	promoted, ok := presp.(apiv1.PromoteRelease202JSONResponse)
	if !ok || promoted.Body.Id != staged.Id || promoted.Body.ReleaseRequest.Action != "promote" || promoted.Headers.Location == nil {
		t.Fatalf("promote = %#v", presp)
	}

	list := func(params apiv1.ListReleaseRequestsParams) []apiv1.ReleaseRequest {
		t.Helper()
		resp, err := h.api().ListReleaseRequests(h.owner(), apiv1.ListReleaseRequestsRequestObject{Tenant: h.tenant.String(), Project: p.String(), Params: params})
		if err != nil {
			t.Fatal(err)
		}
		return resp.(apiv1.ListReleaseRequests200JSONResponse).Items
	}
	if all := list(apiv1.ListReleaseRequestsParams{}); len(all) != 2 || all[0].Id != promoted.Body.ReleaseRequestId || all[1].State != "withdrawn" {
		t.Fatalf("requests = %+v", all)
	}
	pendingOnly := apiv1.ReleaseRequestState("pending")
	if got := list(apiv1.ListReleaseRequestsParams{State: &pendingOnly, Environment: ptr("production")}); len(got) != 1 {
		t.Fatalf("pending = %+v", got)
	}
	page := list(apiv1.ListReleaseRequestsParams{PageSize: ptr(1)})
	if len(page) != 1 {
		t.Fatalf("a page of one = %+v", page)
	}

	// Withdrawing takes it back; a closed request cannot be withdrawn.
	withdraw := func(ctx context.Context, id string) (apiv1.WithdrawReleaseRequestResponseObject, error) {
		return h.api().WithdrawReleaseRequest(ctx, apiv1.WithdrawReleaseRequestRequestObject{Tenant: h.tenant.String(), Project: p.String(),
			ReleaseRequest: id, Body: &apiv1.ReleaseRequestWithdrawal{Reason: ptr("wrong release")}})
	}
	if _, err := withdraw(h.as("reviewer"), promoted.Body.ReleaseRequestId); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a reviewer withdrawing: %v", err)
	}
	wresp, err := withdraw(h.owner(), promoted.Body.ReleaseRequestId)
	if err != nil {
		t.Fatal(err)
	}
	if w := wresp.(apiv1.WithdrawReleaseRequest200JSONResponse); w.State != "withdrawn" || *w.Reason != "wrong release" || w.DecidedBy == nil {
		t.Fatalf("withdrawn = %+v", w)
	}
	if _, err := withdraw(h.owner(), promoted.Body.ReleaseRequestId); problemCode(t, err) != "release_request_closed" {
		t.Fatalf("withdrawing twice: %v", err)
	}
	if h.current(t, p, "production").String() != stable.Id {
		t.Fatal("a request moved the pointer")
	}

	// A rollout into an approval environment is refused until a request
	// can carry one.
	if _, err := h.api().StartRollout(h.owner(), apiv1.StartRolloutRequestObject{Tenant: h.tenant.String(), Project: p.String(),
		Environment: "production", Body: &apiv1.StartRollout{ReleaseId: staged.Id, Percent: 10}}); problemCode(t, err) != "rollout_needs_approval" {
		t.Fatalf("a rollout into an approval environment: %v", err)
	}
}
