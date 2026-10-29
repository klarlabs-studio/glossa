package checkpolicy

// The source layer's thresholds (RFC 0005 §3.5), in one place the
// policy owns.
//
// Every code the source layer emits is a warning by default, because
// source copy is the product team's call and a check that failed a
// build over a colon would be turned off within the week. What is
// tunable here is not the severity — the policy document already says
// that — but what counts as the shape at all: how short "short" is,
// which characters make a message look like a fragment, and which
// symbols look like a format somebody typed instead of asking for.

// SourceThresholds are every number and table the source layer reads.
type SourceThresholds struct {
	// AmbiguousMaxWords is how few words a message may have before it is
	// `ambiguous-short` without a description. One word is the RFC's
	// case ("Open", "Save"); two is where most products' ambiguity
	// actually lives ("Open file" is not ambiguous, "Set up" is).
	AmbiguousMaxWords int
	// FragmentEndings are the characters that make a message look like
	// half of a sentence somebody means to concatenate.
	FragmentEndings []rune
	// CurrencySymbols and FormatSymbols are what `hardcoded-format`
	// looks for beside a plain placeholder: a symbol the author typed
	// because the placeholder was not typed.
	CurrencySymbols []rune
	FormatSymbols   []rune
	// NumericFunctions are the MessageFormat functions that make a
	// placeholder a *typed* one, which is what `hardcoded-format` asks
	// the author to reach for instead.
	NumericFunctions []string
	// PluralMarkers are the hand-written plurals of `manual-plural`.
	// They are matched against the literal text, so `(s)` is the string
	// and not a pattern.
	PluralMarkers []string
}

// DefaultSourceThresholds are RFC 0005 §3.5's rules as the layer ships
// with them.
func DefaultSourceThresholds() SourceThresholds {
	return SourceThresholds{
		AmbiguousMaxWords: 1,
		// A trailing space or colon, or a message that opens a quotation
		// and never closes it, is a fragment. A leading space is the same
		// signal from the other end.
		FragmentEndings:  []rune{':', '–', '—', ',', ';'},
		CurrencySymbols:  []rune{'$', '€', '£', '¥', '₽', '₹', '₩'},
		FormatSymbols:    []rune{'%', '‰'},
		NumericFunctions: []string{"number", "integer", "currency", "percent", "date", "time", "datetime"},
		PluralMarkers:    []string{"(s)", "(es)", "(n)", "(ren)", "（s）"},
	}
}

// Source is the thresholds this policy's source layer reads.
func (p Policy) Source() SourceThresholds { return DefaultSourceThresholds() }
