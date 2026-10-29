package domain_test

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// shipped is a release of messages source messages, with the locales it
// ships and how many of them each carries. Built and Release both hand
// the gate the same pair, which is the point.
type shipped struct {
	content domain.Content
	stats   domain.Stats
}

func built(messages int, locales map[string]int) shipped {
	out := shipped{stats: domain.Stats{Messages: messages, Locales: map[string]domain.LocaleStats{}}}
	for _, code := range slices.Sorted(maps.Keys(locales)) {
		out.stats.Locales[code] = domain.LocaleStats{Messages: locales[code]}
		out.content.Locales = append(out.content.Locales, domain.Locale{Code: code, Direction: "ltr"})
	}
	return out
}

// check asks the gate about a release built under p.
func check(g domain.PolicyGate, p domain.Policy, s shipped) error {
	return g.Check(p, s.content, s.stats)
}

// approvedOnly is what production and staging ship.
var approvedOnly = domain.Policy{States: []string{domain.StateApproved}}

// everything is what a preview or a branch ships.
var everything = domain.Policy{States: []string{domain.StateDraft, domain.StateNeedsReview, domain.StateApproved}}

func TestPolicyGateUnnamedEnvironmentIsNotGated(t *testing.T) {
	// The document requires every locale (its zero value), and says
	// nothing about staging. Publishing to staging is what it always
	// was: a project that never wrote a policy must not discover that
	// every environment silently became strict.
	g := domain.NewPolicyGate(checkpolicy.Policy{}, "staging")
	if g.Bound {
		t.Fatalf("staging is gated by a policy that does not name it: %+v", g)
	}
	if err := check(g, everything, built(10, map[string]int{"en": 10, "de": 3})); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
}

func TestPolicyGateRefusesAnIncompleteLocale(t *testing.T) {
	doc := checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de", "en")},
	}}
	g := domain.NewPolicyGate(doc, "production")
	err := check(g, approvedOnly, built(10, map[string]int{"en": 10, "de": 7, "fr": 1}))
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Check = %v, want ErrPolicyNotMet", err)
	}
	var notMet *domain.PolicyNotMetError
	if !errors.As(err, &notMet) {
		t.Fatalf("Check = %v, want a *PolicyNotMetError", err)
	}
	if len(notMet.Unmet) != 1 || notMet.Unmet[0].Locale != "de" {
		t.Fatalf("unmet = %+v, want de alone (fr is not required)", notMet.Unmet)
	}
	if !strings.Contains(err.Error(), "de") {
		t.Errorf("the refusal does not name the locale: %s", err)
	}
	// Complete in both required locales publishes, however far behind
	// the locales the environment does not require are.
	if err := check(g, approvedOnly, built(10, map[string]int{"en": 10, "de": 10, "fr": 0})); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
}

func TestPolicyGateRequireCompleteNullIsEveryLocale(t *testing.T) {
	doc := checkpolicy.Policy{
		RequireComplete: []string{"de"},
		Environments:    map[string]checkpolicy.Environment{"production": {RequireComplete: checkpolicy.AllLocales()}},
	}
	g := domain.NewPolicyGate(doc, "production")
	if g.RequireComplete != nil {
		t.Fatalf("RequireComplete = %v, want nil (every locale)", g.RequireComplete)
	}
	err := check(g, approvedOnly, built(4, map[string]int{"en": 4, "de": 4, "fr": 3}))
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Check = %v, want ErrPolicyNotMet: null means every locale, fr among them", err)
	}
}

func TestPolicyGateInheritsTheDocumentsRequireComplete(t *testing.T) {
	// The block names a review state and no require_complete. "I did not
	// say" is not "every locale": it inherits the document's, which is
	// de alone. Getting this wrong makes production quietly stricter
	// than anyone wrote — the same property wave 3 pins on the CLI side.
	doc := checkpolicy.Policy{
		RequireComplete: []string{"de"},
		Environments:    map[string]checkpolicy.Environment{"production": {RequireReview: checkpolicy.ReviewApproved}},
	}
	g := domain.NewPolicyGate(doc, "production")
	if len(g.RequireComplete) != 1 || g.RequireComplete[0] != "de" {
		t.Fatalf("RequireComplete = %v, want [de] inherited from the document", g.RequireComplete)
	}
	// fr is nowhere near complete and is not required anywhere.
	if err := check(g, approvedOnly, built(4, map[string]int{"en": 4, "de": 4, "fr": 0})); err != nil {
		t.Fatalf("Check = %v, want nil: only de is required", err)
	}
}

func TestPolicyGateInheritsEveryLocaleWhenTheDocumentSaysSo(t *testing.T) {
	// The document's nil require_complete is "every locale", and a block
	// that names none inherits that too.
	doc := checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireReview: checkpolicy.ReviewApproved},
	}}
	g := domain.NewPolicyGate(doc, "production")
	if g.RequireComplete != nil {
		t.Fatalf("RequireComplete = %v, want nil (every locale)", g.RequireComplete)
	}
	if err := check(g, approvedOnly, built(4, map[string]int{"en": 4, "fr": 3})); !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Check = %v, want ErrPolicyNotMet", err)
	}
}

func TestPolicyGateEmptyRequireCompleteRequiresNothing(t *testing.T) {
	doc := checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"development": {RequireComplete: checkpolicy.RequiredLocales()},
	}}
	g := domain.NewPolicyGate(doc, "development")
	if !g.Bound {
		t.Fatal("the policy names development, so it is gated")
	}
	if err := check(g, everything, built(9, map[string]int{"en": 9, "de": 0})); err != nil {
		t.Fatalf("Check = %v, want nil: an empty list requires no locale", err)
	}
}

func TestPolicyGateEnforcesRequireReview(t *testing.T) {
	doc := checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales(), RequireReview: checkpolicy.ReviewApproved},
	}}
	g := domain.NewPolicyGate(doc, "production")
	complete := built(3, map[string]int{"en": 3, "de": 3})
	// An environment shipping drafts has not reached approved, however
	// complete it is.
	err := check(g, everything, complete)
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Check = %v, want ErrPolicyNotMet", err)
	}
	if !strings.Contains(err.Error(), checkpolicy.ReviewApproved) {
		t.Errorf("the refusal does not name the review state: %s", err)
	}
	if err := check(g, approvedOnly, complete); err != nil {
		t.Fatalf("Check = %v, want nil: approved-only text has reached approved", err)
	}
}

func TestPolicyGateRequiresALocaleTheReleaseDoesNotShip(t *testing.T) {
	doc := checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("ja")},
	}}
	g := domain.NewPolicyGate(doc, "production")
	if err := check(g, approvedOnly, built(2, map[string]int{"en": 2})); !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Check = %v, want ErrPolicyNotMet", err)
	}
}

func TestPolicyReached(t *testing.T) {
	cases := []struct {
		name  string
		p     domain.Policy
		state string
		want  bool
	}{
		{"approved only", approvedOnly, domain.StateApproved, true},
		{"everything", everything, domain.StateApproved, false},
		{"needs_review and approved", domain.Policy{States: []string{domain.StateNeedsReview, domain.StateApproved}}, domain.StateApproved, false},
		{"everything reaches draft", everything, domain.StateDraft, true},
		{"unknown state", approvedOnly, "published", false},
	}
	for _, tc := range cases {
		if got := tc.p.Reached(tc.state); got != tc.want {
			t.Errorf("%s: Reached(%q) = %v", tc.name, tc.state, got)
		}
	}
}

func TestNewOverride(t *testing.T) {
	o, err := domain.NewOverride("  the launch ships before the German copy  ")
	if err != nil {
		t.Fatalf("NewOverride = %v", err)
	}
	if !o.Forced || o.Reason != "the launch ships before the German copy" {
		t.Fatalf("override = %+v", o)
	}
	for _, blank := range []string{"", "   ", "\t\n"} {
		if _, err := domain.NewOverride(blank); !errors.Is(err, domain.ErrForceNeedsReason) {
			t.Errorf("NewOverride(%q) = %v, want ErrForceNeedsReason", blank, err)
		}
	}
	long := strings.Repeat("ü", domain.MaxForceReasonLen+1)
	if _, err := domain.NewOverride(long); !errors.Is(err, domain.ErrInvalidForceReason) {
		t.Errorf("NewOverride(long) = %v, want ErrInvalidForceReason", err)
	}
}

func TestPolicyGateEnforce(t *testing.T) {
	doc := checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}}
	g := domain.NewPolicyGate(doc, "production")
	short, complete := built(4, map[string]int{"en": 4, "de": 1}), built(4, map[string]int{"en": 4, "de": 4})
	forced, err := domain.NewOverride("the launch is tomorrow")
	if err != nil {
		t.Fatal(err)
	}

	// Unmet and not forced: refused, and nothing to record.
	if ov, err := g.Enforce(approvedOnly, short.content, short.stats, domain.Override{}); !errors.Is(err, domain.ErrPolicyNotMet) || ov.Forced {
		t.Fatalf("Enforce = %+v, %v, want ErrPolicyNotMet", ov, err)
	}
	// Unmet and forced: through, and the reason is what gets recorded.
	ov, err := g.Enforce(approvedOnly, short.content, short.stats, forced)
	if err != nil || !ov.Forced || ov.Reason != "the launch is tomorrow" {
		t.Fatalf("Enforce = %+v, %v, want the override recorded", ov, err)
	}
	// Met: nothing recorded, however the caller asked. "Forced" in a
	// history means the policy was overridden, not that somebody passed
	// a flag.
	if ov, err := g.Enforce(approvedOnly, complete.content, complete.stats, forced); err != nil || ov.Forced || ov.Reason != "" {
		t.Fatalf("Enforce = %+v, %v, want no override on a release that meets the gate", ov, err)
	}
}
