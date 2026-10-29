//go:build integration

package capture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/capture"
	"go.klarlabs.de/glossa/platform/internal/cli/capture/capturetest"
	"go.klarlabs.de/glossa/platform/internal/cli/extract"
)

// Headless Chrome against the fixture app (capture/testdata/app): regions
// for t() text, a component host, an attribute and a hidden message; a
// message only the wide viewport shows; a playbook; the black-out; the
// production refusal. Skipped without Chrome, except in CI.
//
// Every test attaches to a Chrome capturetest started (Options.CDP), so
// the attach path CI needs is the one a developer runs too.

func run(t *testing.T, plan *capture.Plan) []capture.Shot {
	t.Helper()
	shots, err := capture.Run(context.Background(), plan, capture.Options{CDP: capturetest.StartChrome(t)})
	if err != nil {
		t.Fatal(err)
	}
	return shots
}

// regionsOf are the regions of key in c: its host's, or its renders'.
func regionsOf(c capture.Capture, key string) []capture.Region {
	idx := map[int]bool{}
	for _, r := range c.Renders {
		if r.Key == key {
			idx[r.Index] = true
		}
	}
	var out []capture.Region
	for _, r := range c.Regions {
		if r.Key == key || (r.Index != nil && idx[*r.Index]) {
			out = append(out, r)
		}
	}
	return out
}

func renderLocale(c capture.Capture, key string) string {
	for _, r := range c.Renders {
		if r.Key == key {
			return r.Locale
		}
	}
	return ""
}

func TestCaptureTheFixtureApp(t *testing.T) {
	app := capturetest.NewApp(t)
	cfg := load(t, `
  base_url: `+app.URL+`
  locales: [de, ja]
  locale: { query: lang }
  routes:
    - { route: /, playbook: open.json }
  cookies: [{ name: session, value: "${FIXTURE_SESSION}" }]
  headers: { X-Fixture-User: "${FIXTURE_USER}" }
`, map[string]string{"open.json": `{"name":"open the dialog","actions":[{"type":"click","selector":"#open"}]}`})
	plan, err := capture.NewPlan(cfg, "", env(map[string]string{"FIXTURE_SESSION": "s3cret", "FIXTURE_USER": "lina"}))
	if err != nil {
		t.Fatal(err)
	}
	shots := run(t, plan)

	var got []string
	for _, s := range shots {
		c := s.Capture
		got = append(got, c.Locale+" "+itoa(c.Viewport.Width))
	}
	if strings.Join(got, ",") != "de 1280,de 390,ja 1280,ja 390" {
		t.Fatalf("captures = %v", got)
	}
	doc := capture.NewDocument(extract.Header{Application: "web", Commit: strings.Repeat("a", 40), Branch: "main",
		Tool: extract.Tool{Name: "glossa", Version: "0.0.0-dev"}}, nil)
	for _, s := range shots {
		doc.Captures = append(doc.Captures, s.Capture)
	}
	raw, _ := json.Marshal(doc)
	if err := schemaErrors(t, raw); err != nil {
		t.Fatalf("schema: %v", err)
	}

	for _, s := range shots {
		c := s.Capture
		name := c.Locale + "@" + itoa(c.Viewport.Width)
		visible := func(key, kind string) bool {
			rs := regionsOf(c, key)
			if len(rs) == 0 {
				t.Errorf("%s: no region for %s", name, key)
				return false
			}
			for _, r := range rs {
				if r.Kind != kind {
					t.Errorf("%s: %s is a %s region, want %s", name, key, r.Kind, kind)
				}
			}
			return rs[0].Visible
		}
		if !visible("home.title", "text") || !visible("cart.checkout", "element") || !visible("search.placeholder", "attribute") {
			t.Errorf("%s: a shown message isn't visible: %+v", name, c.Regions)
		}
		if visible("hidden.note", "text") {
			t.Errorf("%s: hidden.note is visible", name)
		}
		if !visible("dialog.body", "text") {
			t.Errorf("%s: the playbook didn't open the dialog", name)
		}
		if wide := c.Viewport.Width > 600; visible("desktop.hint", "text") != wide {
			t.Errorf("%s: desktop.hint visible = %v", name, !wide)
		}
		if visible("account.label", "text") {
			t.Errorf("%s: a message under data-glossa-redact is visible", name)
		}
		if a := regionsOf(c, "search.placeholder"); len(a) > 0 && a[0].Attribute != "placeholder" {
			t.Errorf("%s: attribute = %q", name, a[0].Attribute)
		}
		if s.Redacted != 1 {
			t.Errorf("%s: redacted = %d", name, s.Redacted)
		}

		// The image: the full page, PNG, named by its digest, with the redaction black.
		img, err := png.Decode(bytes.NewReader(s.PNG))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != c.Image.Width || b.Dy() != c.Image.Height || b.Dx() != c.Viewport.Width || b.Dy() < 1200 {
			t.Errorf("%s: image %v, capture %+v", name, b, c.Image)
		}
		label := regionsOf(c, "account.label")[0].Box
		if r, g, b := rgb(img, int(label.X+label.Width/2), int(label.Y+label.Height/2)); r+g+b != 0 {
			t.Errorf("%s: the redacted label isn't black: %d,%d,%d", name, r, g, b)
		}
		// Boxes and pixels agree: the title's text is inside its region.
		if title := regionsOf(c, "home.title")[0].Box; !dark(img, title) {
			t.Errorf("%s: no text pixels in the title's region %+v", name, title)
		}
		if green(img) {
			t.Errorf("%s: the redacted element's green background shows", name)
		}
	}
	if de, ja := shots[0].Capture, shots[2].Capture; renderLocale(de, "home.title") != "de" || renderLocale(ja, "home.title") != "ja" ||
		renderLocale(ja, "search.placeholder") != "de" {
		t.Errorf("render locales: de %+v, ja %+v", de.Renders, ja.Renders)
	}
	for _, l := range app.Logins() {
		if l.Header != "lina" || l.Cookie != "s3cret" {
			t.Errorf("page request without the fixture login: %+v", l)
		}
	}
}

// The visual probe pass in a real browser (RFC 0005 §5): the thresholds
// `glossa capture` hands the page are the check policy's, and what comes
// back is findings and line-box metrics.
//
// The fixture app renders the ja page's search placeholder from de, so
// `explain()` has something true to say and `untranslated-on-screen` has
// something to report. The second run proves the thresholds *arrive*:
// the same page, the same probes, a cap of none.
func TestCaptureProbesWithThePolicysThresholds(t *testing.T) {
	app := capturetest.NewApp(t)
	cfg := load(t, "  base_url: "+app.URL+
		"\n  locales: [ja]\n  locale: { query: lang }\n  viewports: [{ width: 1280, height: 800 }]\n  routes: [{ route: / }]\n", nil)
	plan, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := capturetest.StartChrome(t)
	thresholds := &capture.ProbeOptions{Slack: 1, Overlap: 25, Tolerance: 0, Max: 500}
	shots, err := capture.Run(context.Background(), plan, capture.Options{CDP: endpoint, Probe: thresholds})
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) != 1 {
		t.Fatalf("captures = %d", len(shots))
	}
	s := shots[0]
	if s.Metrics["home.title"] < 1 {
		t.Errorf("metrics = %v, want a line count for every text region", s.Metrics)
	}
	var codes []string
	for _, f := range s.Probes {
		codes = append(codes, f.Code)
		if f.Schema != "glossa.finding/v1" || f.Layer != "visual" || f.Severity != "warning" || f.Locus.Region == "" {
			t.Errorf("probe finding = %+v", f)
		}
	}
	if !strings.Contains(strings.Join(codes, ","), "untranslated-on-screen") {
		t.Errorf("probes = %v, want the placeholder the ja page takes from de", codes)
	}

	// The same page with a cap of none: the page measured against what it
	// was given, so the thresholds crossed the wire.
	capped, err := capture.Run(context.Background(), plan, capture.Options{
		CDP: endpoint, Probe: &capture.ProbeOptions{Slack: 1, Overlap: 25, Max: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped[0].Probes) != 0 {
		t.Errorf("a capture capped at no findings reported %d", len(capped[0].Probes))
	}
}

func TestCaptureRefusesAProductionPage(t *testing.T) {
	app := capturetest.NewApp(t)
	cfg := load(t, "  base_url: "+app.URL+"\n  routes:\n    - { route: /, url: '/?env=production' }\n", nil)
	plan, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = capture.Run(context.Background(), plan, capture.Options{CDP: capturetest.StartChrome(t)})
	var r *capture.Refusal
	if !errors.As(err, &r) || r.Code != "production_page" {
		t.Fatalf("err = %v, want a production_page refusal", err)
	}
}

func TestCaptureRefusesAPageInAnotherLocale(t *testing.T) {
	app := capturetest.NewApp(t)
	// The page reads ?lang=, the plan sends ?locale=: every page renders de.
	cfg := load(t, "  base_url: "+app.URL+"\n  locales: [ja]\n  locale: { query: locale }\n  viewports: [{ width: 800, height: 600 }]\n  routes: [{ route: / }]\n", nil)
	plan, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = capture.Run(context.Background(), plan, capture.Options{CDP: capturetest.StartChrome(t)})
	var r *capture.Refusal
	if !errors.As(err, &r) || r.Code != "locale_mismatch" || !strings.Contains(r.Reason, "renders de") {
		t.Fatalf("err = %v, want a locale_mismatch refusal", err)
	}
}

// Glossa attaches to a browser it didn't start, so it must leave that
// browser running and close only the tabs it opened.
func TestCaptureLeavesAnAttachedBrowserAlone(t *testing.T) {
	endpoint := capturetest.StartChrome(t)
	app := capturetest.NewApp(t)
	cfg := load(t, "  base_url: "+app.URL+"\n  viewports: [{ width: 800, height: 600 }]\n  routes: [{ route: / }]\n", nil)
	plan, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	before := pageTargets(t, endpoint)
	if _, err := capture.Run(context.Background(), plan, capture.Options{CDP: endpoint}); err != nil {
		t.Fatal(err)
	}
	// Still answering, with no tab of the run left behind. Chrome drops a
	// closed target a moment after the close, so wait for it rather than
	// sampling once: a tab that leaks never goes away.
	after := before
	for range 50 {
		if after = pageTargets(t, endpoint); after == before {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if after != before {
		t.Errorf("pages: %d before the run, %d after", before, after)
	}
	// And a second run attaches to the same browser.
	if _, err := capture.Run(context.Background(), plan, capture.Options{CDP: endpoint}); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

// pageTargets is how many page targets the browser has.
func pageTargets(t *testing.T, endpoint string) int {
	t.Helper()
	resp, err := http.Get(endpoint + "/json/list")
	if err != nil {
		t.Fatalf("the attached browser is gone: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var targets []struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, tg := range targets {
		if tg.Type == "page" {
			n++
		}
	}
	return n
}

func rgb(img image.Image, x, y int) (r, g, b uint32) {
	r, g, b, _ = img.At(x, y).RGBA()
	return r >> 8, g >> 8, b >> 8
}

// dark reports whether box holds text-colored pixels.
func dark(img image.Image, box capture.Box) bool {
	for y := int(box.Y); y < int(box.Y+box.Height); y++ {
		for x := int(box.X); x < int(box.X+box.Width); x++ {
			if r, g, b := rgb(img, x, y); r+g+b < 300 {
				return true
			}
		}
	}
	return false
}

// green reports whether any pixel is the redacted element's background.
func green(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl := rgb(img, x, y); r < 20 && g > 140 && g < 180 && bl < 20 {
				return true
			}
		}
	}
	return false
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
