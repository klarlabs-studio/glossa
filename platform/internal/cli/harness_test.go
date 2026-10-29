package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
)

// memStore is an in-memory credentials.Store.
type memStore struct{ tokens map[string]string }

func (m *memStore) Get(s string) (string, error) {
	if t, ok := m.tokens[s]; ok {
		return t, nil
	}
	return "", credentials.ErrNotFound
}
func (m *memStore) Set(s, t string) error { m.tokens[s] = t; return nil }
func (m *memStore) Delete(s string) error {
	if _, ok := m.tokens[s]; !ok {
		return credentials.ErrNotFound
	}
	delete(m.tokens, s)
	return nil
}
func (m *memStore) Name() string { return "memory" }

// workspace is a project directory plus the environment commands see.
type workspace struct {
	t           *testing.T
	dir         string
	env         map[string]string
	store       *memStore
	stdin       string
	interactive bool
	// slept is the waits a device sign-in asked for; none really happen.
	slept []time.Duration
}

func newWorkspace(t *testing.T) *workspace {
	return &workspace{t: t, dir: t.TempDir(), env: map[string]string{}, store: &memStore{tokens: map[string]string{}}}
}

// withProject writes glossa.yaml for srv and the given catalogs.
func (w *workspace) withProject(srv *fakeServer, catalogs map[string]string) *workspace {
	server := "http://unused.invalid"
	if srv != nil {
		server = srv.URL()
	}
	w.write("glossa.yaml", `version: 1
server: `+server+`
project: shop
source_locale: en
catalogs:
  path: locales/{locale}.json
generate:
  typescript: src/glossa/messages.ts
  vue: src/glossa/glossa-vue.ts
  react: src/glossa/glossa-react.ts
  go: internal/msg/messages.go
  dart: lib/glossa/messages.dart
extract:
  include: ["src/**/*.{ts,vue}", "**/*.go"]
  exclude: ["src/glossa/**"]
`)
	for locale, body := range catalogs {
		w.write("locales/"+locale+".json", body)
	}
	w.env["GLOSSA_TOKEN"] = testToken
	return w
}

func (w *workspace) write(name, body string) {
	w.t.Helper()
	p := filepath.Join(w.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workspace) read(name string) string {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.dir, name))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

type result struct {
	code           int
	stdout, stderr string
}

func (w *workspace) run(args ...string) result {
	w.t.Helper()
	var out, errb bytes.Buffer
	code := Main(context.Background(), args, Env{
		Stdin: strings.NewReader(w.stdin), Stdout: &out, Stderr: &errb,
		Getenv:      func(k string) string { return w.env[k] },
		Dir:         w.dir,
		Interactive: w.interactive,
		Credentials: w.store,
		Version:     "test",
		Sleep:       func(_ context.Context, d time.Duration) error { w.slept = append(w.slept, d); return nil },
	})
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

// json runs a command with --json and decodes its document.
func (w *workspace) json(v any, args ...string) result {
	w.t.Helper()
	r := w.run(append(args, "--json")...)
	if err := json.Unmarshal([]byte(r.stdout), v); err != nil {
		w.t.Fatalf("glossa %s --json: not one JSON document (%v):\n%s\nstderr: %s", strings.Join(args, " "), err, r.stdout, r.stderr)
	}
	return r
}

func (r result) want(t *testing.T, code ExitCode) {
	t.Helper()
	if r.code != int(code) {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.stdout, r.stderr)
	}
}

// errorDoc is the --json error shape.
type errorDoc struct {
	Schema string    `json:"schema"`
	Error  jsonError `json:"error"`
}

// runInteractive runs a command as if stdin were a terminal.
func runInteractive(w *workspace, args ...string) result {
	w.t.Helper()
	w.interactive = true
	defer func() { w.interactive = false }()
	return w.run(args...)
}
