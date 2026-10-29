// Package capturetest serves the fixture app of the `glossa capture`
// integration tests (capture/testdata/app, built from
// runtimes/js/capture/src/testing/cli-fixture.ts) and starts the headless
// Chrome they attach to. Test-only.
package capturetest

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Login is what a page request carried of the fixture login.
type Login struct {
	Path, Header, Cookie string
}

// App is the fixture app on a local server.
type App struct {
	*httptest.Server
	mu     sync.Mutex
	logins []Login
}

// Logins are the fixture header (X-Fixture-User) and cookie (session)
// each page request carried.
func (a *App) Logins() []Login {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Login(nil), a.logins...)
}

// Dir is capture/testdata/app.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", "app")
}

// NewApp serves the fixture app: app.js, and index.html for every other
// path (the query picks the locale and the manifest environment).
func NewApp(t testing.TB) *App {
	t.Helper()
	dir := Dir()
	a := &App{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			http.ServeFile(w, r, filepath.Join(dir, "app.js"))
			return
		}
		if r.URL.Path == "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		login := Login{Path: r.URL.Path, Header: r.Header.Get("X-Fixture-User")}
		if c, err := r.Cookie("session"); err == nil {
			login.Cookie = c.Value
		}
		a.mu.Lock()
		a.logins = append(a.logins, login)
		a.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}))
	t.Cleanup(a.Close)
	return a
}

// StartChrome starts a headless Chrome for the test and returns its
// DevTools endpoint, for `glossa capture --cdp` (GLOSSA_CAPTURE_CDP).
// It is killed and its profile removed in cleanup.
//
// The browser tests start Chrome themselves because scout's launcher
// passes a fixed flag list (platform/README.md, "scout follow-ups"):
// there is no way to add --no-sandbox and --disable-dev-shm-usage, which
// a CI runner needs — its kernel forbids the user namespaces Chrome's
// own sandbox wants, and Chrome then never opens a DevTools port. Local
// and CI runs take the same path, the attach path, so what CI exercises
// is what a developer exercises.
//
// Without a Chrome it skips the test, except in CI (CI=true), where a
// browser test must run.
func StartChrome(t testing.TB) string {
	t.Helper()
	bin, err := findChrome()
	if err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI=true and no Chrome: %v", err)
		}
		t.Skipf("no Chrome here: %v", err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatalf("no free port for Chrome: %v", err)
	}
	// Not t.TempDir(): Chrome's helper processes outlive the kill below by a
	// moment and keep writing into the profile, and Go's own cleanup fails the
	// test when the directory isn't empty yet. This one is removed with
	// patience, and a leftover in the OS temp dir is harmless.
	dir, err := os.MkdirTemp("", "glossa-capture-chrome-*")
	if err != nil {
		t.Fatalf("no profile directory for Chrome: %v", err)
	}
	cmd := exec.Command(bin, //nolint:gosec // G204: the resolved Chrome binary, in a test
		"--headless=new",
		// The two flags scout's launcher can't pass: a CI runner's kernel
		// denies Chrome's sandbox, and its /dev/shm is too small.
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-gpu",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--disable-translate",
		"--disable-popup-blocking",
		"--metrics-recording-only",
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("can't start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		// Chrome's helpers close their files shortly after the parent dies.
		for i := range 20 {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
		}
	})
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForDevTools(endpoint, 30*time.Second); err != nil {
		t.Fatalf("%s started but never opened DevTools: %v", bin, err)
	}
	return endpoint
}

// findChrome looks where scout's launcher looks, plus GLOSSA_TEST_CHROME
// and CHROME_PATH for a runner that keeps its browser elsewhere.
func findChrome() (string, error) {
	var candidates []string
	for _, k := range []string{"GLOSSA_TEST_CHROME", "CHROME_PATH"} {
		if v := os.Getenv(k); v != "" {
			candidates = append(candidates, v)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser")
	default:
		candidates = append(candidates,
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "microsoft-edge-stable", "brave-browser")
	}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome or Chromium found (set GLOSSA_TEST_CHROME)")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected listener address")
	}
	return addr.Port, nil
}

// waitForDevTools polls the endpoint's /json/version until it answers.
func waitForDevTools(endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(endpoint + "/json/version")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = errors.New(resp.Status)
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s/json/version: %w", endpoint, last)
}
