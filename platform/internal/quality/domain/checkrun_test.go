package domain

import (
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

func TestCheckRunValidate(t *testing.T) {
	valid := CheckRun{Ref: "main", Trigger: TriggerCLI, Layers: []Layer{LayerParity}}
	cases := []struct {
		name string
		run  CheckRun
		want error
	}{
		{"a run of a branch", valid, nil},
		{"a run of a commit", withCommit(valid, strings.Repeat("a", 40)), nil},
		{"no ref", withRef(valid, ""), ErrInvalidRef},
		{"a ref past the column", withRef(valid, strings.Repeat("b", MaxRefLength+1)), ErrInvalidRef},
		{"a short commit", withCommit(valid, "abc1234"), ErrInvalidCommit},
		{"an upper-case commit", withCommit(valid, strings.ToUpper(strings.Repeat("a", 40))), ErrInvalidCommit},
		{"an unknown trigger", withTrigger(valid, "cron"), ErrUnknownTrigger},
		{"an unknown layer", withLayers(valid, Layer("spelling")), ErrUnknownLayer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func withRef(r CheckRun, ref string) CheckRun     { r.Ref = ref; return r }
func withCommit(r CheckRun, c string) CheckRun    { r.Commit = c; return r }
func withTrigger(r CheckRun, tr Trigger) CheckRun { r.Trigger = tr; return r }
func withLayers(r CheckRun, ls ...Layer) CheckRun { r.Layers = ls; return r }

// TestCountsKeepWaivedApart is RFC 0005 §14 decision 5: waived is
// counted on its own and is never part of errors or warnings.
func TestCountsKeepWaivedApart(t *testing.T) {
	var c Counts
	for _, s := range []Severity{Error, Warning, Waived, Waived} {
		c.Count(Finding{Severity: s})
	}
	if c != (Counts{Errors: 1, Warnings: 1, Waived: 2}) || c.Total() != 4 {
		t.Errorf("counts = %+v (total %d)", c, c.Total())
	}
}

// TestWaivedNeverFailsARun holds the two sides of the waiver together:
// Quality stores `waived` as a finding's severity, and checkpolicy —
// which is the only thing that decides what fails — has no such rank
// and must answer "no" for it under every fail_on the policy document
// allows. If the kernel ever learned to rank it, a waiver would be able
// to fail a build, which is the opposite of what one is for
// (RFC 0005 §2.1).
func TestWaivedNeverFailsARun(t *testing.T) {
	for _, failOn := range []checkpolicy.Severity{checkpolicy.Error, checkpolicy.Warning, checkpolicy.Never, ""} {
		p := checkpolicy.Policy{FailOn: failOn}
		if p.Fails(Waived) {
			t.Errorf("fail_on %q ranks a waived finding as failing", failOn)
		}
		counts, conclusion := Conclude(p, []Finding{
			{Severity: Waived}, {Severity: Waived},
		})
		if conclusion != ConclusionSuccess {
			t.Errorf("fail_on %q: a run of nothing but waived findings concluded %q", failOn, conclusion)
		}
		if counts != (Counts{Waived: 2}) {
			t.Errorf("fail_on %q: counts = %+v", failOn, counts)
		}
	}
	// And a policy rule may not mint the rank either: `waived` is a
	// rendering of a finding, never a severity a rule can set.
	if _, err := checkpolicy.ParseRuleSeverity(string(Waived)); err == nil {
		t.Error("a rule set severity `waived`")
	}
}
