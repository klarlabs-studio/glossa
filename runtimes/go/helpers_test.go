package glossa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"go.klarlabs.de/glossa/messageformat"
)

// mutateJSON decodes a JSON object, lets mutate change it and re-encodes it.
func mutateJSON(t *testing.T, raw []byte, mutate func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func manifestWithSignatures(t *testing.T, raw []byte, sigs []any) []byte {
	t.Helper()
	return mutateJSON(t, raw, func(m map[string]any) { m["signatures"] = sigs })
}

// testRelease is a release built from MF2 source, as the edge would serve it.
type testRelease struct {
	manifest  []byte
	artifacts map[string][]byte
}

// catalogs: locale → message ID → MF2 source. The first locale is the
// source locale; "ar" and "he" are right-to-left.
func buildRelease(t *testing.T, id string, version int, catalogs map[string]map[string]string, order ...string) testRelease {
	t.Helper()
	models := map[string]map[string]any{}
	for locale, sources := range catalogs {
		models[locale] = map[string]any{}
		for msgID, src := range sources {
			msg, err := messageformat.ParseMF2(src)
			if err != nil {
				t.Fatalf("%s/%s: %v", locale, msgID, err)
			}
			models[locale][msgID] = msg
		}
	}
	return buildReleaseModels(t, id, version, models, order...)
}

// buildReleaseModels is buildRelease over data-model messages (anything
// that marshals to one).
func buildReleaseModels(t *testing.T, id string, version int, catalogs map[string]map[string]any, order ...string) testRelease {
	t.Helper()
	if len(order) == 0 {
		order = slices.Sorted(maps.Keys(catalogs))
	}
	rel := testRelease{artifacts: map[string][]byte{}}
	var locales []any
	refs := map[string]any{}
	for _, locale := range order {
		body := artifactBytes(t, locale, catalogs[locale])
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		rel.artifacts[digest] = body
		refs[locale] = map[string]any{"default": map[string]any{"sha256": digest, "size": len(body)}}
		locales = append(locales, map[string]any{"code": locale, "direction": string(scriptDirection(locale))})
	}
	m := map[string]any{
		"schema": "glossa.manifest/v1", "project": "prj_test", "environment": fakeEnv,
		"release":      map[string]any{"id": id, "version": version, "createdAt": "2026-09-19T08:00:00Z"},
		"sourceLocale": order[0], "locales": locales, "fallback": map[string]any{}, "artifacts": refs,
	}
	var err error
	if rel.manifest, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	return rel
}

func artifactBytes(t *testing.T, locale string, messages map[string]any) []byte {
	t.Helper()
	if messages == nil {
		messages = map[string]any{}
	}
	b, err := json.Marshal(map[string]any{
		"schema": "glossa.artifact/v1", "locale": locale, "namespace": "default", "messages": messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (r testRelease) response(etag string) fakeResponse {
	return fakeResponse{status: http.StatusOK, etag: etag, body: r.manifest}
}

func (r testRelease) fs() fstest.MapFS {
	fsys := fstest.MapFS{"manifest.json": {Data: r.manifest}}
	for digest, body := range r.artifacts {
		fsys["a/"+digest+".json"] = &fstest.MapFile{Data: body}
	}
	return fsys
}

// errorLog collects reported errors safely across goroutines.
type errorLog struct {
	mu   sync.Mutex
	errs []Error
}

func (l *errorLog) handle(e Error) { l.mu.Lock(); l.errs = append(l.errs, e); l.mu.Unlock() }

func (l *errorLog) types() []ErrorType {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []ErrorType{}
	for _, e := range l.errs {
		out = append(out, e.Type)
	}
	return out
}

func (l *errorLog) all() []Error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Error(nil), l.errs...)
}

// edgeConfig is a client config against a fake edge.
func edgeConfig(f *fakeEdge, log *errorLog) Config {
	return Config{
		EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
		HTTPClient: &http.Client{Transport: f}, DisableCache: true, OnError: log.handle,
	}
}
