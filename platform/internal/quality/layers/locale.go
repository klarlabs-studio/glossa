package layers

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/bidi"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The locale layer (RFC 0005 §3.4): locale correctness is more than
// strings (intent §14–§15), and this is the part of it CLDR can decide.
//
// Everything decidable is decided from CLDR through
// `golang.org/x/text`: the separators a locale writes numbers with, the
// digits of its numbering system, its script, and the bidi algorithm
// itself. Nothing here is a second copy of that data, because the
// runtimes format with `Intl.*` and `golang.org/x/text` too, and a
// check that disagreed with the formatter would be telling people their
// correct translation is wrong.
//
// What CLDR does not state — French's narrow no-break space before
// `;:!?` is typography, not locale data — is curated in
// checkpolicy.LocaleThresholds, and it is a table with holes on
// purpose. Intent §41 asks the product not to pretend every language is
// equally supported, so a locale the table does not name gets no
// finding from the rule that needed it, and each finding says in its
// evidence what decided it (`cldr` or `curated`). A confident red about
// a language nobody checked is worse than a silence anybody can see.
//
// The layer is not the style layer. Where a project's style guide
// states a number convention *beyond* CLDR, that is the guide's rule
// and the style layer grades it; here the only authority is the
// standard's.
type LocaleLayer struct{}

// Locale codes.
const (
	// CodeNumberConvention is a number written with separators that
	// contradict the locale's.
	CodeNumberConvention = "number-convention"
	// CodeSpacingConvention is a locale's spacing before punctuation,
	// missing or written with the wrong space.
	CodeSpacingConvention = "spacing-convention"
	// CodeDateLiteral is a date or a clock time typed into the text
	// instead of asked for as a placeholder.
	CodeDateLiteral = "date-literal"
	// CodeBidiStrayControl is a bidi control character in a translation.
	CodeBidiStrayControl = "bidi-stray-control"
	// CodeBidiLeadingRun is an RTL translation whose first run is LTR
	// with nothing isolating it.
	CodeBidiLeadingRun = "bidi-leading-run"
	// CodeDigitShaping is digits from a numbering system the locale does
	// not use.
	CodeDigitShaping = "digit-shaping"
	// CodeUntranslatedSuspected is a translation identical to its source
	// in a locale whose script differs from the source's.
	CodeUntranslatedSuspected = "untranslated-suspected"
)

// Evidence keys every locale finding carries, so a reader can tell what
// decided it.
const (
	// EvidenceCLDR marks a finding CLDR decided, through x/text.
	EvidenceCLDR = "cldr"
	// EvidenceCurated marks one decided from Glossa's own curated data,
	// which covers fewer locales and says so.
	EvidenceCurated = "curated"
)

// Layer implements Checker.
func (LocaleLayer) Layer() domain.Layer { return domain.LayerLocale }

// Check implements Checker.
func (LocaleLayer) Check(p *Project, policy checkpolicy.Policy) []domain.Finding {
	th := policy.Locale()
	src, _ := LookupLocale(p.SourceLocale)
	var out []domain.Finding
	for _, l := range p.TargetLocales() {
		data, known := LookupLocale(l.Code)
		for _, t := range p.SortedTranslations(l.Code) {
			m, ok := p.Message(t.Key)
			if !ok || t.Model == nil || t.State == "rejected" {
				continue
			}
			out = append(out, localeFindings(localeCheck{
				thresholds: th, target: data, targetKnown: known, source: src,
				project: p, message: m, translation: t, locale: l.Code,
			})...)
		}
	}
	return out
}

// localeCheck is one translation's worth of context, gathered once so
// the rules below read like the rules they are.
type localeCheck struct {
	thresholds  checkpolicy.LocaleThresholds
	target      LocaleData
	targetKnown bool
	source      LocaleData
	project     *Project
	message     *Message
	translation Translation
	locale      string
}

func localeFindings(c localeCheck) []domain.Finding {
	var out []domain.Finding
	seen := map[string]bool{}
	add := func(f domain.Finding) {
		k := f.Code + "\x00" + f.Subject
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, domain.New(f))
	}
	for _, v := range Variants(c.translation.Model) {
		if v.Text == "" {
			continue
		}
		c.bidi(v.Text, add)
		c.numbers(v.Text, add)
		c.digits(v.Text, add)
		c.spacing(v.Text, add)
		c.dates(v.Text, add)
	}
	c.untranslated(add)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// bidi is the two bidi rules of §3.4. Both need only the Unicode
// algorithm, which x/text implements, so both hold in every locale.
func (c localeCheck) bidi(text string, add func(domain.Finding)) {
	for _, r := range c.thresholds.BidiControls {
		if !strings.ContainsRune(text, r) {
			continue
		}
		add(domain.Finding{
			Layer: domain.LayerLocale, Code: CodeBidiStrayControl, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.translation.Text, string(r), domain.SideTarget)),
			Subject: fmt.Sprintf("U+%04X", r),
			Message: fmt.Sprintf("a stray bidi control (U+%04X) in the text; the runtime isolates "+
				"placeholders itself and the character survives into the product", r),
			Evidence:       map[string]any{"control": fmt.Sprintf("U+%04X", r), "decided_by": EvidenceCLDR},
			SourceRevision: sourceRevision(c.translation),
		})
	}
	if !c.targetKnown || !c.target.RTL || !hasStrongRTL(text) {
		return
	}
	var pg bidi.Paragraph
	if _, err := pg.SetString(text); err != nil {
		return
	}
	order, err := pg.Order()
	if err != nil || order.NumRuns() < 2 {
		return
	}
	first := order.Run(0)
	if first.Direction() != bidi.LeftToRight {
		return
	}
	run := first.String()
	add(domain.Finding{
		Layer: domain.LayerLocale, Code: CodeBidiLeadingRun, Severity: domain.Warning,
		Locus:   c.locus(SpanOf(c.translation.Text, run, domain.SideTarget)),
		Subject: run,
		Message: fmt.Sprintf("an %s translation beginning with a left-to-right run (%q); it will "+
			"reorder unless the product isolates it", c.locale, run),
		Evidence: map[string]any{
			"leading_run": run, "script": c.target.Script, "decided_by": EvidenceCLDR,
		},
		SourceRevision: sourceRevision(c.translation),
	})
}

// numbers grades a written-out number's separators against CLDR's.
func (c localeCheck) numbers(text string, add func(domain.Finding)) {
	if !c.targetKnown {
		return
	}
	for _, n := range NumberLiterals(text) {
		if n.Ambiguous || n.Digits < c.thresholds.MinNumberDigits {
			continue
		}
		var why string
		switch {
		case n.Decimal != 0 && n.Decimal != c.target.Decimal:
			why = fmt.Sprintf("a %q decimal separator; %s writes %q",
				string(n.Decimal), c.locale, string(c.target.Decimal))
		case n.Group != 0 && c.target.Group != 0 && !SameSpace(n.Group, c.target.Group):
			why = fmt.Sprintf("a %q grouping separator; %s writes %q",
				string(n.Group), c.locale, string(c.target.Group))
		default:
			continue
		}
		add(domain.Finding{
			Layer: domain.LayerLocale, Code: CodeNumberConvention, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.translation.Text, n.Text, domain.SideTarget)),
			Subject: n.Text,
			Message: fmt.Sprintf("%q uses %s", n.Text, why),
			Evidence: map[string]any{
				"literal": n.Text, "locale_decimal": string(c.target.Decimal),
				"locale_group": string(c.target.Group), "decided_by": EvidenceCLDR,
			},
			SourceRevision: sourceRevision(c.translation),
		})
	}
}

// digits reports digits from a numbering system the locale does not
// use. Latin digits are never reported: every locale on earth reads
// them, and CLDR's default numbering system is what a locale *prefers*,
// not the only set it accepts.
func (c localeCheck) digits(text string, add func(domain.Finding)) {
	if !c.targetKnown {
		return
	}
	seen := map[rune]bool{}
	for _, r := range text {
		if !unicode.IsDigit(r) {
			continue
		}
		zero := zeroOf(r)
		if zero == '0' || zero == c.target.Zero || seen[zero] {
			continue
		}
		seen[zero] = true
		name := ScriptName(zero)
		if name == "" {
			name = fmt.Sprintf("U+%04X", zero)
		}
		add(domain.Finding{
			Layer: domain.LayerLocale, Code: CodeDigitShaping, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.translation.Text, string(r), domain.SideTarget)),
			Subject: name,
			Message: fmt.Sprintf("%s digits in a %s translation, which writes them as %s",
				name, c.locale, digitSetName(c.target.Zero)),
			Evidence: map[string]any{
				"digits": name, "locale_digits": digitSetName(c.target.Zero), "decided_by": EvidenceCLDR,
			},
			SourceRevision: sourceRevision(c.translation),
		})
	}
}

// spacing is the one rule whose data CLDR does not hold. A locale the
// curated table does not name gets nothing.
func (c localeCheck) spacing(text string, add func(domain.Finding)) {
	conv, ok := c.thresholds.SpacingFor(c.locale)
	if !ok {
		return
	}
	runes := []rune(text)
	for i, r := range runes {
		if !containsRune(conv.BeforePunctuation, r) || i == 0 {
			continue
		}
		before := runes[i-1]
		switch {
		case before == conv.Space, containsRune(conv.AlsoAccept, before):
			continue
		case unicode.IsSpace(before):
			add(c.spacingFinding(r, before, conv,
				fmt.Sprintf("a plain space before %q; %s wants U+%04X", string(r), c.locale, conv.Space)))
		case unicode.IsLetter(before) || unicode.IsDigit(before):
			add(c.spacingFinding(r, 0, conv,
				fmt.Sprintf("no space before %q; %s wants U+%04X", string(r), c.locale, conv.Space)))
		}
	}
}

func (c localeCheck) spacingFinding(
	punct, before rune, conv checkpolicy.SpacingConvention, message string,
) domain.Finding {
	run := string(punct)
	if before != 0 {
		run = string(before) + run
	}
	return domain.Finding{
		Layer: domain.LayerLocale, Code: CodeSpacingConvention, Severity: domain.Warning,
		Locus:   c.locus(SpanOf(c.translation.Text, run, domain.SideTarget)),
		Subject: string(punct),
		Message: message,
		Evidence: map[string]any{
			"punctuation": string(punct), "wanted": fmt.Sprintf("U+%04X", conv.Space),
			"decided_by": EvidenceCurated,
		},
		SourceRevision: sourceRevision(c.translation),
	}
}

// dates reports a date or a clock typed into the text. It is the one
// rule that is as true of the source as of a translation, and it is
// reported on the translation because that is where a formatter would
// have been asked for the locale's own format.
func (c localeCheck) dates(text string, add func(domain.Finding)) {
	for _, d := range DateLiterals(text) {
		add(domain.Finding{
			Layer: domain.LayerLocale, Code: CodeDateLiteral, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.translation.Text, d, domain.SideTarget)),
			Subject: d,
			Message: fmt.Sprintf("%q is a date written into the text; a `:date` placeholder would be "+
				"formatted for %s and this will not be", d, c.locale),
			Evidence:       map[string]any{"literal": d, "decided_by": EvidenceCLDR},
			SourceRevision: sourceRevision(c.translation),
		})
	}
}

// untranslated is the cheapest finding in the layer and one of the most
// useful: text that never got translated, in a locale whose script says
// so without a model's opinion.
//
// It needs both scripts to be known and to differ. A German string left
// in the French catalog is a real problem and this rule cannot see it —
// that is the linguistic layer's, and saying so is better than a rule
// that fires on every product name.
func (c localeCheck) untranslated(add func(domain.Finding)) {
	if !c.targetKnown || c.source.Script == "" || c.source.Script == c.target.Script {
		return
	}
	if c.message.Model == nil {
		return
	}
	src := LongestVariant(c.message.Model).Text
	tgt := LongestVariant(c.translation.Model).Text
	if src == "" || src != tgt || utf8.RuneCountInString(src) < c.thresholds.MinUntranslatedRunes {
		return
	}
	add(domain.Finding{
		Layer: domain.LayerLocale, Code: CodeUntranslatedSuspected, Severity: domain.Warning,
		Locus: c.locus(nil),
		Message: fmt.Sprintf("identical to the %s source, and %s is written in %s rather than %s",
			c.project.SourceLocale, c.locale, c.target.Script, c.source.Script),
		Evidence: map[string]any{
			"source_script": c.source.Script, "target_script": c.target.Script, "decided_by": EvidenceCLDR,
		},
		SourceRevision: sourceRevision(c.translation),
	})
}

func (c localeCheck) locus(span *domain.Span) domain.Locus {
	return locusOf(c.message, c.translation, c.locale, span)
}

// hasStrongRTL reports whether the text has a right-to-left character
// at all. A translation with none is untranslated, not misordered, and
// the leading-run rule would be a second finding about the first one.
func hasStrongRTL(s string) bool {
	for _, r := range s {
		if p, _ := bidi.LookupRune(r); p.Class() == bidi.R || p.Class() == bidi.AL {
			return true
		}
	}
	return false
}

func containsRune(rs []rune, r rune) bool {
	for _, c := range rs {
		if c == r {
			return true
		}
	}
	return false
}

func digitSetName(zero rune) string {
	if n := ScriptName(zero); n != "" {
		return n
	}
	return fmt.Sprintf("U+%04X", zero)
}
