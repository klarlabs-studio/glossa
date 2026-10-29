package checkpolicy

import (
	"fmt"
	"slices"
)

// Rules are the policy document's selectors (RFC 0005 §4.1): the part
// that says a different thing about a different layer, code, locale,
// namespace or environment, instead of one severity for the whole
// project.
//
// A rule is a selector plus what to do with what it selects. The
// selector is a subset of five fields; every field it names must match,
// and every field it leaves out matches anything. The evaluation is
// pure: given a Target — what a layer found, and where — the policy
// answers with a Decision, which is the severity the finding has here,
// whether it can fail the run, and which rule said so. "Why did this
// fail?" has a mechanical answer (§4.3).

// Mode is what a rule's severity is allowed to do to a run.
type Mode string

// Modes.
const (
	// ModeEnforce is the default: the rule's severity counts and can
	// change the conclusion.
	ModeEnforce Mode = "enforce"
	// ModeWarn computes and reports at the rule's severity but can never
	// change the conclusion. It is the on-ramp for a stricter policy:
	// ship the rule, watch the number, flip it (RFC 0005 §4.3).
	ModeWarn Mode = "warn"
)

// Off is the severity that means "don't compute". A layer a rule
// switches off is not run at all, so a project does not pay for QA it
// ignores, and its findings are neither reported nor counted.
const Off Severity = "off"

// Layers are the QA layers a rule may select, spelled as
// quality/domain spells them (RFC 0005 §3).
//
// The kernel owns the list because the policy is what selects on it and
// the policy has to be able to reject a rule naming a layer that does
// not exist. quality/domain.Layers mirrors it and a test there proves
// the two agree.
var Layers = []string{
	"structure", "parity", "completeness", "terminology", "style",
	"length", "locale", "source", "visual", "linguistic",
}

// AdvisoryLayers are the layers a model decides. A policy may not raise
// one to Error: a build never fails on an opinion (RFC 0005 §14
// decision 10).
var AdvisoryLayers = []string{"linguistic"}

// Advisory reports whether a layer is model-decided.
func Advisory(layer string) bool { return slices.Contains(AdvisoryLayers, layer) }

// Selector picks the findings a rule is about. Every field it names
// must match the finding; every field it leaves out matches anything.
//
// A field the *target* does not carry never matches a rule that names
// it: a rule for `environment: production` says nothing about a pull
// request's branch check, which runs in no environment at all.
type Selector struct {
	Layer       string `json:"layer,omitempty"`
	Code        string `json:"code,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// selectorFields is the selector's fields in their canonical order,
// which is the order Specificity counts them in and the order a rule
// prints in.
func (s Selector) fields() [5]string {
	return [5]string{s.Layer, s.Code, s.Locale, s.Namespace, s.Environment}
}

// Specificity is how many fields the selector names. It is the first
// half of precedence: the rule matching on more fields wins.
func (s Selector) Specificity() int {
	n := 0
	for _, f := range s.fields() {
		if f != "" {
			n++
		}
	}
	return n
}

// Matches reports whether the selector selects t.
func (s Selector) Matches(t Target) bool {
	sel, tgt := s.fields(), Selector{
		Layer: t.Layer, Code: t.Code, Locale: t.Locale,
		Namespace: t.Namespace, Environment: t.Environment,
	}.fields()
	for i, f := range sel {
		if f != "" && f != tgt[i] {
			return false
		}
	}
	return true
}

// Rule is one line of the policy document: what it selects, what that
// is worth, and whether it may fail a run yet.
type Rule struct {
	Selector
	// Severity is Error, Warning or Off.
	Severity Severity `json:"severity"`
	// Mode is ModeEnforce (the default, and what "" means) or ModeWarn.
	Mode Mode `json:"mode,omitempty"`
}

// mode is the rule's mode with "" spelled out.
func (r Rule) mode() Mode {
	if r.Mode == ModeWarn {
		return ModeWarn
	}
	return ModeEnforce
}

// Target is what the policy grades: a finding's layer, code and locus,
// and the severity the layer gave it.
type Target struct {
	Layer     string
	Code      string
	Locale    string
	Namespace string
	// Environment is the environment the run is about; "" for a branch
	// check, which is every check in CI.
	Environment string
	// Severity is what the layer emitted. It stands where no rule
	// matches, because a policy that says nothing about a finding does
	// not change it.
	Severity Severity
	// Advisory marks a finding a model decided. It is never raised to
	// Error, whatever a rule asks for.
	Advisory bool
	// Provisional marks a finding that is not yet evidence: a visual
	// finding seen fewer times than VisualThresholds.PromoteAfterSightings
	// asks for (RFC 0005 §5.2). Like an advisory one, its severity is
	// never raised to Error — a single sighting of a text metric that
	// moves with font availability cannot be allowed to fail a build,
	// however strict the policy is.
	Provisional bool
}

// Decision is what the policy decided about one target, and why.
type Decision struct {
	// Severity is the finding's severity here: Error, Warning or Off.
	Severity Severity
	Mode     Mode
	// Rule is the index of the deciding rule in the document's Rules, or
	// -1 when no rule matched and the layer's own severity stands. It is
	// what `glossa check --explain-policy` prints.
	Rule int
	// Clamped says a rule asked for Error on a finding that may not have
	// one and got Warning instead: an advisory layer's (RFC 0005 §14
	// decision 10), or a visual finding on its first sighting (§5.2).
	// Validation rejects a rule that names an advisory layer outright;
	// this catches the wildcard that raises everything without naming
	// it, and the sighting a rule cannot know about.
	Clamped bool
}

// Computed reports whether the finding is computed and reported at all.
func (d Decision) Computed() bool { return d.Severity != Off }

// Enforced reports whether the decision may change a run's conclusion.
func (d Decision) Enforced() bool { return d.Mode != ModeWarn }

// Decide answers what severity t has under this policy.
//
// Precedence is by specificity, exactly as the schema states it:
//
//  1. Only rules whose every named field matches t are candidates.
//  2. Of those, the one naming the most fields wins.
//  3. Ties — two rules naming the same number of fields — go to the one
//     later in the document, so a rule appended to a policy always wins
//     against the rule it was written to override.
//
// The ordering is total: every pair of candidate rules is separated
// either by specificity or by position, and no two rules share a
// position. A policy nobody can predict is worse than no policy.
func (p Policy) Decide(t Target) Decision {
	d := Decision{Severity: t.Severity, Mode: ModeEnforce, Rule: -1}
	best := MostSpecific(p.Rules, func(r Rule) bool { return r.Matches(t) }, Rule.Specificity)
	if best >= 0 {
		d.Severity, d.Mode, d.Rule = p.Rules[best].Severity, p.Rules[best].mode(), best
	}
	if (t.Advisory || Advisory(t.Layer) || t.Provisional) && d.Severity == Error {
		d.Severity, d.Clamped = Warning, true
	}
	return d
}

// MostSpecific is the precedence rule itself, separated from what it
// orders so that every selector in the platform resolves the same way:
// the check policy's rules here, and Workflow's bindings (RFC 0006
// §2.3), which RFC 0006 asks to use "the check policy's precedence
// rule … one rule, shared, not a second one to learn".
//
// Of the candidates that match, the one with the highest specificity
// wins; ties go to the one later in candidates. It returns the winner's
// index, or -1 when nothing matches. The caller decides what "later"
// means by the order it passes candidates in — document order for a
// policy's rules, creation order for bindings.
func MostSpecific[T any](candidates []T, matches func(T) bool, specificity func(T) int) int {
	best, bestSpec := -1, 0
	for i, c := range candidates {
		if !matches(c) {
			continue
		}
		// >= and not >: candidates are visited in order, so taking the
		// later one on a tie is what "ties go to the later rule" means.
		if s := specificity(c); best < 0 || s >= bestSpec {
			best, bestSpec = i, s
		}
	}
	return best
}

// Computes reports whether the layer runs at all in environment env: a
// rule may switch a whole layer off, and then nothing computes it.
//
// It asks the same question Decide answers, with only the fields a
// planner knows before any finding exists — so a rule about one locale
// or one code can never switch the layer off for everything.
func (p Policy) Computes(layer, env string) bool {
	return p.Decide(Target{Layer: layer, Environment: env, Severity: Warning}).Computed()
}

// FailsDecision reports whether a decision fails the run: the finding
// is computed, the rule is enforcing rather than warning, and the
// severity is at or above fail_on.
func (p Policy) FailsDecision(d Decision) bool {
	return d.Computed() && d.Enforced() && p.Fails(d.Severity)
}

// ParseRuleSeverity validates the severity a rule may set: error,
// warning or off. Unlike a finding's, "" is not a default here — a rule
// that says nothing about severity says nothing at all.
func ParseRuleSeverity(s string) (Severity, error) {
	switch Severity(s) {
	case Error:
		return Error, nil
	case Warning:
		return Warning, nil
	case Off:
		return Off, nil
	}
	return "", fmt.Errorf("%w: a rule's severity is error, warning or off, not %q", ErrInvalidSeverity, s)
}

// ParseMode validates a rule's mode: enforce or warn. "" is
// ModeEnforce, the default.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "", ModeEnforce:
		return ModeEnforce, nil
	case ModeWarn:
		return ModeWarn, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidMode, s)
}

// Validate checks one rule and returns it normalized.
func (r Rule) Validate() (Rule, error) {
	if r.Layer != "" && !slices.Contains(Layers, r.Layer) {
		return Rule{}, fmt.Errorf("%w: %q", ErrUnknownLayer, r.Layer)
	}
	sev, err := ParseRuleSeverity(string(r.Severity))
	if err != nil {
		return Rule{}, err
	}
	mode, err := ParseMode(string(r.Mode))
	if err != nil {
		return Rule{}, err
	}
	if sev == Error && Advisory(r.Layer) {
		return Rule{}, fmt.Errorf("%w: the %s layer is what a model decided, and a build never fails on an opinion;"+
			" the highest severity a rule may give it is warning", ErrAdvisoryLayer, r.Layer)
	}
	r.Severity, r.Mode = sev, mode
	return r, nil
}
