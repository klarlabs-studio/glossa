package cli

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	integration "go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// One policy, two readers (RFC 0005 §4.2).
//
// `glossa check` and the Glossa pull-request check must reach the same
// verdict for the same commit and the same policy — that is the one
// property checkpolicy was created to protect, and the policy document
// is the change most likely to break it, because it is the first time
// the two readers grade findings through anything but a severity rank.
//
// This is the only test in the tree that has both readers in scope. It
// hands them the same findings and the same document and holds them to
// the same answer, and then holds the pull-request check to ignoring
// what a developer typed locally.

// readerFindings are what both readers are given: a terminology finding
// in the legal namespace, one outside it, and a model's opinion.
func readerFindings() []quality.Finding {
	return []quality.Finding{
		quality.New(quality.Finding{
			Layer: quality.LayerTerminology, Code: "term_forbidden", Severity: quality.Warning,
			Locus:   quality.Locus{Key: "legal.terms", Locale: "de", Namespace: "legal"},
			Message: "Anmeldung is not the approved term",
		}),
		quality.New(quality.Finding{
			Layer: quality.LayerTerminology, Code: "term_missing", Severity: quality.Warning,
			Locus:   quality.Locus{Key: "checkout.pay", Locale: "de", Namespace: "checkout"},
			Message: "the approved term for Payment is missing",
		}),
		quality.New(quality.Finding{
			Layer: quality.LayerLinguistic, Code: "tone-mismatch", Severity: quality.Warning,
			Locus:   quality.Locus{Key: "checkout.pay", Locale: "de", Namespace: "checkout"},
			Message: "the tone is more formal than the style guide asks for",
		}),
	}
}

// localVerdict is `glossa check`'s: the layers run over the local
// snapshot and the report is graded by the policy.
func localVerdict(t *testing.T, p checkpolicy.Policy) bool {
	t.Helper()
	s := &snapshot.Snapshot{
		Origin: "local", SourceLocale: "en",
		Locales:      []snapshot.Locale{{Code: "en", IsSource: true}, {Code: "de"}},
		Translations: map[string]map[string]snapshot.Translation{},
	}
	// One checker per layer, as a real run has them: a layer the policy
	// switched off is a checker that never runs, and it may only take
	// its own findings with it.
	var checkers []qa.Checker
	for _, layer := range []quality.Layer{quality.LayerTerminology, quality.LayerLinguistic} {
		var fs []quality.Finding
		for _, f := range readerFindings() {
			if f.Layer == layer {
				fs = append(fs, f)
			}
		}
		checkers = append(checkers, qa.Precomputed(layer, fs))
	}
	return qa.Run(s, p, checkers...).Passed()
}

// prVerdict is the pull-request check's, from the same findings and the
// same document.
func prVerdict(t *testing.T, p checkpolicy.Policy) bool {
	t.Helper()
	r := integration.BuildCheckReport(integration.CheckInput{
		Policy: p,
		Status: integration.BranchStatus{Name: "feature/pay", Outdated: map[string]int{}},
		Quality: integration.BranchQuality{
			Locales: []string{"de"}, Untranslated: map[string]int{}, Findings: readerFindings(),
		},
	})
	return r.Conclusion != string(quality.ConclusionFailure)
}

func TestBothReadersReachTheSameVerdict(t *testing.T) {
	strict := checkpolicy.Rule{
		Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, Severity: checkpolicy.Error,
	}
	tests := []struct {
		name   string
		policy checkpolicy.Policy
		want   bool
	}{
		{name: "the default policy passes on warnings", policy: checkpolicy.Policy{}, want: true},
		{
			name:   "fail_on: warning fails on them",
			policy: checkpolicy.Policy{FailOn: checkpolicy.Warning},
		},
		{
			name:   "a rule raising the legal namespace fails",
			policy: checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{strict}},
		},
		{
			name: "the same rule in warn mode does not",
			policy: checkpolicy.Policy{Version: 8, Rules: []checkpolicy.Rule{{
				Selector: strict.Selector, Severity: checkpolicy.Error, Mode: checkpolicy.ModeWarn,
			}}},
			want: true,
		},
		{
			name: "a rule switching a layer off leaves it uncomputed on both surfaces",
			policy: checkpolicy.Policy{
				Version: 9, FailOn: checkpolicy.Warning,
				Rules: []checkpolicy.Rule{
					{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Off},
					{Selector: checkpolicy.Selector{Layer: "linguistic"}, Severity: checkpolicy.Off},
				},
			},
			want: true,
		},
		{
			name: "switching one layer off still leaves the other to fail on",
			policy: checkpolicy.Policy{
				Version: 9, FailOn: checkpolicy.Warning,
				Rules: []checkpolicy.Rule{{
					Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Off,
				}},
			},
		},
		{
			name: "a rule for another locale changes nothing",
			policy: checkpolicy.Policy{Version: 10, Rules: []checkpolicy.Rule{{
				Selector: checkpolicy.Selector{Layer: "terminology", Locale: "fr"}, Severity: checkpolicy.Error,
			}}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local, pr := localVerdict(t, tc.policy), prVerdict(t, tc.policy)
			if local != pr {
				t.Fatalf("`glossa check` passed = %v, the pull-request check passed = %v:"+
					" the terminal and the pull request disagree", local, pr)
			}
			if local != tc.want {
				t.Errorf("passed = %v, want %v", local, tc.want)
			}
		})
	}
}

// TestThePullRequestCheckIgnoresLocalOverrides is RFC 0005 §14
// decision 3: a developer can tighten or loosen their own loop and
// cannot change what CI decides.
func TestThePullRequestCheckIgnoresLocalOverrides(t *testing.T) {
	server := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Warning}
	if localVerdict(t, server) || prVerdict(t, server) {
		t.Fatal("both readers should fail on the server's policy before any override")
	}
	// `glossa check --fail-on=never`, or a `check:` block that says so.
	local := server.Override(checkpolicy.Overrides{FailOn: checkpolicy.Never})
	if !localVerdict(t, local) {
		t.Error("the local run still failed, want the override honoured for this run")
	}
	// The pull request grades against what the project stores, which the
	// override never reached.
	if prVerdict(t, server) {
		t.Error("the pull-request check passed, want a local flag to have no reach into CI")
	}
	if !server.FailsDecision(server.Decide(checkpolicy.Target{
		Layer: "terminology", Code: "term_forbidden", Severity: checkpolicy.Warning,
	})) {
		t.Error("the stored document changed under an override")
	}
}

// TestNeitherReaderFailsOnAModelsOpinion is §14 decision 10, on both
// surfaces: whatever the policy says, the advisory layer only warns.
func TestNeitherReaderFailsOnAModelsOpinion(t *testing.T) {
	p := checkpolicy.Policy{
		Version: 11, FailOn: checkpolicy.Warning,
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Off},
			{Selector: checkpolicy.Selector{}, Severity: checkpolicy.Error},
		},
	}
	// Only the linguistic finding is left, and it can never be an error,
	// so fail_on: warning is what decides — not the wildcard rule.
	r := integration.BuildCheckReport(integration.CheckInput{
		Policy:  p,
		Status:  integration.BranchStatus{Name: "feature/pay", Outdated: map[string]int{}},
		Quality: integration.BranchQuality{Locales: []string{"de"}, Untranslated: map[string]int{}, Findings: readerFindings()},
	})
	if r.Errors != 0 {
		t.Errorf("errors = %d, want a model's opinion never raised to error", r.Errors)
	}
	for _, f := range r.Findings {
		if f.Layer == quality.LayerLinguistic && f.Severity != quality.Warning {
			t.Errorf("linguistic finding = %q, want a warning", f.Severity)
		}
	}
	if localVerdict(t, p) != (r.Conclusion != string(quality.ConclusionFailure)) {
		t.Error("the two readers disagree about a policy that raises everything")
	}
}
