package checkpolicy_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// findings stands in for what a project has already found: two
// terminology warnings, one of them in the legal namespace, a source
// warning and a parity error.
func findings() []checkpolicy.Target {
	return []checkpolicy.Target{
		{Layer: "terminology", Code: "term_forbidden", Locale: "de", Namespace: "legal", Severity: checkpolicy.Warning},
		{Layer: "terminology", Code: "term_missing", Locale: "fr", Namespace: "checkout", Severity: checkpolicy.Warning},
		{Layer: "source", Code: "manual-plural", Locale: "en", Namespace: "checkout", Severity: checkpolicy.Warning},
		{Layer: "parity", Code: "missing-argument", Locale: "de", Namespace: "checkout", Severity: checkpolicy.Error},
		{Layer: "linguistic", Code: "tone-mismatch", Locale: "de", Namespace: "legal", Severity: checkpolicy.Warning},
	}
}

func TestImpact(t *testing.T) {
	current := checkpolicy.Policy{Version: 6}
	candidate := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{
		rule(checkpolicy.Selector{Layer: "terminology"}, checkpolicy.Error, ""),
		rule(checkpolicy.Selector{Layer: "source"}, checkpolicy.Off, ""),
		rule(checkpolicy.Selector{Layer: "style"}, checkpolicy.Error, ""),
	}}
	got := checkpolicy.Impact(current, candidate, findings())
	if got.Targets != 5 {
		t.Errorf("targets = %d, want 5", got.Targets)
	}
	if got.Raised != 2 || got.Silenced != 1 || got.Lowered != 0 {
		t.Errorf("raised/lowered/silenced = %d/%d/%d, want 2/0/1", got.Raised, got.Lowered, got.Silenced)
	}
	if got.NewlyFailing != 2 || got.NoLongerFailing != 0 {
		t.Errorf("newly failing = %d, no longer failing = %d, want 2 and 0",
			got.NewlyFailing, got.NoLongerFailing)
	}
	if len(got.Rules) != 3 {
		t.Fatalf("rules = %d, want one entry per rule of the candidate", len(got.Rules))
	}
	if got.Rules[0].Matched != 2 || got.Rules[0].Changed != 2 || got.Rules[0].NewlyFailing != 2 {
		t.Errorf("terminology rule = %+v, want two matched, changed and newly failing", got.Rules[0])
	}
	if got.Rules[1].Matched != 1 || got.Rules[1].NewlyFailing != 0 {
		t.Errorf("source rule = %+v, want one matched and none newly failing", got.Rules[1])
	}
	// A rule that changes nothing is exactly what a reader wants to see
	// before saving.
	if got.Rules[2].Matched != 0 || got.Rules[2].Changed != 0 {
		t.Errorf("style rule = %+v, want a rule nothing matched reported as such", got.Rules[2])
	}
}

// TestImpactOfWarnModeIsTheWholePoint: shipping the same rule in warn
// mode changes every severity and fails nothing.
func TestImpactOfWarnMode(t *testing.T) {
	current := checkpolicy.Policy{Version: 6}
	enforcing := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{
		rule(checkpolicy.Selector{Layer: "terminology"}, checkpolicy.Error, ""),
	}}
	warning := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{
		rule(checkpolicy.Selector{Layer: "terminology"}, checkpolicy.Error, checkpolicy.ModeWarn),
	}}
	strictImpact := checkpolicy.Impact(current, enforcing, findings())
	warnImpact := checkpolicy.Impact(current, warning, findings())
	if strictImpact.NewlyFailing == 0 {
		t.Fatal("the enforcing candidate fails nothing new, want the preview to show the risk")
	}
	if warnImpact.NewlyFailing != 0 {
		t.Errorf("newly failing under warn = %d, want none: warn reports and never fails",
			warnImpact.NewlyFailing)
	}
	if warnImpact.Raised != strictImpact.Raised {
		t.Errorf("raised under warn = %d, want the same %d: the severity is reported either way",
			warnImpact.Raised, strictImpact.Raised)
	}
}

func TestImpactStoresNothingAndChangesNothing(t *testing.T) {
	current := rfcPolicy()
	candidate := rfcPolicy()
	candidate.Version = 8
	candidate.Rules = append(candidate.Rules, rule(checkpolicy.Selector{Layer: "style"}, checkpolicy.Error, ""))
	before, after := rfcPolicy(), candidate
	checkpolicy.Impact(current, candidate, findings())
	if !current.Equal(before) || !candidate.Equal(after) {
		t.Error("Impact() changed a document, want a preview that only reads")
	}
}

// TestImpactNeverRaisesAnAdvisoryLayer: a candidate that raises
// everything still cannot make a model's opinion fail a build.
func TestImpactNeverRaisesAnAdvisoryLayer(t *testing.T) {
	candidate := checkpolicy.Policy{Rules: []checkpolicy.Rule{rule(checkpolicy.Selector{}, checkpolicy.Error, "")}}
	got := checkpolicy.Impact(checkpolicy.Policy{}, candidate, findings())
	for _, c := range got.Changes {
		if c.Target.Layer == "linguistic" {
			t.Errorf("change = %+v, want the linguistic layer left alone", c)
		}
	}
}
