package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa check` and the project's waivers (RFC 0005 §2.3, §12.4).
//
// Both halves existed and neither was joined to the other: the CLI
// could create a waiver whose fingerprint provably matches the server's
// finding (cmd_waive_test.go), and the server applied waivers when it
// recorded a run. `glossa check` did neither — it never fetched them and
// never applied them — so a waiver changed the pull request's counts and
// not the terminal's, and the two surfaces disagreed about a decision
// somebody had already made and written a reason for.
//
// These tests are therefore of the command and not of the matcher: they
// waive through the CLI and then read what `glossa check` says.

// theFinding is the one finding of a `pushed` project's check: de has
// nothing in it and the default policy fails on that.
// theFinding is the fixture's missing German translation — the error
// these tests waive.
//
// It selects rather than taking the only finding, because the fixture's
// own source copy legitimately draws warnings from the layers that read
// it (`"Welcome"` is one word with no description). Asserting a count
// here would make this test fail whenever a layer starts finding
// something new, which is the opposite of what it is about: what is
// under test is that a waiver moves *this* finding to `waived` and out
// of the error count.
func theFinding(t *testing.T, out checkJSON) domain.Finding {
	t.Helper()
	for _, f := range out.Findings {
		if f.Layer == domain.LayerCompleteness && f.Code == "missing-translation" {
			return f
		}
	}
	t.Fatalf("findings = %+v, want the missing German translation among them", out.Findings)
	return domain.Finding{}
}

func TestCheckAppliesTheProjectsWaivers(t *testing.T) {
	_, w := pushed(t)

	var before checkJSON
	w.json(&before, "check").want(t, ExitCheckFailed)
	f := theFinding(t, before)
	if f.Severity != domain.Error || before.Errors != 1 || before.Waived != 0 {
		t.Fatalf("before the waiver: %q, %+v", f.Severity, before)
	}
	if !before.Waivers.Applied || before.Waivers.Waived != 0 {
		t.Fatalf("waivers = %+v, want them read and nothing waived yet", before.Waivers)
	}

	var made waiveJSON
	w.json(&made, "waive", f.Fingerprint, "--reason", "German ships next quarter; the gap is known").want(t, ExitOK)
	if made.Action != "created" {
		t.Fatalf("waive = %+v", made)
	}

	// The same run again, against the same catalogs: the finding is
	// still found and still reported, and it is waived.
	var after checkJSON
	w.json(&after, "check").want(t, ExitOK)
	got := theFinding(t, after)
	switch {
	case got.Fingerprint != f.Fingerprint:
		t.Fatalf("a different finding: %s, want %s", got.Fingerprint, f.Fingerprint)
	case got.Severity != domain.Waived:
		t.Fatalf("severity = %q, want the terminal to say what the pull request says", got.Severity)
	case got.Waiver != made.Waiver.ID:
		t.Errorf("waiver = %q, want the waiver the CLI created (%s)", got.Waiver, made.Waiver.ID)
	}
	// Counted on its own and never hidden: a number that goes down
	// without the product getting better is the failure mode of every
	// suppression system (RFC 0005 §14 decision 5).
	if after.Errors != 0 || after.Waived != 1 || !after.Passed {
		t.Errorf("after the waiver: %d errors, %d waived, passed %v", after.Errors, after.Waived, after.Passed)
	}
	if after.Waivers.Waived != 1 || after.Waivers.Live != 1 || !after.Waivers.Applied {
		t.Errorf("waivers = %+v", after.Waivers)
	}
	for _, l := range after.Locales {
		if l.Code == "de" && (l.Waived != 1 || l.Errors != 0) {
			t.Errorf("de = %+v, want the waived finding counted as waived there too", l)
		}
	}

	// And the human output says it, because a count that dropped with
	// no explanation is the thing nobody should have to guess about.
	r := w.run("check")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "1 finding waived by the project") {
		t.Errorf("output doesn't say what the waivers did:\n%s", r.stdout)
	}
}

// A revoked waiver accepts nothing. It is the cheapest proof that the
// command applies domain.Waivers and not a "there is a waiver for this
// fingerprint" test of its own: the two agree until a waiver is taken
// back, expires, is scoped to another branch, or was made against
// source that has since moved.
func TestCheckStopsWaivingWhenTheWaiverIsRevoked(t *testing.T) {
	_, w := pushed(t)
	var before checkJSON
	w.json(&before, "check").want(t, ExitCheckFailed)
	f := theFinding(t, before)

	var made waiveJSON
	w.json(&made, "waive", f.Fingerprint, "--reason", "for now").want(t, ExitOK)
	w.json(&checkJSON{}, "check").want(t, ExitOK)

	w.run("waive", "--revoke", made.Waiver.ID).want(t, ExitOK)
	var after checkJSON
	w.json(&after, "check").want(t, ExitCheckFailed)
	if got := theFinding(t, after); got.Severity != domain.Error || got.Waiver != "" {
		t.Errorf("after the revoke: %q with waiver %q, want an ordinary finding again", got.Severity, got.Waiver)
	}
	if after.Waivers.Waived != 0 || after.Waivers.Live != 0 {
		t.Errorf("waivers = %+v", after.Waivers)
	}
}

// Offline there is no waiver list to read, and the run says so.
//
// The direction of the error is the safe one — an unapplied waiver
// makes the check stricter, never laxer — but a run that reports an
// accepted finding as open without saying why sends somebody to look at
// a decision that was already made, with the reason for it out of
// reach.
func TestCheckSaysWhenItCannotReadTheWaivers(t *testing.T) {
	_, w := pushed(t)
	var before checkJSON
	w.json(&before, "check").want(t, ExitCheckFailed)
	w.run("waive", theFinding(t, before).Fingerprint, "--reason", "German ships next quarter").want(t, ExitOK)
	w.json(&checkJSON{}, "check").want(t, ExitOK)

	// Exit 4: offline the style layer has no effective style guide to
	// grade against either, and a run that lost a layer says so. What
	// this test is about is the waivers, and nothing failed the policy.
	var out checkJSON
	w.json(&out, "check", "--offline").want(t, ExitPartial)
	if !out.Passed {
		t.Errorf("an unread waiver list is not a failed check: %+v", out)
	}
	if out.Waivers.Applied || out.Waivers.Waived != 0 || out.Waivers.Live != 0 {
		t.Fatalf("waivers = %+v, want them unapplied offline", out.Waivers)
	}
	if !strings.Contains(out.Waivers.Why, "offline") {
		t.Errorf("why = %q, want it to say what is out of reach", out.Waivers.Why)
	}
	r := w.run("check", "--offline")
	r.want(t, ExitPartial)
	if !strings.Contains(r.stdout, "waivers were not applied") {
		t.Errorf("output is silent about the waivers it could not read:\n%s", r.stdout)
	}
}
