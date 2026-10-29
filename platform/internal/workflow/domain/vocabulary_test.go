package domain_test

import (
	"slices"
	"testing"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	intelligence "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	localization "go.klarlabs.de/glossa/platform/internal/localization/domain"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// TestTheVocabularyIsClosed pins the vocabulary to RFC 0006 §2.4,
// primitive by primitive (§2.1 rule 4). Adding one is an RFC amendment
// with a reason that is not one organisation; changing this list is how
// that amendment lands, and never by accident.
func TestTheVocabularyIsClosed(t *testing.T) {
	wantGuards := []string{
		"actor_has_permission", "approvals_as_required", "approvals_at_least", "confidence_at_least", "findings_at_least",
		"locale_in", "namespace_in", "origin_in", "review_state_in", "tm_match_at_least",
	}
	wantActions := []string{
		"assign", "deny_release", "deploy_release", "notify", "request_approval", "request_approval_as_required",
		"request_fill", "run_check", "set_review_state",
	}
	wantEvents := []domain.EventName{
		"approval.denied", "approval.granted", "assignment.completed", "assignment.declined",
		"check_run.recorded", "release_request.created", "release_request.deployed", "release_request.refused",
		"release_request.withdrawn", "suggestion.created", "timer.due", "timer.overdue",
		"translation.outdated", "translation.reviewed", "translation.revised",
	}
	if got := domain.GuardPrimitives(); !slices.Equal(got, wantGuards) {
		t.Errorf("guards = %v, want %v", got, wantGuards)
	}
	if got := domain.ActionPrimitives(); !slices.Equal(got, wantActions) {
		t.Errorf("actions = %v, want %v", got, wantActions)
	}
	if got := domain.Events(); !slices.Equal(got, wantEvents) {
		t.Errorf("events = %v, want %v", got, wantEvents)
	}
}

// TestMirrorsAgreeWithTheirOwners proves the values the vocabulary
// validates against are the owning contexts' own. Workflow's domain
// imports no other context, so it keeps a copy; this is what keeps the
// copy honest.
func TestMirrorsAgreeWithTheirOwners(t *testing.T) {
	sorted := func(s []string) []string { s = slices.Clone(s); slices.Sort(s); return s }

	var states []string
	for s := range localization.DefaultReviewFlow().Transitions {
		states = append(states, string(s))
	}
	if !slices.Equal(sorted(states), sorted(domain.ReviewStates)) {
		t.Errorf("ReviewStates = %v, Localization has %v", domain.ReviewStates, states)
	}

	for _, o := range domain.Origins {
		if _, err := localization.ParseOrigin(o, localization.OriginHuman); err != nil {
			t.Errorf("origin %q: Localization refuses it: %v", o, err)
		}
	}

	bands := []string{
		string(intelligence.ActionReviewRequired), string(intelligence.ActionApproveRecommended),
		string(intelligence.ActionAutoApprove),
	}
	if !slices.Equal(bands, domain.Bands) {
		t.Errorf("Bands = %v, Intelligence routes to %v (lowest confidence first)", domain.Bands, bands)
	}

	for _, r := range domain.Roles {
		if _, err := identity.ParseRole(r); err != nil {
			t.Errorf("role %q: Identity refuses it: %v", r, err)
		}
	}
}
