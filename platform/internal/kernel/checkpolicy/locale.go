package checkpolicy

// The locale layer's thresholds and the curated data it needs beyond
// CLDR (RFC 0005 §3.4), in one place the policy owns.
//
// Almost everything this layer decides comes from CLDR, through
// `golang.org/x/text`: a locale's decimal and grouping separators, its
// default numbering system's digits, its script, and the bidi algorithm
// itself. What is here is the part CLDR does not state — French's
// narrow no-break space before `;:!?` is a typographic convention, not
// locale data — plus the guards that keep a correct translation from
// being reported.
//
// Intent §41 is the reason this is a table with holes rather than a
// default with exceptions: a locale whose conventions Glossa has no
// data for gets no findings from the rules that need them, and the
// dashboard says which layers are available per locale. A false green
// is bad; a confident red about a language nobody checked is worse.

// SpacingConvention is one locale's spacing rules beyond CLDR.
type SpacingConvention struct {
	// BeforePunctuation are the characters that take a space before
	// them in this locale (French's `;:!?`).
	BeforePunctuation []rune
	// Space is the character the locale wants there. French wants
	// U+202F NARROW NO-BREAK SPACE; a plain space breaks across a line
	// and leaves the punctuation stranded.
	Space rune
	// AlsoAccept are the characters that are wrong but not worth a
	// finding — U+00A0 before French punctuation is what most editors
	// and most of the web produce, and reporting it would drown the
	// case that matters.
	AlsoAccept []rune
}

// LocaleThresholds are every number and table the locale layer measures
// against.
type LocaleThresholds struct {
	// Spacing are the curated spacing conventions, by language subtag.
	// A locale the map does not name gets no `spacing-convention`
	// findings at all.
	Spacing map[string]SpacingConvention
	// BidiControls are the characters a translation may not carry. The
	// runtimes add isolation themselves (runtimes/SPEC.md §5), so a
	// control in the text is either a copy-and-paste accident or an
	// attempt to reorder text the runtime already ordered.
	BidiControls []rune
	// MinUntranslatedRunes is the shortest text `untranslated-suspected`
	// is reported for. "OK" is "OK" in every locale on earth, and a
	// check that said otherwise would be wrong about the most common
	// string in any product.
	MinUntranslatedRunes int
	// MinNumberDigits is the fewest digits a numeric literal must have
	// before its separators are graded. A version number and a street
	// number are not prices.
	MinNumberDigits int
}

// DefaultLocaleThresholds are RFC 0005 §3.4's rules as the layer ships
// with them.
func DefaultLocaleThresholds() LocaleThresholds {
	return LocaleThresholds{
		Spacing: map[string]SpacingConvention{
			// French, and the locales that took its typography.
			"fr": {BeforePunctuation: []rune{';', ':', '!', '?', '»'}, Space: ' ', AlsoAccept: []rune{' '}},
		},
		BidiControls: []rune{
			'‎', '‏', // LRM, RLM
			'‪', '‫', '‬', '‭', '‮', // LRE, RLE, PDF, LRO, RLO
			'⁦', '⁧', '⁨', '⁩', // LRI, RLI, FSI, PDI
		},
		MinUntranslatedRunes: 8,
		MinNumberDigits:      4,
	}
}

// Locale is the thresholds this policy's locale layer measures against.
//
// As with Visual and Length: the document cannot override them yet,
// because RFC 0005 §4.1's rules say what a finding is worth and not
// what it takes to find one. This is the seam where a document that
// learns to say the latter is read.
func (p Policy) Locale() LocaleThresholds { return DefaultLocaleThresholds() }

// SpacingFor is the spacing convention for a locale, and whether Glossa
// has one at all. The tag is reduced to its language subtag: `fr-CH`
// spaces its punctuation as `fr` does.
func (t LocaleThresholds) SpacingFor(locale string) (SpacingConvention, bool) {
	c, ok := t.Spacing[primary(locale)]
	return c, ok
}
