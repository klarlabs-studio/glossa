//go:build integration

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	glossa "go.klarlabs.de/glossa/runtimes/go"

	"go.klarlabs.de/glossa/platform/internal/cli"
	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
)

// testAuthSecret is base64 of 42 bytes.
const testAuthSecret = "YS10ZXN0LWF1dGgtc2VjcmV0LXRoYXQtaXMtbG9uZy1lbm91Z2gtMTIz"

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// server is a real glossa-server binary on a testcontainers Postgres
// (and, for releases, a MinIO bucket).
type server struct {
	t    *testing.T
	base string
	logs *syncBuffer
}

func startServer(t *testing.T, vars map[string]string) *server {
	t.Helper()
	env, err := dbtest.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)

	bin := filepath.Join(t.TempDir(), "glossa-server")
	build := exec.Command("go", "build", "-o", bin, "./cmd/glossa-server")
	build.Dir = filepath.Join("..", "..") // the platform module
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build glossa-server: %v\n%s", err, out)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	logs := &syncBuffer{}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+env.AppDSN,
		"MIGRATION_DATABASE_URL="+env.OwnerDSN,
		"GLOSSA_MIGRATE=up",
		"GLOSSA_HTTP_ADDR="+addr,
		"GLOSSA_AUTH_SECRET="+testAuthSecret,
		"GLOSSA_MAIL_DRIVER=log",
		"GLOSSA_STUDIO_URL=https://studio.test",
		"GLOSSA_OUTBOX_POLL_INTERVAL=50ms",
	)
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	s := &server{t: t, base: "http://" + addr, logs: logs}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(s.base + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("glossa-server never became ready:\n%s", logs)
	return nil
}

type call struct {
	method, path string
	body         any
	cookie, csrf string
	headers      map[string]string
}

func (s *server) do(c call, wantStatus int, out any) http.Header {
	s.t.Helper()
	var body io.Reader
	if c.body != nil {
		b, _ := json.Marshal(c.body)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(c.method, s.base+c.path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", "__Host-glossa_session="+c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		s.t.Fatalf("%s %s = %d, want %d: %s", c.method, c.path, resp.StatusCode, wantStatus, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			s.t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return resp.Header
}

var mailedToken = regexp.MustCompile(`/auth/sign-in#token=([A-Za-z0-9_-]{43})`)

// signIn runs the magic-link flow and returns the session cookie and
// CSRF token.
func (s *server) signIn(email string) (string, string) {
	s.t.Helper()
	s.do(call{method: "POST", path: "/v1/auth/magic-links", body: map[string]string{"email": email}}, http.StatusAccepted, nil)
	var link []string
	for range 50 {
		if m := mailedToken.FindAllStringSubmatch(s.logs.String(), -1); len(m) > 0 {
			link = m[len(m)-1]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if link == nil {
		s.t.Fatalf("no sign-in link in the log:\n%s", s.logs)
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	h := s.do(call{method: "POST", path: "/v1/auth/magic-link-redemptions", body: map[string]string{"token": link[1]}}, http.StatusOK, &session)
	cookie, _, _ := strings.Cut(strings.TrimPrefix(h.Get("Set-Cookie"), "__Host-glossa_session="), ";")
	return cookie, session.CSRFToken
}

// fakeV03 serves a Glossa v0.3 project (de source, en translations).
func fakeV03(t *testing.T) *httptest.Server {
	bundles := map[string]string{
		"de": `{"project":"brotwerk","locale":"de","messages":{"cart.items":"{count, plural, one {# Brot} other {# Brote}}","checkout.pay":"Bezahle {amount, number}","home.title":"Willkommen"},"statuses":{"cart.items":"approved","checkout.pay":"approved","home.title":"approved"}}`,
		"en": `{"project":"brotwerk","locale":"en","messages":{"cart.items":"{count, plural, one {# loaf} other {# loaves}}","checkout.pay":"Pay {amount, number}","home.title":"Welcome"},"statuses":{"cart.items":"approved","checkout.pay":"ai_translated","home.title":"pending"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer glossa_v03" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; {
		case p == "/api/v1/projects/brotwerk/locales":
			_, _ = io.WriteString(w, `[{"code":"de"},{"code":"en"}]`)
		case strings.HasPrefix(p, "/api/v1/projects/brotwerk/locales/"):
			_, _ = io.WriteString(w, bundles[strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/projects/brotwerk/locales/"), "/messages")])
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type runner struct {
	t     *testing.T
	dir   string
	env   map[string]string
	store credentials.Store
}

func (r runner) run(want cli.ExitCode, out any, args ...string) string {
	r.t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Main(context.Background(), append(args, "--json"), cli.Env{
		Stdout: &stdout, Stderr: &stderr, Dir: r.dir, Credentials: r.store, Version: "integration",
		Getenv: func(k string) string { return r.env[k] },
	})
	if code != int(want) {
		r.t.Fatalf("glossa %s: exit %d, want %d\n%s\n%s", strings.Join(args, " "), code, want, stdout.String(), stderr.String())
	}
	if out != nil {
		if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
			r.t.Fatalf("glossa %s --json: %v\n%s", strings.Join(args, " "), err, stdout.String())
		}
	}
	return stdout.String()
}

// TestCLIAgainstGlossaServer is M1's CLI loop against the real server:
// init → push (ICU MF1) → check fails (en missing) → import the
// translations from Glossa v0.3 → check passes → generate. Re-running
// push and import changes nothing. Then the release half, on MinIO:
// publish → pull --release → the Go runtime loads the bundle offline →
// promote → rollback → delivery keys. Then M2's knowledge and AI loop
// (knowledgeLoop), and import/export jobs (interchangeLoop).
func TestCLIAgainstGlossaServer(t *testing.T) {
	vars := objectStorage(t)
	vars["GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS"] = "true" // the fake provider listens on loopback
	vars["GLOSSA_AI_POLL_INTERVAL"] = "100ms"
	vars["GLOSSA_INTEGRATION_POLL_INTERVAL"] = "100ms"
	s := startServer(t, vars)
	cookie, csrf := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: cookie, csrf: csrf,
		body: map[string]string{"slug": "klarlabs", "name": "Klarlabs"}}, http.StatusCreated, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: cookie, csrf: csrf,
		body: map[string]any{"slug": "brotwerk", "name": "Brotwerk", "source_locale": "de"}}, http.StatusCreated, &project)
	s.do(call{method: "POST", path: base + "/projects/" + project.ID + "/locales", cookie: cookie, csrf: csrf,
		body: map[string]string{"code": "en"}}, http.StatusCreated, nil)
	var tok struct{ Secret string }
	s.do(call{method: "POST", path: base + "/tokens", cookie: cookie, csrf: csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"write", "publish"}}}, http.StatusCreated, &tok)

	dir := t.TempDir()
	r := runner{t: t, dir: dir, env: map[string]string{"GLOSSA_TOKEN": tok.Secret, "GLOSSA_V0_KEY": "glossa_v03"},
		store: &credentials.File{Path: filepath.Join(t.TempDir(), "credentials.json")}}

	// init reads the tenant and the source locale from the server.
	var initOut struct {
		Checked bool `json:"checked_with_server"`
		Config  struct {
			Tenant       string `json:"tenant"`
			SourceLocale string `json:"source_locale"`
		} `json:"config"`
	}
	r.run(cli.ExitOK, &initOut, "init", "--server", s.base, "--project", "brotwerk",
		"--typescript", "src/glossa/messages.ts", "--vue", "src/glossa/glossa-vue.ts", "--go", "internal/msg/messages.go")
	if !initOut.Checked || initOut.Config.Tenant != org.ID || initOut.Config.SourceLocale != "de" {
		t.Fatalf("init = %+v", initOut)
	}
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locales", "de.json"), []byte(`{
  "cart": {"items": "{count, plural, one {# Brot} other {# Brote}}"},
  "checkout.pay": "Bezahle {amount, number}",
  "home.title": "Willkommen"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var push struct {
		Summary map[string]int `json:"summary"`
	}
	r.run(cli.ExitOK, &push, "push")
	if push.Summary["created"] != 3 {
		t.Fatalf("push = %+v", push)
	}
	r.run(cli.ExitOK, &push, "push")
	if push.Summary["unchanged"] != 3 {
		t.Errorf("second push = %+v", push)
	}

	// `check --json` is glossa.cli.check/v2: the findings are
	// glossa.finding/v1, so the locale and the key live on the locus
	// (RFC 0005 §2.1). Reading them flat silently counts nothing.
	var check struct {
		Passed   bool `json:"passed"`
		Findings []struct {
			Code  string `json:"code"`
			Layer string `json:"layer"`
			Locus struct {
				Key    string `json:"key"`
				Locale string `json:"locale"`
			} `json:"locus"`
		} `json:"findings"`
	}
	r.run(cli.ExitCheckFailed, &check, "check")
	missing := 0
	for _, f := range check.Findings {
		if f.Code == "missing-translation" && f.Locus.Locale == "en" {
			if f.Locus.Key == "" {
				t.Errorf("finding %+v carries no key", f)
			}
			missing++
		}
	}
	if check.Passed || missing != 3 {
		t.Fatalf("check before import = %+v", check)
	}

	old := fakeV03(t)
	var imp struct {
		Summary map[string]map[string]int `json:"summary"`
		Items   []struct {
			Kind, Key, Status, State, V0Status string
			Downgraded                         bool
		} `json:"items"`
	}
	r.run(cli.ExitOK, &imp, "import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "brotwerk")
	if imp.Summary["message"]["unchanged"] != 3 || imp.Summary["translation"]["created"] != 3 {
		t.Fatalf("import = %+v", imp.Summary)
	}
	states := map[string]string{}
	for _, it := range imp.Items {
		if it.Kind == "translation" {
			states[it.Key] = it.State
		}
	}
	// review_required: approved waits for a human; ai_translated needs review; pending is a draft.
	if states["cart.items"] != "needs_review" || states["checkout.pay"] != "needs_review" || states["home.title"] != "draft" {
		t.Errorf("imported states = %v", states)
	}
	r.run(cli.ExitOK, &imp, "import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "brotwerk")
	if imp.Summary["translation"]["unchanged"] != 3 {
		t.Errorf("re-import = %+v", imp.Summary)
	}

	r.run(cli.ExitOK, &check, "check")
	if !check.Passed {
		t.Fatalf("check after import = %+v", check)
	}

	var status struct {
		Locales []struct {
			Code        string
			Translated  int
			NeedsReview int `json:"needs_review"`
			Draft       int
		} `json:"locales"`
	}
	r.run(cli.ExitOK, &status, "status")
	if len(status.Locales) != 2 || status.Locales[1].Code != "en" || status.Locales[1].Translated != 3 || status.Locales[1].Draft != 1 {
		t.Errorf("status = %+v", status)
	}

	var gen struct {
		Files []struct{ Path, Kind string } `json:"files"`
	}
	r.run(cli.ExitOK, &gen, "generate")
	if len(gen.Files) != 3 {
		t.Fatalf("generate = %+v", gen)
	}
	ts, err := os.ReadFile(filepath.Join(dir, "src", "glossa", "messages.ts"))
	if err != nil || !strings.Contains(string(ts), `"checkout.pay": { amount: number };`) {
		t.Errorf("messages.ts = %s (%v)", ts, err)
	}
	goSrc, err := os.ReadFile(filepath.Join(dir, "internal", "msg", "messages.go"))
	if err != nil || !strings.Contains(string(goSrc), "func (m Messages) CartItems(count float64") {
		t.Errorf("messages.go = %s (%v)", goSrc, err)
	}
	r.run(cli.ExitOK, nil, "generate", "--check")
	r.run(cli.ExitOK, nil, "diff", "--exit-code")

	contextLoop(t, r, s, session{cookie: cookie, csrf: csrf}, base+"/projects/"+project.ID)
	releaseLoop(t, r)
	knowledgeLoop(t, r, s, session{cookie: cookie, csrf: csrf}, base, project.ID)
	interchangeLoop(t, r)
}

// objectStorage starts MinIO with a bucket and returns glossa-server's
// storage configuration for it.
func objectStorage(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	minio, err := s3test.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(minio.Close)
	const bucket = "glossa-cli"
	if _, err := minio.Store(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"GLOSSA_STORAGE_DRIVER":       "s3",
		"GLOSSA_S3_ENDPOINT":          minio.Endpoint,
		"GLOSSA_S3_BUCKET":            bucket,
		"GLOSSA_S3_ACCESS_KEY_ID":     minio.AccessKeyID,
		"GLOSSA_S3_SECRET_ACCESS_KEY": minio.SecretAccessKey,
		"GLOSSA_S3_INSECURE":          "true",
		"GLOSSA_S3_PATH_STYLE":        "true",
	}
}

type releaseDoc struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Counts  struct {
		Messages int `json:"messages"`
		Locales  map[string]struct {
			Messages int `json:"messages"`
		} `json:"locales"`
	} `json:"counts"`
}

type movedDoc struct {
	Environment struct {
		Name    string `json:"name"`
		Release *struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"release"`
	} `json:"environment"`
	Previous *struct {
		Version int `json:"version"`
	} `json:"previous"`
}

type errorDoc struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// offline loads a bundle with the Go runtime and nothing else.
func offline(t *testing.T, dir, environment string) *glossa.Client {
	t.Helper()
	c, err := glossa.New(glossa.Config{
		Environment: environment, Bundled: os.DirFS(dir),
		DisableCache: true, RefreshInterval: -1, DisableBidiIsolation: true,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// releaseLoop publishes what the import brought (en: two translations
// needing review, one draft), bundles it, and moves environments.
func releaseLoop(t *testing.T, r runner) {
	// A dry run shows what preview would ship and publishes nothing.
	var dry struct {
		Schema     string `json:"schema"`
		Releasable bool   `json:"releasable"`
		Base       *struct {
			ID string `json:"id"`
		} `json:"base"`
		Release *struct {
			Counts struct {
				Messages int `json:"messages"`
				Locales  map[string]struct {
					Messages int `json:"messages"`
				} `json:"locales"`
			} `json:"counts"`
		} `json:"release"`
		Changes []struct {
			Locale string   `json:"locale"`
			Added  []string `json:"added"`
		} `json:"changes"`
	}
	r.run(cli.ExitOK, &dry, "release", "publish", "--dry-run", "--environment", "preview")
	if dry.Schema != "glossa.cli.release.preview/v1" || !dry.Releasable || dry.Base != nil || dry.Release == nil ||
		dry.Release.Counts.Messages != 3 || dry.Release.Counts.Locales["en"].Messages != 3 || len(dry.Changes) != 2 || len(dry.Changes[1].Added) != 3 {
		t.Fatalf("dry run = %+v", dry)
	}
	var none struct {
		Releases []any `json:"releases"`
	}
	r.run(cli.ExitOK, &none, "release", "list")
	if len(none.Releases) != 0 {
		t.Fatalf("the dry run published: %+v", none)
	}

	var pub struct {
		Replayed bool       `json:"replayed"`
		Release  releaseDoc `json:"release"`
	}
	// Preview ships unreviewed text; the key makes a retried CI step safe.
	r.run(cli.ExitOK, &pub, "release", "publish", "--environment", "preview", "--note", "from v0.3", "--idempotency-key", "ci-42")
	v1 := pub.Release
	if pub.Replayed || v1.Version != 1 || v1.Counts.Messages != 3 || v1.Counts.Locales["en"].Messages != 3 {
		t.Fatalf("publish = %+v", pub)
	}
	r.run(cli.ExitOK, &pub, "release", "publish", "--environment", "preview", "--note", "from v0.3", "--idempotency-key", "ci-42")
	if !pub.Replayed || pub.Release.ID != v1.ID {
		t.Errorf("replayed publish = %+v", pub)
	}
	// The same key for a different request is refused, not replayed.
	var e errorDoc
	r.run(cli.ExitUsage, &e, "release", "publish", "--environment", "preview", "--idempotency-key", "ci-42")
	if e.Error.Code != "idempotency_key_reused" {
		t.Errorf("reused key = %+v", e)
	}

	// pull --release: the bundle loads offline with the Go runtime.
	var pull struct {
		Release struct {
			ReleaseID   string `json:"release_id"`
			Environment string `json:"environment"`
			Artifacts   int    `json:"artifacts"`
		} `json:"release"`
	}
	r.run(cli.ExitOK, &pull, "pull", "--release", "latest", "--environment", "preview", "--out", "public/glossa")
	if pull.Release.ReleaseID != v1.ID || pull.Release.Environment != "preview" || pull.Release.Artifacts != 2 {
		t.Fatalf("pull --release = %+v", pull)
	}
	bundle := filepath.Join(r.dir, "public", "glossa")
	c := offline(t, bundle, "preview")
	if got := c.For("en").T("checkout.pay", glossa.Args{"amount": 3}); got != "Pay 3" {
		t.Errorf("en checkout.pay = %q", got)
	}
	if got := c.For("de").T("cart.items", glossa.Args{"count": 2}); got != "2 Brote" {
		t.Errorf("de cart.items = %q", got)
	}
	if ex := c.For("en").Explain("home.title"); ex.Source != glossa.SourceBundled || ex.Release == nil || ex.Release.ID != v1.ID {
		t.Errorf("explain = %+v", ex)
	}
	// Runtimes refuse a manifest for another environment: that's why the
	// bundle is written for one.
	if ex := offline(t, bundle, "production").For("en").Explain("home.title"); ex.Release != nil {
		t.Errorf("a preview bundle loaded as production: %+v", ex)
	}

	// Production ships approved text only, so preview's release can't go
	// there; development takes everything.
	r.run(cli.ExitNetwork, &e, "release", "promote", "v1", "--to", "production")
	if e.Error.Code != "release_ineligible" {
		t.Errorf("promote to production = %+v", e)
	}
	var moved movedDoc
	r.run(cli.ExitOK, &moved, "release", "promote", "v1", "--to", "development")
	if moved.Environment.Release == nil || moved.Environment.Release.ID != v1.ID || moved.Previous != nil {
		t.Fatalf("promote = %+v", moved)
	}
	r.run(cli.ExitOK, &pub, "release", "publish", "--environment", "development")
	if pub.Release.Version != 2 {
		t.Fatalf("second publish = %+v", pub)
	}
	var diff struct {
		Base      *struct{ ID string } `json:"base"`
		Identical bool                 `json:"identical"`
	}
	r.run(cli.ExitOK, &diff, "release", "diff", "v2")
	if diff.Base == nil || diff.Base.ID != v1.ID || !diff.Identical {
		t.Errorf("diff = %+v", diff)
	}
	r.run(cli.ExitOK, &moved, "release", "rollback", "--environment", "development")
	if moved.Environment.Release == nil || moved.Environment.Release.ID != v1.ID || moved.Previous == nil || moved.Previous.Version != 2 {
		t.Fatalf("rollback = %+v", moved)
	}

	var envs struct {
		Environments []struct {
			Name    string `json:"name"`
			Release *struct {
				Version int `json:"version"`
			} `json:"release"`
		} `json:"environments"`
	}
	r.run(cli.ExitOK, &envs, "release", "environments")
	serving := map[string]int{}
	for _, e := range envs.Environments {
		if e.Release != nil {
			serving[e.Name] = e.Release.Version
		}
	}
	if serving["development"] != 1 || serving["preview"] != 1 || len(serving) != 2 {
		t.Errorf("environments = %+v", envs)
	}
	var list struct {
		Releases []struct {
			Version int      `json:"version"`
			Serving []string `json:"serving"`
		} `json:"releases"`
	}
	r.run(cli.ExitOK, &list, "release", "list")
	if len(list.Releases) != 2 || list.Releases[0].Version != 2 || len(list.Releases[0].Serving) != 0 ||
		strings.Join(list.Releases[1].Serving, ",") != "development,preview" {
		t.Errorf("list = %+v", list)
	}

	// Delivery keys: shown in full, revoked by name.
	var key struct {
		Key struct {
			ID        string     `json:"id"`
			Key       string     `json:"key"`
			RevokedAt *time.Time `json:"revoked_at"`
		} `json:"key"`
	}
	r.run(cli.ExitOK, &key, "release", "keys", "create", "web")
	if key.Key.Key == "" || key.Key.RevokedAt != nil {
		t.Fatalf("keys create = %+v", key)
	}
	id := key.Key.ID
	r.run(cli.ExitOK, &key, "release", "keys", "revoke", "web")
	if key.Key.ID != id || key.Key.RevokedAt == nil {
		t.Errorf("keys revoke = %+v", key)
	}
	r.run(cli.ExitNetwork, &e, "release", "keys", "revoke", id)
	if e.Error.Code != "key_revoked" {
		t.Errorf("second revoke = %+v", e)
	}
}
