package checkpolicy_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

func TestOverride(t *testing.T) {
	server := checkpolicy.Policy{
		Version: 7, RequireComplete: []string{"de", "en"}, FailOn: checkpolicy.Error,
		MissingTranslations: checkpolicy.Warning,
		Rules: []checkpolicy.Rule{
			rule(checkpolicy.Selector{Layer: "terminology"}, checkpolicy.Error, ""),
		},
	}
	tests := []struct {
		name      string
		overrides checkpolicy.Overrides
		want      checkpolicy.Policy
	}{
		{name: "nothing local changes nothing", want: server},
		{
			name:      "--fail-on=warning tightens the local loop",
			overrides: checkpolicy.Overrides{FailOn: checkpolicy.Warning},
			want: checkpolicy.Policy{
				Version: 7, RequireComplete: []string{"de", "en"}, FailOn: checkpolicy.Warning,
				MissingTranslations: checkpolicy.Warning, Rules: server.Rules,
			},
		},
		{
			name:      "--require-complete=none loosens it",
			overrides: checkpolicy.Overrides{RequireComplete: checkpolicy.RequiredLocales()},
			want: checkpolicy.Policy{
				Version: 7, RequireComplete: []string{}, FailOn: checkpolicy.Error,
				MissingTranslations: checkpolicy.Warning, Rules: server.Rules,
			},
		},
		{
			name:      "a list replaces the list",
			overrides: checkpolicy.Overrides{RequireComplete: checkpolicy.RequiredLocales("fr")},
			want: checkpolicy.Policy{
				Version: 7, RequireComplete: []string{"fr"}, FailOn: checkpolicy.Error,
				MissingTranslations: checkpolicy.Warning, Rules: server.Rules,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := server.Override(tc.overrides)
			if !got.Equal(tc.want) {
				t.Errorf("Override() = %+v, want %+v", got, tc.want)
			}
			// The document it was applied to is untouched: an override is
			// a local view, not an edit.
			if server.FailOn != checkpolicy.Error || len(server.RequireComplete) != 2 {
				t.Errorf("the server's document changed: %+v", server)
			}
		})
	}
}

// TestOverrideCannotReachTheRules: a developer may tighten or loosen
// their own loop; they may not change what the project decides
// (RFC 0005 §4.2).
func TestOverrideCannotReachTheRules(t *testing.T) {
	server := rfcPolicy()
	got := server.Override(checkpolicy.Overrides{
		FailOn: checkpolicy.Never, RequireComplete: checkpolicy.AllLocales(),
	})
	if len(got.Rules) != len(server.Rules) {
		t.Fatalf("rules = %d, want the server's %d", len(got.Rules), len(server.Rules))
	}
	for i := range got.Rules {
		if got.Rules[i] != server.Rules[i] {
			t.Errorf("rule %d = %+v, want the server's %+v", i, got.Rules[i], server.Rules[i])
		}
	}
	if got.Version != server.Version {
		t.Errorf("version = %d, want an overridden run to still name the version the server issued", got.Version)
	}
	// The severity of a finding is the rules', whatever the flags said.
	d := got.Decide(checkpolicy.Target{
		Layer: "terminology", Code: "term_forbidden", Namespace: "legal", Severity: checkpolicy.Warning,
	})
	if d.Severity != checkpolicy.Error || d.Rule != 1 {
		t.Errorf("Decide() = %+v, want rule 1's error", d)
	}
	// fail_on: never is local, and is the one thing that changed.
	if got.FailsDecision(d) {
		t.Error("FailsDecision() = true, want --fail-on=never to hold locally")
	}
	if !server.FailsDecision(server.Decide(checkpolicy.Target{
		Layer: "terminology", Code: "term_forbidden", Namespace: "legal", Severity: checkpolicy.Warning,
	})) {
		t.Error("the server's own verdict changed, want it untouched by a local flag")
	}
}

func TestOverridesEmptyAndSelects(t *testing.T) {
	if !(checkpolicy.Overrides{}).Empty() {
		t.Error("Empty() = false for no overrides")
	}
	if (checkpolicy.Overrides{FailOn: checkpolicy.Warning}).Empty() {
		t.Error("Empty() = true for --fail-on")
	}
	if (checkpolicy.Overrides{Layers: []string{}}).Empty() {
		t.Error("Empty() = true for --layer with no layer, which is a run that reports nothing")
	}
	o := checkpolicy.Overrides{Layers: []string{"structure", "parity"}}
	if !o.Selects("parity") || o.Selects("terminology") {
		t.Error("Selects() does not follow --layer")
	}
	if !(checkpolicy.Overrides{}).Selects("terminology") {
		t.Error("Selects() = false without --layer, want every layer")
	}
}
