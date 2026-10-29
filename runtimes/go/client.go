package glossa

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults.
const (
	DefaultRefreshInterval     = 5 * time.Minute
	DefaultRefreshTimeout      = 30 * time.Second
	DefaultErrorRepeatInterval = time.Minute
	defaultHTTPTimeout         = 15 * time.Second
	refreshJitter              = 0.1
	bundledManifest            = "manifest.json"
)

// Config configures a Client. EdgeURL (with DeliveryKey and Environment)
// or Bundled is required; everything else has a sane default.
type Config struct {
	// EdgeURL is the glossa-edge base URL, e.g. "https://edge.glossa.dev".
	// Leave it empty for a client that only uses Bundled catalogs.
	EdgeURL string
	// DeliveryKey is the project's publishable delivery key.
	DeliveryKey string
	// Environment is the release environment, e.g. "production".
	Environment string

	// PublicKeys are the release signing keys to trust. With keys, a
	// manifest from the edge or the cache directory without a valid
	// signature from one of them is rejected (SPEC §1.3). Bundled catalogs
	// ship with the binary and are trusted like its code. Server-side
	// clients should configure keys.
	PublicKeys []PublicKey

	// CacheDir is where the last-good release is persisted. Empty means
	// os.UserCacheDir()/glossa.
	CacheDir string
	// DisableCache turns persistence off.
	DisableCache bool

	// Bundled holds catalogs shipped with the binary (for example an
	// embed.FS narrowed with fs.Sub), laid out like the edge:
	// manifest.json and a/<sha256>.json. It serves offline and cold
	// starts.
	Bundled fs.FS

	// Locales restricts which locales are loaded to the fallback chains
	// of these tags. Empty loads every locale of the release, which suits
	// servers rendering for many users.
	Locales []string

	// HTTPClient performs edge requests. The default has a 15s timeout.
	HTTPClient *http.Client
	// Retry configures retries of edge requests.
	Retry RetryPolicy
	// RefreshInterval is the background refresh period (default 5m, with
	// ±10% jitter). A negative value disables background refresh; call
	// Refresh yourself.
	RefreshInterval time.Duration
	// RefreshTimeout bounds one background refresh (default 30s).
	RefreshTimeout time.Duration

	// OnError receives load, verification and format errors (SPEC §6).
	// When nil, errors are logged to Logger as warnings. Identical errors
	// (all fields equal) are reported at most once per ErrorRepeatInterval
	// (default 1m). OnError may be called concurrently, from rendering
	// goroutines and from background refresh, and must not block.
	OnError             func(Error)
	ErrorRepeatInterval time.Duration
	// Logger is used for the default error channel and for cache
	// problems. Default: slog.Default().
	Logger *slog.Logger

	// DisableBidiIsolation turns off MF2 bidi isolation of placeholders
	// for every call (see BidiIsolation for one call).
	DisableBidiIsolation bool

	// DisableRollout turns staged rollout support off (SPEC §1.4): the
	// client ignores a manifest's `rollout`, serves the stable release,
	// never fetches a candidate artifact, and Explain reports no rollout.
	DisableRollout bool
	// InstallationID is the process's cohort key under a staged rollout:
	// any stable, non-empty text. Empty means a random 128-bit id, created
	// the first time a rollout is read and kept in the cache directory (or
	// for the life of the process when there is none).
	InstallationID string
	// PerRequestCohorts loads both sides of a staged rollout, so that each
	// request renders from the side its own cohort key (WithCohortKey)
	// selects, as a server serving many users should. Without it the
	// client loads only the installation's side, and every request
	// renders from that.
	PerRequestCohorts bool
}

// Client loads releases and renders messages. It is safe for concurrent
// use. Create one per project and environment and share it.
type Client struct {
	cfg      Config
	edge     *edge
	store    *store
	reporter *reporter
	state    atomic.Pointer[snapshot]

	idOnce sync.Once
	id     string // the installation id, once a rollout needed it

	refreshMu sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// snapshot is the client's view of the active release. It is replaced,
// never mutated, so a render sees one consistent release.
type snapshot struct {
	// rel is the active release: under a staged rollout, the view the
	// installation's cohort selects (in a per-request copy, the request's).
	rel    *release
	source Source
	// fresh marks a release restored at startup: the startup load cycle
	// spans New and the first refresh.
	fresh bool
	// ro holds the views of a valid rollout in the active manifest, nil
	// without one; cohort is the cohort that selected rel.
	ro     *rolloutViews
	cohort int
}

// artifacts are every parsed artifact s holds, by digest. Nil-safe.
func (s *snapshot) artifacts() map[string]catalog {
	if s == nil || s.rel == nil {
		return nil
	}
	if s.ro == nil {
		return s.rel.bySHA
	}
	out := map[string]catalog{}
	for _, v := range []*release{s.ro.stable, s.ro.candidate} {
		if v != nil {
			out = mergeCatalogs(out, v.bySHA)
		}
	}
	return out
}

// New creates a client. It restores the persisted last-good release (or
// the bundled one, if newer) synchronously, so messages render right
// away, and starts background refresh from the edge. New never fails
// because the edge is unreachable; it fails only on invalid Config.
func New(cfg Config) (*Client, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg}
	handler := cfg.OnError
	if handler == nil {
		handler = logHandler(cfg.Logger)
	}
	c.reporter = newReporter(handler, cfg.ErrorRepeatInterval)
	if cfg.EdgeURL != "" {
		if c.edge, err = newEdge(cfg.EdgeURL, cfg.DeliveryKey, cfg.Environment, cfg.HTTPClient, cfg.Retry); err != nil {
			return nil, err
		}
		c.store = openStore(cfg)
	}
	c.state.Store(&snapshot{})
	c.restore()
	c.startRefresh()
	return c, nil
}

func (cfg Config) normalize() (Config, error) {
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	cfg.Retry = cfg.Retry.withDefaults()
	cfg.RefreshInterval = orDefault(cfg.RefreshInterval, DefaultRefreshInterval)
	cfg.RefreshTimeout = orDefault(cfg.RefreshTimeout, DefaultRefreshTimeout)
	cfg.ErrorRepeatInterval = orDefault(cfg.ErrorRepeatInterval, DefaultErrorRepeatInterval)
	return cfg, nil
}

func (cfg Config) validate() error {
	if cfg.EdgeURL == "" && cfg.Bundled == nil {
		return errors.New("glossa: Config needs EdgeURL or Bundled")
	}
	if cfg.EdgeURL != "" && (cfg.DeliveryKey == "" || cfg.Environment == "") {
		return errors.New("glossa: Config with EdgeURL needs DeliveryKey and Environment")
	}
	for _, k := range cfg.PublicKeys {
		if k.KeyID == "" || len(k.Key) != 32 {
			return fmt.Errorf("glossa: public key %q is not an Ed25519 key with an ID", k.KeyID)
		}
	}
	return nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// openStore opens the per-project cache directory, or returns nil when
// persistence is off or no cache directory exists.
func openStore(cfg Config) *store {
	if cfg.DisableCache {
		return nil
	}
	base := cfg.CacheDir
	if base == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			cfg.Logger.Warn("glossa: no user cache directory; the last-good release won't persist", "error", err)
			return nil
		}
		base = filepath.Join(dir, "glossa")
	}
	return newStore(filepath.Join(base, cacheScope(cfg.EdgeURL, cfg.DeliveryKey, cfg.Environment)))
}

// restore activates the persisted last-good release, or the bundled one
// when it is newer (SPEC §3 steps 2 and 4).
func (c *Client) restore() {
	persisted := c.loadPersisted()
	bundled := c.loadBundled()
	switch {
	case bundled != nil && (persisted == nil || newer(bundled, persisted)):
		bundled.fresh = true
		c.state.Store(bundled)
	case persisted != nil:
		persisted.fresh = true
		c.state.Store(persisted)
	}
}

// newer compares the versions of the views each snapshot activates
// (SPEC §1.4).
func newer(a, b *snapshot) bool {
	return a.rel.manifest.Release.Version > b.rel.manifest.Release.Version
}

func (c *Client) loadPersisted() *snapshot {
	st, err := c.store.loadState()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		var snap *snapshot
		if snap, err = c.assemble(context.Background(), []byte(st.Manifest), st.ETag, SourcePersisted, nil); err == nil {
			return snap
		}
	}
	c.reportLoad(fmt.Errorf("persisted release unusable: %w", err))
	return nil
}

func (c *Client) loadBundled() *snapshot {
	if c.cfg.Bundled == nil {
		return nil
	}
	raw, err := fs.ReadFile(c.cfg.Bundled, bundledManifest)
	if err != nil {
		c.reportLoad(fmt.Errorf("%w: bundled catalogs: %v", errSchema, err))
		return nil
	}
	snap, err := c.assemble(context.Background(), raw, "", SourceBundled, nil)
	if err != nil {
		c.reportLoad(fmt.Errorf("bundled release unusable: %w", err))
		return nil
	}
	return snap
}

// Refresh revalidates the manifest with the edge and, when a new release
// is published, activates it once it has verified completely (SPEC §3).
// Until then the current release keeps serving. Errors are reported to the
// error channel and returned; rendering is never affected by them.
func (c *Client) Refresh(ctx context.Context) error {
	if c.edge == nil {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	activated, err := c.refresh(ctx)
	c.settle(activated)
	if err != nil && ctx.Err() == nil {
		c.reportLoad(err)
	}
	return err
}

func (c *Client) refresh(ctx context.Context) (bool, error) {
	cur := c.state.Load()
	etag := ""
	if cur.rel != nil {
		etag = cur.rel.etag
	}
	res, err := c.edge.manifest(ctx, etag)
	if err != nil {
		return false, err
	}
	if res.notModified {
		if cur.rel == nil {
			return false, fmt.Errorf("%w: edge answered 304 but no release is loaded", errNetwork)
		}
		return false, nil
	}
	snap, err := c.assemble(ctx, res.body, res.etag, SourceNetwork, cur)
	if err != nil {
		return false, err
	}
	c.state.Store(snap)
	c.persist(snap)
	return true, nil
}

// settle ends a load cycle that activated nothing: the active release is
// now served from memory, except right after startup.
func (c *Client) settle(activated bool) {
	snap := c.state.Load()
	if activated || snap.rel == nil {
		return
	}
	next := *snap
	if snap.fresh {
		next.fresh = false
	} else {
		next.source = SourceMemory
	}
	c.state.Store(&next)
}

// persist commits snap as the last-good release: the manifest as served,
// `rollout` included, so a restart computes the same side (SPEC §1.4).
// Its artifacts were cached as they verified; the manifest goes last.
func (c *Client) persist(snap *snapshot) {
	if c.store == nil {
		return
	}
	rel := snap.rel
	if err := c.store.saveState(persistedState{ETag: rel.etag, Manifest: string(rel.raw)}); err != nil {
		c.cfg.Logger.Warn("glossa: persisting the last-good release failed", "release", rel.manifest.Release.ID, "error", err)
		return
	}
	keep := map[string]bool{}
	for digest := range snap.artifacts() {
		keep[digest] = true
	}
	c.store.prune(keep)
}

func (c *Client) reportLoad(err error) {
	c.reporter.report(Error{Type: errorType(err), Detail: err.Error(), ReleaseID: releaseOf(err)})
}

func (c *Client) startRefresh() {
	if c.edge == nil || c.cfg.RefreshInterval < 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.wg.Add(1)
	go c.refreshLoop(ctx)
}

// refreshLoop refreshes right away and then every RefreshInterval ± 10%,
// until Close.
func (c *Client) refreshLoop(ctx context.Context) {
	defer c.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		c.refreshOnce(ctx)
		timer.Reset(jittered(c.cfg.RefreshInterval))
	}
}

func (c *Client) refreshOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RefreshTimeout)
	defer cancel()
	_ = c.Refresh(ctx)
}

func jittered(d time.Duration) time.Duration {
	return d + time.Duration((rand.Float64()*2-1)*refreshJitter*float64(d))
}

// Close stops background refresh and waits for it to finish. The client
// keeps rendering from memory afterwards. Close is idempotent.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		c.wg.Wait()
		if c.edge != nil {
			c.edge.close()
		}
	})
	return nil
}

// Release returns the active release, and false when nothing is loaded.
func (c *Client) Release() (ReleaseRef, bool) {
	rel := c.state.Load().rel
	if rel == nil {
		return ReleaseRef{}, false
	}
	return rel.manifest.Release, true
}

// Locales returns the active release's locales, or nil when nothing is
// loaded.
func (c *Client) Locales() []string {
	rel := c.state.Load().rel
	if rel == nil {
		return nil
	}
	return rel.manifest.localeCodes()
}
