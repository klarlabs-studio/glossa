package checkpolicy

import "strings"

// The length layer's thresholds (RFC 0005 §3.3), in one place the
// policy owns.
//
// Same reason as VisualThresholds: a project told that its build went
// amber because a French string grew 74 % has to be able to find the
// number that decided it, and change it in one place every surface
// reads. A norm baked into the layer would be a number nobody can see
// and nobody can tune.
//
// Every length here is in **CSS pixels, never device pixels**. The
// boxes the layout budget is computed from were measured in a page, and
// a tolerance in device pixels would mean one thing on a CI runner and
// another on a retina laptop — the same reason §5.2 gives for the
// visual layer.

// ExpansionNorm is how much a translation into one language is expected
// to grow against its source, as a ratio of rendered characters.
//
// The seed values are the published expansion ranges the industry has
// used for twenty years (the IBM and W3C globalization guidance, which
// CLDR does not itself state), widened at both ends because Glossa
// measures *literal* characters and those tables measure rendered
// strings. They are a starting point and not a truth: RFC 0005 §3.3
// says the norms are "refined from the tenant's own approved
// translations", and ExpansionNorms is the map that refinement fills.
type ExpansionNorm struct {
	// Low and High bound the expected ratio. A translation outside them
	// is `expansion-suspicious`.
	Low  float64
	High float64
	// Excessive is the ratio above which the finding is
	// `expansion-excessive` instead. Zero means High × ExcessiveFactor.
	Excessive float64
}

// LengthThresholds are every number the length layer measures against.
type LengthThresholds struct {
	// ExpansionNorms are the norms by target, keyed either by a language
	// pair ("de→fr") or by a target language subtag ("fr"). The pair
	// wins where both are given, because a language's expansion is a
	// property of the pair and only approximately a property of the
	// target.
	ExpansionNorms map[string]ExpansionNorm
	// DefaultNorm applies to a target the map does not name. It is
	// deliberately wide: a language Glossa has no data for should not
	// produce findings it cannot justify (intent §41 — do not pretend
	// every language is equally supported).
	DefaultNorm ExpansionNorm
	// ExcessiveFactor turns a norm's High into its Excessive where the
	// norm does not state one.
	ExcessiveFactor float64
	// MinSourceRunes is the shortest source a ratio is computed from. A
	// two-character source makes every ratio a cliff — "OK" to "D'accord"
	// is 350 % and perfectly correct — so below this the layer says
	// nothing and `max_length` is the only thing that bounds the string.
	MinSourceRunes int
	// SingleLineMaxHeightPx is how tall a measured region may be and
	// still be treated as one line of text. The layout budget divides a
	// box's width by the characters that filled it, which is a font
	// metric only while the text did not wrap; a taller box is skipped
	// rather than guessed at.
	SingleLineMaxHeightPx float64
	// MinRegionSampleRunes is the shortest text a region's advance may
	// be derived from. A box that held three characters gives an advance
	// dominated by its padding.
	MinRegionSampleRunes int
	// OverflowSlackPercent is how far a predicted advance width may
	// exceed the region's width before `layout-overflow-predicted` is
	// reported, as a percentage of that width. It absorbs the sub-pixel
	// rounding and the proportional-font variance that an average
	// advance cannot model.
	OverflowSlackPercent float64
}

// DefaultLengthThresholds are RFC 0005 §3.3's numbers as the layer ships
// with them.
func DefaultLengthThresholds() LengthThresholds {
	return LengthThresholds{
		ExpansionNorms:        defaultExpansionNorms(),
		DefaultNorm:           ExpansionNorm{Low: 0.5, High: 1.8},
		ExcessiveFactor:       1.5,
		MinSourceRunes:        12,
		SingleLineMaxHeightPx: 32,
		MinRegionSampleRunes:  6,
		OverflowSlackPercent:  5,
	}
}

// defaultExpansionNorms is the seed table, by target language subtag.
//
// The CJK and RTL entries are the ones that matter most and the ones a
// per-locale table is most often missing: Japanese and Chinese
// *contract* against a European source, and a check that expected them
// to grow would flag every correct translation.
func defaultExpansionNorms() map[string]ExpansionNorm {
	return map[string]ExpansionNorm{
		"en": {Low: 0.65, High: 1.20},
		"de": {Low: 0.85, High: 1.45},
		"nl": {Low: 0.85, High: 1.40},
		"da": {Low: 0.80, High: 1.30},
		"sv": {Low: 0.80, High: 1.30},
		"no": {Low: 0.80, High: 1.30},
		"fr": {Low: 0.90, High: 1.45},
		"es": {Low: 0.90, High: 1.45},
		"pt": {Low: 0.90, High: 1.45},
		"it": {Low: 0.85, High: 1.40},
		"ro": {Low: 0.90, High: 1.45},
		"pl": {Low: 0.90, High: 1.50},
		"cs": {Low: 0.85, High: 1.45},
		"ru": {Low: 0.85, High: 1.45},
		"uk": {Low: 0.85, High: 1.45},
		"tr": {Low: 0.75, High: 1.30},
		"fi": {Low: 0.80, High: 1.45},
		"hu": {Low: 0.85, High: 1.45},
		"el": {Low: 0.90, High: 1.50},
		"ar": {Low: 0.70, High: 1.30},
		"he": {Low: 0.65, High: 1.20},
		"fa": {Low: 0.70, High: 1.30},
		"hi": {Low: 0.80, High: 1.40},
		"th": {Low: 0.55, High: 1.30},
		"vi": {Low: 0.85, High: 1.45},
		// The CJK lows are far below the published tables' because those
		// tables measure rendered width and this layer counts
		// characters: one Han character carries a German compound, and a
		// correct Japanese translation of a German sentence really is a
		// quarter of its length.
		"ja": {Low: 0.22, High: 0.95},
		"ko": {Low: 0.28, High: 1.00},
		"zh": {Low: 0.16, High: 0.85},
	}
}

// Length is the thresholds this policy's length layer measures against.
//
// As with Visual: the document cannot override them yet, because RFC
// 0005 §4.1's rules say what a finding is *worth* and not what it takes
// to find one. This is the seam where a document that learns to say the
// latter is read, and the one place every caller already asks.
func (p Policy) Length() LengthThresholds { return DefaultLengthThresholds() }

// NormFor is the expansion norm for one language pair: the pair's own
// where the table names it, the target language's where it names that,
// and DefaultNorm otherwise.
//
// Both tags are reduced to their language subtag. `fr-CA` expands like
// `fr` against a German source, and a table with an entry per region
// would be a table nobody could fill.
func (t LengthThresholds) NormFor(source, target string) ExpansionNorm {
	src, tgt := primary(source), primary(target)
	if n, ok := t.ExpansionNorms[src+"→"+tgt]; ok {
		return t.withExcessive(n)
	}
	if n, ok := t.ExpansionNorms[tgt]; ok {
		return t.withExcessive(n)
	}
	return t.withExcessive(t.DefaultNorm)
}

func (t LengthThresholds) withExcessive(n ExpansionNorm) ExpansionNorm {
	if n.Excessive == 0 {
		factor := t.ExcessiveFactor
		if factor <= 1 {
			factor = DefaultLengthThresholds().ExcessiveFactor
		}
		n.Excessive = n.High * factor
	}
	return n
}

// primary is a BCP 47 tag's language subtag, lowercased. The tags a
// layer sees are already canonical (bcp47.Parse made them), so this
// takes the first subtag and does not re-parse.
func primary(tag string) string {
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		return strings.ToLower(tag[:i])
	}
	return strings.ToLower(tag)
}
