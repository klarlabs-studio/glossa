package cli

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa findings` (RFC 0005 §13 wave 6).

// storedFindings gives the fake server a check run and the findings of
// it, sealed the way a layer seals them: through domain.New, so every
// fingerprint in these tests is the one the real code computes.
func storedFindings(f *fakeServer, findings ...domain.Finding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qa.run = map[string]any{
		"id": "run_1", "ref": "main", "commit": "abc1234def5678", "trigger": "pull_request",
		"conclusion": "failure", "policy_version": 7, "created_by": "token:1",
		"layers":     []string{"structure", "parity", "completeness", "terminology"},
		"started_at": time.Now().UTC().Format(time.RFC3339),
		"counts":     map[string]int{"errors": 0, "warnings": 0, "waived": 0},
	}
	f.qa.findings = findings
}

func termFinding(key, locale, term string) domain.Finding {
	rev := 3
	return domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: domain.Error,
		Locus: domain.Locus{
			Message: "msg_" + key, Key: key, Locale: locale, Namespace: "checkout",
			File: "src/checkout/Pay.vue", Line: 42,
			Span: &domain.Span{Side: domain.SideTarget, Start: 0, End: len(term)},
		},
		Message: term + " is forbidden", Subject: term, SourceRevision: &rev,
		Fix: &domain.Fix{Kind: domain.FixUseTerm, Hint: "Anmelden"},
	})
}

func lengthFinding(key, locale string) domain.Finding {
	to := 28
	return domain.New(domain.Finding{
		Layer: domain.LayerLength, Code: "expansion-excessive", Severity: domain.Warning,
		Locus:   domain.Locus{Message: "msg_" + key, Key: key, Locale: locale},
		Message: "74 % longer than the source", Subject: "",
		Fix: &domain.Fix{Kind: domain.FixShorten, To: &to, Hint: "Payer maintenant"},
	})
}

func TestFindingsListsTheStoredFindingsWithTheirRun(t *testing.T) {
	srv := newFakeServer(t)
	storedFindings(srv, termFinding("checkout.login", "de", "Login"), lengthFinding("checkout.pay", "fr"))
	w := newWorkspace(t).withProject(srv, nil)

	var out findingsJSON
	w.json(&out, "findings").want(t, ExitOK)

	if out.Schema != findingsSchema {
		t.Errorf("schema = %q", out.Schema)
	}
	if len(out.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(out.Findings))
	}
	// The run the findings came from is named: "no errors" from a run of
	// three layers is a different sentence from the same words about a
	// run of nine.
	if out.Run == nil || out.Run.Ref != "main" || out.Run.PolicyVersion != 7 {
		t.Fatalf("run = %+v", out.Run)
	}
	if len(out.Run.Layers) != 4 {
		t.Errorf("run layers = %v", out.Run.Layers)
	}
	// The finding kept everything the shape carries — the span and the
	// fix included, which every pre-M4 conversion dropped.
	got := out.Findings[0]
	if got.Fingerprint != termFinding("checkout.login", "de", "Login").Fingerprint {
		t.Errorf("fingerprint = %q, want the one the layer computes", got.Fingerprint)
	}
	if got.Locus.Span == nil || got.Locus.Span.Side != domain.SideTarget {
		t.Errorf("span = %+v, want the target span", got.Locus.Span)
	}
	if got.Fix == nil || got.Fix.Kind != domain.FixUseTerm || got.Fix.Hint != "Anmelden" {
		t.Errorf("fix = %+v", got.Fix)
	}
	if got.Locus.File != "src/checkout/Pay.vue" || got.Locus.Line != 42 {
		t.Errorf("locus = %+v, want Context's file and line", got.Locus)
	}
}

// The human output prints the fingerprint, because it is the argument
// `glossa waive` takes: a finding you cannot address is a finding you
// cannot accept.
func TestFindingsPrintsTheFingerprintToWaiveBy(t *testing.T) {
	srv := newFakeServer(t)
	f := termFinding("checkout.login", "de", "Login")
	storedFindings(srv, f)
	w := newWorkspace(t).withProject(srv, nil)

	r := w.run("findings")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, f.Fingerprint) {
		t.Errorf("the output doesn't print the fingerprint %q:\n%s", f.Fingerprint, r.stdout)
	}
	if !strings.Contains(r.stdout, "term_forbidden") || !strings.Contains(r.stdout, "de checkout.login") {
		t.Errorf("the output doesn't name the finding:\n%s", r.stdout)
	}
}

func TestFindingsFiltersAreTheOnesTheContractDocuments(t *testing.T) {
	srv := newFakeServer(t)
	storedFindings(srv, termFinding("checkout.login", "de", "Login"), lengthFinding("checkout.pay", "fr"))
	w := newWorkspace(t).withProject(srv, nil)

	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"by layer", []string{"--layer", "length"}, 1},
		{"by severity", []string{"--severity", "error"}, 1},
		{"by code", []string{"--code", "term_forbidden"}, 1},
		{"by locale", []string{"--locale", "fr"}, 1},
		{"by namespace", []string{"--namespace", "checkout"}, 1},
		{"by message key", []string{"--message", "checkout.pay"}, 1},
		{"no match", []string{"--locale", "ja"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out findingsJSON
			w.json(&out, append([]string{"findings"}, tc.args...)...).want(t, ExitOK)
			if len(out.Findings) != tc.want {
				t.Errorf("findings = %d, want %d", len(out.Findings), tc.want)
			}
		})
	}
}

// The counts are the run's, whatever the filters selected: a filtered
// view must not be able to say the project is clean.
func TestFindingsCountsTheWholeRunWhateverTheFilterSelects(t *testing.T) {
	srv := newFakeServer(t)
	storedFindings(srv, termFinding("checkout.login", "de", "Login"), lengthFinding("checkout.pay", "fr"))
	w := newWorkspace(t).withProject(srv, nil)

	var out findingsJSON
	w.json(&out, "findings", "--layer", "length").want(t, ExitOK)
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %d, want the filtered one", len(out.Findings))
	}
	if out.Counts.Errors != 1 || out.Counts.Warnings != 1 {
		t.Errorf("counts = %+v, want the whole run's 1 error and 1 warning", out.Counts)
	}
}

func TestFindingsRefusesALayerItCantSpell(t *testing.T) {
	srv := newFakeServer(t)
	storedFindings(srv)
	w := newWorkspace(t).withProject(srv, nil)

	var out errorDoc
	w.json(&out, "findings", "--layer", "spelling").want(t, ExitUsage)
	if !strings.Contains(out.Error.Message, "spelling") || !strings.Contains(out.Error.Message, "terminology") {
		t.Errorf("error = %+v, want the name and the list of layers", out.Error)
	}
}

// A project nothing has checked is not a project with no findings, and
// the document says so: run is null rather than a run with zero counts.
func TestFindingsSaysWhenNothingHasBeenCheckedYet(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)

	var out findingsJSON
	w.json(&out, "findings").want(t, ExitOK)
	if out.Run != nil {
		t.Errorf("run = %+v, want null", out.Run)
	}
	r := w.run("findings")
	if !strings.Contains(r.stdout, "nothing has been checked yet") {
		t.Errorf("the output doesn't say so:\n%s", r.stdout)
	}
}
