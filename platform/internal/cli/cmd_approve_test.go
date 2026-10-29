package cli

import (
	"strings"
	"testing"
)

// withApprovals publishes v1 to production, then makes production
// require n approvals: the state RFC 0006 §12.3 starts from.
func withApprovals(t *testing.T, n int) (*fakeServer, *workspace) {
	t.Helper()
	srv, w := seeded(t)
	publishJSON(t, w, "--environment", "production")
	srv.mu.Lock()
	srv.rel.envs["production"].approvals = n
	srv.mu.Unlock()
	return srv, w
}

func (f *fakeServer) serving(env string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rel.envs[env].current
}

// as switches the workspace's credential.
func (w *workspace) as(token string) *workspace {
	w.env["GLOSSA_TOKEN"] = token
	return w
}

func TestPublishIntoAnEnvironmentWithApprovalsIsHeldNotDeployed(t *testing.T) {
	srv, w := withApprovals(t, 2)

	r := w.run("release", "publish", "--environment", "production", "--idempotency-key", "ci-42", "--json")
	r.want(t, ExitHeld)
	golden(t, "release-publish-held.json", r.stdout)
	var out releasePublishJSON
	w.json(&out, "release", "publish", "--environment", "production", "--idempotency-key", "ci-42").want(t, ExitHeld)
	if !out.Held || !out.Replayed || out.ReleaseRequest == nil || out.ReleaseRequest.ID != "rr_1" ||
		out.Release.Version != 2 || out.ReleaseRequest.State != "pending" {
		t.Fatalf("held publish = %+v", out)
	}
	if srv.serving("production") != "rel_1" {
		t.Fatalf("a held publish moved the pointer to %s", srv.serving("production"))
	}

	human := w.run("release", "publish", "--environment", "production")
	human.want(t, ExitHeld)
	for _, want := range []string{"Held for approval: v3 was recorded but not deployed to production",
		"production keeps serving v1 until 2 people of role reviewer, none of them the requester, approve",
		"`glossa approve rr_2`", "glossa login --device", "nothing was deployed"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("held publish lacks %q:\n%s", want, human.stdout)
		}
	}
	if strings.Contains(human.stdout, "Published") || strings.Contains(human.stdout, "serves it now") {
		t.Errorf("a held publish reads as deployed:\n%s", human.stdout)
	}
}

func TestPromoteIntoAnEnvironmentWithApprovalsIsHeld(t *testing.T) {
	srv, w := withApprovals(t, 1)
	staged := publishJSON(t, w, "--environment", "staging").Release

	var out releaseMoveJSON
	r := w.json(&out, "release", "promote", "v2", "--to", "production")
	r.want(t, ExitHeld)
	if out.Schema != "glossa.cli.release.promote/v1" || !out.Held || out.Release == nil || out.Release.ID != staged.ID ||
		out.Environment.Release == nil || out.Environment.Release.Version != 1 || out.ReleaseRequest.Action != "promote" {
		t.Fatalf("held promote = %+v", out)
	}
	if srv.serving("production") != "rel_1" {
		t.Fatal("a held promote moved the pointer")
	}
	human := w.run("release", "promote", "v2", "--to", "production")
	human.want(t, ExitHeld)
	if !strings.Contains(human.stdout, "Held for approval: production does not serve v2 yet") ||
		!strings.Contains(human.stdout, "glossa approve rr_2") {
		t.Fatalf("held promote:\n%s", human.stdout)
	}
}

func TestApproveRefusesAnAPITokenPlainly(t *testing.T) {
	_, w := withApprovals(t, 2)
	w.run("release", "publish", "--environment", "production").want(t, ExitHeld)

	doc := wantError(t, w, ExitNetwork, "person_required", "approve", "rr_1")
	if doc.Error.Message != "approvals need a person: sign in with `glossa login --device`" ||
		!strings.Contains(doc.Error.Why, "no token scope grants approvals.decide") || !strings.Contains(doc.Error.Fix, "Studio") {
		t.Fatalf("token refusal = %+v", doc.Error)
	}
	r := w.run("approve", "rr_1")
	r.want(t, ExitNetwork)
	if !strings.Contains(r.stderr, "error: approvals need a person: sign in with `glossa login --device`") {
		t.Fatalf("human refusal:\n%s", r.stderr)
	}
	wantError(t, w, ExitNetwork, "person_required", "deny", "rr_1", "--reason", "no")
}

func TestApproveTwoPeopleOtherThanTheRequesterDeploy(t *testing.T) {
	srv, w := withApprovals(t, 2)
	w.run("release", "publish", "--environment", "production").want(t, ExitHeld)

	var first approveDoc
	w.as(testPersonVera).json(&first, "approve", "rr_1", "--reason", "copy checked").want(t, ExitOK)
	if first.Schema != approveSchema || first.Action != "approve" || first.Kind != "release_request" ||
		first.Approval.Granted != 1 || first.Approval.Required != 2 || first.ReleaseRequest.State != "pending" ||
		first.Approval.Decisions[0].Reason != "copy checked" {
		t.Fatalf("first approval = %+v", first)
	}
	if srv.serving("production") != "rel_1" {
		t.Fatal("one approval of two moved the pointer")
	}
	// A person's repeated grant counts once.
	r := w.run("approve", "rr_1")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "1 of 2") || !strings.Contains(r.stdout, "production keeps serving v1 until 1 more person") {
		t.Fatalf("repeated approval:\n%s", r.stdout)
	}

	r = w.as(testPersonOtto).run("approve", "rr_1")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "Approved release request rr_1 (v2 to production): 2 of 2") ||
		!strings.Contains(r.stdout, "production now serves v2") {
		t.Fatalf("second approval:\n%s", r.stdout)
	}
	if srv.serving("production") != "rel_2" {
		t.Fatal("two approvals did not deploy")
	}
	doc := wantError(t, w, ExitNetwork, "release_request_closed", "approve", "rr_1")
	if !strings.Contains(doc.Error.Fix, "glossa release requests show") {
		t.Errorf("closed = %+v", doc.Error)
	}
}

func TestApproveRefusesTheRequesterAndExplainsTheOtherRefusals(t *testing.T) {
	srv, w := withApprovals(t, 1)
	w.as(testPersonVera).run("release", "publish", "--environment", "production").want(t, ExitHeld)

	doc := wantError(t, w, ExitNetwork, "own_text", "approve", "rr_1")
	if !strings.Contains(doc.Error.Why, "four-eyes") {
		t.Errorf("own = %+v", doc.Error)
	}
	srv.mu.Lock()
	srv.appr.notAsked = true
	srv.mu.Unlock()
	w.run("release", "publish", "--environment", "production").want(t, ExitHeld)
	doc = wantError(t, w.as(testPersonOtto), ExitNetwork, "approval_not_requested", "approve", "rr_2")
	if !strings.Contains(doc.Error.Fix, "retry in a few seconds") {
		t.Errorf("not requested = %+v", doc.Error)
	}
	wantError(t, w, ExitNetwork, "not_found", "approve", "rr_nope")
	wantError(t, w, ExitUsage, "invalid_usage", "approve", "rr_1", "rr_2")
}

func TestDenyNeedsAReasonAndClosesTheRequest(t *testing.T) {
	srv, w := withApprovals(t, 2)
	w.run("release", "publish", "--environment", "production").want(t, ExitHeld)
	w.as(testPersonVera)

	doc := wantError(t, w, ExitUsage, "reason_required", "deny", "rr_1")
	if !strings.Contains(doc.Error.Fix, `glossa deny rr_1 --reason`) {
		t.Errorf("no reason = %+v", doc.Error)
	}
	wantError(t, w, ExitUsage, "invalid_usage", "deny", "--reason", "x")

	var out approveDoc
	w.json(&out, "deny", "rr_1", "--reason", "the de copy is wrong").want(t, ExitOK)
	if out.Action != "deny" || out.ReleaseRequest.State != "denied" || out.ReleaseRequest.Reason != "the de copy is wrong" ||
		out.Approval.State != "denied" {
		t.Fatalf("deny = %+v", out)
	}
	if srv.serving("production") != "rel_1" {
		t.Fatal("a denial moved the pointer")
	}
	r := w.run("deny", "rr_1", "--reason", "again")
	r.want(t, ExitNetwork)
	if !strings.Contains(r.stderr, "can't deny release request rr_1") {
		t.Fatalf("deny twice:\n%s", r.stderr)
	}
}

func TestApproveListsWhatWaitsAndDecidesTranslations(t *testing.T) {
	srv, w := withApprovals(t, 2)
	w.run("release", "publish", "--environment", "production", "--idempotency-key", "k1").want(t, ExitHeld)
	srv.translationApproval("checkout.pay", "de", 2)
	srv.translationApproval("cart.items", "ja", 1)

	r := w.run("approve", "--json")
	r.want(t, ExitOK)
	golden(t, "approve-list.json", r.stdout)
	var list approveListDoc
	w.json(&list, "approve", "list", "--locale", "de").want(t, ExitOK)
	if len(list.ReleaseRequests) != 0 || len(list.Approvals) != 1 || list.Approvals[0].Message != "checkout.pay" {
		t.Fatalf("de only = %+v", list)
	}
	w.json(&list, "approve", "--environment", "production").want(t, ExitOK)
	if len(list.ReleaseRequests) != 1 || len(list.Approvals) != 0 {
		t.Fatalf("production only = %+v", list)
	}
	human := w.run("approve")
	human.want(t, ExitOK)
	for _, want := range []string{"Release requests", "rr_1", "2 of role reviewer", "Translations", "checkout.pay@de", "0 of 2"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("inbox lacks %q:\n%s", want, human.stdout)
		}
	}

	// A translation unit, by key@locale and by the approval's ID.
	var out approveDoc
	w.as(testPersonVera).json(&out, "approve", "checkout.pay@de").want(t, ExitOK)
	if out.Kind != "translation" || out.Approval.Message != "checkout.pay" || out.Approval.Locale != "de" || out.Approval.Granted != 1 {
		t.Fatalf("unit approval = %+v", out)
	}
	r = w.as(testPersonOtto).run("approve", out.Approval.ID)
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "Approved checkout.pay@de") || !strings.Contains(r.stdout, "2 of 2") ||
		!strings.Contains(r.stdout, "the project's workflow approves the translation") {
		t.Fatalf("second unit approval:\n%s", r.stdout)
	}
	doc := wantError(t, w, ExitNetwork, "approval_not_found", "approve", "checkout.pay@de")
	if !strings.Contains(doc.Error.Message, "nothing waits for approval on checkout.pay@de") {
		t.Errorf("none pending = %+v", doc.Error)
	}
	w.json(&out, "deny", "cart.items@ja", "--reason", "wrong counter").want(t, ExitOK)
	if out.Approval.State != "denied" {
		t.Fatalf("unit denial = %+v", out)
	}
	// An approval's ID that is a release request's routes to the request.
	w.json(&out, "approve", "apr_1").want(t, ExitOK)
	if out.Kind != "release_request" || out.ReleaseRequest.ID != "rr_1" {
		t.Fatalf("by approval ID = %+v", out)
	}
	wantError(t, w, ExitUsage, "invalid_usage", "approve", "x@not a locale")

	srv.mu.Lock()
	srv.appr.approvals = nil
	srv.appr.requests = nil
	srv.mu.Unlock()
	human = w.run("approve")
	if !strings.Contains(human.stdout, "Nothing waits for approval in this project.") {
		t.Fatalf("empty inbox:\n%s", human.stdout)
	}
}

func TestReleaseRequestsListShowWithdraw(t *testing.T) {
	_, w := withApprovals(t, 2)
	w.run("release", "publish", "--environment", "production").want(t, ExitHeld)
	w.run("release", "publish", "--environment", "production").want(t, ExitHeld) // withdraws rr_1

	var list releaseRequestsListDoc
	w.json(&list, "release", "requests").want(t, ExitOK)
	if list.Schema != releaseRequestsSchema || list.State != "pending" || len(list.ReleaseRequests) != 1 ||
		list.ReleaseRequests[0].ID != "rr_2" || list.ReleaseRequests[0].Release.Version != 3 {
		t.Fatalf("pending = %+v", list)
	}
	w.json(&list, "release", "requests", "list", "--state", "all", "--environment", "production").want(t, ExitOK)
	if len(list.ReleaseRequests) != 2 || list.ReleaseRequests[1].State != "withdrawn" {
		t.Fatalf("all = %+v", list)
	}
	w.json(&list, "release", "requests", "--environment", "staging").want(t, ExitOK)
	if len(list.ReleaseRequests) != 0 {
		t.Fatalf("staging = %+v", list)
	}
	wantError(t, w, ExitUsage, "invalid_usage", "release", "requests", "--state", "late")

	w.as(testPersonVera).run("approve", "rr_2").want(t, ExitOK)
	r := w.run("release", "requests", "show", "rr_2", "--json")
	r.want(t, ExitOK)
	golden(t, "release-requests-show.json", r.stdout)
	human := w.run("release", "requests", "show", "rr_2")
	for _, want := range []string{"rr_2 · pending · publish v3 to production", "needs        2 of role reviewer", "1 of 2 granted",
		"granted person:vera"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("show lacks %q:\n%s", want, human.stdout)
		}
	}

	var out releaseRequestDoc
	w.json(&out, "release", "requests", "withdraw", "rr_2", "--reason", "wrong build").want(t, ExitOK)
	if out.Action != "withdraw" || out.ReleaseRequest.State != "withdrawn" || out.ReleaseRequest.Reason != "wrong build" {
		t.Fatalf("withdraw = %+v", out)
	}
	wantError(t, w, ExitNetwork, "release_request_closed", "release", "requests", "withdraw", "rr_2")
	wantError(t, w, ExitNetwork, "not_found", "release", "requests", "show", "rr_9")
}
