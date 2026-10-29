package app

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// decisions are the action primitives bound to the actor with no
// fallback (§2.5): moving production and the release request's end.
// set_review_state is a decision only for approved and rejected, which
// TestDecisionsNeverFallBack covers.
var decisions = map[string]bool{"deploy_release": true, "deny_release": true}

// TestEveryPrimitiveIsClassified fails when a primitive is added
// without saying whether it decides: one that is in neither list is
// treated as a decision (it never falls back), but saying so is the
// author's job, not the default's.
func TestEveryPrimitiveIsClassified(t *testing.T) {
	for _, use := range domain.ActionPrimitives() {
		if decisions[use] == mayFallBack[use] {
			t.Errorf("action primitive %q is in %s: put it in exactly one of decisions (bound to the actor) and mayFallBack (decides nothing)",
				use, map[bool]string{true: "both lists", false: "neither list"}[decisions[use]])
		}
	}
}

func TestDecisionsNeverFallBack(t *testing.T) {
	for _, c := range []struct {
		e    domain.Effect
		want bool
	}{
		{domain.Effect{Use: "set_review_state", Params: domain.SetReviewState{State: "approved"}}, true},
		{domain.Effect{Use: "set_review_state", Params: domain.SetReviewState{State: "rejected"}}, true},
		{domain.Effect{Use: "set_review_state", Params: domain.SetReviewState{State: "needs_review"}}, false},
		{domain.Effect{Use: "deploy_release", Params: domain.DeployRelease{}}, true},
		{domain.Effect{Use: "deny_release", Params: domain.DenyRelease{}}, true},
		{domain.Effect{Use: "request_approval", Params: domain.RequestApproval{}}, false},
		{domain.Effect{Use: "a_primitive_from_the_future"}, true},
	} {
		if got := decision(c.e); got != c.want {
			t.Errorf("decision(%s %+v) = %v, want %v", c.e.Use, c.e.Params, got, c.want)
		}
	}
}
