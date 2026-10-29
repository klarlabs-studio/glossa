package checkpolicy_test

import (
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

var saved = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// strict is the version somebody ships to make terminology an error,
// superseding the policy that only warned.
func strict(grace time.Duration) checkpolicy.Policy {
	lenient := checkpolicy.Policy{Version: 6}
	return checkpolicy.Policy{
		Rules: []checkpolicy.Rule{rule(checkpolicy.Selector{Layer: "terminology"}, checkpolicy.Error, "")},
	}.Supersede(lenient, saved, grace)
}

func TestSupersede(t *testing.T) {
	p := strict(checkpolicy.DefaultGrace)
	if p.Version != 7 {
		t.Errorf("version = %d, want the version after 6", p.Version)
	}
	if p.Schema != checkpolicy.Schema {
		t.Errorf("schema = %q, want %q", p.Schema, checkpolicy.Schema)
	}
	if p.EffectiveFrom == nil || !p.EffectiveFrom.Equal(saved) {
		t.Errorf("effective_from = %v, want the moment it was saved", p.EffectiveFrom)
	}
	if p.GraceUntil == nil || !p.GraceUntil.Equal(saved.Add(checkpolicy.DefaultGrace)) {
		t.Errorf("grace_until = %v, want the default fortnight", p.GraceUntil)
	}
	if p.Previous == nil || p.Previous.Version != 6 {
		t.Fatalf("previous = %+v, want version 6", p.Previous)
	}
	if p.Previous.Previous != nil {
		t.Error("previous.previous is set, want a history one version deep")
	}
	if _, err := p.Validate(nil); err != nil {
		t.Errorf("Validate() = %v, want a superseded document to be valid", err)
	}
}

func TestSupersedeWithoutGracePinsNothing(t *testing.T) {
	p := strict(0)
	if p.GraceUntil != nil || p.Previous != nil {
		t.Fatalf("policy = %+v, want no grace and no history", p)
	}
	opened := saved.Add(-time.Hour)
	if p.Pins(opened, saved) {
		t.Error("Pins() = true, want a policy saved without a grace to grade every pull request at once")
	}
}

// TestEffectivePinsOpenPullRequests is RFC 0005 §4.3: nobody wakes up
// to forty red pull requests.
func TestEffectivePins(t *testing.T) {
	p := strict(checkpolicy.DefaultGrace)
	finding := checkpolicy.Target{
		Layer: "terminology", Code: "term_forbidden", Locale: "de", Severity: checkpolicy.Warning,
	}
	tests := []struct {
		name        string
		opened, now time.Time
		wantVersion int
		wantFails   bool
	}{
		{
			name:   "a pull request opened before the save keeps the version it opened under",
			opened: saved.Add(-48 * time.Hour), now: saved.Add(time.Hour),
			wantVersion: 6,
		},
		{
			name:   "a pull request opened after the save gets the new version at once",
			opened: saved.Add(time.Minute), now: saved.Add(time.Hour),
			wantVersion: 7, wantFails: true,
		},
		{
			name:   "the same old pull request, once the grace has run out",
			opened: saved.Add(-48 * time.Hour), now: saved.Add(checkpolicy.DefaultGrace + time.Second),
			wantVersion: 7, wantFails: true,
		},
		{
			name:   "a run that is not a pull request's grades against the current version",
			opened: time.Time{}, now: saved.Add(time.Hour),
			wantVersion: 7, wantFails: true,
		},
		{
			name:   "the last second of the grace still pins",
			opened: saved.Add(-time.Hour), now: saved.Add(checkpolicy.DefaultGrace - time.Nanosecond),
			wantVersion: 6,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eff := p.Effective(tc.opened, tc.now)
			if eff.Version != tc.wantVersion {
				t.Fatalf("version = %d, want %d", eff.Version, tc.wantVersion)
			}
			if eff.Previous != nil {
				t.Error("the effective document carries a history, want the version itself")
			}
			if got := eff.FailsDecision(eff.Decide(finding)); got != tc.wantFails {
				t.Errorf("fails = %v, want %v", got, tc.wantFails)
			}
		})
	}
}

func TestEffectiveWithoutHistory(t *testing.T) {
	p := checkpolicy.Policy{Version: 3}
	if got := p.Effective(saved.Add(-time.Hour), saved); !got.Equal(p) {
		t.Errorf("Effective() = %+v, want the document itself", got)
	}
}

func TestValidateRejectsADeepHistory(t *testing.T) {
	first := checkpolicy.Policy{Version: 5}
	second := checkpolicy.Policy{}.Supersede(first, saved, checkpolicy.DefaultGrace)
	third := checkpolicy.Policy{}.Supersede(second, saved.Add(time.Hour), checkpolicy.DefaultGrace)
	if third.Previous == nil || third.Previous.Previous != nil {
		t.Fatalf("previous = %+v, want the history kept one version deep", third.Previous)
	}
	// Hand-built nesting is refused rather than silently flattened.
	deep := third
	nested := second
	deep.Previous = &nested
	if _, err := deep.Validate(nil); err == nil {
		t.Error("Validate() = nil, want a two-version history refused")
	}
}
