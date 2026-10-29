package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The delivery plane must keep serving while the control plane's
// database is down (RFC 0002 §3), so nothing that talks to Postgres may
// be linked into glossa-edge, not even transitively.
func TestNoDatabaseInImportGraph(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").CombinedOutput() //nolint:gosec // fixed arguments
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{
		"go.klarlabs.de/glossa/platform/internal/kernel/db",
		"go.klarlabs.de/glossa/platform/internal/kernel/outbox",
		"go.klarlabs.de/glossa/platform/internal/kernel/tenancy/tenantpg",
		"github.com/jackc/pgx",
		"github.com/lib/pq",
		"github.com/golang-migrate",
		"/adapters/postgres",
		"/internal/release/app",
		"/internal/catalog/",
		"/internal/localization/",
		"/internal/identity/",
	}
	deps := strings.Fields(string(out))
	if len(deps) < 10 {
		t.Fatalf("suspiciously few dependencies: %v", deps)
	}
	for _, dep := range deps {
		for _, f := range forbidden {
			// "/…" entries match anywhere in the path, others as a prefix.
			if (strings.HasPrefix(f, "/") && strings.Contains(dep, f)) || strings.HasPrefix(dep, f) {
				t.Errorf("glossa-edge depends on %s (%s)", dep, f)
			}
		}
	}
}

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

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestServeAndShutdown(t *testing.T) {
	addr := freeAddr(t)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, lookupFrom(map[string]string{
			"GLOSSA_HTTP_ADDR": addr, "GLOSSA_STORAGE_DIR": t.TempDir(), "GLOSSA_SHUTDOWN_TIMEOUT": "5s",
		}), logs)
	}()
	// No keep-alive: a spare connection the transport dialed but never used
	// would hold up the graceful shutdown for five seconds.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get("http://" + addr + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("edge never became ready; logs:\n%s", logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, err := client.Get("http://" + addr + "/v1/glossa_pk_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/production/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("unknown key: %d %v", resp.StatusCode, resp.Header)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("edge did not stop")
	}
	for _, want := range []string{"glossa-edge starting", "listening", "glossa-edge stopped"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs lack %q", want)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	err := run(context.Background(), nil, lookupFrom(map[string]string{"GLOSSA_STORAGE_DRIVER": "s3"}), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "GLOSSA_S3_ENDPOINT") {
		t.Fatalf("err = %v", err)
	}
}

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, lookupFrom(nil), &out); err != nil || strings.TrimSpace(out.String()) == "" {
		t.Fatalf("version: %v %q", err, out.String())
	}
}
