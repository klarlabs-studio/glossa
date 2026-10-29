//go:build integration

package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
)

// syncBuffer is a goroutine-safe log sink.
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

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// TestServerLifecycle boots the real composition root against Postgres:
// migrate on start, verify the app role, serve, become ready, then shut
// down gracefully on cancellation.
func TestServerLifecycle(t *testing.T) {
	env, err := dbtest.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()

	addr := freeAddr(t)
	lookup := lookupFrom(map[string]string{
		"DATABASE_URL":                env.AppDSN,
		"MIGRATION_DATABASE_URL":      env.OwnerDSN,
		"GLOSSA_MIGRATE":              "up",
		"GLOSSA_HTTP_ADDR":            addr,
		"GLOSSA_SHUTDOWN_TIMEOUT":     "5s",
		"GLOSSA_OUTBOX_POLL_INTERVAL": "50ms",
		"GLOSSA_AUTH_SECRET":          testAuthSecret,
		"GLOSSA_STORAGE_DIR":          t.TempDir(),
	})
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, lookup, logs) }()

	waitReady(t, "http://"+addr+"/readyz", done)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v\nlogs:\n%s", err, logs)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
	for _, want := range []string{"migrations applied", "listening", "glossa-server stopped"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs lack %q", want)
		}
	}
	if strings.Contains(logs.String(), "app-test-password") {
		t.Error("DSN password leaked into logs")
	}
}

func TestServerRefusesSuperuserConnection(t *testing.T) {
	env, err := dbtest.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	err = run(context.Background(), nil, lookupFrom(map[string]string{
		"DATABASE_URL":       env.SuperDSN,
		"GLOSSA_AUTH_SECRET": testAuthSecret,
		"GLOSSA_HTTP_ADDR":   freeAddr(t),
	}), &syncBuffer{})
	if err == nil || !strings.Contains(err.Error(), "bypasses row-level security") {
		t.Fatalf("err = %v, want the RLS bypass refusal", err)
	}
}

func waitReady(t *testing.T, url string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run exited early: %v", err)
		default:
		}
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server never became ready")
}
