package cli

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa waive` (RFC 0005 §2.3, §13 wave 6).

// The rule the whole mechanism rests on: a waiver without a reason is
// refused, and refused *here*, with a sentence saying why rather than a
// field name from a 400.
func TestWaiveRefusesAWaiverWithNoReason(t *testing.T) {
	srv := newFakeServer(t)
	f := termFinding("checkout.login", "de", "Login")
	storedFindings(srv, f)
	w := newWorkspace(t).withProject(srv, nil)

	for _, args := range [][]string{
		{"waive", f.Fingerprint},
		{"waive", f.Fingerprint, "--reason", "   "},
	} {
		var out errorDoc
		w.json(&out, args...).want(t, ExitUsage)
		if out.Error.Code != "waiver_needs_a_reason" {
			t.Fatalf("%v: error = %+v", args, out.Error)
		}
		if !strings.Contains(out.Error.Why, "justify") {
			t.Errorf("%v: why = %q, want it to say what a reason is for", args, out.Error.Why)
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.waivers) != 0 {
		t.Errorf("a waiver was created anyway: %d", len(srv.qa.waivers))
	}
}

func TestWaiveAcceptsAFindingByFingerprint(t *testing.T) {
	srv := newFakeServer(t)
	f := termFinding("checkout.login", "de", "Login")
	storedFindings(srv, f)
	w := newWorkspace(t).withProject(srv, nil)

	var out waiveJSON
	w.json(&out, "waive", f.Fingerprint, "--reason", `"Login" is the German term`).want(t, ExitOK)
	if out.Schema != waiveSchema || out.Action != "created" {
		t.Fatalf("waive = %+v", out)
	}
	if out.Waiver.Fingerprint != f.Fingerprint || out.Waiver.Reason != `"Login" is the German term` {
		t.Fatalf("waiver = %+v", out.Waiver)
	}
	if out.Waiver.Scope != string(domain.WaiverProject) {
		t.Errorf("scope = %q, want project by default", out.Waiver.Scope)
	}
	// It is made against the revision the finding carries, so the waiver
	// dies when the source moves past it.
	if out.Waiver.SourceRevision != 3 {
		t.Errorf("source_revision = %d, want the finding's 3", out.Waiver.SourceRevision)
	}
	// The same waiver again is the same waiver, not a second one.
	var again waiveJSON
	w.json(&again, "waive", f.Fingerprint, "--reason", "again").want(t, ExitOK)
	if again.Action != "unchanged" || again.Waiver.ID != out.Waiver.ID {
		t.Errorf("second waive = %+v", again)
	}
}

// The property the command depends on, proved rather than assumed
// (RFC 0005 §2.1): the fingerprint `glossa check` computes for a
// finding is the one the server stored for the same finding, so a
// waiver created from the terminal matches it server-side.
//
// Nothing here copies a fingerprint from the server. `glossa check
// --terminology` computes one from its own run; the fake stores a
// finding it sealed itself, from the catalog message's ID, locale,
// layer, code and subject. The waiver is created with the CLI's number
// and has to reach the server's finding.
func TestAWaiverFromTheCLIMatchesTheServersFinding(t *testing.T) {
	srv, w := seeded(t)
	termbase(t, w)
	w.write("locales/de.json", `{"cart.checkout": "Zum Einkaufswagen", "cart.items": "{count, plural, one {# Artikel} other {# Artikel}}", "checkout.pay": "Zahle {amount, number}"}`)
	w.run("push", "--translations").want(t, ExitOK)

	// What the CLI computes, from its own run.
	var checked checkJSON
	w.json(&checked, "check", "--terminology").want(t, ExitCheckFailed)
	var fingerprint string
	for _, f := range checked.Findings {
		if f.Layer == domain.LayerTerminology && f.Code == "term_forbidden" {
			fingerprint = f.Fingerprint
		}
	}
	if fingerprint == "" {
		t.Fatalf("`glossa check --terminology` found no forbidden term: %+v", checked.Findings)
	}

	// What the server stores, sealed independently from the catalog
	// message's ID, the locale, the layer, the code and the subject.
	stored := domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: domain.Error,
		Locus:   domain.Locus{Message: "msg_cart.checkout", Key: "cart.checkout", Locale: "de"},
		Message: `"Einkaufswagen" is forbidden`, Subject: "Einkaufswagen",
	})
	storedFindings(srv, stored)
	if stored.Fingerprint != fingerprint {
		t.Fatalf("the CLI computed %q and the server stored %q: one finding, two identities",
			fingerprint, stored.Fingerprint)
	}

	// And the waiver, created with the CLI's number, reaches it.
	w.run("waive", fingerprint, "--reason", "Einkaufswagen is the legal term in this flow").want(t, ExitOK)

	var after findingsJSON
	w.json(&after, "findings").want(t, ExitOK)
	if len(after.Findings) != 1 {
		t.Fatalf("findings = %+v", after.Findings)
	}
	got := after.Findings[0]
	if got.Severity != domain.Waived || got.Waiver == "" {
		t.Fatalf("the server's finding is %q with waiver %q, want waived by the CLI's waiver", got.Severity, got.Waiver)
	}
	// It is still reported and counted on its own, never hidden: a
	// number that goes down without the product getting better is the
	// failure mode of every suppression system.
	if after.Counts.Errors != 0 || after.Counts.Waived != 1 {
		t.Errorf("counts = %+v, want the finding counted as waived and not as an error", after.Counts)
	}
}

func TestWaiveRefusesSomethingThatIsNotAFingerprint(t *testing.T) {
	srv := newFakeServer(t)
	storedFindings(srv)
	w := newWorkspace(t).withProject(srv, nil)

	var out errorDoc
	w.json(&out, "waive", "checkout.login", "--reason", "it's fine").want(t, ExitUsage)
	if !strings.Contains(out.Error.Message, "fingerprint") {
		t.Errorf("error = %+v", out.Error)
	}
	// And no fingerprint at all says where to get one.
	w.json(&out, "waive", "--reason", "it's fine").want(t, ExitUsage)
	if !strings.Contains(out.Error.Message, "glossa findings") {
		t.Errorf("error = %+v, want it to name the command that prints fingerprints", out.Error)
	}
}

func TestWaiveTakesAScopeAnExpiryAndARevision(t *testing.T) {
	srv := newFakeServer(t)
	f := termFinding("checkout.login", "de", "Login")
	storedFindings(srv, f)
	w := newWorkspace(t).withProject(srv, nil)
	w.env["GITHUB_HEAD_REF"] = "feature/copy"

	var out waiveJSON
	w.json(&out, "waive", f.Fingerprint, "--reason", "for this branch only",
		"--scope", "branch", "--expires", "2099-12-31", "--source-revision", "9").want(t, ExitOK)
	if out.Waiver.Scope != "branch" || out.Waiver.Ref != "feature/copy" {
		t.Fatalf("waiver = %+v, want the CI branch when --ref is not given", out.Waiver)
	}
	if out.Waiver.SourceRevision != 9 {
		t.Errorf("source_revision = %d, want the one named", out.Waiver.SourceRevision)
	}
	// A bare date is through the end of that day: a waiver that says
	// "expires 2099-12-31" covers the 31st.
	at, err := time.Parse(time.RFC3339, out.Waiver.ExpiresAt)
	if err != nil || at.Format(time.DateOnly) != "2099-12-31" || at.Hour() != 23 {
		t.Errorf("expires_at = %q (%v)", out.Waiver.ExpiresAt, err)
	}

	var e errorDoc
	w.json(&e, "waive", f.Fingerprint, "--reason", "x", "--expires", "soon").want(t, ExitUsage)
	if !strings.Contains(e.Error.Message, "--expires") {
		t.Errorf("error = %+v", e.Error)
	}
	w.json(&e, "waive", f.Fingerprint, "--reason", "x", "--scope", "everywhere").want(t, ExitUsage)
	if !strings.Contains(e.Error.Message, "--scope") {
		t.Errorf("error = %+v", e.Error)
	}
}

func TestWaiveListsAndRevokes(t *testing.T) {
	srv := newFakeServer(t)
	f := termFinding("checkout.login", "de", "Login")
	storedFindings(srv, f)
	w := newWorkspace(t).withProject(srv, nil)
	w.run("waive", f.Fingerprint, "--reason", "agreed with marketing").want(t, ExitOK)

	var list waiversJSON
	w.json(&list, "waive", "--list").want(t, ExitOK)
	if list.Schema != waiversSchema || len(list.Waivers) != 1 {
		t.Fatalf("list = %+v", list)
	}
	got := list.Waivers[0]
	if !got.Active || got.Accepts == nil || got.Accepts.Layer != "terminology" || got.Accepts.Key != "checkout.login" {
		t.Fatalf("waiver = %+v, want it to say what it accepts", got)
	}
	// The human listing names the reason: an unexamined waiver is
	// technical debt with a reason attached, and the reason is the part
	// that makes it examinable.
	h := w.run("waive", "--list")
	if !strings.Contains(h.stdout, "agreed with marketing") {
		t.Errorf("human list:\n%s", h.stdout)
	}

	var revoked waiveJSON
	w.json(&revoked, "waive", "--revoke", got.ID).want(t, ExitOK)
	if revoked.Action != "revoked" {
		t.Fatalf("revoke = %+v", revoked)
	}
	// And the finding it accepted is an ordinary finding again.
	var after findingsJSON
	w.json(&after, "findings").want(t, ExitOK)
	if after.Findings[0].Severity != domain.Error {
		t.Errorf("severity = %q, want the finding back at its own severity", after.Findings[0].Severity)
	}

	var e errorDoc
	w.json(&e, "waive", "--revoke", "wv_nope").want(t, ExitNetwork)
	if e.Error.Code != "waiver_not_found" || e.Error.Fix == "" {
		t.Errorf("unknown waiver = %+v", e.Error)
	}
	w.run("waive", "--list", "--revoke", "wv_1").want(t, ExitUsage)
}
