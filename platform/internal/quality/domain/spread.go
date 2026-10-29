package domain

import (
	"math"
	"slices"
	"time"
)

// Spread is the p50 and p90 of a sample of durations, with the size of
// the sample they came from (RFC 0005 §8: the review queue's age and
// the lead time are both "p50/p90 of …").
//
// N == 0 means there was nothing to measure, and Measured says so.
// That distinction is the whole point of the type: an empty queue has
// no median wait, and a dashboard that printed 0 s for it would say
// "nobody waits" where the truth is "nobody is waiting". Zero is an
// answer; no answer is not zero.
type Spread struct {
	// N is how many observations produced P50 and P90.
	N int
	// P50 and P90 are the percentiles, meaningless unless Measured.
	P50 time.Duration
	P90 time.Duration
}

// Measured reports whether the percentiles stand for anything.
func (s Spread) Measured() bool { return s.N > 0 }

// NewSpread computes the percentiles of ds. It sorts a copy, so the
// caller's sample keeps the order it had.
//
// The percentiles interpolate linearly between the two closest ranks —
// the definition Postgres's percentile_cont uses — so a number computed
// here and one computed in SQL are the same number. Some of the seven
// are aggregated by the database and some are joined across contexts in
// Go, and two definitions of "median" would make those two columns of a
// dashboard quietly incomparable.
func NewSpread(ds []time.Duration) Spread {
	if len(ds) == 0 {
		return Spread{}
	}
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	return Spread{N: len(sorted), P50: percentile(sorted, 0.5), P90: percentile(sorted, 0.9)}
}

// SpreadFromSeconds takes percentiles a query already computed. A
// negative second is the sentinel a query uses for "no sample", and it
// reads back as not measured rather than as a duration.
func SpreadFromSeconds(n int, p50, p90 float64) Spread {
	if n <= 0 || p50 < 0 || p90 < 0 {
		return Spread{}
	}
	return Spread{N: n, P50: seconds(p50), P90: seconds(p90)}
}

// percentile is percentile_cont over a sorted, non-empty sample.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	// Round, never truncate. A Duration conversion truncates toward
	// zero, so an interpolation that lands a hair under a whole
	// nanosecond loses one — and whether it lands under depends on the
	// architecture, because Go fuses multiply-add on arm64 and not on
	// amd64. The p90 of 1s…10s came out 9.1s on one and 9.099999999s on
	// the other. Rounding is also the faithful reading of
	// percentile_cont, which interpolates exactly and leaves the
	// nanosecond to the caller.
	return sorted[lo] + time.Duration(math.Round(float64(sorted[hi]-sorted[lo])*frac))
}

// seconds converts a float of seconds to a Duration without losing the
// sub-second part a percentile usually has.
func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// Share is "how many of these have that": the numerator, the
// denominator, and no ratio when there is nothing to divide by.
//
// Context coverage (RFC 0005 §8) is two of these — the active messages
// with a usage, and the ones with a visible region. A project with no
// active messages has no coverage to report: 0/0 is neither 0 % nor
// 100 %, and the Prometheus gauge's convention of calling it 1 is fine
// for a graph that must plot something and wrong for a number a person
// reads.
type Share struct {
	// Of is the population — the project's active messages.
	Of int
	// With is how many of them have the thing.
	With int
}

// Measured reports whether there is a population to take a share of.
func (s Share) Measured() bool { return s.Of > 0 }

// Ratio is With/Of, 0 where there is nothing to divide.
func (s Share) Ratio() float64 {
	if !s.Measured() {
		return 0
	}
	return float64(s.With) / float64(s.Of)
}

// Without is the rest of the population.
func (s Share) Without() int { return s.Of - s.With }

// LocaleLayerCount is one locale's findings of one layer, counted as
// they stand now. The locale is "" for the layers that have none —
// `structure`, `completeness` and `source` say what is wrong with a
// message, not with a translation of it — so a project's total is every
// row and a locale's is the rows carrying it.
type LocaleLayerCount struct {
	Locale string
	LayerCount
}

// DailyFindings is one day's findings of one layer, as the rollup
// stores them (migration 0035).
type DailyFindings struct {
	// Day is a UTC date.
	Day time.Time
	// Layer is the layer that reported them, and Findings is how many
	// distinct fingerprints it reported that day.
	Layer    Layer
	Findings int
}
