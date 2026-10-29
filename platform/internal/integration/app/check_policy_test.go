package app_test

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The pull-request check under the policy document (RFC 0005 §4): the
// version it graded against, the grace that pins an old pull request,
// and the rules that decide a finding's severity.

var policySaved = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// terminologyFinding is one warning the layers produced, which a
// stricter policy raises.
func terminologyFinding() quality.Finding {
	return quality.New(quality.Finding{
		Layer: quality.LayerTerminology, Code: "term_forbidden", Severity: quality.Warning,
		Locus:   quality.Locus{Key: "legal.terms", Locale: "de", Namespace: "legal"},
		Message: "Anmeldung is not the approved term",
	})
}

func checkWith(p checkpolicy.Policy, opened, now time.Time) app.CheckReport {
	return app.BuildCheckReport(app.CheckInput{
		Policy:   p,
		Status:   app.BranchStatus{Name: "feat/terms", NewKeys: []string{"legal.terms"}, Outdated: map[string]int{}},
		Quality:  app.BranchQuality{Locales: []string{"de"}, Untranslated: map[string]int{}, Findings: []quality.Finding{terminologyFinding()}},
		Usages:   app.BranchUsages{Builds: 1},
		OpenedAt: opened, Now: now,
	})
}

// strictPolicy is version 7: terminology becomes an error, with the
// fortnight's grace §4.3 asks for.
func strictPolicy() checkpolicy.Policy {
	return checkpolicy.Policy{
		Rules: []checkpolicy.Rule{{
			Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error,
		}},
	}.Supersede(checkpolicy.Policy{Version: 6}, policySaved, checkpolicy.DefaultGrace)
}

func TestTheCheckSaysWhichPolicyVersionItUsed(t *testing.T) {
	rep := checkWith(strictPolicy(), policySaved.Add(time.Hour), policySaved.Add(2*time.Hour))
	if rep.PolicyVersion != 7 || rep.Pinned {
		t.Fatalf("version = %d, pinned = %v, want the current version 7", rep.PolicyVersion, rep.Pinned)
	}
	if !strings.Contains(rep.Summary, "check policy v7") {
		t.Errorf("summary does not name the policy version:\n%s", rep.Summary)
	}
	if rep.Conclusion != app.ConclusionFailure || rep.Errors != 1 {
		t.Errorf("conclusion = %q with %d errors, want the rule to bite", rep.Conclusion, rep.Errors)
	}
}

// TestTheGracePinsAnOlderPullRequest is §4.3: nobody wakes up to forty
// red pull requests.
func TestTheGracePinsAnOlderPullRequest(t *testing.T) {
	p := strictPolicy()
	opened := policySaved.Add(-48 * time.Hour)
	rep := checkWith(p, opened, policySaved.Add(time.Hour))
	if !rep.Pinned || rep.PolicyVersion != 6 {
		t.Fatalf("version = %d, pinned = %v, want the version this pull request was opened under",
			rep.PolicyVersion, rep.Pinned)
	}
	if rep.Conclusion == app.ConclusionFailure {
		t.Error("the pinned pull request failed on a rule it predates")
	}
	if !strings.Contains(rep.Summary, "check policy v6") ||
		!strings.Contains(rep.Summary, "opened under") ||
		!strings.Contains(rep.Summary, "2026-10-04") {
		t.Errorf("summary does not say which version it used and when that ends:\n%s", rep.Summary)
	}
	// Once the grace runs out, the same pull request grades against the
	// current version: nothing is pinned forever.
	after := checkWith(p, opened, policySaved.Add(checkpolicy.DefaultGrace+time.Hour))
	if after.Pinned || after.PolicyVersion != 7 || after.Conclusion != app.ConclusionFailure {
		t.Errorf("after the grace: version = %d, pinned = %v, conclusion = %q",
			after.PolicyVersion, after.Pinned, after.Conclusion)
	}
}

// TestAPolicyWithoutAVersionSaysNothingNew: a project that has not
// written a document is graded and reported exactly as it always was.
func TestAPolicyWithoutAVersionSaysNothingNew(t *testing.T) {
	rep := checkWith(checkpolicy.Policy{}, time.Time{}, policySaved)
	if rep.PolicyVersion != 0 || rep.Pinned {
		t.Fatalf("version = %d, pinned = %v, want a policy with no version", rep.PolicyVersion, rep.Pinned)
	}
	if strings.Contains(rep.Summary, "check policy v") {
		t.Errorf("summary grew a line about machinery the project does not use:\n%s", rep.Summary)
	}
	if rep.Conclusion == app.ConclusionFailure {
		t.Error("a warning failed the check under the default policy")
	}
}

func TestTheCheckDropsAFindingThePolicySwitchedOff(t *testing.T) {
	p := checkpolicy.Policy{Version: 3, FailOn: checkpolicy.Warning, Rules: []checkpolicy.Rule{{
		Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, Severity: checkpolicy.Off,
	}}}
	rep := checkWith(p, time.Time{}, policySaved)
	for _, f := range rep.Findings {
		if f.Layer == quality.LayerTerminology {
			t.Errorf("finding %q reported, want a layer the policy does not compute to be absent", f.Code)
		}
	}
	if rep.Conclusion == app.ConclusionFailure {
		t.Error("the check failed on a finding it was told not to compute")
	}
}
