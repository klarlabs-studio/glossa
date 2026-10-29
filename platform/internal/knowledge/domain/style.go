package domain

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Style guides are structured, not prose (RFC 0003 §2.3): fields an
// agent and a check can read, plus rules with rationale and examples.
// Every leaf field is optional: unset inherits from a broader guide.
// The JSON tags are the stored shape; never rename one.

// Register is the formality of address.
type Register string

// Registers.
const (
	RegisterFormal   Register = "formal"
	RegisterInformal Register = "informal"
	RegisterNeutral  Register = "neutral"
)

// Formality is how the product addresses people: the register and, for
// languages that mark it, the pronoun (de du/Sie, fr tu/vous, es
// tú/usted).
type Formality struct {
	Register *Register `json:"register,omitempty"`
	Pronoun  *string   `json:"pronoun,omitempty"`
}

// Punctuation holds punctuation and typography preferences.
type Punctuation struct {
	// Quotes are the opening and closing quotation marks ("„“", "«»").
	Quotes       *string `json:"quotes,omitempty"`
	NestedQuotes *string `json:"nested_quotes,omitempty"`
	// Dash is the dash between clauses and ranges: hyphen, en or em.
	Dash *string `json:"dash,omitempty"`
	// SpaceBeforeUnit: "10 km" rather than "10km".
	SpaceBeforeUnit *bool `json:"space_before_unit,omitempty"`
	// SpaceBeforePunctuation: French "Bonjour !" (a narrow no-break space
	// before ; : ! ?).
	SpaceBeforePunctuation *bool   `json:"space_before_punctuation,omitempty"`
	SerialComma            *bool   `json:"serial_comma,omitempty"`
	Ellipsis               *string `json:"ellipsis,omitempty"`
}

// NumberStyle holds number conventions beyond CLDR.
type NumberStyle struct {
	DecimalSeparator  *string `json:"decimal_separator,omitempty"`
	GroupingSeparator *string `json:"grouping_separator,omitempty"`
	Notes             *string `json:"notes,omitempty"`
}

// DateStyle holds date conventions beyond CLDR.
type DateStyle struct {
	// Format is a CLDR date pattern ("d. MMMM y").
	Format *string `json:"format,omitempty"`
	Notes  *string `json:"notes,omitempty"`
}

// StyleFields are a guide's structured fields.
type StyleFields struct {
	Formality Formality `json:"formality"`
	// Tone lists tone tags (concise, friendly, technical). nil inherits;
	// a narrower guide's list replaces a broader one's.
	Tone        []string    `json:"tone,omitempty"`
	Punctuation Punctuation `json:"punctuation"`
	Numbers     NumberStyle `json:"numbers"`
	Dates       DateStyle   `json:"dates"`
}

// StyleRule is one explicit constraint with its reason and examples.
// A rule's ID is its identity across scopes: a narrower guide replaces
// a broader guide's rule with the same ID, or switches it off with
// Disabled.
type StyleRule struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Rationale string   `json:"rationale,omitempty"`
	Good      []string `json:"good,omitempty"`
	Bad       []string `json:"bad,omitempty"`
	Disabled  bool     `json:"disabled,omitempty"`
}

// StyleScope is where a guide applies: the tenant (all nil), a project,
// a locale (with its descendants: de covers de-AT) and a namespace of a
// project, in any combination except a namespace without a project.
type StyleScope struct {
	ProjectID *uuid.UUID
	Locale    *bcp47.Tag
	Namespace string
}

// specificity orders scopes from broadest to narrowest: a namespace
// beats a locale, a deeper locale beats a shallower one, a locale beats
// a project, a project beats the tenant (tenant → project → locale →
// namespace, RFC 0003 §2.3).
func (s StyleScope) specificity() [3]int {
	var k [3]int
	if s.Namespace != "" {
		k[0] = 1
	}
	if s.Locale != nil {
		k[1] = 1 + len(s.Locale.Truncations())
	}
	if s.ProjectID != nil {
		k[2] = 1
	}
	return k
}

// appliesTo reports whether a guide with scope s applies to text of a
// project (nil: tenant-level) in locale and namespace.
func (s StyleScope) appliesTo(project *uuid.UUID, locale bcp47.Tag, namespace string) bool {
	if s.ProjectID != nil && (project == nil || *s.ProjectID != *project) {
		return false
	}
	if s.Locale != nil && (locale.IsZero() || !appliesTo(*s.Locale, locale)) {
		return false
	}
	return s.Namespace == "" || s.Namespace == namespace
}

// StyleGuide is one guide at one scope, versioned.
type StyleGuide struct {
	ID    uuid.UUID
	Scope StyleScope
	Name  string
	StyleContent
	Version   int
	CreatedBy string
	CreatedAt time.Time
	UpdatedBy string
	UpdatedAt time.Time
}

// StyleContent is what a guide says.
type StyleContent struct {
	Fields StyleFields
	Rules  []StyleRule
}

// StyleInput is a guide as written.
type StyleInput struct {
	Name   string
	Fields StyleFields
	Rules  []StyleRule
}

// Limits of a style guide.
const (
	MaxStyleRules     = 200
	MaxRuleExamples   = 20
	MaxStyleNameRunes = 200
	MaxStyleTextRunes = 4000
	MaxToneTags       = 20
)

var (
	ruleIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// NewStyleGuide validates in and creates version 1 at scope.
func NewStyleGuide(id uuid.UUID, scope StyleScope, in StyleInput, by string, now time.Time) (StyleGuide, error) {
	if scope.Namespace != "" {
		if scope.ProjectID == nil {
			return StyleGuide{}, ErrNamespaceScope
		}
		if !namespacePattern.MatchString(scope.Namespace) {
			return StyleGuide{}, fmt.Errorf("%w: namespace %q", ErrInvalidStyleGuide, scope.Namespace)
		}
	}
	g := StyleGuide{ID: id, Scope: scope, CreatedBy: by, CreatedAt: now}
	if err := g.apply(in, by, now); err != nil {
		return StyleGuide{}, err
	}
	return g, nil
}

// Replace replaces the guide's name, fields and rules (never its scope)
// and bumps its version; it reports false when nothing changed.
func (g *StyleGuide) Replace(in StyleInput, by string, now time.Time) (bool, error) {
	next := *g
	if err := next.apply(in, by, now); err != nil {
		return false, err
	}
	if g.Name == next.Name && g.StyleContent.equal(next.StyleContent) {
		return false, nil
	}
	*g = next
	return true, nil
}

func (g *StyleGuide) apply(in StyleInput, by string, now time.Time) error {
	name := strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(name) > MaxStyleNameRunes {
		return fmt.Errorf("%w: name is longer than %d characters", ErrInvalidStyleGuide, MaxStyleNameRunes)
	}
	if err := validateFields(in.Fields); err != nil {
		return err
	}
	if len(in.Rules) > MaxStyleRules {
		return fmt.Errorf("%w: at most %d rules", ErrInvalidStyleRule, MaxStyleRules)
	}
	seen := map[string]bool{}
	for i, r := range in.Rules {
		if err := validateRule(r); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		if seen[r.ID] {
			return fmt.Errorf("%w: rule %q is listed twice", ErrInvalidStyleRule, r.ID)
		}
		seen[r.ID] = true
	}
	g.Name = name
	g.Fields, g.Rules = in.Fields, slices.Clone(in.Rules)
	g.Version++
	g.UpdatedBy, g.UpdatedAt = by, now
	return nil
}

func validateFields(f StyleFields) error {
	if r := f.Formality.Register; r != nil && *r != RegisterFormal && *r != RegisterInformal && *r != RegisterNeutral {
		return fmt.Errorf("%w: register must be formal, informal or neutral", ErrInvalidStyleGuide)
	}
	if d := f.Punctuation.Dash; d != nil && *d != "hyphen" && *d != "en" && *d != "em" {
		return fmt.Errorf("%w: dash must be hyphen, en or em", ErrInvalidStyleGuide)
	}
	if len(f.Tone) > MaxToneTags {
		return fmt.Errorf("%w: at most %d tone tags", ErrInvalidStyleGuide, MaxToneTags)
	}
	for _, t := range f.Tone {
		if t == "" || utf8.RuneCountInString(t) > 50 {
			return fmt.Errorf("%w: tone tags are 1 to 50 characters", ErrInvalidStyleGuide)
		}
	}
	for _, s := range []*string{
		f.Formality.Pronoun, f.Punctuation.Quotes, f.Punctuation.NestedQuotes, f.Punctuation.Ellipsis,
		f.Numbers.DecimalSeparator, f.Numbers.GroupingSeparator, f.Numbers.Notes, f.Dates.Format, f.Dates.Notes,
	} {
		if s != nil && utf8.RuneCountInString(*s) > MaxStyleTextRunes {
			return fmt.Errorf("%w: a field is longer than %d characters", ErrInvalidStyleGuide, MaxStyleTextRunes)
		}
	}
	return nil
}

func validateRule(r StyleRule) error {
	if !ruleIDPattern.MatchString(r.ID) {
		return fmt.Errorf("%w: id %q must be lowercase letters, digits, - and _", ErrInvalidStyleRule, r.ID)
	}
	if !r.Disabled && strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("%w: rule %q needs a title", ErrInvalidStyleRule, r.ID)
	}
	if utf8.RuneCountInString(r.Title) > MaxStyleNameRunes || utf8.RuneCountInString(r.Rationale) > MaxStyleTextRunes {
		return fmt.Errorf("%w: rule %q: title or rationale too long", ErrInvalidStyleRule, r.ID)
	}
	if len(r.Good) > MaxRuleExamples || len(r.Bad) > MaxRuleExamples {
		return fmt.Errorf("%w: rule %q: at most %d examples each", ErrInvalidStyleRule, r.ID, MaxRuleExamples)
	}
	for _, e := range slices.Concat(r.Good, r.Bad) {
		if utf8.RuneCountInString(e) > MaxStyleTextRunes {
			return fmt.Errorf("%w: rule %q: example too long", ErrInvalidStyleRule, r.ID)
		}
	}
	return nil
}

func (c StyleContent) equal(o StyleContent) bool {
	return rulesEqual(c.Rules, o.Rules) && fieldsEqual(c.Fields, o.Fields)
}

func rulesEqual(a, b []StyleRule) bool {
	return slices.EqualFunc(a, b, func(x, y StyleRule) bool {
		return x.ID == y.ID && x.Title == y.Title && x.Rationale == y.Rationale && x.Disabled == y.Disabled &&
			slices.Equal(x.Good, y.Good) && slices.Equal(x.Bad, y.Bad)
	})
}

// fieldsEqual compares leaf values, not pointers.
func fieldsEqual(a, b StyleFields) bool {
	var ma, mb StyleFields
	ma.merge(a)
	mb.merge(b)
	return eq(ma.Formality.Register, mb.Formality.Register) && eq(ma.Formality.Pronoun, mb.Formality.Pronoun) &&
		slices.Equal(ma.Tone, mb.Tone) && (ma.Tone == nil) == (mb.Tone == nil) &&
		eq(ma.Punctuation.Quotes, mb.Punctuation.Quotes) && eq(ma.Punctuation.NestedQuotes, mb.Punctuation.NestedQuotes) &&
		eq(ma.Punctuation.Dash, mb.Punctuation.Dash) && eq(ma.Punctuation.SpaceBeforeUnit, mb.Punctuation.SpaceBeforeUnit) &&
		eq(ma.Punctuation.SpaceBeforePunctuation, mb.Punctuation.SpaceBeforePunctuation) &&
		eq(ma.Punctuation.SerialComma, mb.Punctuation.SerialComma) && eq(ma.Punctuation.Ellipsis, mb.Punctuation.Ellipsis) &&
		eq(ma.Numbers.DecimalSeparator, mb.Numbers.DecimalSeparator) &&
		eq(ma.Numbers.GroupingSeparator, mb.Numbers.GroupingSeparator) && eq(ma.Numbers.Notes, mb.Numbers.Notes) &&
		eq(ma.Dates.Format, mb.Dates.Format) && eq(ma.Dates.Notes, mb.Dates.Notes)
}

func eq[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// merge lays o over f: every leaf o sets wins.
func (f *StyleFields) merge(o StyleFields) {
	over(&f.Formality.Register, o.Formality.Register)
	over(&f.Formality.Pronoun, o.Formality.Pronoun)
	if o.Tone != nil {
		f.Tone = slices.Clone(o.Tone)
	}
	over(&f.Punctuation.Quotes, o.Punctuation.Quotes)
	over(&f.Punctuation.NestedQuotes, o.Punctuation.NestedQuotes)
	over(&f.Punctuation.Dash, o.Punctuation.Dash)
	over(&f.Punctuation.SpaceBeforeUnit, o.Punctuation.SpaceBeforeUnit)
	over(&f.Punctuation.SpaceBeforePunctuation, o.Punctuation.SpaceBeforePunctuation)
	over(&f.Punctuation.SerialComma, o.Punctuation.SerialComma)
	over(&f.Punctuation.Ellipsis, o.Punctuation.Ellipsis)
	over(&f.Numbers.DecimalSeparator, o.Numbers.DecimalSeparator)
	over(&f.Numbers.GroupingSeparator, o.Numbers.GroupingSeparator)
	over(&f.Numbers.Notes, o.Numbers.Notes)
	over(&f.Dates.Format, o.Dates.Format)
	over(&f.Dates.Notes, o.Dates.Notes)
}

func over[T any](dst **T, src *T) {
	if src != nil {
		v := *src
		*dst = &v
	}
}

// GuideRef names the version of a guide an effective style used; it
// goes into a suggestion's provenance.
type GuideRef struct {
	ID      uuid.UUID
	Version int
	Scope   StyleScope
}

// EffectiveStyle is the style that applies to one project, locale and
// namespace: every applicable guide merged, narrowest last.
type EffectiveStyle struct {
	Fields StyleFields
	Rules  []StyleRule
	// Sources are the guides merged, broadest first.
	Sources []GuideRef
}

// EffectiveStyleOf merges the guides that apply to text of project
// (nil: tenant-level only) in locale and namespace, broadest first: a
// narrower guide's set fields win leaf by leaf, its rules replace
// broader rules with the same ID, and a disabled rule removes one.
// Rules keep the order in which they first appear.
func EffectiveStyleOf(guides []StyleGuide, project *uuid.UUID, locale bcp47.Tag, namespace string) EffectiveStyle {
	var applicable []StyleGuide
	for _, g := range guides {
		if g.Scope.appliesTo(project, locale, namespace) {
			applicable = append(applicable, g)
		}
	}
	slices.SortStableFunc(applicable, func(a, b StyleGuide) int {
		ka, kb := a.Scope.specificity(), b.Scope.specificity()
		for i := range ka {
			if c := cmp.Compare(ka[i], kb[i]); c != 0 {
				return c
			}
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	var (
		e     EffectiveStyle
		order []string
		rules = map[string]StyleRule{}
	)
	for _, g := range applicable {
		e.Fields.merge(g.Fields)
		for _, r := range g.Rules {
			if _, ok := rules[r.ID]; !ok {
				order = append(order, r.ID)
			}
			rules[r.ID] = r
		}
		e.Sources = append(e.Sources, GuideRef{ID: g.ID, Version: g.Version, Scope: g.Scope})
	}
	for _, id := range order {
		if r := rules[id]; !r.Disabled {
			e.Rules = append(e.Rules, r)
		}
	}
	return e
}
