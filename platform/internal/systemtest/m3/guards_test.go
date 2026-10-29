//go:build system

package m3_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	browse "go.klarlabs.de/scout"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

// loaderMarkers are what only the in-product editor's loader puts into a
// bundle (RFC 0004 §5.1): the attribute it sets on the script it adds,
// the overlay's path on the Studio origin, the Studio origin itself and
// the SRI hash pinned in the build.
var loaderMarkers = []string{
	"data-glossa-overlay-loader",
	"/overlay/v1/overlay.js",
	"https://studio.glossa.test",
	"sha384-",
}

// bundleScan is what the scan found in one build.
type bundleScan struct {
	// Build is `preview` or `production`.
	Build string
	// Files is how many files were read, and Bytes how many bytes.
	Files int
	Bytes int64
	// Found names the markers the build holds.
	Found []string
}

// scanBundles reads every byte of both committed builds of the fixture
// application and reports which loader markers each holds. The preview
// build must hold all of them — otherwise the scan proves nothing — and
// the production build none (RFC 0004 §12.3).
func (s *scenario) scanBundles() {
	t := s.t
	for _, build := range []struct{ name, dir string }{
		{"preview", fixture.DirPreview},
		{"production", fixture.DirProduction},
	} {
		scan := bundleScan{Build: build.name}
		found := map[string]bool{}
		root := filepath.Join(appDir(), build.dir)
		err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scan.Files++
			scan.Bytes += int64(len(raw))
			for _, m := range loaderMarkers {
				if strings.Contains(string(raw), m) {
					found[m] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
		if scan.Files == 0 {
			t.Fatalf("%s holds no files: run `pnpm --filter @klarlabs-studio/glossa-unplugin build:m3`", root)
		}
		for m := range found {
			scan.Found = append(scan.Found, m)
		}
		sort.Strings(scan.Found)
		switch build.name {
		case "preview":
			if len(scan.Found) != len(loaderMarkers) {
				t.Errorf("the preview build holds only %v of the loader's markers, so the production scan proves nothing", scan.Found)
			}
		case "production":
			if len(scan.Found) > 0 {
				t.Errorf("the production build of the fixture holds the in-product editor's loader: %v", scan.Found)
			}
		}
		s.bundles = append(s.bundles, scan)
	}
}

// overlayProbe is what the browser reported about the loader.
type overlayProbe struct {
	// PreviewEnvironment and ProductionEnvironment are what the page's
	// runtime said its release was for.
	PreviewEnvironment, ProductionEnvironment string
	// PreviewScripts and ProductionScripts count the loader's script
	// element on the page after `?glossa=edit`.
	PreviewScripts, ProductionScripts int
	// Warning is what the loader said when it refused.
	Warning string
}

// probe is what the page reports back.
type probe struct {
	Environment string   `json:"environment"`
	Runtimes    int      `json:"runtimes"`
	Scripts     int      `json:"scripts"`
	Overlay     bool     `json:"overlay"`
	Warnings    []string `json:"warnings"`
}

// probeScript records console.warn before the page's scripts run, then
// reports the page's runtimes, the loader's script element and whether
// the overlay module ever registered itself.
const probeScript = `(() => {
  const warnings = [];
  const warn = console.warn.bind(console);
  console.warn = (...a) => { warnings.push(a.map(String).join(" ")); warn(...a); };
  globalThis.__m3probe = () => {
    const runtimes = globalThis[Symbol.for("glossa.runtimes")] ?? [];
    return JSON.stringify({
      environment: runtimes[0]?.environment ?? "",
      runtimes: runtimes.length,
      scripts: document.querySelectorAll("script[data-glossa-overlay-loader]").length,
      overlay: globalThis[Symbol.for("glossa.overlay")] !== undefined,
      warnings,
    });
  };
})()`

// refuseOverlay drives the preview build — the one that does contain the
// loader — in a real browser, once with the preview release and once
// with a production release, both with the `?glossa=edit` gesture. The
// loader must add its script for the first and refuse the second
// (RFC 0004 §5.1, §12.3).
func (s *scenario) refuseOverlay() {
	t := s.t
	app := serveApp(t, filepath.Join(appDir(), fixture.DirPreview))

	engine := browse.New(browse.WithHeadless(true), browse.WithTimeout(30*time.Second),
		browse.WithAllowPrivateIPs(true), browse.WithRemoteCDP(webSocketURL(t, s.chrome)))
	if err := engine.Launch(); err != nil {
		t.Fatalf("attach to Chrome at %s: %v", s.chrome, err)
	}
	defer func() { _ = engine.Close() }()

	preview := s.probePage(engine, app.URL+"/kasse?glossa=edit")
	production := s.probePage(engine, app.URL+"/kasse?env=production&glossa=edit")

	s.overlay = overlayProbe{
		PreviewEnvironment:    preview.Environment,
		ProductionEnvironment: production.Environment,
		PreviewScripts:        preview.Scripts,
		ProductionScripts:     production.Scripts,
	}
	for _, w := range production.Warnings {
		if strings.Contains(w, "the in-product editor stays off") {
			s.overlay.Warning = strings.TrimSpace(strings.TrimPrefix(w, "[glossa]"))
		}
	}
	if preview.Runtimes == 0 || preview.Environment != "preview" {
		t.Fatalf("the preview page reported %d runtimes for %q", preview.Runtimes, preview.Environment)
	}
	if preview.Scripts != 1 {
		t.Errorf("the preview page has %d loader scripts after ?glossa=edit, want 1 — the guard below would prove nothing",
			preview.Scripts)
	}
	if production.Environment != "production" {
		t.Fatalf("the production page reported %q, want a production release", production.Environment)
	}
	if production.Scripts != 0 || production.Overlay {
		t.Errorf("the in-product editor started against a production release: %d loader scripts, overlay %v",
			production.Scripts, production.Overlay)
	}
	if s.overlay.Warning == "" {
		t.Errorf("the loader refused silently; it said %v", production.Warnings)
	}
}

// probePage opens one page with the probe installed and reads it back
// once the loader has had its chance to run.
func (s *scenario) probePage(engine *browse.Engine, url string) probe {
	t := s.t
	page, err := engine.NewPage()
	if err != nil {
		t.Fatalf("open a tab: %v", err)
	}
	defer func() { _ = page.Close() }()
	if _, err := page.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": probeScript}); err != nil {
		t.Fatalf("install the probe: %v", err)
	}
	if err := page.Navigate(url); err != nil {
		t.Fatalf("load %s: %v", url, err)
	}
	// The loader starts on window load and decides in one microtask
	// queue; the overlay script it adds never arrives, because the Studio
	// origin is a stand-in. Wait for the runtime's release, then give the
	// loader the same fixed moment on both pages.
	read := func() probe {
		v, err := page.Evaluate(`globalThis.__m3probe ? globalThis.__m3probe() : ""`)
		if err != nil {
			t.Fatalf("read the probe on %s: %v", url, err)
		}
		raw, _ := v.(string)
		var out probe
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				t.Fatalf("probe on %s: %v: %s", url, err, raw)
			}
		}
		return out
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if out := read(); out.Runtimes > 0 && out.Environment != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never activated a release", url)
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	return read()
}

// webSocketURL turns a DevTools HTTP endpoint into the browser's
// WebSocket URL, the way `glossa capture --cdp` does.
func webSocketURL(t interface {
	Fatalf(string, ...any)
	Helper()
}, endpoint string) string {
	t.Helper()
	resp, err := http.Get(endpoint + "/json/version")
	if err != nil {
		t.Fatalf("read %s/json/version: %v", endpoint, err)
	}
	defer resp.Body.Close()
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode %s/json/version: %v", endpoint, err)
	}
	if v.WebSocketDebuggerURL == "" {
		t.Fatalf("%s names no browser WebSocket", endpoint)
	}
	return v.WebSocketDebuggerURL
}
