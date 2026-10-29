package checkpolicy_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// TestDocumentRoundTrip: the document of RFC 0005 §4.1, as YAML spells
// it and as the project stores it, survives a round trip through JSON
// with every distinction intact.
func TestDocumentRoundTrip(t *testing.T) {
	const stored = `{
	  "schema": "glossa.check-policy/v1",
	  "version": 7,
	  "require_complete": ["de", "en"],
	  "fail_on": "error",
	  "environments": {
	    "production": {"require_complete": ["de", "en", "fr"], "require_review": "approved"},
	    "staging": {"require_complete": ["de", "en"]},
	    "preview": {"require_review": "approved"}
	  },
	  "rules": [
	    {"layer": "source", "severity": "off"},
	    {"layer": "terminology", "namespace": "legal", "severity": "error"},
	    {"layer": "length", "locale": "ja", "code": "expansion-excessive", "severity": "off"},
	    {"layer": "visual", "severity": "warning", "mode": "warn"}
	  ]
	}`
	var p checkpolicy.Policy
	if err := json.Unmarshal([]byte(stored), &p); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	known := []string{"de", "en", "fr", "ja"}
	p, err := p.Validate(known)
	if err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if p.Version != 7 || len(p.Rules) != 4 || len(p.Environments) != 3 {
		t.Fatalf("policy = %+v, want the document read whole", p)
	}
	if !p.RequiresIn("production", "fr") || p.RequiresIn("staging", "fr") {
		t.Error("require_complete does not differ per environment")
	}
	if !p.RequiresIn("preview", "de") || p.RequiresIn("preview", "fr") {
		t.Error("an environment that names no require_complete must inherit the document's")
	}
	if p.ReviewIn("production") != checkpolicy.ReviewApproved || p.ReviewIn("staging") != "" {
		t.Error("require_review does not differ per environment")
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	var again checkpolicy.Policy
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatalf("Unmarshal(round trip) = %v", err)
	}
	again, err = again.Validate(known)
	if err != nil {
		t.Fatalf("Validate(round trip) = %v", err)
	}
	if !again.Equal(p) {
		t.Errorf("round trip = %s, want the same document", b)
	}
}

// TestRequiredKeepsItsThreeMeanings: absent, null and a list are three
// different things, and a round trip may not confuse them.
func TestRequiredKeepsItsThreeMeanings(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		want     checkpolicy.Required
		requires bool
	}{
		{name: "absent inherits", json: `{}`, want: checkpolicy.Required{}, requires: false},
		{name: "null is every locale", json: `{"require_complete": null}`,
			want: checkpolicy.AllLocales(), requires: true},
		{name: "a list is those locales", json: `{"require_complete": ["fr"]}`,
			want: checkpolicy.RequiredLocales("fr"), requires: false},
		{name: "an empty list is none", json: `{"require_complete": []}`,
			want: checkpolicy.RequiredLocales(), requires: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var e checkpolicy.Environment
			if err := json.Unmarshal([]byte(tc.json), &e); err != nil {
				t.Fatalf("Unmarshal() = %v", err)
			}
			if !e.RequireComplete.Equal(tc.want) {
				t.Fatalf("require_complete = %+v, want %+v", e.RequireComplete, tc.want)
			}
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("Marshal() = %v", err)
			}
			var back checkpolicy.Environment
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("Unmarshal(round trip) = %v", err)
			}
			if !back.Equal(e) {
				t.Errorf("round trip = %s, want %+v", b, e)
			}
			// The document requires de and nothing else; what the
			// environment decides about ja is the distinction itself.
			p := checkpolicy.Policy{
				RequireComplete: []string{"de"},
				Environments:    map[string]checkpolicy.Environment{"production": e},
			}
			if got := p.RequiresIn("production", "ja"); got != tc.requires {
				t.Errorf("RequiresIn(production, ja) = %v, want %v", got, tc.requires)
			}
		})
	}
}

// TestPreM4PolicyIsAValidDocument: what migration 0026 stored is read,
// validated and decided exactly as it was before the document existed.
func TestPreM4PolicyIsAValidDocument(t *testing.T) {
	const stored = `{"require_complete": ["de"], "fail_on": "warning", "missing_translations": "warning"}`
	var p checkpolicy.Policy
	if err := json.Unmarshal([]byte(stored), &p); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	p, err := p.Validate([]string{"de", "fr"})
	if err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if p.Schema != "" || p.Version != 0 || p.Rules != nil || p.Environments != nil {
		t.Errorf("policy = %+v, want validation to leave a pre-M4 policy as the project wrote it", p)
	}
	if !p.Requires("de") || p.Requires("fr") {
		t.Error("require_complete changed meaning")
	}
	if !p.Fails(checkpolicy.Warning) || p.Severity("de") != checkpolicy.Warning {
		t.Error("fail_on or missing_translations changed meaning")
	}
	// And the evaluator answers the same about a finding as the three
	// fields did: nothing selects it, so the layer's severity stands.
	d := p.Decide(checkpolicy.Target{Layer: "parity", Code: "missing-argument", Severity: checkpolicy.Error})
	if d.Rule != -1 || d.Severity != checkpolicy.Error || !p.FailsDecision(d) {
		t.Errorf("Decide() = %+v, want the layer's own error, failing as before", d)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if string(b) != `{"require_complete":["de"],"fail_on":"warning","missing_translations":"warning"}` {
		t.Errorf("stored shape = %s, want the pre-M4 members and nothing else", b)
	}
}

func TestValidateDocument(t *testing.T) {
	future := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		policy checkpolicy.Policy
		want   error
	}{
		{
			name:   "a schema that is not ours",
			policy: checkpolicy.Policy{Schema: "glossa.check-policy/v2"},
			want:   checkpolicy.ErrInvalidDocument,
		},
		{
			name:   "a negative version",
			policy: checkpolicy.Policy{Version: -1},
			want:   checkpolicy.ErrInvalidDocument,
		},
		{
			name:   "a history with no effective_from to pin against",
			policy: checkpolicy.Policy{Version: 2, Previous: &checkpolicy.Policy{Version: 1}},
			want:   checkpolicy.ErrInvalidDocument,
		},
		{
			name: "a previous version that does not precede this one",
			policy: checkpolicy.Policy{
				Version: 2, EffectiveFrom: &future, Previous: &checkpolicy.Policy{Version: 2},
			},
			want: checkpolicy.ErrInvalidDocument,
		},
		{
			name: "an environment requiring a locale the project has not",
			policy: checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
				"production": {RequireComplete: checkpolicy.RequiredLocales("ja")},
			}},
			want: checkpolicy.ErrUnknownLocale,
		},
		{
			name: "a review state that is not one",
			policy: checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
				"production": {RequireReview: "signed-off"},
			}},
			want: checkpolicy.ErrInvalidEnvironment,
		},
		{
			name:   "a rule naming a layer that does not exist",
			policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{rule(checkpolicy.Selector{Layer: "spelling"}, checkpolicy.Off, "")}},
			want:   checkpolicy.ErrUnknownLayer,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.policy.Validate([]string{"de", "en", "fr"}); !errors.Is(err, tc.want) {
				t.Errorf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEqualComparesTheWholeDocument(t *testing.T) {
	base := rfcPolicy()
	tests := []struct {
		name  string
		other checkpolicy.Policy
	}{
		{name: "a different version", other: func() checkpolicy.Policy { p := rfcPolicy(); p.Version = 8; return p }()},
		{name: "a rule appended", other: func() checkpolicy.Policy {
			p := rfcPolicy()
			p.Rules = append(p.Rules, rule(checkpolicy.Selector{Layer: "style"}, checkpolicy.Off, ""))
			return p
		}()},
		{name: "the same rules in another order", other: func() checkpolicy.Policy {
			p := rfcPolicy()
			p.Rules = []checkpolicy.Rule{p.Rules[1], p.Rules[0], p.Rules[2], p.Rules[3]}
			return p
		}()},
		{name: "a rule in warn mode", other: func() checkpolicy.Policy {
			p := rfcPolicy()
			p.Rules[0].Mode = checkpolicy.ModeWarn
			return p
		}()},
		{name: "an environment added", other: func() checkpolicy.Policy {
			p := rfcPolicy()
			p.Environments = map[string]checkpolicy.Environment{"production": {RequireReview: checkpolicy.ReviewApproved}}
			return p
		}()},
	}
	if !base.Equal(rfcPolicy()) {
		t.Fatal("Equal() = false for two copies of one document")
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if base.Equal(tc.other) {
				t.Error("Equal() = true, want two documents that say different things to differ")
			}
		})
	}
}
