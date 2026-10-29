package layers

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The source layer (RFC 0005 §3.5): problematic source copy, before
// anybody translates it.
//
// Intent §28 asks for this check before translation and intent §30 puts
// it in CI, which is the whole point: a plural written out by hand is
// cheap to fix in English and expensive to fix in thirty languages that
// already translated it. It runs on the **source locale only** — a
// translation cannot be blamed for the message it was given — and every
// code is a warning by default, because source copy is the product
// team's call and a check that failed a build over a colon would be
// switched off within the week.
//
// Where the layer knows less than it would like, it says so rather than
// staying silent. `concatenation-suspected` and `ambiguous-short` are
// both sharper when Context's usages are in hand (RFC 0004 §8): two
// fragments on one route is what concatenation looks like, and a
// one-word label used in one place is not ambiguous. An offline `glossa
// check` over local catalogs has no usages, and there the layer reports
// the shape alone and records `"usages": "unknown"` in the evidence.
// The alternative — no finding without Context — would make the IDE and
// pre-commit surface intent §28 asks for the one surface where the
// layer does nothing.
type Source struct{}

// Source codes.
const (
	// CodeManualPlural is a plural written by hand instead of selected.
	CodeManualPlural = "manual-plural"
	// CodeConcatenationSuspected is a message that is half a sentence.
	CodeConcatenationSuspected = "concatenation-suspected"
	// CodeAmbiguousShort is a one-word message with no description.
	CodeAmbiguousShort = "ambiguous-short"
	// CodeMissingDescription is a message with a placeholder or a
	// selector and nothing telling a translator what it is.
	CodeMissingDescription = "missing-description"
	// CodeHardcodedFormat is a currency symbol, a percent sign or a date
	// pattern typed next to a plain placeholder.
	CodeHardcodedFormat = "hardcoded-format"
)

// Layer implements Checker.
func (Source) Layer() domain.Layer { return domain.LayerSource }

// Check implements Checker.
func (Source) Check(p *Project, policy checkpolicy.Policy) []domain.Finding {
	src, ok := p.SourceLocaleOf()
	if !ok {
		return nil
	}
	th := policy.Source()
	msgs := make([]Message, len(p.Messages))
	copy(msgs, p.Messages)
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Key < msgs[j].Key })

	routes := fragmentRoutes(msgs, th)
	var out []domain.Finding
	for i := range msgs {
		m := &msgs[i]
		if m.Model == nil {
			continue
		}
		c := sourceCheck{thresholds: th, message: m, locale: src.Code, routes: routes}
		out = append(out, c.findings()...)
	}
	return out
}

type sourceCheck struct {
	thresholds checkpolicy.SourceThresholds
	message    *Message
	locale     string
	// routes counts, per route, how many messages on it look like a
	// fragment. Two is what concatenation looks like from the outside.
	routes map[string]int
}

func (c sourceCheck) findings() []domain.Finding {
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
	variants := Variants(c.message.Model)
	for _, v := range variants {
		c.manualPlural(v.Text, add)
		c.hardcodedFormat(v.Text, add)
	}
	c.concatenation(add)
	c.ambiguousShort(add)
	c.missingDescription(add)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// manualPlural is `(s)`, `(n)` and `item/items` in a message that never
// selects. A message that does select has already said what it meant.
func (c sourceCheck) manualPlural(text string, add func(domain.Finding)) {
	if HasSelector(c.message.Model) {
		return
	}
	for _, marker := range c.thresholds.PluralMarkers {
		if !strings.Contains(text, marker) {
			continue
		}
		add(domain.Finding{
			Layer: domain.LayerSource, Code: CodeManualPlural, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.message.Text, marker, domain.SideSource)),
			Subject: marker,
			Message: fmt.Sprintf("%q writes the plural by hand; most languages have more than two forms "+
				"and a `plural` selector is what gives them", marker),
			Evidence: map[string]any{"marker": marker},
			Fix:      &domain.Fix{Kind: domain.FixReplace, Hint: "select on the count with a `plural` selector"},
		})
	}
	if slash, ok := slashPlural(text); ok {
		add(domain.Finding{
			Layer: domain.LayerSource, Code: CodeManualPlural, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.message.Text, slash, domain.SideSource)),
			Subject: slash,
			Message: fmt.Sprintf("%q offers two forms in the text; a `plural` selector is what a "+
				"language with six of them needs", slash),
			Evidence: map[string]any{"marker": slash},
			Fix:      &domain.Fix{Kind: domain.FixReplace, Hint: "select on the count with a `plural` selector"},
		})
	}
}

// slashPlural finds `item/items`: the same word twice with a slash
// between, the second a suffixed form of the first. Requiring the
// prefix is what keeps "and/or" and "km/h" out of it.
func slashPlural(text string) (string, bool) {
	for i, r := range text {
		if r != '/' {
			continue
		}
		left, right := trailingWord(text[:i]), leadingWord(text[i+1:])
		if left == "" || right == "" || len(right) <= len(left) {
			continue
		}
		if strings.EqualFold(right[:len(left)], left) {
			return left + "/" + right, true
		}
	}
	return "", false
}

func trailingWord(s string) string {
	ws := words(s)
	if len(ws) == 0 || !strings.HasSuffix(s, ws[len(ws)-1]) {
		return ""
	}
	return ws[len(ws)-1]
}

func leadingWord(s string) string {
	ws := words(s)
	if len(ws) == 0 || !strings.HasPrefix(s, ws[0]) {
		return ""
	}
	return ws[0]
}

// hardcodedFormat is a currency symbol, a percent sign or a date typed
// beside a placeholder that was not typed. The placeholder is what
// makes it a finding: a message that says "50 % off" and never
// interpolates is a sentence, not a format.
func (c sourceCheck) hardcodedFormat(text string, add func(domain.Finding)) {
	if len(Placeholders(c.message.Model)) == 0 {
		return
	}
	typed := map[string]bool{}
	for _, f := range Functions(c.message.Model) {
		typed[f] = true
	}
	numeric := false
	for _, f := range c.thresholds.NumericFunctions {
		if typed[f] {
			numeric = true
			break
		}
	}
	if numeric {
		return
	}
	symbols := append(append([]rune{}, c.thresholds.CurrencySymbols...), c.thresholds.FormatSymbols...)
	for _, r := range symbols {
		if !strings.ContainsRune(text, r) {
			continue
		}
		add(domain.Finding{
			Layer: domain.LayerSource, Code: CodeHardcodedFormat, Severity: domain.Warning,
			Locus:   c.locus(SpanOf(c.message.Text, string(r), domain.SideSource)),
			Subject: string(r),
			Message: fmt.Sprintf("a literal %q beside a plain placeholder; a typed placeholder would "+
				"put the symbol where each locale puts it", string(r)),
			Evidence: map[string]any{"symbol": string(r)},
			Fix: &domain.Fix{
				Kind: domain.FixReplace,
				Hint: "annotate the placeholder (`:number style=percent`, `:currency`) instead",
			},
		})
	}
	for _, v := range Variants(c.message.Model) {
		for _, d := range DateLiterals(v.Text) {
			add(domain.Finding{
				Layer: domain.LayerSource, Code: CodeHardcodedFormat, Severity: domain.Warning,
				Locus:   c.locus(SpanOf(c.message.Text, d, domain.SideSource)),
				Subject: d,
				Message: fmt.Sprintf("%q is a date pattern in the text; a `:date` placeholder is what "+
					"gets formatted per locale", d),
				Evidence: map[string]any{"literal": d},
				Fix:      &domain.Fix{Kind: domain.FixReplace, Hint: "use a `:date` placeholder"},
			})
		}
	}
}

// concatenation is a message that is half a sentence. The shape is what
// the layer can always see; Context's usages are what confirm it, and
// the evidence says which of the two it had.
func (c sourceCheck) concatenation(add func(domain.Finding)) {
	why, ok := fragmentShape(c.message, c.thresholds)
	if !ok {
		return
	}
	evidence := map[string]any{"shape": why}
	confirmed := false
	if len(c.message.Usages) == 0 {
		evidence["usages"] = "unknown"
	} else {
		var routes []string
		for _, u := range c.message.Usages {
			if u.Route != "" && c.routes[u.Route] > 1 {
				routes = append(routes, u.Route)
			}
		}
		sort.Strings(routes)
		if len(routes) == 0 {
			// The usages are in hand and say this fragment stands alone.
			// Nothing is being concatenated, and the layer withdraws.
			return
		}
		confirmed = true
		evidence["routes"] = routes
	}
	message := fmt.Sprintf("looks like a fragment (%s); a sentence built from two messages cannot be "+
		"reordered by a language that needs to", why)
	if confirmed {
		message = fmt.Sprintf("looks like a fragment (%s) and shares a route with another one; a sentence "+
			"built from two messages cannot be reordered by a language that needs to", why)
	}
	add(domain.Finding{
		Layer: domain.LayerSource, Code: CodeConcatenationSuspected, Severity: domain.Warning,
		Locus: c.locus(nil), Message: message, Evidence: evidence,
	})
}

// fragmentShape says whether a message looks like half of a sentence,
// and how.
//
// It reads the pattern's *boundaries*, not its literal text, and that
// distinction is the whole rule. `{count, plural, one {# item} other
// {# items}}` has literal text that begins with a space in every
// variant, and it is not a fragment: the space is between the
// placeholder and the word, which is exactly where it belongs. What
// makes a fragment is a pattern that *starts or ends* with text that
// trails off — and a pattern ending in a placeholder has not trailed
// off, it has finished with the thing it was interpolating.
//
// A select message must have the shape in every variant. One variant
// ending in a colon is a typo; all of them ending in one is a design.
func fragmentShape(m *Message, th checkpolicy.SourceThresholds) (string, bool) {
	patterns := m.Model.Patterns()
	if len(patterns) == 0 {
		return "", false
	}
	var why string
	for _, p := range patterns {
		got, ok := patternFragment(p, th)
		if !ok || (why != "" && got != why) {
			return "", false
		}
		why = got
	}
	return why, why != ""
}

// patternFragment is one pattern's fragment shape.
func patternFragment(p mf.Pattern, th checkpolicy.SourceThresholds) (string, bool) {
	lead, trail := boundaryText(p)
	if trail != "" {
		runes := []rune(trail)
		last := runes[len(runes)-1]
		switch {
		case unicode.IsSpace(last):
			return "it ends in a space", true
		case containsRune(th.FragmentEndings, last):
			return fmt.Sprintf("it ends in %q", string(last)), true
		case openQuote(last):
			return "it ends in an opening quotation mark", true
		}
	}
	if lead != "" {
		if runes := []rune(lead); unicode.IsSpace(runes[0]) {
			return "it begins with a space", true
		}
	}
	return "", true
}

// boundaryText is the literal text at each end of a pattern, empty
// where that end is a placeholder or markup rather than text.
func boundaryText(p mf.Pattern) (lead, trail string) {
	if len(p) == 0 {
		return "", ""
	}
	if t, ok := p[0].(mf.Text); ok {
		lead = string(t)
	}
	if t, ok := p[len(p)-1].(mf.Text); ok {
		trail = string(t)
	}
	return lead, trail
}

func openQuote(r rune) bool {
	switch r {
	case '"', '\'', '“', '„', '«', '‘':
		return true
	}
	return false
}

// fragmentRoutes counts the fragment-shaped messages on each route, so
// concatenation can ask whether this one has company.
func fragmentRoutes(msgs []Message, th checkpolicy.SourceThresholds) map[string]int {
	out := map[string]int{}
	for i := range msgs {
		m := &msgs[i]
		if m.Model == nil {
			continue
		}
		if _, ok := fragmentShape(m, th); !ok {
			continue
		}
		seen := map[string]bool{}
		for _, u := range m.Usages {
			if u.Route == "" || seen[u.Route] {
				continue
			}
			seen[u.Route] = true
			out[u.Route]++
		}
	}
	return out
}

// ambiguousShort is a one-word label with nothing said about it.
// "Open" is a verb and an adjective and a state, and the three are
// three different words in German.
//
// Usages only ever *withdraw* the finding: a label the product asks for
// in exactly one component is a label with one meaning, whatever its
// length. A caller with no usages keeps the finding and says so.
func (c sourceCheck) ambiguousShort(add func(domain.Finding)) {
	if c.message.Description != "" {
		return
	}
	text := LongestVariant(c.message.Model).Text
	ws := words(text)
	if len(ws) == 0 || len(ws) > c.thresholds.AmbiguousMaxWords {
		return
	}
	if len(Placeholders(c.message.Model)) > 0 {
		// A one-word message with a placeholder is missing a description
		// for a stronger reason, and missingDescription says so.
		return
	}
	evidence := map[string]any{"words": len(ws)}
	components := distinctComponents(c.message.Usages)
	switch {
	case len(c.message.Usages) == 0:
		evidence["usages"] = "unknown"
	case components < 2:
		return
	default:
		evidence["components"] = components
	}
	add(domain.Finding{
		Layer: domain.LayerSource, Code: CodeAmbiguousShort, Severity: domain.Warning,
		Locus: c.locus(nil),
		Message: fmt.Sprintf("%q is one word with no description; a translator cannot tell a verb from "+
			"a noun and most languages spell them differently", text),
		Evidence: evidence,
		Fix:      &domain.Fix{Kind: domain.FixReplace, Hint: "add a description saying what it does here"},
	})
}

func distinctComponents(us []Usage) int {
	seen := map[string]bool{}
	for _, u := range us {
		if u.Component != "" {
			seen[u.Component] = true
		}
	}
	return len(seen)
}

// missingDescription is a message a translator cannot resolve: a
// placeholder or a selector, and nothing said about either.
func (c sourceCheck) missingDescription(add func(domain.Finding)) {
	if c.message.Description != "" {
		return
	}
	placeholders := Placeholders(c.message.Model)
	if len(placeholders) == 0 && !HasSelector(c.message.Model) {
		return
	}
	add(domain.Finding{
		Layer: domain.LayerSource, Code: CodeMissingDescription, Severity: domain.Warning,
		Locus: c.locus(nil),
		Message: fmt.Sprintf("no description, and the message interpolates %s; a translator cannot "+
			"resolve a placeholder they were told nothing about", strings.Join(quoted(placeholders), ", ")),
		Evidence: map[string]any{"placeholders": placeholders, "selects": HasSelector(c.message.Model)},
		Fix:      &domain.Fix{Kind: domain.FixReplace, Hint: "describe what each placeholder holds"},
	})
}

func quoted(ss []string) []string {
	if len(ss) == 0 {
		return []string{"a selector"}
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "$" + s
	}
	return out
}

// locus is the source layer's locus: the source locale, because a
// source finding is about the message and not about anybody's
// translation of it, and the catalog message ID wherever the caller has
// one (RFC 0005 §2.1).
func (c sourceCheck) locus(span *domain.Span) domain.Locus {
	return domain.Locus{
		Message: c.message.ID, Key: c.message.Key, Locale: c.locale,
		Namespace: c.message.Namespace, File: c.message.File, Span: span,
	}
}
