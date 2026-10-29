package checkpolicy_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// RFC 0005 §5.2's thresholds are the policy's, and they are the numbers
// the RFC states. A test that only asserted "they exist" would let one
// drift to whatever the probe happened to use.
func TestVisualThresholdsAreTheRFCsNumbers(t *testing.T) {
	v := checkpolicy.Policy{}.Visual()
	for _, tc := range []struct {
		what string
		got  int
		want int
	}{
		{"text-clipped's slack, in CSS pixels", v.ClipSlackPx, 1},
		{"region-overlap's share of the smaller region", v.OverlapPercent, 25},
		{"line-growth's tolerance in line boxes", v.LineGrowthLines, 0},
		{"the findings one capture may report", v.MaxFindingsPerCapture, 500},
		{"the sightings before a visual finding may be an error", v.PromoteAfterSightings, 2},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.what, tc.got, tc.want)
		}
	}
}

// The per-locale tolerance §5.2 names: a locale the thresholds name
// gets its own, every other one the default.
func TestLineGrowthToleranceIsPerLocale(t *testing.T) {
	v := checkpolicy.DefaultVisualThresholds()
	v.LineGrowthByLocale = map[string]int{"ja": 2}
	if got := v.LineGrowthIn("ja"); got != 2 {
		t.Errorf("ja = %d, want 2", got)
	}
	if got := v.LineGrowthIn("de"); got != 0 {
		t.Errorf("de = %d, want the default 0", got)
	}
}

// The first half of the flake-control rule (RFC 0005 §5.2): a visual
// finding that is not yet evidence can never be an error, however
// strict the policy is. A single sighting of a headless browser's text
// metrics is not a reason to fail a build.
func TestAProvisionalFindingIsNeverRaisedToError(t *testing.T) {
	p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "visual"}, Severity: checkpolicy.Error},
	}}
	first := p.Decide(checkpolicy.Target{
		Layer: "visual", Code: "text-clipped", Locale: "de",
		Severity: checkpolicy.Warning, Provisional: true,
	})
	if first.Severity != checkpolicy.Warning || !first.Clamped {
		t.Errorf("first sighting = %+v, want a clamped warning", first)
	}
	if p.FailsDecision(first) {
		t.Error("a first sighting failed the run")
	}
	second := p.Decide(checkpolicy.Target{
		Layer: "visual", Code: "text-clipped", Locale: "de",
		Severity: checkpolicy.Warning,
	})
	if second.Severity != checkpolicy.Error || second.Clamped {
		t.Errorf("second sighting = %+v, want the rule's error", second)
	}
	if !p.FailsDecision(second) {
		t.Error("a promoted finding didn't fail the run")
	}
}

// Promotion makes a finding *eligible* for error, not an error: a
// project whose policy says nothing about the visual layer keeps the
// warning the layer emitted however often it is seen.
func TestPromotionAloneDoesNotFailARun(t *testing.T) {
	p := checkpolicy.Policy{}
	d := p.Decide(checkpolicy.Target{Layer: "visual", Code: "region-overlap", Severity: checkpolicy.Warning})
	if d.Severity != checkpolicy.Warning || p.FailsDecision(d) {
		t.Errorf("decision = %+v, want a warning that doesn't fail", d)
	}
}
