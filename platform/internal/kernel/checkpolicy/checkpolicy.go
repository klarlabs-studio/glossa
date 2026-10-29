// Package checkpolicy is a project's check policy and its evaluator:
// which locales must be complete, what severity a finding has here, and
// what fails the run.
//
// Since M4 the policy is a versioned *document* (RFC 0005 §4.1) rather
// than three fields. It keeps the three — `require_complete`, `fail_on`
// and `missing_translations` are still the base every project starts
// from, and a project that stored them before M4 reads as a version-0
// document that decides every question exactly as it did — and adds
// what different locales, namespaces and environments genuinely deserve
// different answers about:
//
//   - `rules`, each selecting on layer, code, locale, namespace and
//     environment, and setting a severity (Rule, §4.1). Precedence is by
//     specificity; see Decide, which is the whole ordering.
//   - `environments`, where completeness and review are asked for
//     differently in production than on a branch.
//   - `version`, `effective_from`, `grace_until` and `previous`, which
//     are how a stricter policy rolls out without turning forty open
//     pull requests red (§4.3, Effective).
//
// The package answers two questions and nothing else: "what severity
// does this finding have here?" (Decide) and "does this run fail?"
// (Fails, FailsDecision). It reads no file, no database and no clock it
// was not handed.
//
// It lives in the kernel because two callers decide the same question
// and must decide it the same way: `glossa check` in the CLI (where the
// policy is read from `glossa.yaml` as `require_complete` and `fail_on`)
// and the Glossa PR check on the server, which reports "per the
// project's check policy (the same `require_complete` and `fail_on` as
// `glossa check`)" (RFC 0004 §6.4). A second, subtly different policy
// would make the PR check disagree with the command developers run
// locally, which is the one thing a check must never do.
//
// The project stores one (Catalog's `settings.check_policy`), so the
// two agree by construction rather than by both defaulting to the same
// thing: the PR check reads the stored policy, and `glossa check` reads
// it too whenever it can reach the server.
//
// The codes are shared for the same reason: a finding a person saw as
// `missing-translation` on their terminal is the same finding in the
// pull request.
package checkpolicy

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// Schema is the policy document's wire schema (RFC 0005 §4.1). A
// document that names none is read as this one: there has never been
// another.
const Schema = "glossa.check-policy/v1"

// Severity ranks a finding.
type Severity string

// Severities. Never ranks no finding: it is a FailOn value alone,
// meaning nothing the check finds fails it.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Never   Severity = "never"
)

// Policy errors.
var (
	// ErrInvalidSeverity is a fail_on or missing_translations that names
	// no severity.
	ErrInvalidSeverity = errors.New("checkpolicy: invalid severity")
	// ErrUnknownLocale is a required locale the project does not have.
	ErrUnknownLocale = errors.New("checkpolicy: the project has no such locale")
	// ErrInvalidMode is a rule's mode that is neither enforce nor warn.
	ErrInvalidMode = errors.New("checkpolicy: a rule's mode is enforce or warn")
	// ErrUnknownLayer is a rule selecting a layer that does not exist.
	ErrUnknownLayer = errors.New("checkpolicy: unknown layer")
	// ErrAdvisoryLayer is a rule raising a model-decided layer to error
	// (RFC 0005 §14 decision 10).
	ErrAdvisoryLayer = errors.New("checkpolicy: an advisory layer may not be raised to error")
	// ErrInvalidDocument is a document whose bookkeeping does not hold
	// together: a negative version, a schema it does not have, or a
	// history nested more than one version deep.
	ErrInvalidDocument = errors.New("checkpolicy: invalid policy document")
	// ErrInvalidEnvironment is an environment block that asks for
	// something the policy cannot say.
	ErrInvalidEnvironment = errors.New("checkpolicy: invalid environment")
)

// Finding codes Glossa adds to the MessageFormat kernel's (compat
// findings keep the kernel's codes, e.g. missing-argument).
const (
	CodeInvalidMessage      = "invalid-message"
	CodeInvalidTranslation  = "invalid-translation"
	CodeMissingTranslation  = "missing-translation"
	CodeOutdatedTranslation = "outdated-translation"
	CodeUnknownKey          = "unknown-key"
	CodeMissingLocale       = "missing-locale"
	// CodeKeyConflict is two open branches proposing the same new key
	// with different source (RFC 0004 §4.1). Only the PR check can see
	// it, because only the server knows the other branches.
	CodeKeyConflict = "key-conflict"
)

// Policy is the check-policy document and the evaluator of it. Its
// zero value is the documented default: every locale must be complete,
// an untranslated key in one is an error, errors fail, and no rule says
// anything else — which is what every project written before M4 reads
// as, so the document changes what a policy can say and nothing about
// what the policies that exist decide.
type Policy struct {
	// Schema is Schema. A document that stores none is that one.
	Schema string `json:"schema,omitempty"`
	// Version is monotonic: every saved policy has one, and every check
	// run records the version it graded itself against, so a run can say
	// which it used when two are live at once (RFC 0005 §4.3). 0 is the
	// pre-M4 policy, which had no versions.
	Version int `json:"version,omitempty"`
	// RequireComplete lists the locales whose missing translations are
	// errors; nil means every locale, and an empty slice means none.
	// Others' are warnings.
	RequireComplete []string `json:"require_complete"`
	// FailOn is the lowest severity that fails the check: Error (the
	// default, and what "" means), Warning, or Never — nothing fails it,
	// and the check only reports.
	FailOn Severity `json:"fail_on,omitempty"`
	// MissingTranslations is the severity a key with no translation gets
	// in a locale RequireComplete names: Error (the default, and what ""
	// means) or Warning. A locale it does not name warns either way.
	//
	// It is the one knob that is not about *reporting* the gap but about
	// whether the gap blocks: a team that translates after merging sets
	// it to Warning and keeps every locale required, so the check still
	// lists what is untranslated without failing the pull request.
	MissingTranslations Severity `json:"missing_translations,omitempty"`
	// Environments asks for something different where something
	// different is true: production may require locales a branch does
	// not, and a review state a branch does not.
	Environments map[string]Environment `json:"environments,omitempty"`
	// Rules are the selectors, in document order. Order is part of the
	// meaning: ties in specificity go to the later rule (Decide).
	Rules []Rule `json:"rules,omitempty"`
	// EffectiveFrom is when this version was saved. A pull request older
	// than it is what GraceUntil pins to Previous.
	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
	// GraceUntil is when the pinning ends. Until then a check for a pull
	// request opened before EffectiveFrom grades against Previous, and
	// says so; after it, everything grades against this version
	// (RFC 0005 §4.3).
	GraceUntil *time.Time `json:"grace_until,omitempty"`
	// Previous is the version a pinned pull request grades against. It
	// is exactly one version deep: a policy saved three times in a
	// fortnight pins to the version before the last, because that is the
	// one whose grace is still running.
	Previous *Policy `json:"previous,omitempty"`
}

// Requires reports whether locale must be complete, outside any
// environment — which is every check in CI.
func (p Policy) Requires(locale string) bool { return p.RequiresIn("", locale) }

// RequiresIn reports whether locale must be complete in env. An
// environment that names no require_complete of its own inherits the
// document's, so naming an environment only to ask for a review state
// does not quietly change what must be translated.
func (p Policy) RequiresIn(env, locale string) bool {
	required := p.RequireComplete
	if e, ok := p.Environments[env]; ok && e.RequireComplete.Set {
		if e.RequireComplete.All {
			return true
		}
		required = e.RequireComplete.Locales
	}
	if required == nil {
		return true
	}
	return slices.Contains(required, locale)
}

// In returns the document as it applies in env: the environment's
// require_complete resolved into the base, and everything else as it
// is.
//
// It is how a run hands an environment down to the layers. A layer asks
// the policy whether a locale must be complete and knows nothing about
// environments, which is right — where a finding is graded is the
// run's business, not the layer's — so the run resolves the block once,
// at the top, and every layer below it asks the same question and gets
// the environment's answer.
func (p Policy) In(env string) Policy {
	e, ok := p.Environments[env]
	if !ok || !e.RequireComplete.Set {
		return p
	}
	if e.RequireComplete.All {
		p.RequireComplete = nil
		return p
	}
	p.RequireComplete = slices.Clone(e.RequireComplete.Locales)
	if p.RequireComplete == nil {
		p.RequireComplete = []string{}
	}
	return p
}

// ReviewIn is the review state an environment requires before a release
// may publish to it, or "" where it asks for none. Release enforces it
// at publish (RFC 0005 §4.1); the policy only states it.
func (p Policy) ReviewIn(env string) string { return p.Environments[env].RequireReview }

// Fails reports whether a finding of severity s fails the check.
func (p Policy) Fails(s Severity) bool {
	switch p.FailOn {
	case Never:
		return false
	case Warning:
		return s == Error || s == Warning
	default:
		return s == Error
	}
}

// Severity of a missing translation in locale: an error where the
// policy requires the locale to be complete and missing translations
// are errors, a warning elsewhere.
//
// It is the severity the *completeness layer emits*, which the rules
// then grade like any other finding: a policy can still say that a
// missing translation in one namespace is only a warning.
func (p Policy) Severity(locale string) Severity { return p.SeverityIn("", locale) }

// SeverityIn is Severity in an environment.
func (p Policy) SeverityIn(env, locale string) Severity {
	if p.RequiresIn(env, locale) && p.MissingTranslations != Warning {
		return Error
	}
	return Warning
}

// BaseOnly reports whether the policy says nothing the pre-M4 shape
// could not say: no schema, no version, no rules, no environments and
// no history.
//
// It is how a caller that can only speak the three fields is told
// apart from one writing a document (see WithBase).
func (p Policy) BaseOnly() bool {
	return p.Schema == "" && p.Version == 0 && p.Rules == nil && p.Environments == nil &&
		p.EffectiveFrom == nil && p.GraceUntil == nil && p.Previous == nil
}

// WithBase returns p with its three base fields — require_complete,
// fail_on and missing_translations — taken from b, and everything the
// document adds kept as it is.
//
// It is how a write that can only say the three changes them without
// deleting the rules a project set through the policy document: the
// project-settings API speaks the pre-M4 shape, and will until the
// policy API of RFC 0005 §13 wave 3.
func (p Policy) WithBase(b Policy) Policy {
	p.RequireComplete, p.FailOn, p.MissingTranslations = b.RequireComplete, b.FailOn, b.MissingTranslations
	return p
}

// Equal reports whether p and o are the same document. Two documents
// that decide alike today but say different things are not equal: what
// a project stores is what it says.
func (p Policy) Equal(o Policy) bool {
	if !slices.Equal(p.RequireComplete, o.RequireComplete) ||
		p.FailOn != o.FailOn || p.MissingTranslations != o.MissingTranslations {
		return false
	}
	if p.Schema != o.Schema || p.Version != o.Version || !slices.Equal(p.Rules, o.Rules) {
		return false
	}
	if !maps.EqualFunc(p.Environments, o.Environments, Environment.Equal) {
		return false
	}
	if !sameTime(p.EffectiveFrom, o.EffectiveFrom) || !sameTime(p.GraceUntil, o.GraceUntil) {
		return false
	}
	if (p.Previous == nil) != (o.Previous == nil) {
		return false
	}
	return p.Previous == nil || p.Previous.Equal(*o.Previous)
}

func sameTime(a, b *time.Time) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Equal(*b)
}

// ParseFailOn validates a fail_on: error, warning or never. "" is
// Error, the default.
func ParseFailOn(s string) (Severity, error) {
	switch Severity(s) {
	case "", Error:
		return Error, nil
	case Warning:
		return Warning, nil
	case Never:
		return Never, nil
	}
	return "", fmt.Errorf("%w: fail_on is error, warning or never, not %q", ErrInvalidSeverity, s)
}

// ParseFindingSeverity validates the severity a finding may carry:
// error or warning. "" is Error, the default.
func ParseFindingSeverity(s string) (Severity, error) {
	switch Severity(s) {
	case "", Error:
		return Error, nil
	case Warning:
		return Warning, nil
	}
	return "", fmt.Errorf("%w: missing_translations is error or warning, not %q", ErrInvalidSeverity, s)
}

// Validate checks the policy and returns it normalized: the severities
// spelled out, and a RequireComplete that names no locale left as the
// empty slice ("none") rather than as a slice of nothing in particular.
//
// known is the project's locales, the source locale included; every
// required locale must be one of them. A nil known skips that check,
// for a caller that does not know the project's locales (the CLI's
// flags, which the check itself reports as missing-locale).
func (p Policy) Validate(known []string) (Policy, error) {
	failOn, err := ParseFailOn(string(p.FailOn))
	if err != nil {
		return Policy{}, err
	}
	missing, err := ParseFindingSeverity(string(p.MissingTranslations))
	if err != nil {
		return Policy{}, err
	}
	out := p
	out.FailOn, out.MissingTranslations = failOn, missing
	// A document that names no schema is this one, and is left naming
	// none: the three-field policies stored before M4 are valid
	// documents, and validating one must not rewrite what the project
	// stored.
	if out.Schema != "" && out.Schema != Schema {
		return Policy{}, fmt.Errorf("%w: schema is %q, not %q", ErrInvalidDocument, out.Schema, Schema)
	}
	if out.Version < 0 {
		return Policy{}, fmt.Errorf("%w: version %d is not monotonic", ErrInvalidDocument, out.Version)
	}
	if err := requireKnown(out.RequireComplete, known); err != nil {
		return Policy{}, err
	}
	if out.RequireComplete != nil && len(out.RequireComplete) == 0 {
		// An empty list is "no locale has to be complete". Keep it
		// non-nil and canonical, because nil means the opposite.
		out.RequireComplete = []string{}
	}
	if out.Environments, err = validateEnvironments(out.Environments, known); err != nil {
		return Policy{}, err
	}
	if out.Rules != nil {
		rules := make([]Rule, 0, len(out.Rules))
		for i, r := range out.Rules {
			v, err := r.Validate()
			if err != nil {
				return Policy{}, fmt.Errorf("rule %d: %w", i, err)
			}
			rules = append(rules, v)
		}
		out.Rules = rules
	}
	if out.Previous != nil {
		prev, err := out.Previous.validatePrevious(known, out.Version)
		if err != nil {
			return Policy{}, err
		}
		if out.EffectiveFrom == nil {
			return Policy{}, fmt.Errorf(
				"%w: a document that keeps a previous version needs an effective_from to pin against",
				ErrInvalidDocument)
		}
		out.Previous = &prev
	}
	return out, nil
}

// validatePrevious checks the one version a document keeps behind it.
// A history two versions deep would mean two graces running at once and
// a check that cannot say which policy it used.
func (p Policy) validatePrevious(known []string, version int) (Policy, error) {
	if p.Previous != nil {
		return Policy{}, fmt.Errorf("%w: a policy's history is one version deep", ErrInvalidDocument)
	}
	if p.Version >= version {
		return Policy{}, fmt.Errorf("%w: the previous version (%d) must precede this one (%d)",
			ErrInvalidDocument, p.Version, version)
	}
	out, err := p.Validate(known)
	if err != nil {
		return Policy{}, fmt.Errorf("previous: %w", err)
	}
	return out, nil
}

// requireKnown checks required locales against the project's.
func requireKnown(required, known []string) error {
	if required == nil || known == nil {
		return nil
	}
	for _, l := range required {
		if !slices.Contains(known, l) {
			return fmt.Errorf("%w: %q", ErrUnknownLocale, l)
		}
	}
	return nil
}
