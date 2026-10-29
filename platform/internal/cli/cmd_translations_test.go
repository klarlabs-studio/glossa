package cli

import (
	"strings"
	"testing"
)

// reviewable seeds the catalog and puts every translation in
// needs_review at revision 1.
func reviewable(t *testing.T) (*fakeServer, *workspace) {
	t.Helper()
	srv, w := seeded(t)
	srv.mu.Lock()
	for _, byKey := range srv.translations {
		for _, tr := range byKey {
			tr.state, tr.revision = "needs_review", 1
		}
	}
	srv.mu.Unlock()
	return srv, w
}

func (f *fakeServer) stateOf(locale, key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.translations[locale][key].state
}

func TestTranslationsReviewOneUnit(t *testing.T) {
	srv, w := reviewable(t)

	r := w.run("translations", "review", "checkout.pay@de", "--state", "approved", "--json")
	r.want(t, ExitOK)
	golden(t, "translations-review.json", r.stdout)

	if srv.stateOf("de", "checkout.pay") != "approved" {
		t.Fatalf("state = %s", srv.stateOf("de", "checkout.pay"))
	}
	if got := strings.Join(srv.rv.ifMatch, ","); got != `"1"` {
		t.Fatalf("If-Match = %s, want the ETag that was read", got)
	}
	human := w.run("translations", "review", "checkout.pay@de", "--state", "rejected")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "✓ checkout.pay@de: approved → rejected (revision 3)") {
		t.Fatalf("human output:\n%s", human.stdout)
	}
}

func TestTranslationsReviewSeveralUnits(t *testing.T) {
	srv, w := reviewable(t)

	var out translationsReviewDoc
	w.json(&out, "translations", "review", "checkout.pay@de", "cart.items@ja", "cart.checkout@de", "--state", "draft").want(t, ExitOK)
	if out.Schema != translationsReviewSchema || out.State != "draft" || out.Reviewed != 3 || out.Failed != 0 || len(out.Units) != 3 ||
		out.Units[1].Key != "cart.items" || out.Units[1].Locale != "ja" || out.Units[1].From != "needs_review" || out.Units[1].State != "draft" {
		t.Fatalf("review = %+v", out)
	}
	for _, u := range [][2]string{{"de", "checkout.pay"}, {"ja", "cart.items"}, {"de", "cart.checkout"}} {
		if srv.stateOf(u[0], u[1]) != "draft" {
			t.Errorf("%s@%s = %s", u[1], u[0], srv.stateOf(u[0], u[1]))
		}
	}
	human := w.run("translations", "review", "checkout.pay@de", "cart.items@ja", "--state", "needs_review")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "2 reviewed, 0 failed") {
		t.Fatalf("summary:\n%s", human.stdout)
	}
}

func TestTranslationsReviewSomeFailExitsPartial(t *testing.T) {
	srv, w := reviewable(t)
	srv.rv.problems["cart.items@ja"] = fakeProblem{403, "review_forbidden"}

	var out translationsReviewDoc
	w.json(&out, "translations", "review", "checkout.pay@de", "cart.items@ja", "nope@de", "--state", "approved").want(t, ExitPartial)
	if out.Reviewed != 1 || out.Failed != 2 || out.Units[0].Error != nil || out.Units[1].Error == nil || out.Units[1].Error.Code != "review_forbidden" ||
		out.Units[2].Error == nil || out.Units[2].Error.Code != "not_found" {
		t.Fatalf("review = %+v", out)
	}
	human := w.run("translations", "review", "cart.items@ja", "nope@de", "--state", "approved")
	human.want(t, ExitNetwork)
	for _, want := range []string{"✗ cart.items@ja: you may not review cart.items@ja as approved", "✗ nope@de: no translation nope@de", "0 reviewed, 2 failed"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("missing %q:\n%s", want, human.stdout)
		}
	}
}

func TestTranslationsReviewProblemCodes(t *testing.T) {
	srv, w := reviewable(t)
	srv.rv.problems["cart.items@ja"] = fakeProblem{403, "review_forbidden"}

	doc := wantError(t, w, ExitNetwork, "review_forbidden", "translations", "review", "cart.items@ja", "--state", "approved")
	if doc.Error.Message != "you may not review cart.items@ja as approved" || !strings.Contains(doc.Error.Fix, "translations.review for ja") {
		t.Errorf("forbidden = %+v", doc.Error)
	}

	srv.rv.problems["cart.items@ja"] = fakeProblem{403, "own_text"}
	doc = wantError(t, w, ExitNetwork, "own_text", "translations", "review", "cart.items@ja", "--state", "approved")
	if !strings.Contains(doc.Error.Message, "you wrote its current text") || !strings.Contains(doc.Error.Fix, "another reviewer exists for this locale") {
		t.Errorf("own text = %+v", doc.Error)
	}

	doc = wantError(t, w, ExitNetwork, "invalid_transition", "translations", "review", "checkout.pay@de", "--state", "needs_review")
	if doc.Error.Message != "checkout.pay@de can't move from needs_review to needs_review" {
		t.Errorf("transition = %+v", doc.Error)
	}

	srv.rv.problems["cart.checkout@de"] = fakeProblem{400, "invalid_state"}
	doc = wantError(t, w, ExitUsage, "invalid_state", "translations", "review", "cart.checkout@de", "--state", "approved")
	if !strings.Contains(doc.Error.Fix, "approved, rejected, draft, needs_review") {
		t.Errorf("invalid state = %+v", doc.Error)
	}

	doc = wantError(t, w, ExitNetwork, "not_found", "translations", "review", "cart.items@fr", "--state", "approved")
	if doc.Error.Message != "no translation cart.items@fr" {
		t.Errorf("not found = %+v", doc.Error)
	}
	wantError(t, w, ExitNetwork, "not_found", "translations", "review", "nope@de", "--state", "approved")
}

func TestTranslationsReviewRetriesAStaleReadOnce(t *testing.T) {
	srv, w := reviewable(t)

	// Another writer changes the unit after the first read.
	srv.rv.concurrent = 1
	var out translationsReviewDoc
	w.json(&out, "translations", "review", "checkout.pay@de", "--state", "approved").want(t, ExitOK)
	if !out.Units[0].Retried || out.Units[0].State != "approved" || strings.Join(srv.rv.ifMatch, ",") != `"1","2"` {
		t.Fatalf("retry = %+v, If-Match %v", out.Units[0], srv.rv.ifMatch)
	}

	// Twice stale: reported, not looped.
	srv.rv.ifMatch, srv.rv.concurrent = nil, 2
	doc := wantError(t, w, ExitNetwork, "precondition_failed", "translations", "review", "cart.items@de", "--state", "approved")
	if !strings.Contains(doc.Error.Message, "cart.items@de changed while it was being reviewed") || len(srv.rv.ifMatch) != 2 {
		t.Errorf("stale = %+v, If-Match %v", doc.Error, srv.rv.ifMatch)
	}
}

func TestTranslationsReviewUsage(t *testing.T) {
	_, w := reviewable(t)
	wantError(t, w, ExitUsage, "invalid_state", "translations", "review", "checkout.pay@de")
	wantError(t, w, ExitUsage, "invalid_state", "translations", "review", "checkout.pay@de", "--state", "done")
	wantError(t, w, ExitUsage, "invalid_usage", "translations", "review", "--state", "approved")
	wantError(t, w, ExitUsage, "invalid_usage", "translations", "review", "checkout.pay", "--state", "approved")
	wantError(t, w, ExitUsage, "invalid_usage", "translations", "--state", "approved")
}

func TestApproveNothingWaitsPointsToTranslationsReview(t *testing.T) {
	_, w := reviewable(t)
	doc := wantError(t, w, ExitNetwork, "approval_not_found", "approve", "checkout.pay@de")
	if !strings.Contains(doc.Error.Fix, "glossa translations review checkout.pay@de --state approved") {
		t.Errorf("fix = %q", doc.Error.Fix)
	}
}
