// Package edge is glossa-edge's delivery handler (runtimes/SPEC.md §2):
// manifests and artifacts, read from object storage and nothing else.
// There is no database anywhere in its import graph, so the control
// plane can be down, mid-migration or overloaded while published
// translations keep loading (RFC 0002 §3, intent §34–35, §51).
//
//	GET /v1/{deliveryKey}/{environment}/manifest.json   short TTL, strong ETag, 304
//	GET /v1/{deliveryKey}/a/{sha256}.json               immutable
//
// A delivery key resolves to its project through the key index object
// the Release context writes on create and deletes on revoke. Unknown and
// revoked keys answer 404, never 401, and so does an environment outside
// the key's scope (its allowlist, and branch environments for a preview
// key; RFC 0004 §4.3). Responses carry CORS "*" and never set cookies:
// the artifacts are public by design.
//
// The handler caches key resolutions, manifests and artifacts in
// process (an LRU bounded in bytes). Keys and manifests are fresh for a
// configured TTL; artifacts are content-addressed and immutable. When
// storage fails, stale entries keep serving.
package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/singleflight"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

// Cache-Control values (runtimes/SPEC.md §2).
const (
	manifestCaching = "public, max-age=60, stale-while-revalidate=300, stale-if-error=86400"
	artifactCaching = "public, max-age=31536000, immutable"
	errorCaching    = "no-store"
	// staleLimit bounds how long a stale manifest or key keeps serving
	// through a storage outage (the manifest's stale-if-error).
	staleLimit = 24 * time.Hour
)

// Config tunes the handler.
type Config struct {
	// CacheBytes bounds cached manifests and artifacts.
	CacheBytes int64
	// KeyTTL: how long a key resolution is trusted (the revocation delay).
	KeyTTL time.Duration
	// ManifestTTL: how long a manifest is served before storage is asked
	// again (the delay of a pointer move at this edge).
	ManifestTTL time.Duration
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// Handler serves the delivery endpoints.
type Handler struct {
	store   objectstore.Reader
	cfg     Config
	logger  *slog.Logger
	keys    *lru[keyEntry]
	blobs   *lru[blob]
	flights singleflight.Group
	metrics metrics
}

type keyEntry struct {
	project string // "" when the key doesn't resolve
	// scope is what the key may read.
	scope delivery.Scope
	at    time.Time
}

type blob struct {
	body []byte
	etag string
	at   time.Time
}

type metrics struct {
	cache   *prometheus.CounterVec
	storage *prometheus.CounterVec
}

// New returns the handler; reg receives its metrics.
func New(store objectstore.Reader, cfg Config, logger *slog.Logger, reg prometheus.Registerer) *Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &Handler{
		store: store, cfg: cfg, logger: logger,
		// Key entries are tiny; give them their own budget so a flood of
		// unknown keys can't evict artifacts.
		keys:  newLRU(8<<20, keyCost),
		blobs: newLRU(cfg.CacheBytes, func(k string, b blob) int64 { return int64(len(k)+len(b.body)+len(b.etag)) + 64 }),
		metrics: metrics{
			cache: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "glossa_edge_cache_lookups_total",
				Help: "Edge cache lookups by kind (key, manifest, artifact) and result (hit, miss, stale).",
			}, []string{"kind", "result"}),
			storage: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "glossa_edge_storage_errors_total",
				Help: "Failed object storage reads by kind.",
			}, []string{"kind"}),
		},
	}
	reg.MustRegister(h.metrics.cache, h.metrics.storage)
	return h
}

// Routes registers the delivery endpoints. One pattern serves both
// shapes: /v1/{key}/a/{sha256}.json and /v1/{key}/{environment}/manifest.json
// would conflict as separate ServeMux patterns, and "a" is reserved as
// an environment name for exactly this reason.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/{key}/{segment}/{file}", h.serve)
	mux.HandleFunc("OPTIONS /v1/{key}/{segment}/{file}", h.preflight)
	mux.HandleFunc("/v1/", h.other)
}

// errNotFound is a definite "no such object" (unknown or revoked key,
// missing manifest or artifact).
var errNotFound = errors.New("edge: not found")

// errIntegrity is an object storage returned that can't be served.
var errIntegrity = errors.New("edge: stored object failed its integrity check")

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	cors(w.Header())
	key, segment, file := r.PathValue("key"), r.PathValue("segment"), r.PathValue("file")
	if !delivery.ValidKey(key) {
		h.fail(w, r, errNotFound)
		return
	}
	k, err := h.resolve(r.Context(), key)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	project := k.project
	// Artifacts aren't scoped by environment: they are content-addressed,
	// shared by a project's environments, and a key only learns a hash
	// from a manifest it may read.
	if segment == delivery.ArtifactSegment {
		digest, ok := strings.CutSuffix(file, ".json")
		if !ok || !delivery.ValidDigest(digest) {
			h.fail(w, r, errNotFound)
			return
		}
		b, err := h.artifact(r.Context(), project, digest)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		respond(w, r, b, artifactCaching)
		return
	}
	// Outside the key's scope is not found, exactly like an unknown key,
	// and decided before storage is read.
	if file != "manifest.json" || !delivery.ValidEnvironment(segment) || !k.scope.Allows(segment) {
		h.fail(w, r, errNotFound)
		return
	}
	b, err := h.manifest(r.Context(), project, segment)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	respond(w, r, b, manifestCaching)
}

// resolve maps a delivery key to its project and scope through the key
// index.
func (h *Handler) resolve(ctx context.Context, key string) (keyEntry, error) {
	now := h.cfg.Now()
	cached, fresh, ok := h.keys.get(key, now)
	if ok && fresh {
		h.metrics.cache.WithLabelValues("key", "hit").Inc()
		return found(cached)
	}
	// The flight outlives any one request: a caller hanging up must not
	// fail the others waiting on the same read.
	flightCtx := context.WithoutCancel(ctx)
	v, err, _ := h.flights.Do("key:"+key, func() (any, error) {
		body, err := h.store.Get(flightCtx, delivery.KeyIndexPath(key), delivery.MaxKeyIndexBytes)
		switch {
		case errors.Is(err, objectstore.ErrNotFound):
			return keyEntry{at: now}, nil
		case err != nil:
			return nil, err
		}
		idx, err := delivery.DecodeKeyIndex(body)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errIntegrity, err)
		}
		return keyEntry{project: idx.Project, scope: idx.Scope, at: now}, nil
	})
	if err != nil {
		h.metrics.storage.WithLabelValues("key").Inc()
		if ok && now.Sub(cached.at) < staleLimit {
			h.metrics.cache.WithLabelValues("key", "stale").Inc()
			h.logger.WarnContext(ctx, "edge: storage unavailable; serving a stale key resolution", slog.Any("error", err))
			return found(cached)
		}
		return keyEntry{}, err
	}
	h.metrics.cache.WithLabelValues("key", "miss").Inc()
	e := v.(keyEntry)
	h.keys.put(key, e, now.Add(h.cfg.KeyTTL))
	return found(e)
}

func found(e keyEntry) (keyEntry, error) {
	if e.project == "" {
		return keyEntry{}, errNotFound
	}
	return e, nil
}

// keyCost is a key entry's share of the key cache's byte budget.
func keyCost(key string, e keyEntry) int64 {
	n := int64(len(key)) + 96
	for _, env := range e.scope.Environments {
		n += int64(len(env)) + 16
	}
	return n
}

// manifest returns an environment's manifest, fresh for ManifestTTL.
func (h *Handler) manifest(ctx context.Context, project, environment string) (blob, error) {
	path := delivery.ManifestPath(project, environment)
	return h.load(ctx, "manifest", path, delivery.MaxManifestBytes, h.cfg.ManifestTTL, func(body []byte) (string, error) {
		return delivery.Digest(body), nil
	})
}

// artifact returns an artifact, verified against its digest.
func (h *Handler) artifact(ctx context.Context, project, digest string) (blob, error) {
	path := delivery.ArtifactPath(project, digest)
	return h.load(ctx, "artifact", path, delivery.MaxArtifactBytes, 0, func(body []byte) (string, error) {
		if delivery.Digest(body) != digest {
			return "", fmt.Errorf("%w: %s", errIntegrity, path)
		}
		return digest, nil
	})
}

// load reads path through the cache. ttl 0 means immutable. check
// validates the bytes and returns their ETag value.
func (h *Handler) load(ctx context.Context, kind, path string, limit int64, ttl time.Duration, check func([]byte) (string, error)) (blob, error) {
	now := h.cfg.Now()
	cached, fresh, ok := h.blobs.get(path, now)
	if ok && fresh {
		h.metrics.cache.WithLabelValues(kind, "hit").Inc()
		return cached, nil
	}
	flightCtx := context.WithoutCancel(ctx)
	var until time.Time
	if ttl > 0 {
		until = now.Add(ttl)
	}
	v, err, _ := h.flights.Do(path, func() (any, error) {
		// A request that missed the cache just as the previous flight for
		// this path finished starts a new flight; the object is in the
		// cache by then, so check again before going to storage.
		if again, fresh, ok := h.blobs.get(path, h.cfg.Now()); ok && fresh {
			return again, nil
		}
		body, err := h.store.Get(flightCtx, path, limit)
		switch {
		case errors.Is(err, objectstore.ErrNotFound):
			return nil, errNotFound
		case errors.Is(err, objectstore.ErrTooLarge):
			return nil, fmt.Errorf("%w: %s is larger than %d bytes", errIntegrity, path, limit)
		case err != nil:
			return nil, err
		}
		etag, err := check(body)
		if err != nil {
			return nil, err
		}
		b := blob{body: body, etag: `"` + etag + `"`, at: now}
		// Cache before the flight ends, so no request can miss both the
		// flight and the cache and read storage a second time.
		h.blobs.put(path, b, until)
		return b, nil
	})
	if err != nil {
		if !errors.Is(err, errNotFound) {
			h.metrics.storage.WithLabelValues(kind).Inc()
		}
		if ok && !errors.Is(err, errNotFound) && !errors.Is(err, errIntegrity) && now.Sub(cached.at) < staleLimit {
			h.metrics.cache.WithLabelValues(kind, "stale").Inc()
			h.logger.WarnContext(ctx, "edge: storage unavailable; serving a stale object",
				slog.String("object", path), slog.Any("error", err))
			return cached, nil
		}
		return blob{}, err
	}
	h.metrics.cache.WithLabelValues(kind, "miss").Inc()
	return v.(blob), nil
}

func respond(w http.ResponseWriter, r *http.Request, b blob, caching string) {
	h := w.Header()
	h.Set("ETag", b.etag)
	h.Set("Cache-Control", caching)
	if noneMatch(r.Header.Get("If-None-Match"), b.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(b.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(b.body)
	}
}

// noneMatch reports whether an If-None-Match header matches etag. The
// comparison is weak (RFC 9110 §13.1.2): W/ prefixes are ignored.
func noneMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		c := strings.TrimSpace(candidate)
		if c == "*" || strings.TrimPrefix(c, "W/") == etag {
			return true
		}
	}
	return false
}

func cors(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Expose-Headers", "ETag")
}

// preflight answers CORS preflights: runtimes send If-None-Match, which
// isn't a CORS-safelisted header. Credentials are never allowed.
func (h *Handler) preflight(w http.ResponseWriter, _ *http.Request) {
	hdr := w.Header()
	cors(hdr)
	hdr.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	hdr.Set("Access-Control-Allow-Headers", "If-None-Match, If-Modified-Since, Cache-Control")
	hdr.Set("Access-Control-Max-Age", "86400")
	hdr.Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusNoContent)
}

// other answers every other path under /v1/: 404 for reads, 405 for
// anything else (the edge is read-only).
func (h *Handler) other(w http.ResponseWriter, r *http.Request) {
	cors(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		w.Header().Set("Cache-Control", errorCaching)
		problem.Write(w, http.StatusMethodNotAllowed, "the edge is read-only")
		return
	}
	h.fail(w, r, errNotFound)
}

// fail writes an error. Not found is 404 whatever the reason, so a
// revoked key can't be told from one that never existed; storage
// failures are 503 and an object that fails its integrity check 502.
// None of them may be cached.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", errorCaching)
	switch {
	case errors.Is(err, errNotFound):
		problem.Write(w, http.StatusNotFound, "no such resource")
	case errors.Is(err, errIntegrity):
		h.logger.ErrorContext(r.Context(), "edge: stored object failed its integrity check", slog.Any("error", err))
		problem.Write(w, http.StatusBadGateway, "the stored object is invalid")
	default:
		h.logger.WarnContext(r.Context(), "edge: storage unavailable", slog.Any("error", err))
		w.Header().Set("Retry-After", "5")
		problem.Write(w, http.StatusServiceUnavailable, "storage is unavailable; retry")
	}
}
