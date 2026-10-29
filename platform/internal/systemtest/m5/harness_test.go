//go:build system

package m5_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

const bucket = "glossa-m5"

// signingKeyID and signingSeed sign every release this test publishes.
const signingKeyID = "m5-2026"

var signingSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))

// auditKeyID and auditSeed sign the audit exports of §12.5: a key of
// its own, not the release key (RFC 0006 §6.2). The harness verifies
// with the public half it derives here, never with a key the platform
// hands it, so a platform signing with something else fails §12.5.
const auditKeyID = "m5-audit-2026"

var auditSeed = bytes.Repeat([]byte{6}, 32)

// auditPublicKey is the --public-key `glossa audit verify` trusts.
func auditPublicKey() string {
	pub := ed25519.NewKeyFromSeed(auditSeed).Public().(ed25519.PublicKey)
	return auditKeyID + "=" + base64.StdEncoding.EncodeToString(pub)
}

// studioURL is where the links this server mails point. Nothing in §12
// opens Studio; the links are followed by the test, not a browser.
const studioURL = "http://localhost:4318"

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
// MinIO, a real glossa-edge on the same bucket, and the test mailer the
// server delivers its mail to over SMTP. Nothing here reaches the
// network.
type deployment struct {
	base    string
	edgeURL string
	mail    *mailbox
	// github is the fake GitHub the server's App client talks to, so
	// §12.2's sweep has a Git connection in each project to address.
	github *githubtest.Server
	// provider is the fake AI provider on loopback, so the sweep has an
	// AI fill, job and suggestion in each project to address.
	provider *fakeProvider
	logs     *syncBuffer
	// dbDSN is the superuser DSN of the platform's Postgres; the v0.3
	// fixture of §12.6 gets a database of its own in the same server.
	dbSuper string
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

// deploy starts Postgres and MinIO, the test mailer, glossa-server built
// from this module with SMTP mail and /mcp on, and glossa-edge.
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
	build.Dir = platformDir()
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

	d := &deployment{logs: &syncBuffer{}, mail: startMailbox(t), dbSuper: db.db.SuperDSN}
	d.provider = startFakeProvider(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	d.github = githubtest.New(t, githubtest.Options{AppID: appID, PublicKey: &key.PublicKey})
	d.github.AddInstallation(installationID, "acme", repositoryID)
	d.github.AddRepository(githubtest.Repository{
		ID: repositoryID, Name: "monorepo", FullName: repositoryName, DefaultBranch: "main",
	})
	d.github.AddUser("gho_owner", installationID)
	d.github.AddOAuthCode("code-m5", "gho_owner")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	vars := s3Vars(minio.minio)
	for k, v := range map[string]string{
		"DATABASE_URL":           db.db.AppDSN + "&pool_max_conns=32",
		"MIGRATION_DATABASE_URL": db.db.OwnerDSN,
		"GLOSSA_MIGRATE":         "up",
		"GLOSSA_HTTP_ADDR":       addr,
		"GLOSSA_AUTH_SECRET":     testAuthSecret,
		// The owner's decision (RFC 0006 §15 q1): mail goes through a
		// real SMTP provider. The test mailer stands in for it, with
		// credentials, so an invitation is accepted the way it will be
		// in production — by a verified address — and not by reading a
		// link out of the log.
		"GLOSSA_MAIL_DRIVER":          "smtp",
		"GLOSSA_SMTP_ADDR":            d.mail.addr(),
		"GLOSSA_SMTP_USERNAME":        smtpUser,
		"GLOSSA_SMTP_PASSWORD":        smtpPassword,
		"GLOSSA_SMTP_ALLOW_PLAINTEXT": "true",
		"GLOSSA_MAIL_FROM":            mailFrom,
		"GLOSSA_STUDIO_URL":           studioURL,
		"GLOSSA_RELEASE_SIGNING_KEYS": signingKeyID + "=" + signingSeed,
		// §12.5 exports the run's audit range: exports on, with their
		// own key.
		"GLOSSA_AUDIT_EXPORTS_ENABLED": "true",
		"GLOSSA_AUDIT_SIGNING_KEY":     auditKeyID + "=" + base64.StdEncoding.EncodeToString(auditSeed),
		"GLOSSA_OUTBOX_POLL_INTERVAL":  "50ms",
		"GLOSSA_OUTBOX_BATCH_SIZE":     "200",
		"GLOSSA_AI_POLL_INTERVAL":      "100ms",
		// §12.2 sweeps the MCP read tools as the vendor member.
		"GLOSSA_MCP_ENABLED": "true",
		// The fixture's AI provider is the fake on loopback
		// (fakeprovider_test.go): it exists so §12.2's sweep has AI
		// fills, jobs and suggestions to address, and no request ever
		// leaves the machine.
		"GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS": "true",
		// The GitHub App, served by the fake: §12.2's sweep addresses a
		// Git connection in each project.
		"GLOSSA_GITHUB_APP_ID":              strconv.Itoa(appID),
		"GLOSSA_GITHUB_APP_SLUG":            appSlug,
		"GLOSSA_GITHUB_APP_PRIVATE_KEY":     string(privateKeyPEM(t, key)),
		"GLOSSA_GITHUB_WEBHOOK_SECRET":      webhookSecret,
		"GLOSSA_GITHUB_CLIENT_ID":           "Iv1.m5",
		"GLOSSA_GITHUB_CLIENT_SECRET":       "m5-client-secret",
		"GLOSSA_GITHUB_API_URL":             d.github.URL,
		"GLOSSA_GITHUB_WEB_URL":             d.github.WebURL,
		"GLOSSA_GITHUB_INBOX_POLL_INTERVAL": "50ms",
		"GLOSSA_GITHUB_CHECK_POLL_INTERVAL": "50ms",
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
			t.Logf("glossa-server log (tail):\n%s", tail(d.logs.String(), 80))
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
	d.edgeURL = startEdge(t, minio.minio)
	return d
}

// The GitHub App the fake serves: one installation that sees one
// repository, connected to both projects under different paths.
const (
	appID          = 99005
	appSlug        = "glossa"
	webhookSecret  = "m5-webhook-secret"
	installationID = int64(5252)
	repositoryID   = int64(50505)
	repositoryName = "acme/monorepo"
)

// privateKeyPEM is the App's key, the way the Kubernetes Secret holds it.
func privateKeyPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// platformDir is the platform module, from this package's directory.
func platformDir() string {
	abs, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		panic(err)
	}
	return abs
}

// repoRoot is the repository, one above the platform module.
func repoRoot() string { return filepath.Dir(platformDir()) }

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
		done <- edge.Run(ctx, cfg, slog.New(slog.DiscardHandler), "m5", func(a net.Addr) { addrc <- a })
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

// ── the API client, and the harness's own log of what it changed ─────

// call is one mutating request the harness made, recorded by the
// harness itself and never read back from the platform. §12.5 compares
// the exported audit range against this log: an entry the platform
// forgot to write cannot hide behind an outbox that forgot it too.
type call struct {
	Criterion  string
	Method     string
	Path       string
	Status     int
	Actor      string
	Start, End time.Time
	// IDs are the identifiers in the path and in the response body, so
	// an audit entry can be matched to the call that caused it.
	IDs []string
}

// ok says the call changed something: only those owe an audit entry.
func (c call) ok() bool { return c.Status >= 200 && c.Status < 300 }

// callLog is shared by every client of one scenario.
type callLog struct {
	mu        sync.Mutex
	criterion string
	calls     []call
}

func (l *callLog) setCriterion(id string) {
	l.mu.Lock()
	l.criterion = id
	l.mu.Unlock()
}

func (l *callLog) add(c call) {
	l.mu.Lock()
	c.Criterion = l.criterion
	l.calls = append(l.calls, c)
	l.mu.Unlock()
}

func (l *callLog) snapshot() []call {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]call(nil), l.calls...)
}

// client calls the /v1 API as a signed-in person (session cookie and
// CSRF token), the way Studio does, or with a bearer token.
type client struct {
	t            *testing.T
	base         string
	cookie, csrf string
	bearer       string
	// actor is who the platform should record for this client's
	// changes: person:<id> or token:<id>.
	actor string
	// name is how the report refers to this client.
	name string
	log  *callLog
	http *http.Client
}

// apiError is a non-expected response.
type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	body := e.body
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, strings.TrimSpace(body))
}

// code is the problem document's code, if the body holds one.
func (e *apiError) code() string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(e.body), &p)
	return p.Code
}

func statusOf(err error) int {
	if ae, ok := err.(*apiError); ok {
		return ae.status
	}
	return 0
}

// absent says the server has no such operation: 404 or 405 from a
// path or method nothing routes. The gap messages quote the body, so a
// reader can tell a missing route from a missing resource.
func absent(err error) bool {
	s := statusOf(err)
	return s == http.StatusNotFound || s == http.StatusMethodNotAllowed
}

// send performs a request, records it if it is mutating, and decodes a
// JSON response into out.
func (c *client) send(method, path string, body io.Reader, contentType string, want int, out any, headers ...string) (http.Header, []byte, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, nil, err
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
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if method != http.MethodGet && method != http.MethodHead && c.log != nil && c.actor != "" {
		c.log.add(call{Method: method, Path: path, Status: resp.StatusCode, Actor: c.actor,
			Start: start, End: time.Now(), IDs: idsIn(path, raw)})
	}
	if resp.StatusCode != want {
		return resp.Header, raw, &apiError{status: resp.StatusCode, body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.Header, raw, fmt.Errorf("decode %s %s: %w: %s", method, path, err, raw)
		}
	}
	return resp.Header, raw, nil
}

// do is send with a JSON body that fails the test on any error. Only
// fixture steps that M2–M4 already proved use it; every M5 step goes
// through try and records a gap instead.
func (c *client) do(method, path string, body any, want int, out any, headers ...string) http.Header {
	c.t.Helper()
	h, err := c.try(method, path, body, want, out, headers...)
	if err != nil {
		panic(harnessFailure{fmt.Sprintf("%s %s as %s: %v", method, path, c.name, err)})
	}
	return h
}

// harnessFailure is a fixture step that M2–M4 already proved failing.
// It panics rather than calling t.Fatal, so the phase that ran it can
// turn it into a gap — or, in the fixture, into a report that says the
// fixture failed — instead of ending the run without a REPORT.md.
type harnessFailure struct{ msg string }

func (h harnessFailure) String() string { return h.msg }

func fatalf(format string, args ...any) {
	panic(harnessFailure{fmt.Sprintf(format, args...)})
}

// putTranslation writes a translation as c, with the If-Match an update
// needs: a new translation answers 201, an update 200.
func (c *client) putTranslation(path string, body map[string]any) error {
	var headers []string
	if h, err := c.try(http.MethodGet, path, nil, http.StatusOK, nil); err == nil {
		headers = []string{"If-Match", h.Get("ETag")}
	}
	_, err := c.try(http.MethodPut, path, body, http.StatusOK, nil, headers...)
	if statusOf(err) == http.StatusCreated {
		return nil
	}
	return err
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
	h, _, err := c.send(method, path, r, ct, want, out, headers...)
	return h, err
}

// get is a GET that hands back the status and the body, whatever they
// are: the sweep of §12.2 judges every answer itself.
func (c *client) get(path string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	switch {
	case c.bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	case c.cookie != "":
		req.Header.Set("Cookie", "__Host-glossa_session="+c.cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// list reads every page of a listing ({items, next_page_token}).
func list[T any](c *client, path string, query url.Values) ([]T, error) {
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
		if _, err := c.try(http.MethodGet, path+"?"+q.Encode(), nil, http.StatusOK, &page); err != nil {
			return all, err
		}
		all = append(all, page.Items...)
		if page.NextPageToken == "" {
			return all, nil
		}
		q.Set("page_token", page.NextPageToken)
	}
}

func (d *deployment) newClient(t *testing.T, name string, log *callLog) *client {
	return &client{t: t, base: d.base, name: name, log: log, http: &http.Client{Timeout: 60 * time.Second}}
}

// signIn runs the magic-link flow through the test mailer and returns a
// client with the session, its actor set to the person.
func (d *deployment) signIn(t *testing.T, email string, log *callLog) (*client, error) {
	c := d.newClient(t, email, log)
	since := time.Now()
	if _, err := c.try(http.MethodPost, "/v1/auth/magic-links", map[string]string{"email": email}, http.StatusAccepted, nil); err != nil {
		return nil, fmt.Errorf("request a sign-in link: %w", err)
	}
	token, err := d.mail.linkFor(email, since)
	if err != nil {
		return nil, err
	}
	return c, d.redeem(c, token)
}

// register creates a password account, follows the verification link
// the test mailer receives, and signs in with the password — the path a
// vendor's translator takes to accept an invitation (RFC 0006 §12.2).
func (d *deployment) register(t *testing.T, email, password string, log *callLog) (*client, error) {
	c := d.newClient(t, email, log)
	since := time.Now()
	if _, err := c.try(http.MethodPost, "/v1/auth/registrations",
		map[string]string{"email": email, "password": password, "display_name": "Vera Vendor"}, http.StatusAccepted, nil); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	token, err := d.mail.linkFor(email, since)
	if err != nil {
		return nil, fmt.Errorf("verify the address: %w", err)
	}
	if err := d.redeem(c, token); err != nil {
		return nil, fmt.Errorf("follow the verification link: %w", err)
	}
	// And sign in again with the password, which only a verified
	// address may do on a server that sends mail.
	pw := d.newClient(t, email, log)
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	h, err := pw.try(http.MethodPost, "/v1/auth/password-sessions",
		map[string]string{"email": email, "password": password}, http.StatusOK, &session)
	if err != nil {
		return nil, fmt.Errorf("sign in with the password: %w", err)
	}
	pw.cookie, _, _ = strings.Cut(strings.TrimPrefix(h.Get("Set-Cookie"), "__Host-glossa_session="), ";")
	pw.csrf = session.CSRFToken
	return pw, pw.whoami()
}

func (d *deployment) redeem(c *client, token string) error {
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	h, err := c.try(http.MethodPost, "/v1/auth/magic-link-redemptions", map[string]string{"token": token}, http.StatusOK, &session)
	if err != nil {
		return fmt.Errorf("redeem the link: %w", err)
	}
	c.cookie, _, _ = strings.Cut(strings.TrimPrefix(h.Get("Set-Cookie"), "__Host-glossa_session="), ";")
	c.csrf = session.CSRFToken
	return c.whoami()
}

// whoami sets the client's actor from GET /v1/me.
func (c *client) whoami() error {
	var me struct {
		Person struct {
			ID string `json:"id"`
		} `json:"person"`
	}
	if _, err := c.try(http.MethodGet, "/v1/me", nil, http.StatusOK, &me); err != nil {
		return fmt.Errorf("read /v1/me: %w", err)
	}
	c.actor = "person:" + me.Person.ID
	return nil
}

// softly polls cond and reports whether it ever held, without failing
// the test: a criterion that cannot be met is recorded, not fatal.
func softly(timeout time.Duration, cond func() (bool, string)) (bool, string) {
	deadline := time.Now().Add(timeout)
	var state string
	for {
		ok, s := cond()
		if ok {
			return true, ""
		}
		state = s
		if time.Now().After(deadline) {
			return false, state
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// staysFor is how a negative claim ("the pointer did not move", "the
// translation is not approved yet") is checked: the condition is
// polled for the whole window and the first violation ends it, so a
// transient move is caught rather than slept through. A check that
// cannot be made is an error, never a pass. The window has to cover
// the outbox and the edge's refresh interval; it is not shortened to
// save time, and it is never the only evidence for a positive claim.
func staysFor(window time.Duration, violated func() (string, error)) error {
	deadline := time.Now().Add(window)
	for {
		what, err := violated()
		if err != nil {
			return err
		}
		if what != "" {
			return fmt.Errorf("%s", what)
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
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

var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// idsIn is every UUID in a request path and its response body.
func idsIn(path string, body []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range append(uuidRe.FindAllString(path, -1), uuidRe.FindAllString(string(body), 64)...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// short is an id's last eight characters: UUIDv7s minted together
// share their leading, time-ordered digits, so the tail is what tells
// two apart in a report.
func short(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}
