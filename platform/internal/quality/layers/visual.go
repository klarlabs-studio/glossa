package layers

import (
	"fmt"
	"sort"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The visual layer (RFC 0005 §5): what a capture's probe pass measured
// in the page, reported like every other layer.
//
// The layer computes nothing itself. The measurements need live layout
// — `scrollWidth`, `getComputedStyle`, `document.fonts.check()` — which
// exists only while the page is open, so they are taken in the browser,
// in the product's CI, by `@klarlabs-studio/glossa/capture`'s probe pass. What is left
// here is the part that must not live in a browser: the identity of a
// finding (its fingerprint), where it is (the locus), and the rule that
// decides when one may be believed.
//
// That rule is the two-sighting rule of §5.2. A visual finding is a
// warning on first sighting and becomes eligible for error only when
// the same fingerprint appears in two consecutive captures of the same
// (route, viewport, locale). Headless Chrome's text metrics move with
// font availability, and scout can pass no extra Chrome flags (the gap
// filed in platform/README.md), so a single sighting is not evidence —
// and a check that goes red on a font that was installed on one runner
// and not the next is a check people turn off.

// VisualScope is one capture's (route, viewport, locale): the unit the
// two-sighting rule counts in. The same clipped button at two viewports
// is two sightings of two different things, because the two viewports
// lay the page out differently and only one of them may be wrong.
type VisualScope struct {
	Route  string
	Width  int
	Height int
	Locale string
}

// Key identifies the scope in a sightings record. It is written to
// disk, so its spelling is part of the record's format.
func (s VisualScope) Key() string {
	return fmt.Sprintf("%s|%dx%d|%s", s.Route, s.Width, s.Height, s.Locale)
}

// Probed is one capture's probe findings, as the page wrote them: the
// `glossa.finding/v1` shape with no fingerprint (a browser has the key
// and not the catalog's message ID, so a fingerprint computed there
// would not be the one the server computes) and with the locus's route
// and capture left for the caller to fill.
//
// The message ID is the caller's too, and it is not optional: a caller
// that can resolve the key resolves it before promoting — Project.Identify
// in `glossa capture --check`, the upload itself at the capture ingest —
// because the fingerprint PromoteVisual mints is hashed over it.
type Probed struct {
	Scope    VisualScope
	Findings []domain.Finding
}

// Seen is the fingerprints the previous capture of each scope showed,
// by VisualScope.Key. It is the whole state the two-sighting rule needs:
// what the last capture of this scope found. A fingerprint missing from
// it starts its count again, which is what "consecutive" means.
type Seen map[string][]string

// Visual is the visual layer as a Checker: findings measured elsewhere,
// already promoted, reported with everything else.
type Visual struct {
	Findings []domain.Finding
}

// Layer names the layer.
func (v Visual) Layer() domain.Layer { return domain.LayerVisual }

// Check answers with the probe pass's findings. It reads neither the
// project nor the policy: the measurements are the page's and the
// grading is Evaluate's.
func (v Visual) Check(*Project, checkpolicy.Policy) []domain.Finding { return v.Findings }

// PromoteVisual seals one run's probe findings and applies the
// two-sighting rule to them, against what the previous run of each
// scope saw. It answers with the layer and with the sightings to
// remember for the next run.
//
// Every finding comes out as a warning. Promotion makes a finding
// *eligible* for error — it is what lets a policy rule
// (`{layer: visual, severity: error}`) reach it — and never an error by
// itself, so a project that says nothing about the visual layer is
// never failed by one (RFC 0005 §5.2).
//
// A fingerprint seen in more than one scope is reported once, with the
// highest sighting count any scope gave it: it is one problem, and the
// run that confirmed it anywhere confirmed it.
//
// The sightings that come back are this run's alone: a scope this run
// did not capture keeps no history, because two captures with a gap
// between them are not two consecutive captures of anything.
func PromoteVisual(prev Seen, probed []Probed, t checkpolicy.VisualThresholds) (Visual, Seen) {
	next := Seen{}
	var order []string
	byPrint := map[string]domain.Finding{}
	for _, c := range probed {
		key := c.Scope.Key()
		before := map[string]bool{}
		for _, fp := range prev[key] {
			before[fp] = true
		}
		var here []string
		for i, f := range c.Findings {
			if i >= t.MaxFindingsPerCapture {
				break
			}
			f = seal(f, c.Scope)
			here = append(here, f.Fingerprint)
			sightings := 1
			if before[f.Fingerprint] {
				sightings = t.PromoteAfterSightings
			}
			f = f.WithSightings(sightings)
			if old, ok := byPrint[f.Fingerprint]; ok {
				if old.Sightings() >= sightings {
					continue
				}
				byPrint[f.Fingerprint] = old.WithSightings(sightings)
				continue
			}
			byPrint[f.Fingerprint] = f
			order = append(order, f.Fingerprint)
		}
		sort.Strings(here)
		next[key] = dedupe(here)
	}
	out := make([]domain.Finding, 0, len(order))
	for _, fp := range order {
		out = append(out, byPrint[fp])
	}
	return Visual{Findings: out}, next
}

// seal fills what the page could not know and gives the finding its
// identity: the route and the viewport's locale come from the capture,
// the layer is the visual one whatever the page said, and the severity
// is a warning, because a probe may not grade itself.
func seal(f domain.Finding, s VisualScope) domain.Finding {
	f.Layer = domain.LayerVisual
	f.Severity = domain.Warning
	if f.Locus.Route == "" {
		f.Locus.Route = s.Route
	}
	if f.Locus.Locale == "" {
		f.Locus.Locale = s.Locale
	}
	return domain.New(f)
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || sorted[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}
