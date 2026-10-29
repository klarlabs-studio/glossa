//go:build system

package m2_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/edge"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
)

// testAuthSecret is base64 of 42 bytes.
const testAuthSecret = "YS10ZXN0LWF1dGgtc2VjcmV0LXRoYXQtaXMtbG9uZy1lbm91Z2gtMTIz"

const bucket = "glossa-m2"

// signingKeyID and signingSeed sign the releases the runtime verifies.
const signingKeyID = "m2-2026"

var signingSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))

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

// deployment is a real glossa-server binary on a testcontainers Postgres
// and MinIO, plus glossa-edge on the same bucket.
type deployment struct {
	base    string
	edgeURL string
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

// deploy starts Postgres, MinIO, glossa-server (built from this module)
// and glossa-edge. The AI workers may reach the fake provider on
// loopback (GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS).
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	vars := s3Vars(minio.minio)
	for k, v := range map[string]string{
		"DATABASE_URL":                      db.db.AppDSN + "&pool_max_conns=32",
		"MIGRATION_DATABASE_URL":            db.db.OwnerDSN,
		"GLOSSA_MIGRATE":                    "up",
		"GLOSSA_HTTP_ADDR":                  addr,
		"GLOSSA_AUTH_SECRET":                testAuthSecret,
		"GLOSSA_MAIL_DRIVER":                "log",
		"GLOSSA_STUDIO_URL":                 "https://studio.test",
		"GLOSSA_RELEASE_SIGNING_KEYS":       signingKeyID + "=" + signingSeed,
		"GLOSSA_OUTBOX_POLL_INTERVAL":       "50ms",
		"GLOSSA_OUTBOX_BATCH_SIZE":          "200",
		"GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS": "true",
		"GLOSSA_AI_POLL_INTERVAL":           "50ms",
		"GLOSSA_AI_WORKERS":                 "16",
		"GLOSSA_AI_PROVIDER_CONCURRENCY":    "16",
		"GLOSSA_INTEGRATION_POLL_INTERVAL":  "100ms",
	} {
		vars[k] = v
	}
	logs := &syncBuffer{}
	cmd := exec.Command(bin)
	cmd.Env = os.Environ()
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
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("glossa-server log (tail):\n%s", tail(logs.String(), 60))
		}
	})
	d := &deployment{base: "http://" + addr, logs: logs}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if resp, err := http.Get(d.base + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("glossa-server never became ready:\n%s", tail(logs.String(), 60))
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.edgeURL = startEdge(t, minio.minio)
	return d
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
	cfg, err := config.LoadEdge(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc, done := make(chan net.Addr, 1), make(chan error, 1)
	go func() {
		done <- edge.Run(ctx, cfg, slog.New(slog.DiscardHandler), "m2", func(a net.Addr) { addrc <- a })
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

// client calls the /v1 API as a signed-in person (session cookie and
// CSRF token), the way Studio does.
type client struct {
	t            *testing.T
	base         string
	cookie, csrf string
	http         *http.Client
}

// apiError is a non-expected response.
type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

// send performs a request and decodes a JSON response into out. It
// returns an error for an unexpected status instead of failing, so it
// can run on worker goroutines.
func (c *client) send(method, path string, body io.Reader, contentType string, want int, out any, headers ...string) (http.Header, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", "__Host-glossa_session="+c.cookie)
	}
	if c.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", c.csrf)
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
