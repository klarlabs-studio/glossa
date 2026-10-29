package httpgin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

// sseFrame mirrors one parsed SSE message — id + event name + the
// json-decoded data payload. Test-only.
type sseFrame struct {
	ID    string
	Event string
	Data  translationapp.Event
}

// startSSEServer spins a gin engine that mounts /sse with a stub
// auth middleware planting a known project on the context. Lets us
// hit handleSSE without the DB-dependent apiKeyAuth + rls stack.
//
// Heartbeat is shortened to 50ms so the test can observe the
// keep-alive comment line within a reasonable wait.
func startSSEServer(t *testing.T) (*httptest.Server, *translationapp.Hub, project.Project) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	hub := translationapp.NewHub()

	slug, _ := project.NewSlug("demo")
	name, _ := project.NewName("Demo")
	p := project.Project{
		ID:            uuid.New(),
		TenantID:      uuid.New(),
		Slug:          slug,
		Name:          name,
		DefaultLocale: "de",
	}

	r := gin.New()
	r.GET("/sse", func(c *gin.Context) {
		c.Set(ctxKeyProject, p)
		c.Set(ctxKeyTenantID, p.TenantID)
		handleSSE(hub, 50*time.Millisecond)(c)
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, hub, p
}

// openSSE issues a GET against the server with an optional
// Last-Event-ID and returns the connected response + a bufio.Reader
// pinned to the body so the test can drive the parser per-line.
func openSSE(t *testing.T, ctx context.Context, url, lastEventID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		_ = resp.Body.Close()
		t.Fatalf("Content-Type: want text/event-stream, got %q", got)
	}
	return resp, bufio.NewReader(resp.Body)
}

// readFrame consumes one full SSE frame from r. Returns the parsed
// frame or an error if the deadline expires before the blank
// terminator line. Skips comment lines (heartbeats).
func readFrame(r *bufio.Reader, deadline time.Time) (sseFrame, error) {
	var f sseFrame
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return f, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if f.ID != "" || f.Event != "" {
				return f, nil
			}
			continue // blank line before any field — keep reading
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment; heartbeat for our purposes
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimPrefix(val, " ")
		switch key {
		case "id":
			f.ID = val
		case "event":
			f.Event = val
		case "data":
			if err := json.Unmarshal([]byte(val), &f.Data); err != nil {
				return f, fmt.Errorf("parse data %q: %w", val, err)
			}
		}
		if time.Now().After(deadline) {
			return f, fmt.Errorf("readFrame: deadline exceeded")
		}
	}
}

// TestSSE_DeliversBroadcastWithinOneSecond — the acceptance
// criterion: a translator edit must show up on a subscribing
// consumer within 1s.
func TestSSE_DeliversBroadcastWithinOneSecond(t *testing.T) {
	srv, hub, p := startSSEServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, r := openSSE(t, ctx, srv.URL+"/sse", "")
	defer resp.Body.Close()

	// Publish from the test goroutine — same path the PATCH
	// handler uses in production.
	go func() {
		time.Sleep(50 * time.Millisecond) // let the client connect
		hub.Publish(p.ID, p.TenantID, translationapp.Event{
			Type:    "translation.updated",
			Project: p.Slug.String(),
			Locale:  "de",
			Key:     "cart.checkout",
			Value:   "Zur Kasse",
			Status:  "approved",
		})
	}()

	frame, err := readFrame(r, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if frame.Event != "translation.updated" {
		t.Fatalf("event name: want translation.updated, got %q", frame.Event)
	}
	if frame.Data.Key != "cart.checkout" || frame.Data.Value != "Zur Kasse" {
		t.Fatalf("payload mismatch: %+v", frame.Data)
	}
	if frame.ID == "" {
		t.Fatal("missing id line — Last-Event-ID resume would not work")
	}
}

// TestSSE_LastEventIDReplaysMissed — second acceptance criterion:
// a client that reconnects with Last-Event-ID gets back any events
// it missed during the disconnect.
func TestSSE_LastEventIDReplaysMissed(t *testing.T) {
	srv, hub, p := startSSEServer(t)

	// Publish two events with no client connected — they land
	// in the per-project history ring.
	for i := 0; i < 2; i++ {
		hub.Publish(p.ID, p.TenantID, translationapp.Event{
			Type:  "translation.updated",
			Key:   fmt.Sprintf("k%d", i+1),
			Value: fmt.Sprintf("v%d", i+1),
		})
	}

	// Reconnect after the first event — the second must replay.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, r := openSSE(t, ctx, srv.URL+"/sse", "1")
	defer resp.Body.Close()

	frame, err := readFrame(r, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if frame.ID != "2" {
		t.Fatalf("replay id: want 2, got %q", frame.ID)
	}
	if frame.Data.Key != "k2" {
		t.Fatalf("replay key: want k2, got %q", frame.Data.Key)
	}
}

// TestSSE_HeartbeatKeepsConnectionAlive proves the keep-alive
// comment fires during idle periods. Faster than waiting 60s — we
// shortened the heartbeat to 50ms in startSSEServer.
func TestSSE_HeartbeatKeepsConnectionAlive(t *testing.T) {
	srv, _, _ := startSSEServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, r := openSSE(t, ctx, srv.URL+"/sse", "")
	defer resp.Body.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.HasPrefix(line, ":") {
			return // heartbeat observed
		}
	}
	t.Fatal("no heartbeat received within 1s window")
}
