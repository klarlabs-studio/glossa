package app_test

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The layered report (RFC 0005 §13, wave 4): the pull request groups
// what it found by layer, annotates the findings that point at a line,
// shows the waived ones on their own, and says which policy version
// graded the run.

// waivedFinding is a finding a waiver has already accepted, as the
// server hands it to the check.
func waivedFinding(key, locale, code, message string) quality.Finding {
	f := quality.New(quality.Finding{
		Layer: quality.LayerTerminology, Code: code, Severity: quality.Warning,
		Locus: quality.Locus{Key: key, Locale: locale}, Message: message,
	})
	return f.Waive("w_1")
}

// TestTheSummaryGroupsFindingsByLayer: layer is what the policy selects
// on and what the terminal prints, so it is what the pull request
// groups by.
func TestTheSummaryGroupsFindingsByLayer(t *testing.T) {
	rep := app.BuildCheckReport(app.CheckInput{
		Status: app.BranchStatus{
			Name:     "feat/copy",
			NewKeys:  []string{"checkout.pay"},
			Invalid:  []app.InvalidMessage{{Key: "checkout.pay", Code: "invalid_content", Detail: "unmatched '{'"}},
			Outdated: map[string]int{},
		},
		Quality: app.BranchQuality{
			Locales: []string{"de"}, Untranslated: map[string]int{"de": 1},
			Findings: []quality.Finding{quality.New(quality.Finding{
				Layer: quality.LayerLength, Code: "max-length-exceeded", Severity: quality.Warning,
				Locus:   quality.Locus{Key: "checkout.pay", Locale: "de"},
				Message: "3 characters over max_length",
			})},
		},
		Usages: app.BranchUsages{Builds: 1},
	})
	want := []quality.LayerCount{
		{Layer: quality.LayerStructure, Counts: quality.Counts{Errors: 1}},
		{Layer: quality.LayerCompleteness, Counts: quality.Counts{Errors: 1}},
		{Layer: quality.LayerLength, Counts: quality.Counts{Warnings: 1}},
	}
	if len(rep.Layers) != len(want) {
		t.Fatalf("layers = %+v, want %+v", rep.Layers, want)
	}
	for i := range want {
		if rep.Layers[i] != want[i] {
			t.Fatalf("layers[%d] = %+v, want %+v", i, rep.Layers[i], want[i])
		}
	}
	for _, line := range []string{
		"**Findings by layer**",
		"| structure | 1 | 0 | 0 |",
		"| completeness | 1 | 0 | 0 |",
		"| length | 0 | 1 | 0 |",
		"| **Total** | **2** | **1** | **0** |",
		"**Structure** (1 error)",
		"**Completeness** (1 error)",
		"**Length** (1 warning)",
	} {
		if !strings.Contains(rep.Summary, line) {
			t.Fatalf("summary does not carry %q:\n%s", line, rep.Summary)
		}
	}
}

// TestTheLayerTableAddsUpToTheReportsCounts is RFC 0005 §12.3's
// arithmetic on this side: a breakdown that does not sum to the totals
// cannot be compared with the terminal's.
func TestTheLayerTableAddsUpToTheReportsCounts(t *testing.T) {
	rep := app.BuildCheckReport(app.CheckInput{
		Status: app.BranchStatus{Name: "feat/copy", NewKeys: []string{"a"}, Outdated: map[string]int{"de": 2}},
		Quality: app.BranchQuality{
			Locales: []string{"de", "fr"}, Untranslated: map[string]int{"de": 1, "fr": 1},
			Findings: []quality.Finding{
				waivedFinding("login.title", "de", "term_forbidden", `"Login" is the German term`),
			},
		},
		Usages: app.BranchUsages{Builds: 1, Unknown: []app.UnknownKey{{Key: "stray", File: "a.vue", Line: 3}}},
	})
	var sum quality.Counts
	for _, l := range rep.Layers {
		sum.Errors += l.Counts.Errors
		sum.Warnings += l.Counts.Warnings
		sum.Waived += l.Counts.Waived
	}
	if sum != (quality.Counts{Errors: rep.Errors, Warnings: rep.Warnings, Waived: rep.Waived}) {
		t.Fatalf("the layers sum to %+v, the report counts %d errors, %d warnings, %d waived",
			sum, rep.Errors, rep.Warnings, rep.Waived)
	}
}

// TestAWaivedFindingIsShownSeparatelyAndNeverCounted is RFC 0005 §2.3.
// It is still computed, still reported and still on the diff — at
// `notice`, so it cannot be mistaken for a live warning — and it can
// never fail the run or swell the error count.
func TestAWaivedFindingIsShownSeparatelyAndNeverCounted(t *testing.T) {
	f := waivedFinding("login.title", "de", "term_forbidden", `"Login" is the German term`)
	f.Locus.File, f.Locus.Line = "src/Login.vue", 8
	rep := app.BuildCheckReport(app.CheckInput{
		Policy:  checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Warning},
		Status:  app.BranchStatus{Name: "feat/copy", NewKeys: []string{"a"}, Outdated: map[string]int{}},
		Quality: app.BranchQuality{Locales: []string{"de"}, Findings: []quality.Finding{f}},
		Usages:  app.BranchUsages{Builds: 1},
	})
	if rep.Conclusion != app.ConclusionSuccess {
		t.Fatalf("conclusion = %q: a waiver cannot fail a run, even at fail_on: warning", rep.Conclusion)
	}
	if rep.Errors != 0 || rep.Warnings != 0 || rep.Waived != 1 {
		t.Fatalf("counts = %d errors, %d warnings, %d waived: a waived finding is counted on its own",
			rep.Errors, rep.Warnings, rep.Waived)
	}
	for _, line := range []string{
		"**Accepted by a waiver** (1)",
		"Still found, still reported, and not counted against this check.",
		"`login.title` _de_ — `term_forbidden`",
	} {
		if !strings.Contains(rep.Summary, line) {
			t.Fatalf("summary does not carry %q:\n%s", line, rep.Summary)
		}
	}
	if strings.Contains(rep.Summary, "**Terminology** (") {
		t.Fatalf("a waived finding was listed under its layer as if it were live:\n%s", rep.Summary)
	}
	if strings.Contains(rep.Summary, "Nothing to report") {
		t.Fatalf("the summary claims there is nothing to report while listing a waiver:\n%s", rep.Summary)
	}
	if len(rep.Annotations) != 1 {
		t.Fatalf("annotations = %+v, want the waived finding on its line", rep.Annotations)
	}
	if a := rep.Annotations[0]; a.Level != "notice" || !strings.HasPrefix(a.Message, "waived: ") {
		t.Fatalf("annotation = %+v, want a notice that says it was waived", a)
	}
}

// TestALayeredFindingIsNotAlsoRolledUp: the server's findings are the
// report's spine, and the check's own per-locale roll-up is the
// fallback for a read model that only counts. Saying both would count
// the same gap twice and put the pull request's numbers out of step
// with the terminal's.
func TestALayeredFindingIsNotAlsoRolledUp(t *testing.T) {
	perMessage := []quality.Finding{
		quality.New(quality.Finding{
			Layer: quality.LayerCompleteness, Code: checkpolicy.CodeMissingTranslation, Severity: quality.Error,
			Locus: quality.Locus{Key: "checkout.pay", Locale: "de"}, Message: "missing translation",
		}),
		quality.New(quality.Finding{
			Layer: quality.LayerCompleteness, Code: checkpolicy.CodeMissingTranslation, Severity: quality.Error,
			Locus: quality.Locus{Key: "checkout.total", Locale: "de"}, Message: "missing translation",
		}),
		quality.New(quality.Finding{
			Layer: quality.LayerStructure, Code: checkpolicy.CodeInvalidMessage, Severity: quality.Error,
			Locus: quality.Locus{Key: "checkout.pay", Locale: "en"}, Message: "invalid message: unmatched '{'",
		}),
	}
	rep := app.BuildCheckReport(app.CheckInput{
		Status: app.BranchStatus{
			Name: "feat/copy", NewKeys: []string{"checkout.pay", "checkout.total"},
			Invalid:  []app.InvalidMessage{{Key: "checkout.pay", Code: "invalid_content", Detail: "unmatched '{'"}},
			Outdated: map[string]int{},
		},
		Quality: app.BranchQuality{
			Locales: []string{"de"}, Untranslated: map[string]int{"de": 2}, Findings: perMessage,
		},
		Usages: app.BranchUsages{Builds: 1},
	})
	if rep.Errors != 3 {
		t.Fatalf("errors = %d, want 3: the roll-up repeated what the layers already said:\n%s",
			rep.Errors, rep.Summary)
	}
	if strings.Contains(rep.Summary, "untranslated in de") {
		t.Fatalf("the check rolled up a locale the layers report per message:\n%s", rep.Summary)
	}
	// The table still counts the gap, because it answers a different
	// question: how far the branch's new keys have got.
	if !strings.Contains(rep.Summary, "| de | 0 | 2 | 0 |") {
		t.Fatalf("the per-locale table lost the gap:\n%s", rep.Summary)
	}
}

// TestTheSummarySaysWhichPolicyVersionGradedTheRun is RFC 0005 §4.3: a
// check whose answer changed under someone has to say which document
// decided, and — when a grace pins an older one — that it did and when
// that ends.
func TestTheSummarySaysWhichPolicyVersionGradedTheRun(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	branch := app.CheckInput{
		Status:  app.BranchStatus{Name: "feat/copy", NewKeys: []string{"a"}, Outdated: map[string]int{}},
		Quality: app.BranchQuality{Locales: []string{"de"}, Untranslated: map[string]int{}},
		Usages:  app.BranchUsages{Builds: 1},
		Now:     now,
	}

	// No document, no line: a project that never wrote one is graded
	// exactly as it always was and reads nothing about the machinery.
	if s := app.BuildCheckReport(branch).Summary; strings.Contains(s, "check policy") {
		t.Fatalf("a versionless policy grew a note:\n%s", s)
	}

	current := app.BuildCheckReport(graded(branch, checkpolicy.Policy{Version: 7}, time.Time{}))
	if current.PolicyVersion != 7 || current.Pinned {
		t.Fatalf("report = v%d pinned=%v, want v7 unpinned", current.PolicyVersion, current.Pinned)
	}
	if !strings.Contains(current.Summary, "Graded against the project's check policy v7.") {
		t.Fatalf("summary does not name the version:\n%s", current.Summary)
	}

	// v8 ships with a fortnight's grace. A pull request opened before
	// the save keeps grading against v7 and says so; one opened after it
	// gets v8 at once.
	saved := now.Add(-24 * time.Hour)
	v8 := checkpolicy.Policy{}.Supersede(checkpolicy.Policy{Version: 7}, saved, checkpolicy.DefaultGrace)
	pinned := app.BuildCheckReport(graded(branch, v8, saved.Add(-time.Hour)))
	if pinned.PolicyVersion != 7 || !pinned.Pinned || pinned.CurrentVersion != 8 {
		t.Fatalf("report = v%d pinned=%v current=v%d, want v7 pinned with v8 current",
			pinned.PolicyVersion, pinned.Pinned, pinned.CurrentVersion)
	}
	for _, want := range []string{
		"Graded against the project's check policy v7",
		"the version this pull request was opened under",
		"v8 is the project's current policy and takes this pull request over on 2026-10-03",
	} {
		if !strings.Contains(pinned.Summary, want) {
			t.Fatalf("summary does not carry %q:\n%s", want, pinned.Summary)
		}
	}
	fresh := app.BuildCheckReport(graded(branch, v8, saved.Add(time.Hour)))
	if fresh.PolicyVersion != 8 || fresh.Pinned {
		t.Fatalf("report = v%d pinned=%v, want v8 for a pull request opened after the save",
			fresh.PolicyVersion, fresh.Pinned)
	}
}

// graded is in graded against policy, for a pull request opened at
// openedAt.
func graded(in app.CheckInput, policy checkpolicy.Policy, openedAt time.Time) app.CheckInput {
	in.Policy, in.OpenedAt = policy, openedAt
	return in
}
