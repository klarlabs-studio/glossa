package app_test

import (
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func warning(layer domain.Layer, code, locale, ns string) domain.Finding {
	return domain.New(domain.Finding{
		Layer: layer, Code: code, Severity: domain.Warning,
		Locus:   domain.Locus{Key: "checkout.pay", Locale: locale, Namespace: ns},
		Message: code,
	})
}

func previewRuns() []app.PreviewRun {
	return []app.PreviewRun{
		{
			Ref: "feature/pay", PullRequest: 41, Open: true,
			Findings: []domain.Finding{warning(domain.LayerTerminology, "term_forbidden", "de", "legal")},
		},
		{
			Ref: "feature/cart", PullRequest: 42, Open: true,
			Findings: []domain.Finding{warning(domain.LayerTerminology, "term_missing", "fr", "checkout")},
		},
		{
			Ref: "feature/old", PullRequest: 7, Open: false,
			Findings: []domain.Finding{warning(domain.LayerTerminology, "term_forbidden", "de", "legal")},
		},
		{
			Ref:      "main",
			Findings: []domain.Finding{warning(domain.LayerSource, "manual-plural", "en", "checkout")},
		},
	}
}

// TestPreviewPolicy is the dry run of RFC 0005 §4.3: what a stricter
// policy would do, and how many open pull requests it would turn red.
func TestPreviewPolicy(t *testing.T) {
	current := checkpolicy.Policy{Version: 6}
	candidate := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error},
	}}
	got := app.PreviewPolicy(current, candidate, previewRuns())
	if got.Runs != 4 || got.Targets != 4 {
		t.Errorf("runs = %d, targets = %d, want 4 and 4", got.Runs, got.Targets)
	}
	if got.Raised != 3 || got.NewlyFailing != 3 {
		t.Errorf("raised = %d, newly failing findings = %d, want 3 and 3", got.Raised, got.NewlyFailing)
	}
	want := []string{"feature/cart", "feature/old", "feature/pay"}
	if !slices.Equal(got.NewlyFailingRefs, want) {
		t.Errorf("newly failing refs = %v, want %v", got.NewlyFailingRefs, want)
	}
	if got.OpenPullRequests != 2 {
		t.Errorf("open pull requests = %d, want the two that are still open", got.OpenPullRequests)
	}
	// And it names them, not only counts them: §12.4 asks the preview
	// to name the pull request that would newly fail. The closed one is
	// not among them — nobody wakes up to it.
	wantPRs := []app.PullRequestImpact{{Ref: "feature/cart", Number: 42}, {Ref: "feature/pay", Number: 41}}
	if !slices.Equal(got.PullRequests, wantPRs) {
		t.Errorf("pull requests = %+v, want %+v", got.PullRequests, wantPRs)
	}
	if len(got.PullRequests) != got.OpenPullRequests {
		t.Errorf("%d pull requests named and %d counted: the two must agree", len(got.PullRequests), got.OpenPullRequests)
	}
	if len(got.Rules) != 1 || got.Rules[0].Matched != 3 {
		t.Errorf("rule impact = %+v, want the rule to report what it matched", got.Rules)
	}
}

// TestPreviewOfAWarnRollout: the same rule shipped in warn mode changes
// every severity and breaks nobody, which is what makes it the on-ramp.
func TestPreviewOfAWarnRollout(t *testing.T) {
	current := checkpolicy.Policy{Version: 6}
	candidate := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{{
		Selector: checkpolicy.Selector{Layer: "terminology"},
		Severity: checkpolicy.Error, Mode: checkpolicy.ModeWarn,
	}}}
	got := app.PreviewPolicy(current, candidate, previewRuns())
	if got.Raised != 3 {
		t.Errorf("raised = %d, want the severities still reported", got.Raised)
	}
	if len(got.NewlyFailingRefs) != 0 || got.OpenPullRequests != 0 || len(got.PullRequests) != 0 {
		t.Errorf("newly failing = %v (%d open, %v), want nobody woken up",
			got.NewlyFailingRefs, got.OpenPullRequests, got.PullRequests)
	}
}

func TestPreviewOfALooserPolicy(t *testing.T) {
	current := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Warning}
	candidate := checkpolicy.Policy{Version: 8, FailOn: checkpolicy.Warning, Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Off},
	}}
	got := app.PreviewPolicy(current, candidate, previewRuns())
	if got.Silenced != 3 {
		t.Errorf("silenced = %d, want the three terminology findings", got.Silenced)
	}
	want := []string{"feature/cart", "feature/old", "feature/pay"}
	if !slices.Equal(got.NoLongerFailingRefs, want) {
		t.Errorf("no longer failing = %v, want %v", got.NoLongerFailingRefs, want)
	}
}

// TestPreviewStoresNothing: a dry run is a read.
func TestPreviewStoresNothing(t *testing.T) {
	current := checkpolicy.Policy{Version: 6}
	candidate := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error},
	}}
	runs := previewRuns()
	app.PreviewPolicy(current, candidate, runs)
	for _, r := range runs {
		for _, f := range r.Findings {
			if f.Severity != domain.Warning {
				t.Errorf("%s: finding %q = %q, want the stored findings untouched", r.Ref, f.Code, f.Severity)
			}
		}
	}
	if len(current.Rules) != 0 || candidate.Version != 7 {
		t.Error("a document changed under the preview")
	}
}
