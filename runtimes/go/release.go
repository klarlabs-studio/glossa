package glossa

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"go.klarlabs.de/glossa/messageformat"
)

// An activated release and how one is assembled (runtimes/SPEC.md §3):
// verify the manifest, then load and verify every artifact the client
// needs, from the cheapest source that has it. Only a completely assembled
// release is ever activated.

// release is immutable once assembled.
type release struct {
	manifest *manifest
	raw      []byte
	etag     string
	catalogs map[string]catalog // locale → messages of all its namespaces
	bySHA    map[string]catalog // artifact digest → its messages, for reuse
}

// blobSource yields artifact bytes by reference. remote sources are the
// edge: their failures are real errors, while a local source that lacks
// or has corrupted an artifact just defers to the next source. trusted
// sources ship with the application, like its code, and aren't re-hashed.
type blobSource struct {
	remote  bool
	trusted bool
	get     func(ctx context.Context, ref artifactRef) ([]byte, error)
}

// loadError attaches the release a load failure belongs to.
type loadError struct {
	releaseID string
	err       error
}

func (e *loadError) Error() string { return e.err.Error() }
func (e *loadError) Unwrap() error { return e.err }

func releaseOf(err error) string {
	var le *loadError
	if errors.As(err, &le) {
		return le.releaseID
	}
	return ""
}

// assembly loads one release view's artifacts. lend holds artifacts
// already parsed, by digest.
type assembly struct {
	c       *Client
	rel     *release
	sources []blobSource
	lend    map[string]catalog
}

// assemble builds what manifest bytes loaded from origin (network,
// persisted or bundled) activate: one release, or under a staged rollout
// the view the installation's side selects (SPEC §1.4). prev, when not
// nil, lends its already parsed artifacts.
func (c *Client) assemble(ctx context.Context, raw []byte, etag string, origin Source, prev *snapshot) (*snapshot, error) {
	m, err := c.verifiedManifest(raw, origin)
	if err != nil {
		return nil, err
	}
	lend := prev.artifacts()
	if ro := c.rolloutOf(m); ro != nil {
		return c.assembleRollout(ctx, m, ro, raw, etag, origin, lend)
	}
	rel, err := c.assembleView(ctx, m, raw, etag, origin, lend)
	if err != nil {
		return nil, err
	}
	return &snapshot{rel: rel, source: origin}, nil
}

// assembleView loads every artifact view m needs and returns it as a
// release; raw and etag are the manifest's as served.
func (c *Client) assembleView(ctx context.Context, m *manifest, raw []byte, etag string, origin Source,
	lend map[string]catalog,
) (*release, error) {
	a := &assembly{c: c, sources: c.sourcesFor(origin), lend: lend, rel: &release{
		manifest: m, raw: raw, etag: etag, catalogs: map[string]catalog{}, bySHA: map[string]catalog{},
	}}
	for _, locale := range neededLocales(m, c.cfg.Locales) {
		if err := a.loadLocale(ctx, locale); err != nil {
			return nil, &loadError{releaseID: m.Release.ID, err: err}
		}
	}
	return a.rel, nil
}

// verifiedManifest parses and checks a manifest. Bundled catalogs ship
// with the application and are trusted like its code; signatures guard
// what comes from the network and the cache directory.
func (c *Client) verifiedManifest(raw []byte, origin Source) (*manifest, error) {
	m, err := parseManifest(raw)
	if err != nil {
		return nil, err
	}
	if c.cfg.Environment != "" && m.Environment != c.cfg.Environment {
		err := fmt.Errorf("%w: manifest is for environment %q, not %q", errSchema, m.Environment, c.cfg.Environment)
		return nil, &loadError{releaseID: m.Release.ID, err: err}
	}
	if origin == SourceBundled {
		return m, nil
	}
	if err := verifySignatures(raw, m, c.cfg.PublicKeys); err != nil {
		return nil, &loadError{releaseID: m.Release.ID, err: err}
	}
	return m, nil
}

// sourcesFor lists where a release loaded from origin finds its
// artifacts, cheapest first: the cache directory and the bundled
// catalogs, then the edge for network releases.
func (c *Client) sourcesFor(origin Source) []blobSource {
	sources := []blobSource{storeSource(c.store)}
	if c.cfg.Bundled != nil {
		sources = append(sources, fsSource(c.cfg.Bundled))
	}
	if origin == SourceNetwork {
		sources = append(sources, edgeSource(c.edge))
	}
	return sources
}

// neededLocales are the locales whose artifacts a client loads: every
// locale by default, since a backend renders for whoever it serves, or the
// fallback chains of the configured locales.
func neededLocales(m *manifest, configured []string) []string {
	if len(configured) == 0 {
		return m.localeCodes()
	}
	var out []string
	for _, tag := range canonicalizeAll(configured) {
		for _, l := range m.chain(m.negotiate([]string{tag})) {
			if m.hasLocale(l) && !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	return out
}

// loadLocale loads and merges every namespace of locale.
func (a *assembly) loadLocale(ctx context.Context, locale string) error {
	merged := catalog{}
	namespaces := a.rel.manifest.Artifacts[locale]
	for _, ns := range slices.Sorted(maps.Keys(namespaces)) {
		ref := namespaces[ns]
		cat, err := a.loadArtifact(ctx, ref, locale, ns)
		if err != nil {
			return err
		}
		a.rel.bySHA[ref.SHA256] = cat
		maps.Copy(merged, cat)
	}
	a.rel.catalogs[locale] = merged
	return nil
}

func (a *assembly) loadArtifact(ctx context.Context, ref artifactRef, locale, ns string) (catalog, error) {
	if cat, ok := a.lend[ref.SHA256]; ok {
		return cat, nil
	}
	body, fromRemote, err := fetchVerified(ctx, ref, a.sources)
	if err != nil {
		return nil, err
	}
	cat, bad, err := parseArtifact(body, locale, ns)
	if err != nil {
		return nil, err
	}
	for _, b := range bad {
		a.c.reporter.report(Error{Type: ErrorSchema, Detail: b.err.Error(), MessageID: b.id, Locale: locale, ReleaseID: a.rel.manifest.Release.ID})
	}
	if fromRemote {
		a.cache(ref.SHA256, body)
	}
	return cat, nil
}

// cache persists a verified artifact from the edge. It's best effort: the
// release activates either way.
func (a *assembly) cache(digest string, body []byte) {
	if err := a.c.store.saveArtifact(digest, body); err != nil {
		a.c.cfg.Logger.Warn("glossa: caching an artifact failed", "sha256", digest, "error", err)
	}
}

// fetchVerified returns the first bytes for ref that match its digest.
func fetchVerified(ctx context.Context, ref artifactRef, sources []blobSource) ([]byte, bool, error) {
	for _, src := range sources {
		body, err := src.get(ctx, ref)
		if err == nil && !src.trusted {
			err = verifyArtifact(body, ref)
		}
		switch {
		case err == nil:
			return body, src.remote, nil
		case src.remote:
			return nil, true, err
		}
	}
	return nil, false, fmt.Errorf("%w: artifact %s is not available", errNetwork, ref.SHA256)
}

// resolution is the outcome of resolving one message (SPEC §4.3).
type resolution struct {
	id           string
	requested    []string
	locale       string
	chain        []string
	steps        []Step
	resolvedFrom string // "" when no locale in the chain has the message
	message      messageformat.Message
}

// resolve walks the fallback chain for id. rel may be nil (nothing loaded).
func (rel *release) resolve(id string, requested []string) resolution {
	res := resolution{id: id, requested: requested}
	if rel == nil {
		if len(requested) > 0 {
			res.locale = requested[0]
		}
		return res
	}
	res.locale = rel.manifest.negotiate(requested)
	res.chain = rel.manifest.chain(res.locale)
	for _, l := range res.chain {
		cat, loaded := rel.catalogs[l]
		msg, ok := cat[id]
		switch {
		case !loaded && rel.manifest.hasLocale(l):
			res.steps = append(res.steps, Step{Locale: l, Outcome: OutcomeNotLoaded})
		case !ok:
			res.steps = append(res.steps, Step{Locale: l, Outcome: OutcomeMissing})
		default:
			res.steps = append(res.steps, Step{Locale: l, Outcome: OutcomeFound})
			res.resolvedFrom, res.message = l, msg
			return res
		}
	}
	return res
}

// fsSource reads artifacts from a bundled file system, laid out like the
// edge: a/<sha256>.json. Bundled artifacts are trusted like application
// code, so they aren't re-hashed.
func fsSource(fsys fs.FS) blobSource {
	return blobSource{trusted: true, get: func(_ context.Context, ref artifactRef) ([]byte, error) {
		return fs.ReadFile(fsys, "a/"+ref.SHA256+".json")
	}}
}

func storeSource(s *store) blobSource {
	return blobSource{get: func(_ context.Context, ref artifactRef) ([]byte, error) {
		return s.artifact(ref.SHA256)
	}}
}

func edgeSource(e *edge) blobSource {
	return blobSource{remote: true, get: e.artifact}
}
