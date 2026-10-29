package domain

import (
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// The flake-control rule of RFC 0005 §5.2, on the finding.
//
// A visual finding is a `warning` on first sighting and becomes
// eligible for `error` only when the same fingerprint appears in two
// consecutive captures of the same (route, viewport, locale). Headless
// Chrome's text metrics move with font availability, so one sighting is
// not evidence — and "eligible" is the exact word: promotion does not
// make a finding an error, it makes a policy able to.
//
// The count lives in the finding's evidence rather than in a field of
// its own, for two reasons. `glossa.finding/v1` is a closed schema, and
// a second severity-ish field on it would be a second vocabulary for
// the thing `severity` already says. And a sighting count *is*
// evidence: it is what the run measured about the finding, next to the
// pixels the probe measured about the region.

// EvidenceSightings names the evidence field that carries how many
// consecutive captures have shown the finding.
const EvidenceSightings = "sightings"

// Sightings is how many consecutive captures of the same (route,
// viewport, locale) have shown this finding, as the run that reported
// it counted them. A finding nobody counted has none, which is 0.
func (f Finding) Sightings() int {
	switch n := f.Evidence[EvidenceSightings].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64: // a finding that has been through JSON
		return int(n)
	}
	return 0
}

// WithSightings returns f with its sighting count recorded, which is
// what makes it promotable. It is deliberately not part of the
// fingerprint: the same clipped button is the same finding whether it
// has been seen once or five times.
func (f Finding) WithSightings(n int) Finding {
	if f.Evidence == nil {
		f.Evidence = map[string]any{}
	}
	f.Evidence[EvidenceSightings] = n
	return f
}

// Provisional reports whether the finding is not yet evidence: a visual
// finding seen fewer times than the policy's PromoteAfterSightings asks
// for. The policy never raises a provisional finding to Error
// (checkpolicy.Target.Provisional).
//
// A visual finding with no sighting count is provisional, because
// nobody counted a second sighting for it. That is the safe direction:
// the worst it can do is report a real problem as a warning, and the
// alternative is failing a build on a metric a font changed.
//
// It asks the kernel for the threshold the way Layer.Advisory does, and
// for the same reason: the policy owns the number, and a second copy
// here could drift from the one that clamps.
func (f Finding) Provisional() bool {
	if f.Layer != LayerVisual {
		return false
	}
	return f.Sightings() < checkpolicy.DefaultVisualThresholds().PromoteAfterSightings
}
