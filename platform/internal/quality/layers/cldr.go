package layers

import (
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// What CLDR answers about a locale, asked through `golang.org/x/text`.
//
// Nothing here is a table Glossa wrote. A locale's decimal separator,
// its grouping separator, the digits of its default numbering system
// and its script are all CLDR's answers, and they are obtained the only
// honest way: by asking the standard library to format a number and a
// tag and reading what it produced. A hand-rolled table of separators
// would be a second copy of CLDR that drifts from the first, and the
// runtimes format with `Intl.*` and `golang.org/x/text` — a check that
// disagreed with the formatter would be telling people their correct
// translation is wrong.
//
// A locale x/text cannot parse, or one whose data it has none of, comes
// back not-ok, and the locale layer emits nothing that needed it. Intent
// §41: do not pretend every language is equally supported.

// LocaleData is CLDR's answer about one locale.
type LocaleData struct {
	Tag language.Tag
	// Decimal and Group are the locale's separators.
	Decimal rune
	Group   rune
	// Zero is the digit zero of the locale's default numbering system:
	// '0' for most locales, U+0660 for Arabic, U+06F0 for Persian. It is
	// what `digit-shaping` measures a translation's digits against.
	Zero rune
	// Script is the locale's script subtag as CLDR resolves it ("Latn",
	// "Arab", "Jpan"), and RTL whether that script runs right to left.
	Script string
	RTL    bool
}

// rtlScripts are the right-to-left scripts, by ISO 15924 code. It is a
// property of a writing system and not of a locale, which is why it is
// a set here and not a per-locale table.
var rtlScripts = map[string]bool{
	"Arab": true, "Hebr": true, "Thaa": true, "Syrc": true, "Nkoo": true,
	"Adlm": true, "Mand": true, "Samr": true, "Rohg": true, "Yezi": true,
	"Mend": true, "Armi": true, "Avst": true, "Phnx": true,
}

var localeCache sync.Map // string → LocaleData; the miss is stored too

type localeEntry struct {
	data LocaleData
	ok   bool
}

// LookupLocale is CLDR's answer about code, and whether there is one.
func LookupLocale(code string) (LocaleData, bool) {
	if code == "" {
		return LocaleData{}, false
	}
	if v, ok := localeCache.Load(code); ok {
		e := v.(localeEntry)
		return e.data, e.ok
	}
	d, ok := lookupLocale(code)
	localeCache.Store(code, localeEntry{data: d, ok: ok})
	return d, ok
}

func lookupLocale(code string) (LocaleData, bool) {
	tag, err := language.Parse(code)
	if err != nil {
		return LocaleData{}, false
	}
	script, conf := tag.Script()
	if conf == language.No {
		return LocaleData{}, false
	}
	d := LocaleData{Tag: tag, Script: script.String()}
	d.RTL = rtlScripts[d.Script]
	// A number with a grouping separator and a fraction shows both
	// separators at once, in the locale's own digits. Reading them back
	// is how CLDR is asked without shipping a copy of it.
	p := message.NewPrinter(tag)
	formatted := p.Sprint(number.Decimal(1234567.5))
	var seps []rune
	for _, r := range formatted {
		if !unicode.IsDigit(r) {
			seps = append(seps, r)
			continue
		}
		if d.Zero == 0 {
			d.Zero = zeroOf(r)
		}
	}
	if len(seps) == 0 || d.Zero == 0 {
		return LocaleData{}, false
	}
	// The last separator stands before the fraction; the rest group.
	d.Decimal = seps[len(seps)-1]
	if len(seps) > 1 {
		d.Group = seps[0]
	}
	return d, true
}

// zeroOf is the digit zero of the numbering system r belongs to. Every
// Unicode decimal digit set is ten consecutive code points beginning at
// its own zero, which is what makes this subtraction exact rather than
// a lookup.
func zeroOf(r rune) rune {
	for v := rune(0); v <= 9; v++ {
		if unicode.IsDigit(r-v) && !unicode.IsDigit(r-v-1) {
			return r - v
		}
	}
	return '0'
}

// SameSpace reports whether two separator characters are the same
// space. CLDR's grouping separator for French and Russian is U+00A0 or
// U+202F depending on the release, editors produce U+2009, and a
// translation that used one where the data says another is not a
// finding anybody wants.
func SameSpace(a, b rune) bool {
	return a == b || (isSpaceSeparator(a) && isSpaceSeparator(b))
}

func isSpaceSeparator(r rune) bool {
	switch r {
	case ' ', ' ', ' ', ' ', ' ':
		return true
	}
	return false
}

// DigitZeroOf is the numbering-system zero of the first digit in s, or 0
// when s has no digits.
func DigitZeroOf(s string) rune {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return zeroOf(r)
		}
	}
	return 0
}

// ScriptName names a digit set's script for a finding's message, for
// the few sets a person is likely to meet. An unnamed one is reported
// by its zero's code point, which is still exact.
func ScriptName(zero rune) string {
	switch zero {
	case '0':
		return "Latin"
	case '٠':
		return "Arabic-Indic"
	case '۰':
		return "Extended Arabic-Indic"
	case '०':
		return "Devanagari"
	case '০':
		return "Bengali"
	case '๐':
		return "Thai"
	case '０':
		return "fullwidth"
	}
	return ""
}

// primaryOf is a tag's language subtag, lowercased.
func primaryOf(tag string) string {
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		return strings.ToLower(tag[:i])
	}
	return strings.ToLower(tag)
}
