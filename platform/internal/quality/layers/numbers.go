package layers

import (
	"regexp"
	"unicode"
)

// Numeric and date literals typed into a message's text, which two
// layers read: the locale layer grades them against CLDR, and the style
// layer against the conventions a guide states beyond CLDR.
//
// They are found in the *literal* text, never in the authored string. A
// placeholder is not a number somebody typed, and scanning the authored
// MF2 would find the digits inside `{$count, number, ::.00}` and report
// the author for asking correctly.

// numberSeparators are the characters that may stand between groups of
// digits in a number somebody wrote. A plain space is deliberately not
// among them: "in 2024 12 months" would be one number, and the cost of
// missing a space-grouped number is much lower than the cost of
// reporting a sentence.
var numberSeparators = map[rune]bool{
	'.': true, ',': true, '\'': true, ' ': true, ' ': true, ' ': true, '٫': true, '٬': true,
}

// NumberLiteral is a number found in literal text.
type NumberLiteral struct {
	// Text is the literal as written, digits and separators.
	Text string
	// Decimal and Group are the separators it used, or 0 where it used
	// none of that kind.
	Decimal rune
	Group   rune
	// Digits is how many digits it has.
	Digits int
	// Ambiguous marks a literal whose single separator could be either
	// kind — `1,234` is one thousand two hundred and thirty-four in
	// English and one point two three four in German, and nothing in the
	// string says which was meant. An ambiguous literal is never
	// reported: a check that guessed would be wrong half the time, on
	// the most common shape there is.
	Ambiguous bool
	// Zero is the numbering system its digits came from.
	Zero rune
}

// NumberLiterals are the numbers written out in s, in the order they
// appear.
func NumberLiterals(s string) []NumberLiteral {
	var out []NumberLiteral
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(runes) {
			switch {
			case unicode.IsDigit(runes[i]):
				i++
			case numberSeparators[runes[i]] && i+1 < len(runes) && unicode.IsDigit(runes[i+1]):
				i++
			default:
				goto done
			}
		}
	done:
		out = append(out, classify(string(runes[start:i])))
	}
	return out
}

// classify decides which separator in a literal was the decimal one.
//
// Two different separators: the last is the decimal and the first
// groups, which is true of every convention CLDR describes. One
// separator used more than once: it groups, and there is no fraction.
// One separator used once: it is the decimal unless exactly three
// digits follow it, in which case nothing in the string says which was
// meant and the literal is ambiguous.
func classify(text string) NumberLiteral {
	n := NumberLiteral{Text: text, Zero: DigitZeroOf(text)}
	type sep struct {
		r     rune
		after int
	}
	var seps []sep
	run := 0
	for _, r := range text {
		if unicode.IsDigit(r) {
			n.Digits++
			run++
			continue
		}
		if len(seps) > 0 {
			seps[len(seps)-1].after = run
		}
		seps = append(seps, sep{r: r})
		run = 0
	}
	if len(seps) > 0 {
		seps[len(seps)-1].after = run
	}
	switch len(seps) {
	case 0:
		return n
	case 1:
		if seps[0].after == 3 {
			n.Ambiguous = true
			return n
		}
		n.Decimal = seps[0].r
		return n
	}
	distinct := map[rune]bool{}
	for _, s := range seps {
		distinct[s.r] = true
	}
	last := seps[len(seps)-1]
	if len(distinct) == 1 {
		// One separator, used more than once: it can only group.
		n.Group = last.r
		return n
	}
	n.Decimal = last.r
	n.Group = seps[0].r
	return n
}

// Date and clock literals somebody typed instead of asking for one.
//
// The patterns are deliberately narrow. A four-digit year or a full
// separator triple is required, so a version number ("1.2.3" has no
// year) and a ratio ("3:2") are not dates; a clock needs two digits
// after the colon.
var (
	dateLiteral = regexp.MustCompile(
		`\b(\d{4}-\d{1,2}-\d{1,2}|\d{1,2}[/.-]\d{1,2}[/.-]\d{4}|\d{4}[/.]\d{1,2}[/.]\d{1,2})\b`)
	clockLiteral = regexp.MustCompile(`\b\d{1,2}:\d{2}(:\d{2})?\s?([AaPp]\.?[Mm]\.?)?`)
	monthLiteral = regexp.MustCompile(
		`\b\d{1,2}\.?\s+(January|February|March|April|May|June|July|August|September|October|November|December|` +
			`Januar|Februar|März|Mai|Juni|Juli|Oktober|Dezember|` +
			`janvier|février|mars|avril|mai|juin|juillet|août|septembre|octobre|novembre|décembre)\b`)
)

// DateLiterals are the dates and clock times written out in s.
func DateLiterals(s string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(m []string) {
		for _, v := range m {
			if v = trimSpace(v); v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	add(dateLiteral.FindAllString(s, -1))
	add(clockLiteral.FindAllString(s, -1))
	add(monthLiteral.FindAllString(s, -1))
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
