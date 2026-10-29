package edge_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/edge"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/httpserver"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

const (
	projectA = "0192f5a0-7a4e-7cc3-9d1e-3a4b5c6d7e8f"
	projectB = "0192f5a0-7a4e-7cc3-9d1e-3a4b5c6d7e90"
)

// flakyStore is object storage that can go down and counts reads.
type flakyStore struct {
	*objectstore.Memory
	down  atomic.Bool
	reads atomic.Int64
}

var errDown = errors.New("storage unavailable")

func (s *flakyStore) Get(ctx context.Context, key string, max int64) ([]byte, error) {
	s.reads.Add(1)
	if s.down.Load() {
		return nil, errDown
	}
	return s.Memory.Get(ctx, key, max)
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	store   *flakyStore
	clock   *clock
	handler http.Handler
	key     string
	art     []byte
	digest  string
	mfst    []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := &flakyStore{Memory: objectstore.NewMemory()}
	ctx := context.Background()
	key, _ := delivery.NewKey()
	art := []byte(`{"locale":"de","messages":{},"namespace":"default","schema":"glossa.artifact/v1"}`)
	digest := delivery.Digest(art)
	mfst := []byte(`{"release":{"id":"r1"}}`)
	scope, err := delivery.NewScope([]string{"production", "staging", "huge"}, false)
	must(t, err)
	must(t, store.Put(ctx, delivery.KeyIndexPath(key), delivery.EncodeKeyIndex(projectA, "k1", scope), "application/json"))
	must(t, store.Put(ctx, delivery.ArtifactPath(projectA, digest), art, "application/json"))
	must(t, store.Put(ctx, delivery.ManifestPath(projectA, "production"), mfst, "application/json"))
	clk := &clock{now: time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)}
	h := edge.New(store, edge.Config{
		CacheBytes: 1 << 20, KeyTTL: 30 * time.Second, ManifestTTL: 5 * time.Second, Now: clk.Now,
	}, slog.New(slog.DiscardHandler), observability.NewRegistry())
	srv := httpserver.New(config.HTTP{Addr: ":0", ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
		WriteTimeout: time.Second, IdleTimeout: time.Second, MaxBodyBytes: 1024},
		httpserver.Deps{Logger: slog.New(slog.DiscardHandler), Registry: observability.NewRegistry(), Routes: h.Routes})
	return &fixture{store: store, clock: clk, handler: srv.Handler(), key: key, art: art, digest: digest, mfst: mfst}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) get(t *testing.T, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, http.MethodGet, path, headers...)
}

func (f *fixture) do(t *testing.T, method, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) manifestPath() string { return "/v1/" + f.key + "/production/manifest.json" }
func (f *fixture) artifactPath() string { return "/v1/" + f.key + "/a/" + f.digest + ".json" }

const (
	manifestCaching = "public, max-age=60, stale-while-revalidate=300, stale-if-error=86400"
	artifactCaching = "public, max-age=31536000, immutable"
)

func TestManifest(t *testing.T) {
	f := newFixture(t)
	rec := f.get(t, f.manifestPath())
	etag := `"` + delivery.Digest(f.mfst) + `"`
	if rec.Code != 200 || rec.Body.String() != string(f.mfst) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	for name, want := range map[string]string{
		"Cache-Control": manifestCaching, "ETag": etag, "Content-Type": "application/json",
		"Access-Control-Allow-Origin": "*", "Access-Control-Expose-Headers": "ETag",
		"Content-Length": "23", "X-Content-Type-Options": "nosniff",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if h.Get("Set-Cookie") != "" {
		t.Error("the edge set a cookie")
	}
	for _, inm := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		rec := f.get(t, f.manifestPath(), "If-None-Match", inm)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag ||
			rec.Header().Get("Cache-Control") != manifestCaching {
			t.Errorf("If-None-Match %s: %d %v", inm, rec.Code, rec.Header())
		}
	}
	if rec := f.get(t, f.manifestPath(), "If-None-Match", `"stale"`); rec.Code != 200 {
		t.Errorf("stale ETag: %d", rec.Code)
	}
	if rec := f.do(t, http.MethodHead, f.manifestPath()); rec.Code != 200 || rec.Header().Get("ETag") != etag {
		t.Errorf("HEAD: %d", rec.Code)
	}
}

func TestArtifact(t *testing.T) {
	f := newFixture(t)
	rec := f.get(t, f.artifactPath())
	if rec.Code != 200 || rec.Body.String() != string(f.art) || rec.Header().Get("Cache-Control") != artifactCaching ||
		rec.Header().Get("ETag") != `"`+f.digest+`"` || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if rec := f.get(t, f.artifactPath(), "If-None-Match", `"`+f.digest+`"`); rec.Code != http.StatusNotModified {
		t.Errorf("conditional artifact: %d", rec.Code)
	}
	reads := f.store.reads.Load()
	f.get(t, f.artifactPath())
	if f.store.reads.Load() != reads {
		t.Error("a cached artifact was read from storage again")
	}
}

func TestNotFound(t *testing.T) {
	f := newFixture(t)
	other, _ := delivery.NewKey()
	otherArt := []byte(`{"project":"b"}`)
	must(t, f.store.Put(context.Background(), delivery.ArtifactPath(projectB, delivery.Digest(otherArt)), otherArt, "application/json"))
	must(t, f.store.Put(context.Background(), delivery.ManifestPath(projectB, "production"), []byte(`{}`), "application/json"))
	for name, path := range map[string]string{
		"malformed key":            "/v1/nope/production/manifest.json",
		"unknown key":              "/v1/" + other + "/production/manifest.json",
		"unknown environment":      "/v1/" + f.key + "/staging/manifest.json",
		"invalid environment":      "/v1/" + f.key + "/Prod/manifest.json",
		"other project's artifact": "/v1/" + f.key + "/a/" + delivery.Digest(otherArt) + ".json",
		"malformed digest":         "/v1/" + f.key + "/a/XYZ.json",
		"artifact without .json":   "/v1/" + f.key + "/a/" + f.digest,
		"not a manifest":           "/v1/" + f.key + "/production/other.json",
		"reserved environment a":   "/v1/" + f.key + "/a/manifest.json",
		"too short":                "/v1/" + f.key + "/manifest.json",
	} {
		rec := f.get(t, path)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" ||
			rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: %d %v", name, rec.Code, rec.Header())
		}
	}
	reads := f.store.reads.Load()
	f.get(t, "/v1/"+other+"/production/manifest.json")
	if f.store.reads.Load() != reads {
		t.Error("an unknown key was looked up again within its TTL")
	}
	if rec := f.do(t, http.MethodPost, f.manifestPath()); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestRevocationAndPointerMovesReachTheEdge(t *testing.T) {
	f := newFixture(t)
	f.get(t, f.manifestPath())
	must(t, f.store.Put(context.Background(), delivery.ManifestPath(projectA, "production"), []byte(`{"release":{"id":"r2"}}`), "application/json"))
	if body := f.get(t, f.manifestPath()).Body.String(); !strings.Contains(body, "r1") {
		t.Errorf("manifest refreshed before its TTL: %s", body)
	}
	f.clock.Advance(6 * time.Second)
	if body := f.get(t, f.manifestPath()).Body.String(); !strings.Contains(body, "r2") {
		t.Errorf("new manifest not served after its TTL: %s", body)
	}

	must(t, f.store.Delete(context.Background(), delivery.KeyIndexPath(f.key)))
	if rec := f.get(t, f.artifactPath()); rec.Code != 200 {
		t.Errorf("key dropped before its TTL: %d", rec.Code)
	}
	f.clock.Advance(31 * time.Second)
	for _, p := range []string{f.manifestPath(), f.artifactPath()} {
		if rec := f.get(t, p); rec.Code != http.StatusNotFound {
			t.Errorf("revoked key still serves %s: %d", p, rec.Code)
		}
	}
}

// When storage fails, the edge keeps serving what it has (the
// delivery plane degrades gracefully, intent §51) and answers 503 with
// no-store for what it doesn't.
func TestStorageOutage(t *testing.T) {
	f := newFixture(t)
	f.get(t, f.manifestPath())
	f.get(t, f.artifactPath())
	f.store.down.Store(true)
	f.clock.Advance(10 * time.Minute) // manifest and key entries are stale now
	if rec := f.get(t, f.manifestPath()); rec.Code != 200 || rec.Body.String() != string(f.mfst) {
		t.Errorf("stale manifest not served during the outage: %d", rec.Code)
	}
	if rec := f.get(t, f.artifactPath()); rec.Code != 200 {
		t.Errorf("cached artifact not served during the outage: %d", rec.Code)
	}
	rec := f.get(t, "/v1/"+f.key+"/staging/manifest.json")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("uncached object during the outage: %d %v", rec.Code, rec.Header())
	}
	other, _ := delivery.NewKey()
	if rec := f.get(t, "/v1/"+other+"/production/manifest.json"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unknown key during the outage: %d (must not claim it doesn't exist)", rec.Code)
	}
}

func TestIntegrityFailureIsNotServed(t *testing.T) {
	f := newFixture(t)
	must(t, f.store.Put(context.Background(), delivery.ArtifactPath(projectA, f.digest), []byte(`{"tampered":true}`), "application/json"))
	if rec := f.get(t, f.artifactPath()); rec.Code != http.StatusBadGateway {
		t.Errorf("tampered artifact: %d", rec.Code)
	}
}

func TestPreflight(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodOptions, f.manifestPath(), "Origin", "https://brotwerk.de",
		"Access-Control-Request-Method", "GET", "Access-Control-Request-Headers", "if-none-match")
	h := rec.Header()
	if rec.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "GET") ||
		!strings.Contains(strings.ToLower(h.Get("Access-Control-Allow-Headers")), "if-none-match") ||
		h.Get("Access-Control-Max-Age") == "" || h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("preflight %d %v", rec.Code, h)
	}
}

func TestLargeManifestIsRefused(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("x", delivery.MaxManifestBytes+1)
	must(t, f.store.Put(context.Background(), delivery.ManifestPath(projectA, "huge"), []byte(big), "application/json"))
	if rec := f.get(t, "/v1/"+f.key+"/huge/manifest.json"); rec.Code != http.StatusBadGateway {
		t.Errorf("oversized manifest: %d", rec.Code)
	}
}

func TestConcurrentMissesReadStorageOnce(t *testing.T) {
	f := newFixture(t)
	f.get(t, f.manifestPath()) // resolve the key
	reads := f.store.reads.Load()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := f.get(t, f.artifactPath())
			if rec.Code != 200 {
				t.Errorf("status %d", rec.Code)
			}
			_, _ = io.Copy(io.Discard, rec.Body)
		}()
	}
	wg.Wait()
	// Exactly one: a request that misses the cache just after a flight
	// ends must find the flight's result in the cache, not read again.
	if n := f.store.reads.Load() - reads; n != 1 {
		t.Errorf("%d storage reads for one artifact, want 1", n)
	}
}
