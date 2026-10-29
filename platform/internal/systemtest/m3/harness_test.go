//go:build system

package m3_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/edge"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
)

// testAuthSecret is base64 of 42 bytes.
const testAuthSecret = "YS10ZXN0LWF1dGgtc2VjcmV0LXRoYXQtaXMtbG9uZy1lbm91Z2gtMTIz"

const bucket = "glossa-m3"

// signingKeyID and signingSeed sign the releases the edge serves.
const signingKeyID = "m3-2026"

var signingSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))

// The GitHub App the fake serves: its numbers are the fixtures'.
const (
	appID          = 99001
	appSlug        = "glossa"
	webhookSecret  = "m3-webhook-secret"
	installationID = int64(4242)
	repositoryID   = int64(10101)
	repositoryName = "brotwerk/shop"
)

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

// deployment is a real glossa-server on a testcontainers Postgres and
// MinIO, a real glossa-edge on the same bucket, and a fake GitHub the
// server's App client talks to.
type deployment struct {
	base    string
	edgeURL string
	github  *githubtest.Server
	logs    *syncBuffer
}

func s3Vars(minio *s3test.Env) map[string]string {
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

// deploy starts Postgres and MinIO, then glossa-edge (so the server can
// be told its public URL, which the sticky comment links to), the fake
// GitHub, and finally glossa-server built from this module.
func deploy(t *testing.T) *deployment {
	t.Helper()
	ctx := context.Background()
	type started struct {
		db    *dbtest.Env
		minio *s3test.Env
		err   error
	}
	dbc, minioc := make(chan started, 1), make(chan started, 1)
	go func() { env, err := dbtest.Start(ctx); dbc <- started{db: env, err: err} }()
	go func() { env, err := s3test.Start(ctx); minioc <- started{minio: env, err: err} }()
	bin := filepath.Join(t.TempDir(), "glossa-server")
	build := exec.Command("go", "build", "-o", bin, "./cmd/glossa-server")
	build.Dir = filepath.Join("..", "..", "..") // the platform module
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build glossa-server: %v\n%s", err, out)
	}
	db, minio := <-dbc, <-minioc
	if db.err != nil {
		t.Fatal(db.err)
	}
	t.Cleanup(db.db.Close)
	if minio.err != nil {
		t.Fatal(minio.err)
	}
	t.Cleanup(minio.minio.Close)
	if _, err := minio.minio.Store(ctx, bucket); err != nil {
		t.Fatal(err)
	}

	d := &deployment{logs: &syncBuffer{}}
	d.edgeURL = startEdge(t, minio.minio)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	d.github = githubtest.New(t, githubtest.Options{AppID: appID, PublicKey: &key.PublicKey})
	d.github.AddInstallation(installationID, "brotwerk", repositoryID)
	d.github.AddRepository(githubtest.Repository{
		ID: repositoryID, Name: "shop", FullName: repositoryName, DefaultBranch: "main",
	})
	d.github.AddUser("gho_owner", installationID)
	d.github.AddOAuthCode("code-m3", "gho_owner")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	vars := s3Vars(minio.minio)
	for k, v := range map[string]string{
		"DATABASE_URL":                       db.db.AppDSN + "&pool_max_conns=32",
		"MIGRATION_DATABASE_URL":             db.db.OwnerDSN,
		"GLOSSA_MIGRATE":                     "up",
		"GLOSSA_HTTP_ADDR":                   addr,
		"GLOSSA_AUTH_SECRET":                 testAuthSecret,
		"GLOSSA_MAIL_DRIVER":                 "log",
		"GLOSSA_STUDIO_URL":                  "https://studio.test",
		"GLOSSA_EDGE_PUBLIC_URL":             d.edgeURL,
		"GLOSSA_RELEASE_SIGNING_KEYS":        signingKeyID + "=" + signingSeed,
		"GLOSSA_OUTBOX_POLL_INTERVAL":        "50ms",
		"GLOSSA_OUTBOX_BATCH_SIZE":           "200",
		"GLOSSA_INTEGRATION_POLL_INTERVAL":   "100ms",
		"GLOSSA_GITHUB_APP_ID":               strconv.Itoa(appID),
		"GLOSSA_GITHUB_APP_SLUG":             appSlug,
		"GLOSSA_GITHUB_APP_PRIVATE_KEY":      string(privateKeyPEM(t, key)),
		"GLOSSA_GITHUB_WEBHOOK_SECRET":       webhookSecret,
		"GLOSSA_GITHUB_CLIENT_ID":            "Iv1.m3",
		"GLOSSA_GITHUB_CLIENT_SECRET":        "m3-client-secret",
		"GLOSSA_GITHUB_API_URL":              d.github.URL,
		"GLOSSA_GITHUB_WEB_URL":              d.github.WebURL,
		"GLOSSA_GITHUB_INBOX_POLL_INTERVAL":  "50ms",
		"GLOSSA_GITHUB_CHECK_POLL_INTERVAL":  "50ms",
		"GLOSSA_GITHUB_CHECK_DEPTH_INTERVAL": "1s",
		"GLOSSA_BRANCH_PUBLISH_INTERVAL":     "250ms",
	} {
		vars[k] = v
	}
	cmd := exec.Command(bin)
	cmd.Env = os.Environ()
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = d.logs, d.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("glossa-server log (tail):\n%s", tail(d.logs.String(), 120))
			if d.github != nil {
				t.Logf("the fake GitHub answered: %s", d.githubSummary())
			}
		}
	})
	d.base = "http://" + addr
	deadline := time.Now().Add(90 * time.Second)
	for {
		if resp, err := http.Get(d.base + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("glossa-server never became ready:\n%s", tail(d.logs.String(), 60))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return d
}

// privateKeyPEM is the App's key, the way the Kubernetes Secret holds it.
func privateKeyPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// Summary counts the fake GitHub's requests by route, for a failure
// message that says whether the server called GitHub at all.
func (d *deployment) githubSummary() string {
	counts := map[string]int{}
	for _, r := range d.github.Requests() {
		counts[r.Route]++
	}
	routes := make([]string, 0, len(counts))
	for k := range counts {
		routes = append(routes, fmt.Sprintf("%s ×%d", k, counts[k]))
	}
	sort.Strings(routes)
	if len(routes) == 0 {
		return "nothing"
	}
	return strings.Join(routes, ", ")
}

// tail is the end of the server log without the per-request lines.
func tail(s string, lines int) string {
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.Contains(l, `"message":"http request"`) {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts[max(0, len(parts)-lines):], "\n")
}

// startEdge runs a real glossa-edge (the same edge.Run the binary runs)
// on the bucket and returns its base URL.
func startEdge(t *testing.T, minio *s3test.Env) string {
	t.Helper()
	vars := s3Vars(minio)
	vars["GLOSSA_HTTP_ADDR"] = "127.0.0.1:0"
	vars["GLOSSA_SHUTDOWN_TIMEOUT"] = "5s"
	vars["GLOSSA_EDGE_KEY_TTL"] = "200ms"
	vars["GLOSSA_EDGE_MANIFEST_TTL"] = "200ms"
	cfg, err := config.LoadEdge(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc, done := make(chan net.Addr, 1), make(chan error, 1)
	go func() {
		done <- edge.Run(ctx, cfg, slog.New(slog.DiscardHandler), "m3", func(a net.Addr) { addrc <- a })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("glossa-edge did not stop")
		}
	})
	select {
	case a := <-addrc:
		return "http://" + a.String()
	case err := <-done:
		t.Fatalf("glossa-edge: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("glossa-edge never listened")
	}
	return ""
}

// serveApp serves a built fixture app: every path that is not a file
// answers index.html, the way a single-page deployment does.
func serveApp(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(dir))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))); err == nil && r.URL.Path != "/" {
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}))
	t.Cleanup(s.Close)
	return s
}

// client calls the /v1 API as a signed-in person (session cookie and
// CSRF token), the way Studio does, or with a bearer token.
type client struct {
	t            *testing.T
	base         string
	cookie, csrf string
	bearer       string
	origin       string
	http         *http.Client
}

// apiError is a non-expected response.
type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

// code is the problem document's code, if the body holds one.
func (e *apiError) code() string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(e.body), &p)
	return p.Code
}

// send performs a request and decodes a JSON response into out.
func (c *client) send(method, path string, body io.Reader, contentType string, want int, out any, headers ...string) (http.Header, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	switch {
	case c.bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	case c.cookie != "":
		req.Header.Set("Cookie", "__Host-glossa_session="+c.cookie)
		if c.csrf != "" && method != http.MethodGet {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return resp.Header, &apiError{status: resp.StatusCode, body: string(raw)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.Header, fmt.Errorf("decode %s %s: %w: %s", method, path, err, raw)
		}
	}
	return resp.Header, nil
}

// do is send with a JSON body that fails the test on any error.
func (c *client) do(method, path string, body any, want int, out any, headers ...string) http.Header {
	c.t.Helper()
	h, err := c.try(method, path, body, want, out, headers...)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	return h
}

func (c *client) try(method, path string, body any, want int, out any, headers ...string) (http.Header, error) {
	var r io.Reader
	ct := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r, ct = bytes.NewReader(b), "application/json"
	}
	return c.send(method, path, r, ct, want, out, headers...)
}

// list reads every page of a listing ({items, next_page_token}).
func list[T any](c *client, path string, query url.Values) []T {
	c.t.Helper()
	var all []T
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("page_size", "100")
	for {
		var page struct {
			Items         []T    `json:"items"`
			NextPageToken string `json:"next_page_token"`
		}
		c.do(http.MethodGet, path+"?"+q.Encode(), nil, http.StatusOK, &page)
		all = append(all, page.Items...)
		if page.NextPageToken == "" {
			return all
		}
		q.Set("page_token", page.NextPageToken)
	}
}

var mailedToken = regexp.MustCompile(`/auth/sign-in#token=([A-Za-z0-9_-]{43})`)

// signIn runs the magic-link flow (the log mailer prints the link) and
// returns a client with the session.
func (d *deployment) signIn(t *testing.T, email string) *client {
	t.Helper()
	c := &client{t: t, base: d.base, http: &http.Client{Timeout: 60 * time.Second}}
	c.do(http.MethodPost, "/v1/auth/magic-links", map[string]string{"email": email}, http.StatusAccepted, nil)
	var link []string
	for range 100 {
		if m := mailedToken.FindAllStringSubmatch(d.logs.String(), -1); len(m) > 0 {
			link = m[len(m)-1]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if link == nil {
		t.Fatalf("no sign-in link in the log:\n%s", tail(d.logs.String(), 40))
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	h := c.do(http.MethodPost, "/v1/auth/magic-link-redemptions", map[string]string{"token": link[1]}, http.StatusOK, &session)
	c.cookie, _, _ = strings.Cut(strings.TrimPrefix(h.Get("Set-Cookie"), "__Host-glossa_session="), ";")
	c.csrf = session.CSRFToken
	return c
}

// eventually polls cond until it reports true or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ok, state := cond()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s: %s", timeout, what, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// parallel runs fn for every item on n workers and returns the errors.
func parallel[T any](items []T, n int, fn func(T) error) []error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	ch := make(chan T)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				if err := fn(it); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
	for _, it := range items {
		ch <- it
	}
	close(ch)
	wg.Wait()
	return errs
}
