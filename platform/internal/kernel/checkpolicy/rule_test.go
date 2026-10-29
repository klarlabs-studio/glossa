package checkpolicy_test

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// rule is a short constructor for the tables below.
func rule(sel checkpolicy.Selector, sev checkpolicy.Severity, mode checkpolicy.Mode) checkpolicy.Rule {
	return checkpolicy.Rule{Selector: sel, Severity: sev, Mode: mode}
}

// The policy of RFC 0005 §4.1, rule for rule, which is the fixture
// table the RFC asks the precedence to be covered by.
func rfcPolicy() checkpolicy.Policy {
	return checkpolicy.Policy{
		Version: 7,
		Rules: []checkpolicy.Rule{
			rule(checkpolicy.Selector{Layer: "source"}, checkpolicy.Off, ""),
			rule(checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, checkpolicy.Error, ""),
			rule(checkpolicy.Selector{Layer: "length", Locale: "ja", Code: "expansion-excessive"}, checkpolicy.Off, ""),
			rule(checkpolicy.Selector{Layer: "visual"}, checkpolicy.Warning, checkpolicy.ModeWarn),
		},
	}
}

func TestDecideRFCPolicy(t *testing.T) {
	p := rfcPolicy()
	tests := []struct {
		name     string
		target   checkpolicy.Target
		want     checkpolicy.Severity
		wantRule int
		wantMode checkpolicy.Mode
		wantFail bool
	}{
		{
			name:     "a layer nothing selects keeps the severity it emitted",
			target:   checkpolicy.Target{Layer: "parity", Code: "missing-argument", Severity: checkpolicy.Error},
			want:     checkpolicy.Error,
			wantRule: -1, wantMode: checkpolicy.ModeEnforce, wantFail: true,
		},
		{
			name:     "source is off everywhere",
			target:   checkpolicy.Target{Layer: "source", Code: "manual-plural", Severity: checkpolicy.Warning},
			want:     checkpolicy.Off,
			wantRule: 0, wantMode: checkpolicy.ModeEnforce,
		},
		{
			name: "terminology in the legal namespace is an error",
			target: checkpolicy.Target{
				Layer: "terminology", Code: "term_forbidden", Locale: "de",
				Namespace: "legal", Severity: checkpolicy.Warning,
			},
			want:     checkpolicy.Error,
			wantRule: 1, wantMode: checkpolicy.ModeEnforce, wantFail: true,
		},
		{
			name: "terminology elsewhere is untouched",
			target: checkpolicy.Target{
				Layer: "terminology", Code: "term_forbidden", Locale: "de",
				Namespace: "checkout", Severity: checkpolicy.Warning,
			},
			want:     checkpolicy.Warning,
			wantRule: -1, wantMode: checkpolicy.ModeEnforce,
		},
		{
			name: "the Japanese expansion warning is off",
			target: checkpolicy.Target{
				Layer: "length", Code: "expansion-excessive", Locale: "ja", Severity: checkpolicy.Warning,
			},
			want:     checkpolicy.Off,
			wantRule: 2, wantMode: checkpolicy.ModeEnforce,
		},
		{
			name: "another length code in Japanese is not",
			target: checkpolicy.Target{
				Layer: "length", Code: "max-length-exceeded", Locale: "ja", Severity: checkpolicy.Error,
			},
			want:     checkpolicy.Error,
			wantRule: -1, wantMode: checkpolicy.ModeEnforce, wantFail: true,
		},
		{
			name: "the same expansion code in French is not",
			target: checkpolicy.Target{
				Layer: "length", Code: "expansion-excessive", Locale: "fr", Severity: checkpolicy.Warning,
			},
			want:     checkpolicy.Warning,
			wantRule: -1, wantMode: checkpolicy.ModeEnforce,
		},
		{
			name:     "visual reports in warn mode and cannot fail",
			target:   checkpolicy.Target{Layer: "visual", Code: "text-clipped", Severity: checkpolicy.Error},
			want:     checkpolicy.Warning,
			wantRule: 3, wantMode: checkpolicy.ModeWarn,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := p.Decide(tc.target)
			if d.Severity != tc.want || d.Rule != tc.wantRule || d.Mode != tc.wantMode {
				t.Errorf("Decide() = %+v, want severity %q from rule %d in mode %q",
					d, tc.want, tc.wantRule, tc.wantMode)
			}
			if got := p.FailsDecision(d); got != tc.wantFail {
				t.Errorf("FailsDecision() = %v, want %v", got, tc.wantFail)
			}
		})
	}
}

// TestDecidePrecedenceIsBySpecificity holds the ordering the schema
// states: more named fields wins, and a tie goes to the later rule.
func TestDecidePrecedenceIsBySpecificity(t *testing.T) {
	target := checkpolicy.Target{
		Layer: "length", Code: "expansion-excessive", Locale: "ja",
		Namespace: "checkout", Environment: "production", Severity: checkpolicy.Warning,
	}
	tests := []struct {
		name     string
		rules    []checkpolicy.Rule
		want     checkpolicy.Severity
		wantRule int
	}{
		{
			name: "one more field beats the broader rule, whatever the order",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{Layer: "length", Code: "expansion-excessive"}, checkpolicy.Off, ""),
				rule(checkpolicy.Selector{Layer: "length"}, checkpolicy.Error, ""),
			},
			want: checkpolicy.Off, wantRule: 0,
		},
		{
			name: "and the other way round",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{Layer: "length"}, checkpolicy.Error, ""),
				rule(checkpolicy.Selector{Layer: "length", Code: "expansion-excessive"}, checkpolicy.Off, ""),
			},
			want: checkpolicy.Off, wantRule: 1,
		},
		{
			name: "a tie on two fields goes to the later rule",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{Layer: "length", Locale: "ja"}, checkpolicy.Error, ""),
				rule(checkpolicy.Selector{Layer: "length", Namespace: "checkout"}, checkpolicy.Warning, ""),
			},
			want: checkpolicy.Warning, wantRule: 1,
		},
		{
			name: "a tie between two identical selectors goes to the later rule",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{Layer: "length"}, checkpolicy.Error, ""),
				rule(checkpolicy.Selector{Layer: "length"}, checkpolicy.Warning, ""),
			},
			want: checkpolicy.Warning, wantRule: 1,
		},
		{
			name: "the empty selector matches everything and is the least specific",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{Locale: "ja"}, checkpolicy.Off, ""),
				rule(checkpolicy.Selector{}, checkpolicy.Error, ""),
			},
			want: checkpolicy.Off, wantRule: 0,
		},
		{
			name: "five fields beat four",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{
					Layer: "length", Code: "expansion-excessive", Locale: "ja",
					Namespace: "checkout", Environment: "production",
				}, checkpolicy.Error, ""),
				rule(checkpolicy.Selector{
					Layer: "length", Code: "expansion-excessive", Locale: "ja", Namespace: "checkout",
				}, checkpolicy.Off, ""),
			},
			want: checkpolicy.Error, wantRule: 0,
		},
		{
			name: "a rule that does not match is not a candidate, however specific",
			rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{
					Layer: "length", Code: "expansion-excessive", Locale: "de",
					Namespace: "checkout", Environment: "production",
				}, checkpolicy.Error, ""),
				rule(checkpolicy.Selector{Layer: "length"}, checkpolicy.Off, ""),
			},
			want: checkpolicy.Off, wantRule: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := checkpolicy.Policy{Rules: tc.rules}.Decide(target)
			if d.Severity != tc.want || d.Rule != tc.wantRule {
				t.Errorf("Decide() = %+v, want severity %q from rule %d", d, tc.want, tc.wantRule)
			}
		})
	}
}

// TestDecideNeverMatchesAFieldTheTargetLacks: a rule about production
// says nothing about a branch check, which runs in no environment.
func TestDecideNeverMatchesAFieldTheTargetLacks(t *testing.T) {
	p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
		rule(checkpolicy.Selector{Layer: "completeness", Environment: "production"}, checkpolicy.Error, ""),
		rule(checkpolicy.Selector{Namespace: "legal"}, checkpolicy.Error, ""),
	}}
	branch := checkpolicy.Target{Layer: "completeness", Code: "missing-translation", Severity: checkpolicy.Warning}
	if d := p.Decide(branch); d.Rule != -1 || d.Severity != checkpolicy.Warning {
		t.Fatalf("Decide(branch) = %+v, want the layer's own warning and no rule", d)
	}
	production := branch
	production.Environment = "production"
	if d := p.Decide(production); d.Rule != 0 || d.Severity != checkpolicy.Error {
		t.Fatalf("Decide(production) = %+v, want rule 0's error", d)
	}
}

// TestDecideIsTotal: every ordering of a set of rules that could tie is
// decided, and the answer never depends on anything but specificity and
// position. A policy nobody can predict is worse than no policy.
func TestDecideIsTotal(t *testing.T) {
	target := checkpolicy.Target{
		Layer: "terminology", Code: "term_forbidden", Locale: "de",
		Namespace: "legal", Environment: "staging", Severity: checkpolicy.Warning,
	}
	rules := []checkpolicy.Rule{
		rule(checkpolicy.Selector{}, checkpolicy.Warning, ""),
		rule(checkpolicy.Selector{Layer: "terminology"}, checkpolicy.Error, ""),
		rule(checkpolicy.Selector{Locale: "de"}, checkpolicy.Off, ""),
		rule(checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, checkpolicy.Error, ""),
		rule(checkpolicy.Selector{Code: "term_forbidden", Environment: "staging"}, checkpolicy.Warning, ""),
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 200 {
		shuffled := append([]checkpolicy.Rule{}, rules...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		d := checkpolicy.Policy{Rules: shuffled}.Decide(target)
		// Whatever the order, the winner has the most fields of the
		// matching rules, and is the last such rule in the list.
		best, bestAt := -1, -1
		for j, r := range shuffled {
			if r.Matches(target) && r.Specificity() >= best {
				best, bestAt = r.Specificity(), j
			}
		}
		if d.Rule != bestAt || d.Severity != shuffled[bestAt].Severity {
			t.Fatalf("shuffle %d: Decide() = %+v, want rule %d (%+v)", i, d, bestAt, shuffled[bestAt])
		}
	}
}

func TestComputes(t *testing.T) {
	p := rfcPolicy()
	tests := []struct {
		layer, env string
		want       bool
	}{
		{layer: "source", want: false},
		{layer: "source", env: "production", want: false},
		{layer: "terminology", want: true},
		{layer: "visual", want: true},
		// A rule about one locale and one code can never switch a whole
		// layer off: the planner asks about the layer, not the finding.
		{layer: "length", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.layer+"/"+tc.env, func(t *testing.T) {
			if got := p.Computes(tc.layer, tc.env); got != tc.want {
				t.Errorf("Computes(%q, %q) = %v, want %v", tc.layer, tc.env, got, tc.want)
			}
		})
	}
}

// TestAdvisoryIsNeverRaisedToError is RFC 0005 §14 decision 10: a build
// never fails on an opinion.
func TestAdvisoryIsNeverRaisedToError(t *testing.T) {
	t.Run("a rule naming the layer is rejected at validation", func(t *testing.T) {
		p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
			rule(checkpolicy.Selector{Layer: "linguistic"}, checkpolicy.Error, ""),
		}}
		_, err := p.Validate(nil)
		if !errors.Is(err, checkpolicy.ErrAdvisoryLayer) {
			t.Fatalf("Validate() error = %v, want ErrAdvisoryLayer", err)
		}
		if got := err.Error(); !strings.Contains(got, "linguistic") || !strings.Contains(got, "warning") {
			t.Errorf("error = %q, want it to name the layer and the highest severity allowed", got)
		}
	})
	t.Run("warn and off stay allowed", func(t *testing.T) {
		for _, sev := range []checkpolicy.Severity{checkpolicy.Warning, checkpolicy.Off} {
			p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
				rule(checkpolicy.Selector{Layer: "linguistic"}, sev, ""),
			}}
			if _, err := p.Validate(nil); err != nil {
				t.Errorf("Validate() with %q = %v, want it allowed", sev, err)
			}
		}
	})
	t.Run("a wildcard rule is clamped at evaluation", func(t *testing.T) {
		p := checkpolicy.Policy{Rules: []checkpolicy.Rule{rule(checkpolicy.Selector{}, checkpolicy.Error, "")}}
		if _, err := p.Validate(nil); err != nil {
			t.Fatalf("Validate() = %v, want a wildcard rule to be a valid policy", err)
		}
		d := p.Decide(checkpolicy.Target{Layer: "linguistic", Code: "tone-mismatch", Severity: checkpolicy.Warning})
		if d.Severity != checkpolicy.Warning || !d.Clamped {
			t.Fatalf("Decide() = %+v, want a clamped warning", d)
		}
		if p.FailsDecision(d) {
			t.Error("FailsDecision() = true, want a model's opinion never to fail a run")
		}
	})
	t.Run("the target's own advisory flag clamps too", func(t *testing.T) {
		p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
			rule(checkpolicy.Selector{Layer: "visual"}, checkpolicy.Error, ""),
		}}
		d := p.Decide(checkpolicy.Target{Layer: "visual", Severity: checkpolicy.Warning, Advisory: true})
		if d.Severity != checkpolicy.Warning || !d.Clamped {
			t.Fatalf("Decide() = %+v, want a clamped warning", d)
		}
	})
}

func TestRuleValidate(t *testing.T) {
	tests := []struct {
		name string
		rule checkpolicy.Rule
		want error
	}{
		{name: "a layer that does not exist", rule: rule(checkpolicy.Selector{Layer: "spelling"}, checkpolicy.Error, ""),
			want: checkpolicy.ErrUnknownLayer},
		{name: "a severity that is not one", rule: rule(checkpolicy.Selector{}, "fatal", ""),
			want: checkpolicy.ErrInvalidSeverity},
		{name: "no severity at all", rule: rule(checkpolicy.Selector{Layer: "style"}, "", ""),
			want: checkpolicy.ErrInvalidSeverity},
		{name: "a mode that is not one", rule: rule(checkpolicy.Selector{}, checkpolicy.Warning, "observe"),
			want: checkpolicy.ErrInvalidMode},
		{name: "never is a fail_on, not a rule's severity",
			rule: rule(checkpolicy.Selector{}, checkpolicy.Never, ""), want: checkpolicy.ErrInvalidSeverity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.rule.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}
	t.Run("an empty mode normalizes to enforce", func(t *testing.T) {
		got, err := rule(checkpolicy.Selector{Layer: "style"}, checkpolicy.Warning, "").Validate()
		if err != nil {
			t.Fatalf("Validate() = %v", err)
		}
		if got.Mode != checkpolicy.ModeEnforce {
			t.Errorf("mode = %q, want %q", got.Mode, checkpolicy.ModeEnforce)
		}
	})
}
