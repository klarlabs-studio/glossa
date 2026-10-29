package checkpolicy

// Visual QA's thresholds (RFC 0005 §5.2), in one place the policy owns.
//
// Every number the visual layer measures against is here rather than in
// the probe that measures it or in the layer that reports it, because
// §5.2 asks for exactly that: "every threshold above is a policy-visible
// constant". A project told that its build went red because two regions
// overlapped can find the 25 % that decided it, and — when the document
// grows a knob for them — change it in one place that both the browser
// and the server read.
//
// The probe in the page does not own these numbers either: `glossa
// capture` hands them to `session.collect()` with the capture's options,
// so the page measures against the policy's thresholds and the constants
// in `runtimes/js/capture/src/probes.ts` are only the defaults a probe
// run without a driver falls back to.
//
// Every length is in **CSS pixels, never device pixels**. Headless
// Chrome's text metrics move with font availability and scout can pass
// no extra Chrome flags (the gap filed in platform/README.md), so a
// tolerance in device pixels would mean a different thing on every
// runner and on every retina laptop.
type VisualThresholds struct {
	// ClipSlackPx is how much more content than box a region may have
	// before `text-clipped` is reported: scrollWidth > clientWidth + this
	// (or the same in height). One CSS pixel absorbs sub-pixel rounding
	// without absorbing a clipped word.
	ClipSlackPx int
	// OverlapPercent is how much of the *smaller* of two regions the
	// intersection must cover before `region-overlap` is reported.
	OverlapPercent int
	// LineGrowthLines is how many line boxes a translation may gain over
	// the source capture of the same route and viewport before
	// `line-growth` is reported. Zero means any growth is reported.
	LineGrowthLines int
	// LineGrowthByLocale is the per-locale tolerance §5.2 names, for the
	// locales that need a different one (a locale whose script wraps
	// differently than the source's). A locale it does not name uses
	// LineGrowthLines.
	LineGrowthByLocale map[string]int
	// MaxFindingsPerCapture caps what one capture may report (RFC 0005
	// §10). A page that goes wrong in a thousand places is one problem,
	// and a capture that uploads a thousand findings is a denial of
	// service on the ingest.
	MaxFindingsPerCapture int
	// PromoteAfterSightings is the flake-control rule of §5.2: how many
	// consecutive captures of the same (route, viewport, locale) must
	// show the same fingerprint before it may be an error. Below it a
	// visual finding is a warning that no rule can raise, because one
	// sighting is not evidence.
	PromoteAfterSightings int
}

// DefaultVisualThresholds are RFC 0005 §5.2's numbers as the RFC states
// them. They are the check policy's answer until a document overrides
// them.
func DefaultVisualThresholds() VisualThresholds {
	return VisualThresholds{
		ClipSlackPx:           1,
		OverlapPercent:        25,
		LineGrowthLines:       0,
		MaxFindingsPerCapture: 500,
		PromoteAfterSightings: 2,
	}
}

// Visual is the thresholds this policy's visual layer measures against.
//
// The document cannot override them yet — RFC 0005 §4.1's rules select
// on layer, code, locale, namespace and environment, and say what a
// finding is *worth*, not what it takes to find one. This is the seam
// where a document that learns to say the latter is read, and the one
// place every caller already asks.
func (p Policy) Visual() VisualThresholds { return DefaultVisualThresholds() }

// LineGrowthIn is the line-box tolerance in locale: the locale's own
// where the thresholds name one, the default otherwise.
func (v VisualThresholds) LineGrowthIn(locale string) int {
	if n, ok := v.LineGrowthByLocale[locale]; ok {
		return n
	}
	return v.LineGrowthLines
}
