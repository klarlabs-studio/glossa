//go:build integration

package cli

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/capture/capturetest"
)

// glossa capture end to end: headless Chrome against the fixture app
// (capture/testdata/app), the captures uploaded to the fake Captures API,
// the coverage read from the fake Context API. Skipped without Chrome,
// except in CI.
//
// The browser is the test's own, attached through GLOSSA_CAPTURE_CDP —
// what a CI sandbox does, where scout's launcher can't start one
// (platform/README.md, "scout follow-ups").
func TestCaptureCommandInChrome(t *testing.T) {
	endpoint := capturetest.StartChrome(t)
	app := capturetest.NewApp(t)
	srv := newFakeServer(t)
	srv.ctx.usages = []map[string]any{
		usage("app_web", "home.title", "src/app.ts", 3, "/", true),
		usage("app_web", "cart.checkout", "index.html", 22, "/", true),
		usage("app_web", "search.placeholder", "src/app.ts", 4, "/", true),
		usage("app_web", "hidden.note", "src/app.ts", 5, "/", true),
		usage("app_web", "desktop.hint", "src/app.ts", 6, "/", true),
		usage("app_web", "checkout.pay", "src/pay.ts", 1, "/checkout/[step]", true),
	}
	w := withCapturePlan(newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN}), `capture:
  base_url: `+app.URL+`
  application: web
  locales: [de, ja]
  locale: { query: lang }
  routes:
    - route: /
`)
	w.env["GLOSSA_CAPTURE_CDP"] = endpoint
	var out captureDoc
	w.json(&out, "capture", "--upload", "--commit", testCommit, "--branch", "main").want(t, ExitOK)

	if out.Upload == nil || out.Upload.Captures != 4 || len(out.Captures) != 4 {
		t.Fatalf("--json = %+v", out)
	}
	up := srv.ctx.captures[0]
	if up.parts[0] != "manifest" || len(up.images) != 4 {
		t.Errorf("parts = %v", up.parts)
	}
	for d, data := range up.images {
		if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || (cfg.Width != 1280 && cfg.Width != 390) {
			t.Errorf("image %s: %+v, %v", d, cfg, err)
		}
	}
	var got []string
	for _, n := range out.Coverage.NotCaptured {
		got = append(got, n.Key)
	}
	// hidden.note never shows; desktop.hint shows at 1280; checkout.pay's route isn't in the plan.
	if strings.Join(got, ",") != "checkout.pay,hidden.note" || out.Coverage.Messages != 6 || out.Coverage.Captured != 4 {
		t.Errorf("coverage = %+v", out.Coverage)
	}
}
