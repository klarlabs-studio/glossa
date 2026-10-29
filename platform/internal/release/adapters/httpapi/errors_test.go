package httpapi

import (
	"errors"
	"fmt"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// The codes RFC 0006 wave 4 adds to Release's contract (api/openapi.yaml
// documents each on its operation). A code is part of the contract:
// this pins the status and the spelling.
func TestReleaseApprovalAndRolloutCodes(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   problem.Code
	}{
		{domain.ErrInvalidApproval, 422, "invalid_approval"},
		{domain.ErrApprovalOnBranch, 422, "approval_on_branch"},
		{fmt.Errorf("%w: it is deployed", domain.ErrRequestClosed), 409, "release_request_closed"},
		{fmt.Errorf("%w: 1 of 2 approvals", domain.ErrApprovalNotMet), 409, "approval_not_met"},
		{&domain.DeployRefusedError{Cause: &domain.PolicyNotMetError{}}, 409, "policy_not_met"},
		{domain.ErrInvalidWithdrawReason, 400, "invalid_withdraw_reason"},
		{app.ErrApprovalsUnavailable, 503, "approvals_unavailable"},
		{fmt.Errorf("%w: 101", domain.ErrInvalidPercent), 400, "invalid_percent"},
		{fmt.Errorf("%w: 1m", domain.ErrInvalidMaxDuration), 400, "invalid_max_duration"},
		{domain.ErrRolloutActive, 409, "rollout_active"},
		{domain.ErrRolloutEnded, 409, "rollout_ended"},
		{domain.ErrRolloutNoStable, 409, "rollout_no_stable"},
		{domain.ErrRolloutCandidateServed, 409, "rollout_candidate_served"},
		{domain.ErrRolloutBranchEnvironment, 409, "rollout_branch_environment"},
		{domain.ErrRolloutSourceLocale, 409, "rollout_source_locale"},
		{domain.ErrRolloutNeedsApproval, 409, "rollout_needs_approval"},
		{app.ErrReleaseNotInProject, 404, "release_not_found"},
		{domain.ErrForceNeedsReason, 400, "force_reason_required"},
	} {
		var d *problem.Details
		if !errors.As(mapError(c.err), &d) || d.Status != c.status || d.Code != c.code {
			t.Errorf("%v → %+v, want %d %s", c.err, d, c.status, c.code)
		}
	}
}
