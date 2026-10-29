package layers

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The style layer (RFC 0005 §3.2): the **mechanical** fields of the
// effective style guide, and nothing else.
//
// What that means, and why the line is where it is. A style guide has
// two halves. One half is mechanical: a formality, a pair of quotation
// marks, a dash, an ellipsis character, whether a unit takes a space,
// number and date conventions the project states beyond CLDR, and a few
// punctuation rules. Every one of those is decidable from the text, and
// this layer decides them.
//
// The other half is prose: a rule, a rationale, and some good and bad
// examples. RFC 0005 §3.2 says plainly that it is *not* checked here —
// it is prompt material for M2's translation agent and evidence for the
// linguistic layer — and gives the reason in one sentence: "a regex
// over a rationale would be a lie about what the system knows." So
// layers.StyleGuide cannot carry the prose rules at all. A type that
// cannot hold them is a type nobody can be tempted to grade them from.
//
// A locale with no effective guide gets no findings. That is not a
// green: the run still names the layer, and a reader can tell "clean"
// from "nothing to check against".
type Style struct{}

// Style codes.
const (
	// CodeFormalityMismatch is the opposite register's form of address
	// appearing in a translation.
	CodeFormalityMismatch = "formality-mismatch"
	// CodeTypographyMismatch is a quotation mark, a dash, an ellipsis or
	// a unit space that is not the one the guide asks for.
	CodeTypographyMismatch = "typography-mismatch"
	// CodeConventionMismatch is a number or date convention the guide
	// states beyond CLDR and the text contradicts.
	CodeConventionMismatch = "convention-mismatch"
	// CodePunctuationMismatch is a trailing or doubled space, or a final
	// stop the source does not have.
	CodePunctuationMismatch = "punctuation-mismatch"
)

// Layer implements Checker.
func (Style) Layer() domain.Layer { return domain.LayerStyle }

// Check implements Checker.
func (Style) Check(p *Project, policy checkpolicy.Policy) []domain.Finding {
	conv := policy.Style()
	var out []domain.Finding
	for _, l := range p.TargetLocales() {
		guide, ok := p.Style(l.Code)
		if !ok {
			continue
		}
		for _, t := range p.SortedTranslations(l.Code) {
			m, ok := p.Message(t.Key)
			if !ok || t.Model == nil || t.State == "rejected" {
				continue
			}
			c := styleCheck{
				conventions: conv, guide: guide, message: m, translation: t, locale: l.Code,
			}
			out = append(out, c.findings()...)
		}
	}
	return out
}

type styleCheck struct {
	conventions checkpolicy.StyleConventions
	guide       StyleGuide
	message     *Message
	translation Translation
	locale      string
}

func (c styleCheck) findings() []domain.Finding {
	var out []domain.Finding
	seen := map[string]bool{}
	add := func(f domain.Finding) {
		k := f.Code + "\x00" + f.Subject
		if seen[k] {
			return
		}
		seen[k] = true
		f.Evidence = withGuide(f.Evidence, c.guide.Version)
		out = append(out, domain.New(f))
	}
	for _, v := range Variants(c.translation.Model) {
		if v.Text == "" {
			continue
		}
		c.formality(v.Text, add)
		c.typography(v.Text, add)
		c.conventionsIn(v.Text, add)
		c.punctuation(v.Text, add)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// formality is §3.2's first rule, exactly as it is written: the guide's
// pronoun set appears and its opposite does not. The finding is the
// opposite's appearance, because that is the half a check can be sure
// of — a German sentence with no pronoun in it at all is neither formal
// nor informal, and reporting it for the absence would be wrong about
// most of any catalog.
func (c styleCheck) formality(text string, add func(domain.Finding)) {
	unwanted, fold, ok := c.unwantedForms()
	if !ok {
		return
	}
	wordwise := scriptSeparatesWords(c.locale)
	for _, form := range unwanted {
		// The subject is the run *as the translation wrote it*, not the
		// table's spelling of it: a span has to point at something that
		// is there, and a reader is being shown their own sentence.
		w, ok := findForm(text, form, fold, wordwise)
		if !ok {
			continue
		}
		add(domain.Finding{
			Layer: domain.LayerStyle, Code: CodeFormalityMismatch, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.translation.Text, w, domain.SideTarget)),
			Subject: w,
			Message: fmt.Sprintf("%q is the %s form; the style guide for %s asks for the %s one%s",
				w, opposite(c.guide.Formality), c.locale, c.guide.Formality, c.wantedHint()),
			Evidence: map[string]any{
				"found": w, "formality": c.guide.Formality, "wanted": c.wantedForms(),
			},
			SourceRevision: sourceRevision(c.translation),
			Fix:            c.formalityFix(),
		})
	}
}

// unwantedForms are the forms the guide rules out: the ones it named
// itself, or the opposite register's from the curated table. The second
// answer is whether they may be matched case-insensitively.
//
// Case matters in exactly one direction. German's formal forms are the
// capitalized ones — `Sie` is the pronoun and `sie` is "she" and
// "they", `Ihre` is "your" and `ihre` is "her" — so a guide that asks
// for the *informal* register must match its unwanted formal forms as
// written or it would report every sentence about a woman. The informal
// forms have no such twin, and they are capitalized at the start of a
// sentence like any other word, so they are matched by fold. Italian
// (`Lei`/`lei`) and Russian (`Вы`/`вы`) are the same shape.
func (c styleCheck) unwantedForms() ([]string, bool, bool) {
	if len(c.guide.Forbidden) > 0 {
		return c.guide.Forbidden, false, true
	}
	if c.guide.Formality == "" {
		return nil, false, false
	}
	forms, ok := c.conventions.FormsFor(c.locale)
	if !ok {
		// Glossa has no forms of address for this language and will not
		// guess at them (intent §41).
		return nil, false, false
	}
	unwanted := forms.Unwanted(c.guide.Formality)
	fold := !(forms.CaseSensitive && c.guide.Formality == checkpolicy.FormalityInformal)
	return unwanted, fold, len(unwanted) > 0
}

// findForm finds a form of address in the text and answers with the run
// as the text wrote it.
//
// Word-wise where the script writes spaces between words, which is
// where a substring match would report `durch` for `du`. Where it does
// not — Japanese, Chinese, Thai — there are no word boundaries to match
// on, and a guide that named a form there named the exact string it
// meant.
func findForm(text, form string, fold, wordwise bool) (string, bool) {
	if !wordwise {
		if !fold {
			if strings.Contains(text, form) {
				return form, true
			}
			return "", false
		}
		i := strings.Index(strings.ToLower(text), strings.ToLower(form))
		if i < 0 {
			return "", false
		}
		return text[i : i+len(form)], true
	}
	return findWord(text, form, fold)
}

// scriptSeparatesWords reports whether a locale's script writes spaces
// between words. An unknown locale is assumed to, which is the safe
// answer: word-wise matching reports less than substring matching.
func scriptSeparatesWords(locale string) bool {
	d, ok := LookupLocale(locale)
	if !ok {
		return true
	}
	switch d.Script {
	case "Jpan", "Hani", "Hans", "Hant", "Hira", "Kana", "Thai", "Khmr", "Laoo", "Mymr", "Tibt":
		return false
	}
	return true
}

func (c styleCheck) wantedForms() []string {
	if len(c.guide.Pronouns) > 0 {
		return c.guide.Pronouns
	}
	if forms, ok := c.conventions.FormsFor(c.locale); ok {
		return forms.Wanted(c.guide.Formality)
	}
	return nil
}

func (c styleCheck) wantedHint() string {
	w := c.wantedForms()
	if len(w) == 0 {
		return ""
	}
	return " (" + strings.Join(w[:min(len(w), 3)], ", ") + ")"
}

func (c styleCheck) formalityFix() *domain.Fix {
	w := c.wantedForms()
	if len(w) == 0 {
		return nil
	}
	return &domain.Fix{Kind: domain.FixReplace, Hint: "use " + w[0]}
}

func opposite(formality string) string {
	switch formality {
	case checkpolicy.FormalityFormal:
		return "informal"
	case checkpolicy.FormalityInformal:
		return "formal"
	}
	return "other"
}

// typography is the quotation marks, the dash, the ellipsis and the
// space before a unit — each checked only where the guide states it.
func (c styleCheck) typography(text string, add func(domain.Finding)) {
	report := func(subject, message string, evidence map[string]any, hint string) {
		add(domain.Finding{
			Layer: domain.LayerStyle, Code: CodeTypographyMismatch, Severity: domain.Warning,
			Locus: c.locus(SpanOf(c.translation.Text, subject, domain.SideTarget)), Subject: subject,
			Message: message, Evidence: evidence, SourceRevision: sourceRevision(c.translation),
			Fix: &domain.Fix{Kind: domain.FixReplace, Hint: hint},
		})
	}
	if c.guide.QuoteOpen != "" {
		for _, q := range []string{`"`, "“", "”", "„", "«", "»", "‘", "’"} {
			if q == c.guide.QuoteOpen || q == c.guide.QuoteClose || !strings.Contains(text, q) {
				continue
			}
			report(q,
				fmt.Sprintf("%q is not the quotation mark the guide asks for (%s…%s)",
					q, c.guide.QuoteOpen, c.guide.QuoteClose),
				map[string]any{"found": q, "wanted": c.guide.QuoteOpen + "…" + c.guide.QuoteClose},
				"use "+c.guide.QuoteOpen+"…"+c.guide.QuoteClose)
		}
	}
	if c.guide.Dash != "" {
		for _, d := range []string{"—", "–", "-"} {
			if d == c.guide.Dash {
				continue
			}
			run := " " + d + " "
			if !strings.Contains(text, run) {
				continue
			}
			report(run,
				fmt.Sprintf("%q between words; the guide asks for %q", d, c.guide.Dash),
				map[string]any{"found": d, "wanted": c.guide.Dash}, "use "+c.guide.Dash)
		}
	}
	if c.guide.Ellipsis != "" && c.guide.Ellipsis != "..." && strings.Contains(text, "...") {
		report("...",
			fmt.Sprintf("three full stops; the guide asks for %q", c.guide.Ellipsis),
			map[string]any{"found": "...", "wanted": c.guide.Ellipsis}, "use "+c.guide.Ellipsis)
	}
	if c.guide.SpaceBeforeUnit != nil {
		for _, u := range c.conventions.Units {
			run, wrong := unitSpacing(text, u, *c.guide.SpaceBeforeUnit)
			if !wrong {
				continue
			}
			want := "a space"
			if !*c.guide.SpaceBeforeUnit {
				want = "no space"
			}
			report(run,
				fmt.Sprintf("%q; the guide asks for %s before %q", run, want, u),
				map[string]any{"found": run, "unit": u, "space_before_unit": *c.guide.SpaceBeforeUnit},
				"write the unit with "+want)
		}
	}
}

// unitSpacing finds a number and a unit written the way the guide does
// not ask for, and answers with the run so a span can point at it.
func unitSpacing(text, unit string, want bool) (string, bool) {
	runes := []rune(text)
	u := []rune(unit)
	for i := 0; i+len(u) <= len(runes); i++ {
		if string(runes[i:i+len(u)]) != unit {
			continue
		}
		// The unit must follow a number to be a unit at all: "%" in "100
		// % complete" is one, and in "a % sign" it is a character.
		if i == 0 {
			continue
		}
		switch {
		case unicode.IsDigit(runes[i-1]):
			if want {
				return string(runes[i-1 : i+len(u)]), true
			}
		case isSpaceSeparator(runes[i-1]) && i >= 2 && unicode.IsDigit(runes[i-2]):
			if !want {
				return string(runes[i-2 : i+len(u)]), true
			}
		}
	}
	return "", false
}

// conventionsIn is the number and date conventions a guide states
// *beyond* CLDR. Where the guide says nothing, the locale layer's
// answer stands and this layer is silent: the two must never both grade
// the same character, or a project would get two findings for one
// comma from two layers that disagree about which is right.
func (c styleCheck) conventionsIn(text string, add func(domain.Finding)) {
	report := func(subject, message string, evidence map[string]any) {
		add(domain.Finding{
			Layer: domain.LayerStyle, Code: CodeConventionMismatch, Severity: domain.Warning,
			Locus: c.locus(SpanOf(c.translation.Text, subject, domain.SideTarget)), Subject: subject,
			Message: message, Evidence: evidence, SourceRevision: sourceRevision(c.translation),
		})
	}
	if c.guide.Decimal != "" || c.guide.Group != "" {
		for _, n := range NumberLiterals(text) {
			if n.Ambiguous {
				continue
			}
			if d := c.guide.Decimal; d != "" && n.Decimal != 0 && string(n.Decimal) != d {
				report(n.Text,
					fmt.Sprintf("%q uses %q as its decimal separator; the style guide states %q",
						n.Text, string(n.Decimal), d),
					map[string]any{"literal": n.Text, "wanted_decimal": d})
				continue
			}
			if g := c.guide.Group; g != "" && n.Group != 0 && string(n.Group) != g {
				report(n.Text,
					fmt.Sprintf("%q groups with %q; the style guide states %q", n.Text, string(n.Group), g),
					map[string]any{"literal": n.Text, "wanted_group": g})
			}
		}
	}
	if c.guide.DateOrder != "" {
		for _, d := range DateLiterals(text) {
			order, ok := dateOrderOf(d)
			if !ok || order == c.guide.DateOrder {
				continue
			}
			report(d,
				fmt.Sprintf("%q is written %s; the style guide states %s", d, order, c.guide.DateOrder),
				map[string]any{"literal": d, "order": order, "wanted_order": c.guide.DateOrder})
		}
	}
}

// dateOrderOf names a written date's field order where it can be told
// apart. `01/02/2024` cannot be — it is January the second and the
// first of February and nothing in the string says which — so it is not
// reported at all.
func dateOrderOf(s string) (string, bool) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsDigit(r) })
	if len(parts) < 3 {
		return "", false
	}
	switch {
	case len(parts[0]) == 4:
		return "ymd", true
	case len(parts[2]) != 4:
		return "", false
	}
	// Two leading fields and a year: the day is the one above twelve,
	// and where neither is the date is genuinely ambiguous.
	first, second := numberOf(parts[0]), numberOf(parts[1])
	switch {
	case first > 12 && second <= 12:
		return "dmy", true
	case second > 12 && first <= 12:
		return "mdy", true
	}
	return "", false
}

func numberOf(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// punctuation is the last of §3.2's four: trailing and doubled spaces,
// and a full stop the source does not have.
func (c styleCheck) punctuation(text string, add func(domain.Finding)) {
	report := func(subject, message string, evidence map[string]any) {
		add(domain.Finding{
			Layer: domain.LayerStyle, Code: CodePunctuationMismatch, Severity: domain.Warning,
			Locus: c.locus(SpanOf(c.translation.Text, subject, domain.SideTarget)), Subject: subject,
			Message: message, Evidence: evidence, SourceRevision: sourceRevision(c.translation),
		})
	}
	if c.guide.ForbidTrailingSpace && text != strings.TrimRight(text, " \t ") {
		report("trailing-space", "the translation ends in a space the guide forbids",
			map[string]any{"rule": "trailing-space"})
	}
	if c.guide.ForbidDoubleSpace && strings.Contains(text, "  ") {
		report("double-space", "two spaces in a row, which the guide forbids",
			map[string]any{"rule": "double-space"})
	}
	if c.guide.ForbidAddedFinalStop && c.message.Model != nil {
		src := strings.TrimRight(LongestVariant(c.message.Model).Text, " \t")
		tgt := strings.TrimRight(text, " \t")
		if endsInStop(tgt) && !endsInStop(src) {
			report("final-stop",
				"a full stop the source does not have, which the guide forbids adding",
				map[string]any{"rule": "added-final-stop", "source_ends": lastRune(src)})
		}
	}
}

func endsInStop(s string) bool {
	r := []rune(s)
	if len(r) == 0 {
		return false
	}
	switch r[len(r)-1] {
	case '.', '。', '۔', '।':
		return true
	}
	return false
}

func lastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[len(r)-1])
}

func withGuide(evidence map[string]any, version string) map[string]any {
	if version == "" {
		return evidence
	}
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["style_guide"] = version
	return evidence
}

func (c styleCheck) locus(span *domain.Span) domain.Locus {
	return locusOf(c.message, c.translation, c.locale, span)
}
