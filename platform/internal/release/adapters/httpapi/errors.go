package httpapi

import (
	"errors"

	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// problems maps Release's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrNotFound, 404, problem.CodeNotFound, "no such resource"},
	{app.ErrReleaseNotInProject, 404, "release_not_found", "the project has no such release"},
	{app.ErrEnvironmentExists, 409, "environment_exists", "the project already has an environment with that name"},
	{app.ErrStaleVersion, 409, problem.CodeConflict, "the resource changed concurrently; retry"},
	{domain.ErrIneligible, 409, "release_ineligible", ""},
	{domain.ErrPolicyNotMet, 409, "policy_not_met", ""},
	{domain.ErrNoRollbackTarget, 409, "no_rollback_target", ""},
	{domain.ErrNotInHistory, 409, "not_in_history", ""},
	{domain.ErrKeyRevoked, 409, "key_revoked", ""},
	{domain.ErrBranchReleaseNotPromotable, 409, "branch_release_not_promotable", ""},
	{domain.ErrTooManyBranches, 409, "too_many_branches", ""},
	{domain.ErrFixedPolicy, 409, "fixed_policy", ""},
	// Staged rollouts (RFC 0006 §5.2). rollout_active is also a publish
	// or promote into an environment with an active rollout.
	{domain.ErrRolloutActive, 409, "rollout_active", ""},
	{domain.ErrRolloutEnded, 409, "rollout_ended", ""},
	{domain.ErrRolloutNoStable, 409, "rollout_no_stable", ""},
	{domain.ErrRolloutCandidateServed, 409, "rollout_candidate_served", ""},
	{domain.ErrRolloutBranchEnvironment, 409, "rollout_branch_environment", ""},
	{domain.ErrRolloutSourceLocale, 409, "rollout_source_locale", ""},
	{domain.ErrRolloutNeedsApproval, 409, "rollout_needs_approval", ""},
	{domain.ErrInvalidPercent, 400, "invalid_percent", ""},
	{domain.ErrInvalidMaxDuration, 400, "invalid_max_duration", ""},
	// Release approvals (RFC 0006 §5.1).
	{domain.ErrInvalidApproval, 422, "invalid_approval", ""},
	{domain.ErrApprovalOnBranch, 422, "approval_on_branch", ""},
	{domain.ErrRequestClosed, 409, "release_request_closed", ""},
	{domain.ErrApprovalNotMet, 409, "approval_not_met", ""},
	{domain.ErrInvalidWithdrawReason, 400, "invalid_withdraw_reason", ""},
	{app.ErrApprovalsUnavailable, 503, "approvals_unavailable", ""},
	{app.ErrPreconditionFailed, 412, problem.CodePreconditionFailed, "the resource changed; fetch it and retry with its new ETag"},
	{app.ErrIdempotencyReuse, 422, "idempotency_key_reused", "this Idempotency-Key was used for a different request"},
	{domain.ErrNotReleasable, 422, "not_releasable", ""},
	{app.ErrStorage, 503, "storage_unavailable", "artifact storage is unavailable; retry"},
	{app.ErrInvalidPageToken, 400, "invalid_page_token", "page_token is not one this list issued"},
	{idempotency.ErrInvalidKey, 400, "invalid_idempotency_key", ""},
	{domain.ErrInvalidEnvironment, 400, "invalid_environment", ""},
	{domain.ErrInvalidPolicy, 400, "invalid_policy", ""},
	{domain.ErrInvalidNote, 400, "invalid_note", ""},
	{domain.ErrForceNeedsReason, 400, "force_reason_required", ""},
	{domain.ErrInvalidForceReason, 400, "invalid_force_reason", ""},
	{domain.ErrInvalidKeyName, 400, "invalid_key_name", ""},
	{delivery.ErrInvalidScope, 400, "invalid_key_scope", ""},
}

// mapError turns Release's errors into problem details; anything else
// (authorization, unexpected failures) passes through to the shared
// error hook.
func mapError(err error) error {
	for _, p := range problems {
		if errors.Is(err, p.err) {
			detail := p.detail
			if detail == "" {
				detail = err.Error()
			}
			return problem.New(p.status, p.code, detail)
		}
	}
	return err
}
