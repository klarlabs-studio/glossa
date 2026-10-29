package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/capture"
)

// The command around the capture library, with the browser faked: each
// job's image is a solid PNG in its viewport's size (so the two locales of
// a viewport share an image) and its regions are fixed.

const capturePlan = `capture:
  base_url: http://localhost:4173
  application: web
  locales: [en, de]
  locale: { query: lang }
  routes:
    - route: /
`

func withCapturePlan(w *workspace, plan string) *workspace {
	w.write("glossa.yaml", w.read("glossa.yaml")+plan)
	return w
}

func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// fakeRun stands in for Chrome; it records the plan it was given.
func fakeRun(t *testing.T, seen **capture.Plan, fail error) {
	t.Helper()
	old := runCapture
	t.Cleanup(func() { runCapture = old })
	runCapture = func(_ context.Context, p *capture.Plan, o capture.Options) ([]capture.Shot, error) {
		*seen = p
		if fail != nil {
			return nil, fail
		}
		var shots []capture.Shot
		for i, j := range p.Jobs {
			if o.Progress != nil {
				o.Progress(i, j)
			}
			data := solidPNG(t, j.Viewport.Width/10, 120)
			sum := sha256.Sum256(data)
			zero := 0
			shots = append(shots, capture.Shot{PNG: data, Redacted: 1, Capture: capture.Capture{
				Route: j.Route, URL: j.URL, Locale: j.Locale,
				Viewport: capture.Viewport{Width: j.Viewport.Width, Height: j.Viewport.Height},
				Image:    capture.Image{SHA256: hex.EncodeToString(sum[:]), Width: j.Viewport.Width / 10, Height: 120},
				Renders:  []capture.Render{{Index: 0, Key: "home.title", Locale: j.Locale}},
				Regions: []capture.Region{
					{Index: &zero, Kind: "text", Box: capture.Box{X: 1, Y: 1, Width: 50, Height: 10}, Visible: true},
					{Key: "cart.checkout", Kind: "element", Box: capture.Box{X: 1, Y: 20, Width: 50, Height: 10}, Visible: true},
					{Key: "hidden.note", Kind: "element", Visible: false},
				},
			}})
		}
		return shots, nil
	}
}

type captureDoc struct {
	Schema      string `json:"schema"`
	Application string `json:"application"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch"`
	Captures    []struct {
		Route    string `json:"route"`
		URL      string `json:"url"`
		Locale   string `json:"locale"`
		Viewport struct {
			Width int `json:"width"`
		} `json:"viewport"`
		Image struct {
			SHA256 string `json:"sha256"`
		} `json:"image"`
		Regions  int `json:"regions"`
		Visible  int `json:"visible"`
		Probes   int `json:"probes"`
		Redacted int `json:"redacted"`
	} `json:"captures"`
	Output *struct {
		Dir      string `json:"dir"`
		Manifest string `json:"manifest"`
		Images   int    `json:"images"`
	} `json:"output"`
	Upload *struct {
		Build              string `json:"build"`
		Captures           int    `json:"captures"`
		ImagesStored       int    `json:"images_stored"`
		ImagesDeduplicated int    `json:"images_deduplicated"`
		Findings           int    `json:"findings"`
		Replayed           bool   `json:"replayed"`
	} `json:"upload"`
	Coverage *capture.Coverage `json:"coverage"`
}

func captureServer(t *testing.T) *fakeServer {
	srv := newFakeServer(t)
	srv.ctx.usages = []map[string]any{
		usage("app_web", "home.title", "src/Home.vue", 3, "/", true),
		usage("app_web", "hidden.note", "src/Home.vue", 9, "/", true),
		usage("app_web", "cart.checkout", "src/Cart.vue", 1, "", true),
		usage("app_web", "checkout.pay", "src/Pay.vue", 4, "/checkout/[step]", true),
		usage("app_web", "not.in.catalog", "src/Home.vue", 12, "/", false),
		usage("app_admin", "admin.only", "admin/App.vue", 1, "/", true),
	}
	return srv
}

func TestCaptureWritesTheManifestAndImagesWithCoverage(t *testing.T) {
	srv := captureServer(t)
	w := withCapturePlan(newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN}), capturePlan)
	w.write(".glossa/captures/"+strings.Repeat("0", 64)+".png", "stale")
	w.write(".glossa/captures/keep.txt", "mine")
	var plan *capture.Plan
	fakeRun(t, &plan, nil)

	var out captureDoc
	w.json(&out, "capture", "--commit", testCommit, "--branch", "feat/copy").want(t, ExitOK)
	if len(plan.Jobs) != 4 || plan.Jobs[0].URL != "http://localhost:4173/?lang=de" {
		t.Fatalf("plan = %+v", plan.Jobs)
	}
	if out.Application != "web" || out.Commit != testCommit || out.Branch != "feat/copy" || len(out.Captures) != 4 || out.Upload != nil {
		t.Fatalf("--json = %+v", out)
	}
	if c := out.Captures[0]; c.Locale != "de" || c.Viewport.Width != 1280 || c.Regions != 3 || c.Visible != 2 || c.Redacted != 1 {
		t.Errorf("capture 0 = %+v", c)
	}
	dir := filepath.Join(w.dir, ".glossa", "captures")
	if out.Output == nil || out.Output.Dir != dir || out.Output.Images != 2 {
		t.Fatalf("output = %+v", out.Output)
	}
	var doc capture.Document
	if err := json.Unmarshal([]byte(w.read(".glossa/captures/captures.json")), &doc); err != nil || doc.Schema != "glossa.captures/v1" || len(doc.Captures) != 4 {
		t.Fatalf("manifest = %+v, %v", doc, err)
	}
	for _, c := range doc.Captures {
		data, err := os.ReadFile(filepath.Join(dir, c.Image.SHA256+".png"))
		sum := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(sum[:]) != c.Image.SHA256 {
			t.Errorf("image %s: %v", c.Image.SHA256, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, strings.Repeat("0", 64)+".png")); !os.IsNotExist(err) {
		t.Errorf("a stale image survived: %v", err)
	}
	if w.read(".glossa/captures/keep.txt") != "mine" {
		t.Error("removed a file capture didn't write")
	}

	// Coverage: web's known keys, in the branch's view, minus those shown.
	want := capture.Coverage{Messages: 4, Captured: 2, NotCaptured: []capture.NotCaptured{
		{Key: "checkout.pay", Usages: 1, File: "src/Pay.vue", Line: 4, Routes: []string{"/checkout/[step]"}},
		{Key: "hidden.note", Usages: 1, File: "src/Home.vue", Line: 9, Routes: []string{"/"}},
	}}
	if got, _ := json.Marshal(out.Coverage); string(got) != string(mustJSON(t, want)) {
		t.Errorf("coverage = %s", got)
	}
	if !slices.Equal(srv.ctx.usageBranches, []string{"feat/copy", "feat/copy", "feat/copy"}) {
		t.Errorf("usage pages read = %v", srv.ctx.usageBranches)
	}

	r := w.run("capture", "--commit", testCommit, "--branch", "feat/copy")
	r.want(t, ExitOK)
	for _, s := range []string{"✓ 4 captures of web at 0123456789ab (feat/copy)", "/      de      1280×800  3        2        1",
		"✓ Wrote captures.json and 2 images to " + dir, "! 2 of 4 used messages not captured", "  checkout.pay  src/Pay.vue:4 [/checkout/[step]]"} {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("output lacks %q:\n%s", s, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, "[1/4] http://localhost:4173/?lang=de at 1280×800 in de") {
		t.Errorf("progress = %q", r.stderr)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCaptureUploadsToTheCapturesAPI(t *testing.T) {
	srv := captureServer(t)
	w := withCapturePlan(newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN}), capturePlan)
	var plan *capture.Plan
	fakeRun(t, &plan, nil)

	var out captureDoc
	w.json(&out, "capture", "--upload", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if out.Upload == nil || out.Upload.Build != "bld_c1" || out.Upload.Captures != 4 || out.Upload.ImagesStored != 2 || out.Upload.Replayed || out.Output != nil {
		t.Fatalf("--json = %+v / %+v", out.Upload, out.Output)
	}
	ups := srv.ctx.captures
	if len(ups) != 1 {
		t.Fatalf("uploads = %d", len(ups))
	}
	up := ups[0]
	digests := slices.Sorted(func(yield func(string) bool) {
		for d := range up.images {
			if !yield(d) {
				return
			}
		}
	})
	if !slices.Equal(up.parts, append([]string{"manifest"}, digests...)) || len(digests) != 2 {
		t.Errorf("parts = %v", up.parts)
	}
	if up.doc.Commit != testCommit || up.doc.Branch != "main" || len(up.doc.Captures) != 4 {
		t.Errorf("manifest = %+v", up.doc)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".glossa", "captures")); !os.IsNotExist(err) {
		t.Errorf("--upload wrote files: %v", err)
	}

	// The same captures again: the server answers with the first build.
	w.json(&out, "capture", "--upload", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if !out.Upload.Replayed || out.Upload.Build != "bld_c1" {
		t.Errorf("replay = %+v", out.Upload)
	}
	r := w.run("capture", "--upload", "--no-coverage", "--commit", testCommit, "--branch", "main")
	if !strings.Contains(r.stdout, "Already uploaded 4 captures to build bld_c1") || strings.Contains(r.stdout, "not captured") {
		t.Errorf("output:\n%s", r.stdout)
	}
}

func TestCaptureWorksOfflineWithoutCoverage(t *testing.T) {
	w := withCapturePlan(newWorkspace(t).withProject(nil, map[string]string{"en": sourceEN}), capturePlan)
	delete(w.env, "GLOSSA_TOKEN")
	var plan *capture.Plan
	fakeRun(t, &plan, nil)
	var out captureDoc
	w.json(&out, "capture", "--no-coverage", "--out", "shots", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if out.Coverage != nil || out.Output == nil || out.Output.Dir != filepath.Join(w.dir, "shots") {
		t.Errorf("--json = %+v", out)
	}
	var e errorDoc
	w.json(&e, "capture", "--commit", testCommit, "--branch", "main").want(t, ExitNetwork)
	if e.Error.Code != "no_token" {
		t.Errorf("coverage without a token = %+v", e.Error)
	}
}

func TestCaptureRefusals(t *testing.T) {
	srv := captureServer(t)
	var e errorDoc
	for _, tc := range []struct {
		name, plan string
		args       []string
		fail       error
		exit       ExitCode
		code       string
	}{
		{"no routes", "capture:\n  base_url: http://localhost:4173\n", nil, nil, ExitUsage, "invalid_capture_plan"},
		{"bad --base-url", capturePlan, []string{"--base-url", "localhost"}, nil, ExitUsage, "invalid_capture_plan"},
		{"missing secret", capturePlan + "  cookies: [{ name: s, value: '${FIXTURE_SESSION}' }]\n", nil, nil, ExitUsage, "invalid_capture_plan"},
		{"no application", strings.Replace(capturePlan, "  application: web\n", "", 1), nil, nil, ExitUsage, "application_required"},
		{"unknown application", capturePlan, []string{"--application", "ios"}, nil, ExitUsage, "unknown_application"},
		{"arguments", capturePlan, []string{"/"}, nil, ExitUsage, "invalid_usage"},
		{"production", capturePlan, nil, &capture.Refusal{URL: "http://localhost:4173/", Code: "production_page", Reason: "production"}, ExitUsage, "production_page"},
		{"page", capturePlan, nil, &capture.PageError{URL: "http://localhost:4173/", Step: "load the page", Err: os.ErrDeadlineExceeded}, ExitUsage, "page_failed"},
		{"no chrome", capturePlan, nil, &capture.BrowserError{Err: os.ErrNotExist}, ExitUsage, "no_browser"},
		{"no browser there", capturePlan, nil,
			&capture.CDPError{Endpoint: "ws://127.0.0.1:9222/devtools/browser/8f3c", Code: "cdp_unreachable", Reason: "connection refused"},
			ExitUsage, "cdp_unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := withCapturePlan(newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN}), tc.plan)
			var plan *capture.Plan
			fakeRun(t, &plan, tc.fail)
			args := append([]string{"capture", "--commit", testCommit, "--branch", "main"}, tc.args...)
			w.json(&e, args...).want(t, tc.exit)
			if e.Error.Code != tc.code || e.Error.Fix == "" {
				t.Errorf("error = %+v, want %s", e.Error, tc.code)
			}
			if strings.Contains(e.Error.Message+e.Error.Why+e.Error.Where, "FIXTURE_SESSION=") {
				t.Errorf("error leaks: %+v", e.Error)
			}
		})
	}
	if len(srv.ctx.captures) != 0 {
		t.Errorf("uploads = %d", len(srv.ctx.captures))
	}
}

// --cdp and GLOSSA_CAPTURE_CDP say which browser to attach to; the flag
// wins. --cdp-allow-remote (GLOSSA_CAPTURE_CDP_ALLOW_REMOTE) is what it
// takes to name a host that isn't loopback.
func TestCaptureAttachesToABrowser(t *testing.T) {
	srv := captureServer(t)
	for _, tc := range []struct {
		name        string
		args        []string
		env         map[string]string
		endpoint    string
		allowRemote bool
	}{
		{name: "the flag", args: []string{"--cdp", "ws://127.0.0.1:9222/devtools/browser/8f3c"},
			endpoint: "ws://127.0.0.1:9222/devtools/browser/8f3c"},
		{name: "the environment", env: map[string]string{"GLOSSA_CAPTURE_CDP": "http://127.0.0.1:9333"},
			endpoint: "http://127.0.0.1:9333"},
		{name: "the flag wins", args: []string{"--cdp", "http://127.0.0.1:1"},
			env: map[string]string{"GLOSSA_CAPTURE_CDP": "http://127.0.0.1:2"}, endpoint: "http://127.0.0.1:1"},
		{name: "a remote browser, by flag", args: []string{"--cdp", "http://chrome.example:9222", "--cdp-allow-remote"},
			endpoint: "http://chrome.example:9222", allowRemote: true},
		{name: "a remote browser, by environment", args: []string{"--cdp", "http://chrome.example:9222"},
			env:      map[string]string{"GLOSSA_CAPTURE_CDP_ALLOW_REMOTE": "true"},
			endpoint: "http://chrome.example:9222", allowRemote: true},
		{name: "none", endpoint: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := withCapturePlan(newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN}), capturePlan)
			for k, v := range tc.env {
				w.env[k] = v
			}
			var plan *capture.Plan
			var got capture.Options
			fakeRun(t, &plan, nil)
			inner := runCapture
			runCapture = func(ctx context.Context, p *capture.Plan, o capture.Options) ([]capture.Shot, error) {
				got = o
				return inner(ctx, p, o)
			}
			args := append([]string{"capture", "--no-coverage", "--commit", testCommit, "--branch", "main"}, tc.args...)
			w.run(args...).want(t, ExitOK)
			if got.CDP != tc.endpoint || got.AllowRemoteCDP != tc.allowRemote {
				t.Errorf("options = %q, remote %v; want %q, %v", got.CDP, got.AllowRemoteCDP, tc.endpoint, tc.allowRemote)
			}
		})
	}
}

// A --cdp endpoint that isn't loopback is refused before any browser is
// started or any request is made to it.
func TestCaptureRefusesARemoteCDPEndpoint(t *testing.T) {
	srv := captureServer(t)
	w := withCapturePlan(newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN}), capturePlan)
	var e errorDoc
	w.json(&e, "capture", "--no-coverage", "--commit", testCommit, "--branch", "main", "--cdp", "http://chrome.example:9222").want(t, ExitUsage)
	if e.Error.Code != "invalid_cdp_endpoint" || !strings.Contains(e.Error.Why, "loopback") || e.Error.Fix == "" {
		t.Fatalf("error = %+v", e.Error)
	}
}
