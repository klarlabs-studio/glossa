package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/capture"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa capture --check` (RFC 0005 §13 wave 4): one command that
// captures and checks, and the two-sighting rule that decides when what
// it measured in a headless browser is allowed to fail a build.

// probeFinding is a finding as the page's probe pass writes one: the
// glossa.finding/v1 shape with no fingerprint and no route, which only
// a caller that knows the catalog and the capture can fill.
func probeFinding(code, key string) domain.Finding {
	return domain.Finding{
		Schema: domain.Schema, Layer: domain.LayerVisual, Code: code, Severity: domain.Warning,
		Locus: domain.Locus{Key: key, Region: "r_0"}, Message: "Clipped: 210×20 px of text in 148×20 px.",
	}
}

// fakeProbedRun stands in for Chrome and hands back the probe findings
// the page would have measured, with the options it was given.
func fakeProbedRun(t *testing.T, probes []domain.Finding, opts *capture.Options) {
	t.Helper()
	old := runCapture
	t.Cleanup(func() { runCapture = old })
	runCapture = func(_ context.Context, p *capture.Plan, o capture.Options) ([]capture.Shot, error) {
		if opts != nil {
			*opts = o
		}
		var shots []capture.Shot
		for _, j := range p.Jobs {
			data := solidPNG(t, j.Viewport.Width/10, 120)
			sum := sha256.Sum256(data)
			shots = append(shots, capture.Shot{PNG: data, Probes: probes, Capture: capture.Capture{
				Route: j.Route, URL: j.URL, Locale: j.Locale,
				Viewport: capture.Viewport{Width: j.Viewport.Width, Height: j.Viewport.Height},
				Image:    capture.Image{SHA256: hex.EncodeToString(sum[:]), Width: j.Viewport.Width / 10, Height: 120},
				Renders:  []capture.Render{{Index: 0, Key: "home.title", Locale: j.Locale}},
				Regions:  []capture.Region{{Key: "home.title", Kind: "element", Box: capture.Box{Width: 50, Height: 10}, Visible: true}},
			}})
		}
		return shots, nil
	}
}

// capturing is a project whose catalogs are pushed, with a capture plan
// and a policy that requires no locale — so what the check says is what
// the captures said, and nothing else.
func capturing(t *testing.T, rules ...map[string]any) (*fakeServer, *workspace) {
	t.Helper()
	srv, w := pushed(t)
	doc := map[string]any{
		"schema":               "glossa.check-policy/v1",
		"require_complete":     "none",
		"fail_on":              "error",
		"missing_translations": "error",
	}
	if len(rules) > 0 {
		doc["rules"] = rules
	}
	srv.mu.Lock()
	srv.policyDoc = map[string]any{"version": 4, "document": doc}
	srv.mu.Unlock()
	withCapturePlan(w, "capture:\n  base_url: http://localhost:4173\n  application: web\n  locales: [en]\n  routes:\n    - route: /\n")
	return srv, w
}

func visualFindings(doc captureCheckJSON) []domain.Finding {
	var out []domain.Finding
	for _, f := range doc.Check.Findings {
		if f.Layer == domain.LayerVisual {
			out = append(out, f)
		}
	}
	return out
}

// captureCheckJSON is `glossa capture --check --json`: the capture
// document with the whole check run inside it.
type captureCheckJSON struct {
	Schema   string `json:"schema"`
	Captures []struct {
		Locale string `json:"locale"`
	} `json:"captures"`
	Check *checkJSON `json:"check"`
}

// The visual findings are reported with every other layer, in the one
// finding shape, and the run passes: a finding seen once is a warning.
func TestCaptureCheckReportsTheVisualLayer(t *testing.T) {
	_, w := capturing(t)
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)

	var doc captureCheckJSON
	w.json(&doc, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if doc.Check == nil || doc.Check.Schema != checkSchema {
		t.Fatalf("check = %+v", doc.Check)
	}
	fs := visualFindings(doc)
	if len(fs) != 1 {
		t.Fatalf("visual findings = %+v", doc.Check.Findings)
	}
	f := fs[0]
	switch {
	case f.Code != "text-clipped" || f.Severity != domain.Warning:
		t.Errorf("finding = %+v", f)
	case f.Fingerprint == "":
		t.Error("the finding has no fingerprint")
	case f.Locus.Route != "/" || f.Locus.Locale != "en" || f.Locus.Region != "r_0":
		t.Errorf("locus = %+v", f.Locus)
	case f.Sightings() != 1 || !f.Provisional():
		t.Errorf("sightings = %d, provisional %v", f.Sightings(), f.Provisional())
	}
	// The probe named a key; the snapshot resolved it to the catalog
	// message ID, which is what the fingerprint is hashed over — so the
	// print the terminal shows is the print the capture ingest stores and
	// a waiver written against either matches the other (RFC 0005 §2.1).
	if f.Locus.Message != "msg_home.title" {
		t.Errorf("locus = %+v, want the catalog message ID the snapshot resolved", f.Locus)
	}
	if want := domain.Fingerprint(domain.LayerVisual, "text-clipped",
		domain.Locus{Message: "msg_home.title", Locale: "en"}, ""); f.Fingerprint != want {
		t.Errorf("fingerprint = %s, want the server's over the message ID (%s)", f.Fingerprint, want)
	}
	if !slices.Contains(doc.Check.Layers, domain.LayerVisual) {
		t.Errorf("layers = %v, want the visual one among them", doc.Check.Layers)
	}
	r := w.run("capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main")
	r.want(t, ExitOK)
	for _, s := range []string{"the visual layer found 1 finding on 2 captures", "text-clipped"} {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("output lacks %q:\n%s", s, r.stdout)
		}
	}
}

// The two-sighting rule end to end: a policy that makes visual findings
// errors cannot fail the first capture, and fails the second.
func TestCaptureCheckPromotesOnTheSecondSighting(t *testing.T) {
	_, w := capturing(t, map[string]any{"layer": "visual", "severity": "error"})
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)

	var first captureCheckJSON
	w.json(&first, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if f := visualFindings(first)[0]; f.Severity != domain.Warning {
		t.Fatalf("first sighting = %q, want a warning no rule can raise", f.Severity)
	}
	if !strings.Contains(w.read(sightingsPath), visualFindings(first)[0].Fingerprint) {
		t.Fatalf("the run remembered nothing:\n%s", w.read(sightingsPath))
	}

	var second captureCheckJSON
	w.json(&second, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitCheckFailed)
	f := visualFindings(second)[0]
	if f.Sightings() != 2 || f.Provisional() || f.Severity != domain.Error {
		t.Errorf("second sighting = %+v", f)
	}
	if second.Check.Passed || second.Check.Errors == 0 {
		t.Errorf("check = %+v", second.Check)
	}

	// A capture that no longer shows it starts the count again.
	fakeProbedRun(t, nil, nil)
	w.json(&second, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)
	var again captureCheckJSON
	w.json(&again, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if f := visualFindings(again)[0]; f.Sightings() != 1 {
		t.Errorf("after a clean capture = %d sightings, want 1", f.Sightings())
	}
}

// The thresholds the page measures against are the policy's, handed to
// the probe pass with the capture's options (RFC 0005 §5.2).
func TestCaptureCheckHandsThePolicysThresholdsToTheProbe(t *testing.T) {
	_, w := capturing(t)
	var opts capture.Options
	fakeProbedRun(t, nil, &opts)
	w.run("capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if opts.Probe == nil {
		t.Fatal("the probe pass was given no thresholds")
	}
	if p := *opts.Probe; p.Slack != 1 || p.Overlap != 25 || p.Tolerance != 0 || p.Max != 500 {
		t.Errorf("thresholds = %+v", *opts.Probe)
	}

	// Without --check there is no policy in hand, and the probe keeps
	// its own defaults.
	var plain capture.Options
	fakeProbedRun(t, nil, &plain)
	w.run("capture", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if plain.Probe != nil {
		t.Errorf("a capture without --check carried thresholds: %+v", plain.Probe)
	}
}

// Another application's sightings are not this one's evidence.
func TestCaptureCheckIgnoresAnotherApplicationsSightings(t *testing.T) {
	_, w := capturing(t)
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)
	var doc captureCheckJSON
	w.json(&doc, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)

	var file sightingsFile
	if err := json.Unmarshal([]byte(w.read(sightingsPath)), &file); err != nil {
		t.Fatal(err)
	}
	if file.Schema != sightingsSchema || file.Application != "web" || file.Project != "shop" {
		t.Fatalf("record = %+v", file)
	}
	file.Application = "admin"
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	w.write(sightingsPath, string(body))
	w.json(&doc, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if f := visualFindings(doc)[0]; f.Sightings() != 1 {
		t.Errorf("sightings = %d; another application's record promoted a finding", f.Sightings())
	}
}

// A record that cannot be read costs one promotion and never a run: the
// worst a lost sighting does is report a real problem as a warning.
func TestCaptureCheckSurvivesAnUnreadableRecord(t *testing.T) {
	_, w := capturing(t)
	w.write(sightingsPath, "{not json")
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)
	var doc captureCheckJSON
	w.json(&doc, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if f := visualFindings(doc)[0]; f.Sightings() != 1 {
		t.Errorf("sightings = %d, want a fresh count", f.Sightings())
	}
}
