package layers_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The two-sighting rule (RFC 0005 §5.2), which is the whole reason the
// visual layer has a promotion step: a finding measured once in a
// headless browser is not evidence, and a finding measured twice in a
// row in the same place is.

func phone() layers.VisualScope {
	return layers.VisualScope{Route: "/checkout", Width: 390, Height: 844, Locale: "de"}
}

func clipped(key string) domain.Finding {
	return domain.Finding{
		Layer: domain.LayerVisual, Code: "text-clipped", Severity: domain.Warning,
		Locus: domain.Locus{Key: key, Region: "r_3"}, Message: "Clipped: 210×20 px of text in 148×20 px.",
	}
}

func thresholds() checkpolicy.VisualThresholds { return checkpolicy.DefaultVisualThresholds() }

func only(t *testing.T, v layers.Visual) domain.Finding {
	t.Helper()
	if len(v.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(v.Findings), v.Findings)
	}
	return v.Findings[0]
}

// A finding nobody has seen before is a warning that no policy can
// raise, and the run remembers it for the next one.
func TestAFirstSightingIsProvisional(t *testing.T) {
	v, next := layers.PromoteVisual(nil, []layers.Probed{{Scope: phone(), Findings: []domain.Finding{clipped("checkout.pay")}}}, thresholds())
	f := only(t, v)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Sightings() != 1:
		t.Errorf("sightings = %d, want 1", f.Sightings())
	case !f.Provisional():
		t.Error("a first sighting is not provisional")
	case f.Fingerprint == "":
		t.Error("the layer left the finding without an identity")
	case f.Locus.Route != "/checkout" || f.Locus.Locale != "de":
		t.Errorf("locus = %+v, want the capture's route and locale", f.Locus)
	}
	if got := next[phone().Key()]; len(got) != 1 || got[0] != f.Fingerprint {
		t.Errorf("remembered %v, want %q", got, f.Fingerprint)
	}
}

// The same fingerprint in the next capture of the same (route,
// viewport, locale) is the second consecutive sighting, and the finding
// becomes eligible for error.
func TestASecondConsecutiveSightingIsPromoted(t *testing.T) {
	probed := []layers.Probed{{Scope: phone(), Findings: []domain.Finding{clipped("checkout.pay")}}}
	_, after := layers.PromoteVisual(nil, probed, thresholds())
	v, _ := layers.PromoteVisual(after, probed, thresholds())
	f := only(t, v)
	if f.Sightings() != 2 || f.Provisional() {
		t.Fatalf("second sighting = %d sightings, provisional %v", f.Sightings(), f.Provisional())
	}
	// Eligible, not failed: without a rule it is still a warning.
	if f.Severity != domain.Warning {
		t.Errorf("severity = %q; promotion makes a finding eligible for error, not an error", f.Severity)
	}
	p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "visual"}, Severity: checkpolicy.Error},
	}}
	e := domain.Evaluate(p, "", v.Findings)
	if e.Passed() {
		t.Error("a promoted finding didn't fail a policy that raises the visual layer")
	}
	first, _ := layers.PromoteVisual(nil, probed, thresholds())
	if !domain.Evaluate(p, "", first.Findings).Passed() {
		t.Error("a first sighting failed the same policy")
	}
}

// "Consecutive" means consecutive: a capture that doesn't show the
// finding starts its count again.
func TestAMissedCaptureResetsTheCount(t *testing.T) {
	scope := phone()
	probed := []layers.Probed{{Scope: scope, Findings: []domain.Finding{clipped("checkout.pay")}}}
	_, after := layers.PromoteVisual(nil, probed, thresholds())
	_, clean := layers.PromoteVisual(after, []layers.Probed{{Scope: scope}}, thresholds())
	if len(clean[scope.Key()]) != 0 {
		t.Fatalf("a clean capture remembered %v", clean[scope.Key()])
	}
	v, _ := layers.PromoteVisual(clean, probed, thresholds())
	if f := only(t, v); f.Sightings() != 1 || !f.Provisional() {
		t.Errorf("after a clean capture = %d sightings, provisional %v", f.Sightings(), f.Provisional())
	}
}

// The scope is (route, viewport, locale): the same finding at another
// viewport is another sighting of another thing, because the two
// viewports lay the page out differently.
func TestSightingsAreCountedPerRouteViewportAndLocale(t *testing.T) {
	desktop := phone()
	desktop.Width, desktop.Height = 1280, 800
	_, after := layers.PromoteVisual(nil, []layers.Probed{{Scope: phone(), Findings: []domain.Finding{clipped("checkout.pay")}}}, thresholds())
	v, _ := layers.PromoteVisual(after, []layers.Probed{{Scope: desktop, Findings: []domain.Finding{clipped("checkout.pay")}}}, thresholds())
	if f := only(t, v); f.Sightings() != 1 {
		t.Errorf("another viewport = %d sightings, want 1", f.Sightings())
	}
}

// One problem is one finding, however many captures show it, and a run
// that confirmed it anywhere confirmed it.
func TestOneFingerprintIsReportedOnceWithItsHighestCount(t *testing.T) {
	desktop := phone()
	desktop.Width, desktop.Height = 1280, 800
	both := []layers.Probed{
		{Scope: phone(), Findings: []domain.Finding{clipped("checkout.pay")}},
		{Scope: desktop, Findings: []domain.Finding{clipped("checkout.pay")}},
	}
	_, after := layers.PromoteVisual(nil, both[1:], thresholds())
	v, _ := layers.PromoteVisual(after, both, thresholds())
	if f := only(t, v); f.Sightings() != 2 {
		t.Errorf("sightings = %d, want the highest of the two scopes", f.Sightings())
	}
}

// RFC 0005 §10: a capture reports at most MaxFindingsPerCapture.
func TestACaptureIsCappedAtTheThreshold(t *testing.T) {
	th := thresholds()
	th.MaxFindingsPerCapture = 2
	var fs []domain.Finding
	for _, key := range []string{"a", "b", "c", "d"} {
		fs = append(fs, clipped(key))
	}
	v, _ := layers.PromoteVisual(nil, []layers.Probed{{Scope: phone(), Findings: fs}}, th)
	if len(v.Findings) != 2 {
		t.Errorf("findings = %d, want the cap of 2", len(v.Findings))
	}
}

// A probe finding is identified against the project before it is
// promoted: a browser named the key it rendered, and the fingerprint is
// hashed over the catalog message ID (RFC 0005 §2.1). Without this step
// the terminal's print is not the server's and a waiver made on one
// surface stops matching on the other.
func TestIdentifyGivesAProbeFindingTheCatalogsMessageID(t *testing.T) {
	p := &layers.Project{Messages: []layers.Message{
		{ID: "msg_1", Key: "checkout.pay"},
		{Key: "checkout.total"}, // offline: the local catalogs know no IDs
	}}
	in := []domain.Finding{
		clipped("checkout.pay"),
		clipped("checkout.total"),
		clipped("checkout.gone"), // a key the catalog has never seen
	}
	out := p.Identify(in)
	for i, want := range []string{"msg_1", "", ""} {
		if out[i].Locus.Message != want {
			t.Errorf("%s identified as %q, want %q", out[i].Locus.Key, out[i].Locus.Message, want)
		}
		if out[i].Locus.Key != in[i].Locus.Key || out[i].Code != in[i].Code {
			t.Errorf("identifying changed the finding: %+v", out[i])
		}
	}
	// A finding that already names a message keeps it, and the input is
	// never edited in place.
	if in[0].Locus.Message != "" {
		t.Errorf("Identify edited its input: %+v", in[0].Locus)
	}
	named := clipped("checkout.pay")
	named.Locus.Message = "msg_elsewhere"
	if got := p.Identify([]domain.Finding{named})[0].Locus.Message; got != "msg_elsewhere" {
		t.Errorf("message = %q, want the one the caller already knew", got)
	}

	// A finding its layer already minted — the terminology layer's, which
	// arrives from the server sealed — is re-minted: its identity moved
	// with its locus, and a print over the key is a stale one.
	minted := domain.New(clipped("checkout.pay"))
	got := p.Identify([]domain.Finding{minted})[0]
	if got.Fingerprint == minted.Fingerprint {
		t.Error("the fingerprint stayed over the key after the locus named the message")
	}
	if want := domain.Fingerprint(got.Layer, got.Code, got.Locus, got.Subject); got.Fingerprint != want {
		t.Errorf("fingerprint = %s, want %s", got.Fingerprint, want)
	}
}

// The identity reaches the fingerprint: the same probe finding
// identified against a project that knows the ID and one that does not
// prints differently, and the first print is the server's.
func TestAnIdentifiedProbeFindingPrintsAsTheServerPrintsIt(t *testing.T) {
	online := &layers.Project{Messages: []layers.Message{{ID: "msg_1", Key: "checkout.pay"}}}
	offline := &layers.Project{Messages: []layers.Message{{Key: "checkout.pay"}}}
	printOf := func(p *layers.Project) string {
		t.Helper()
		v, _ := layers.PromoteVisual(nil,
			[]layers.Probed{{Scope: phone(), Findings: p.Identify([]domain.Finding{clipped("checkout.pay")})}},
			thresholds())
		return only(t, v).Fingerprint
	}
	want := domain.Fingerprint(domain.LayerVisual, "text-clipped",
		domain.Locus{Message: "msg_1", Locale: "de"}, "")
	if got := printOf(online); got != want {
		t.Errorf("fingerprint = %s, want the print over the message ID (%s)", got, want)
	}
	// An offline run falls back to the key. Its prints are consistent
	// among themselves and deliberately not the server's: no ID was
	// resolved, and nobody holds a waiver offline.
	if got := printOf(offline); got == want {
		t.Error("a project with no message IDs printed the server's fingerprint")
	} else if key := domain.Fingerprint(domain.LayerVisual, "text-clipped",
		domain.Locus{Key: "checkout.pay", Locale: "de"}, ""); got != key {
		t.Errorf("fingerprint = %s, want the fallback over the key (%s)", got, key)
	}
}
