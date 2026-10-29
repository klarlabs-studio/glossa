package messageformat

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"go.klarlabs.de/glossa/messageformat/internal/cldr"
)

// Structural QA (product intent §29.1): is a translation structurally
// compatible with its source? testdata/glossa/compat.json is the shared
// specification.

// Severity ranks a finding.
type Severity string

// Severities. An error breaks the message at runtime or loses meaning; a
// warning is likely but not certainly wrong.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// FindingCode is a stable, machine-readable finding identifier.
type FindingCode string

// Finding codes.
const (
	// FindingInvalidMessage: the translation is not a valid MF2 message.
	// Detail holds the MF2 error code (e.g. missing-fallback-variant).
	FindingInvalidMessage FindingCode = "invalid-message"
	// FindingInvalidLocale: the target locale is not a BCP 47 tag.
	FindingInvalidLocale FindingCode = "invalid-locale"
	// FindingMissingArgument: a source argument is not in the translation.
	FindingMissingArgument FindingCode = "missing-argument"
	// FindingExtraArgument: the translation uses an argument the source
	// doesn't provide.
	FindingExtraArgument FindingCode = "extra-argument"
	// FindingArgumentTypeChanged: the translation formats an argument as a
	// different type. Detail is "<source type>-><translation type>".
	FindingArgumentTypeChanged FindingCode = "argument-type-changed"
	// FindingSelectorAdded: the translation selects on an argument the
	// source doesn't select on (warning: some languages need it).
	FindingSelectorAdded FindingCode = "selector-added"
	// FindingSelectorRemoved: the source selects on an argument the
	// translation doesn't (warning; not reported for plurals when the
	// target locale has no plural distinctions).
	FindingSelectorRemoved FindingCode = "selector-removed"
	// FindingSelectorChanged: the translation selects on different
	// arguments than the source. Detail names the translation's selector.
	FindingSelectorChanged FindingCode = "selector-changed"
	// FindingSelectorKindChanged: an argument selects differently (e.g.
	// plural vs ordinal). Detail is "<source kind>-><translation kind>".
	FindingSelectorKindChanged FindingCode = "selector-kind-changed"
	// FindingInvalidPluralKey: a plural or ordinal variant key that is
	// neither a number nor a CLDR category of the target locale. Detail
	// is the key.
	FindingInvalidPluralKey FindingCode = "invalid-plural-key"
	// FindingMissingPluralCategory: a CLDR category of the target locale
	// only reaches the catch-all variant. Detail is the category.
	FindingMissingPluralCategory FindingCode = "missing-plural-category"
	// FindingMarkupUnbalanced: markup opened and not closed, closed without
	// being opened, or closed out of order.
	FindingMarkupUnbalanced FindingCode = "markup-unbalanced"
	// FindingMarkupMissing: a source markup element is not in the translation.
	FindingMarkupMissing FindingCode = "markup-missing"
	// FindingMarkupExtra: the translation has a markup element the source
	// doesn't.
	FindingMarkupExtra FindingCode = "markup-extra"
	// FindingMarkupRenamed: a source markup element appears under another
	// name. Detail is the new name.
	FindingMarkupRenamed FindingCode = "markup-renamed"
)

// Finding is one structural QA result.
type Finding struct {
	Code     FindingCode `json:"code"`
	Severity Severity    `json:"severity"`
	// Subject is the argument or markup element concerned, if any.
	Subject string `json:"subject,omitempty"`
	// Detail qualifies the finding (see each code).
	Detail string `json:"detail,omitempty"`
	// Message is a human-readable explanation; its wording is not stable.
	Message string `json:"message"`
}

// CheckCompat checks that translation is structurally compatible with
// source for targetLocale. The source is taken as correct; only the
// translation is validated. Findings come in a stable order: validity,
// arguments, selectors, plural keys, markup. No findings means compatible.
func CheckCompat(source, translation Message, targetLocale string) []Finding {
	findings := []Finding{}
	findings = append(findings, checkValidity(translation)...)
	srcArgs, trArgs := Arguments(source), Arguments(translation)
	findings = append(findings, checkArguments(srcArgs, trArgs)...)
	categories, localeFinding := localeCategories(targetLocale)
	if localeFinding != nil {
		findings = append(findings, *localeFinding)
	}
	findings = append(findings, checkSelectors(srcArgs, trArgs, categories)...)
	findings = append(findings, checkPluralKeys(trArgs, categories)...)
	findings = append(findings, checkMarkup(source, translation)...)
	return findings
}

func checkValidity(translation Message) []Finding {
	err := Validate(translation)
	if err == nil {
		return nil
	}
	code := CodeInvalidMessage
	var mfErr *Error
	if errors.As(err, &mfErr) {
		code = mfErr.Code
	}
	return []Finding{{
		Code: FindingInvalidMessage, Severity: SeverityError, Detail: string(code),
		Message: fmt.Sprintf("the translation is not a valid MessageFormat 2 message: %v", err),
	}}
}

// --- arguments ------------------------------------------------------------------

func checkArguments(src, tr []Argument) []Finding {
	var out []Finding
	trByName := argumentsByName(tr)
	srcByName := argumentsByName(src)
	for _, s := range src {
		if _, ok := trByName[s.Name]; !ok {
			out = append(out, Finding{
				Code: FindingMissingArgument, Severity: SeverityError, Subject: s.Name,
				Message: fmt.Sprintf("the translation does not use {$%s}", s.Name),
			})
		}
	}
	for _, t := range tr {
		if _, ok := srcByName[t.Name]; !ok {
			out = append(out, Finding{
				Code: FindingExtraArgument, Severity: SeverityError, Subject: t.Name,
				Message: fmt.Sprintf("the translation uses {$%s}, which the source does not provide", t.Name),
			})
		}
	}
	for _, s := range src {
		if t, ok := trByName[s.Name]; ok {
			if f, changed := typeChange(s, t); changed {
				out = append(out, f)
			}
		}
	}
	return out
}

// typeChange compares an argument's type in source and translation.
// Selection differences are left to the selector checks.
func typeChange(s, t Argument) (Finding, bool) {
	st, tt := typeFamily(s.Type), typeFamily(t.Type)
	if st == tt || (s.Selector != nil && t.Selector != nil && s.Selector.Kind != t.Selector.Kind) {
		return Finding{}, false
	}
	severity := SeverityError
	switch {
	case tt == ArgString:
		severity = SeverityWarning // formatted as plain text: works, loses localization
	case isNumeric(st) && isNumeric(tt):
		severity = SeverityWarning // number <-> integer
	}
	return Finding{
		Code: FindingArgumentTypeChanged, Severity: severity, Subject: s.Name,
		Detail:  fmt.Sprintf("%s->%s", s.Type, t.Type),
		Message: fmt.Sprintf("{$%s} is a %s in the source but a %s in the translation", s.Name, s.Type, t.Type),
	}, true
}

// typeFamily folds select into string: whether an argument selects is a
// selector question, not a type question.
func typeFamily(t ArgumentType) ArgumentType {
	if t == ArgSelect {
		return ArgString
	}
	return t
}

func isNumeric(t ArgumentType) bool { return t == ArgNumber || t == ArgInteger }

func argumentsByName(args []Argument) map[string]Argument {
	out := make(map[string]Argument, len(args))
	for _, a := range args {
		out[a.Name] = a
	}
	return out
}

// --- selectors ------------------------------------------------------------------

// pluralCategories holds the target locale's CLDR categories.
type pluralCategories struct {
	cardinal, ordinal []string
}

func (c *pluralCategories) forKind(kind SelectorKind) ([]string, bool) {
	switch {
	case c == nil:
		return nil, false
	case kind == SelectPlural:
		return c.cardinal, true
	case kind == SelectOrdinal:
		return c.ordinal, true
	default:
		return nil, false
	}
}

func localeCategories(tag string) (*pluralCategories, *Finding) {
	cardinal, err := cldr.PluralCategories(tag, false)
	if err == nil {
		var ordinal []string
		if ordinal, err = cldr.PluralCategories(tag, true); err == nil {
			return &pluralCategories{cardinal: cardinal, ordinal: ordinal}, nil
		}
	}
	return nil, &Finding{
		Code: FindingInvalidLocale, Severity: SeverityError, Subject: tag,
		Message: fmt.Sprintf("the target locale %q is not a valid BCP 47 tag; plural keys were not checked", tag),
	}
}

func checkSelectors(src, tr []Argument, cats *pluralCategories) []Finding {
	srcSel, trSel := selectorArgs(src), selectorArgs(tr)
	var added, removed []Argument
	for _, t := range trSel {
		if !containsArg(srcSel, t.Name) {
			added = append(added, t)
		}
	}
	for _, s := range srcSel {
		if !containsArg(trSel, s.Name) && !pluralWithoutDistinctions(s, cats) {
			removed = append(removed, s)
		}
	}
	var out []Finding
	if len(added) > 0 && len(removed) > 0 {
		for i, s := range removed {
			t := added[min(i, len(added)-1)]
			out = append(out, Finding{
				Code: FindingSelectorChanged, Severity: SeverityError, Subject: s.Name, Detail: t.Name,
				Message: fmt.Sprintf("the source selects on $%s, the translation on $%s instead", s.Name, t.Name),
			})
		}
	} else {
		for _, t := range added {
			out = append(out, Finding{
				Code: FindingSelectorAdded, Severity: SeverityWarning, Subject: t.Name,
				Message: fmt.Sprintf("the translation selects on $%s; the source does not", t.Name),
			})
		}
		for _, s := range removed {
			out = append(out, Finding{
				Code: FindingSelectorRemoved, Severity: SeverityWarning, Subject: s.Name,
				Message: fmt.Sprintf("the source selects on $%s; the translation does not", s.Name),
			})
		}
	}
	return append(out, selectorKindChanges(srcSel, trSel)...)
}

func selectorKindChanges(srcSel, trSel []Argument) []Finding {
	var out []Finding
	for _, s := range srcSel {
		i := slices.IndexFunc(trSel, func(t Argument) bool { return t.Name == s.Name })
		if i < 0 || trSel[i].Selector.Kind == s.Selector.Kind {
			continue
		}
		t := trSel[i]
		out = append(out, Finding{
			Code: FindingSelectorKindChanged, Severity: SeverityError, Subject: s.Name,
			Detail:  fmt.Sprintf("%s->%s", s.Selector.Kind, t.Selector.Kind),
			Message: fmt.Sprintf("$%s selects by %s in the source but by %s in the translation", s.Name, s.Selector.Kind, t.Selector.Kind),
		})
	}
	return out
}

// pluralWithoutDistinctions reports whether s is a plural or ordinal
// selector that the target locale cannot distinguish (only "other"), so a
// translation may legitimately drop it.
func pluralWithoutDistinctions(s Argument, cats *pluralCategories) bool {
	list, ok := cats.forKind(s.Selector.Kind)
	return ok && len(list) == 1 && list[0] == "other"
}

func selectorArgs(args []Argument) []Argument {
	var out []Argument
	for _, a := range args {
		if a.Selector != nil {
			out = append(out, a)
		}
	}
	return out
}

func containsArg(args []Argument, name string) bool {
	return slices.ContainsFunc(args, func(a Argument) bool { return a.Name == name })
}

// --- plural keys ----------------------------------------------------------------

func checkPluralKeys(tr []Argument, cats *pluralCategories) []Finding {
	var out []Finding
	for _, a := range selectorArgs(tr) {
		valid, ok := cats.forKind(a.Selector.Kind)
		if !ok {
			continue
		}
		for _, key := range a.Selector.Keys {
			if !isNumberLiteral(key) && !slices.Contains(valid, key) {
				out = append(out, Finding{
					Code: FindingInvalidPluralKey, Severity: SeverityError, Subject: a.Name, Detail: key,
					Message: fmt.Sprintf("%q is not a %s category of the target locale (valid: %v, numbers, and *)", key, a.Selector.Kind, valid),
				})
			}
		}
		for _, cat := range valid {
			if cat != "other" && !slices.Contains(a.Selector.Keys, cat) {
				out = append(out, Finding{
					Code: FindingMissingPluralCategory, Severity: SeverityWarning, Subject: a.Name, Detail: cat,
					Message: fmt.Sprintf("the target locale's %s category %q has no variant of its own and falls back to *", a.Selector.Kind, cat),
				})
			}
		}
	}
	return out
}

// numberLiteral is the MF2 number-literal production, which :number and
// :integer selectors match exactly.
var numberLiteral = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

func isNumberLiteral(key string) bool { return numberLiteral.MatchString(key) }

// --- markup ---------------------------------------------------------------------

func checkMarkup(source, translation Message) []Finding {
	out := checkMarkupBalance(translation)
	srcNames, trNames := markupNames(source), markupNames(translation)
	var missing, extra []string
	for _, n := range srcNames {
		if !slices.Contains(trNames, n) {
			missing = append(missing, n)
		}
	}
	for _, n := range trNames {
		if !slices.Contains(srcNames, n) {
			extra = append(extra, n)
		}
	}
	paired := min(len(missing), len(extra))
	for i := range paired {
		out = append(out, Finding{
			Code: FindingMarkupRenamed, Severity: SeverityError, Subject: missing[i], Detail: extra[i],
			Message: fmt.Sprintf("markup {#%s} appears as {#%s} in the translation", missing[i], extra[i]),
		})
	}
	for _, n := range missing[paired:] {
		out = append(out, Finding{
			Code: FindingMarkupMissing, Severity: SeverityError, Subject: n,
			Message: fmt.Sprintf("the translation drops markup {#%s}", n),
		})
	}
	for _, n := range extra[paired:] {
		out = append(out, Finding{
			Code: FindingMarkupExtra, Severity: SeverityError, Subject: n,
			Message: fmt.Sprintf("the translation adds markup {#%s}, which the source does not have", n),
		})
	}
	return out
}

// checkMarkupBalance reports unbalanced markup in each translation pattern,
// once per element name.
func checkMarkupBalance(msg Message) []Finding {
	var out []Finding
	reported := map[string]bool{}
	report := func(name, why string) {
		if reported[name] {
			return
		}
		reported[name] = true
		out = append(out, Finding{
			Code: FindingMarkupUnbalanced, Severity: SeverityError, Subject: name,
			Message: fmt.Sprintf("markup {#%s} %s", name, why),
		})
	}
	for _, p := range msg.Patterns() {
		var open []string
		for _, el := range p {
			mk, ok := el.(Markup)
			if !ok {
				continue
			}
			switch mk.Kind {
			case MarkupOpen:
				open = append(open, mk.Name)
			case MarkupClose:
				open = closeMarkup(open, mk.Name, report)
			}
		}
		for _, name := range open {
			report(name, "is opened but never closed")
		}
	}
	return out
}

// closeMarkup applies a close element to the stack of open elements. A
// close that does not match the innermost open element is reported, and so
// is every element it crosses.
func closeMarkup(open []string, name string, report func(name, why string)) []string {
	i := -1 // innermost open element with this name
	for j := len(open) - 1; j >= 0; j-- {
		if open[j] == name {
			i = j
			break
		}
	}
	switch {
	case i < 0:
		report(name, "is closed without being open")
		return open
	case i < len(open)-1:
		report(name, "is closed out of order")
		for _, crossed := range open[i+1:] {
			report(crossed, "is closed out of order")
		}
	}
	return open[:i]
}

// markupNames returns the distinct markup element names of msg in order.
func markupNames(msg Message) []string {
	var out []string
	for _, el := range MarkupElements(msg) {
		out = appendUnique(out, el.Name)
	}
	return out
}
