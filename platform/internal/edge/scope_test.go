package edge_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

// putKey writes index as the index object of a new key.
func (f *fixture) putKey(t *testing.T, index []byte) string {
	t.Helper()
	key, _ := delivery.NewKey()
	must(t, f.store.Put(context.Background(), delivery.KeyIndexPath(key), index, "application/json"))
	return key
}

func (f *fixture) scopedKey(t *testing.T, environments []string, branches bool) string {
	t.Helper()
	scope, err := delivery.NewScope(environments, branches)
	must(t, err)
	return f.putKey(t, delivery.EncodeKeyIndex(projectA, "k2", scope))
}

// RFC 0004 §4.3: a key reads the environments on its allowlist and, as a
// preview key, branch environments. Anything else answers exactly what
// an unknown key gets, so a scope can't be probed. Artifacts aren't
// scoped: they are content-addressed, shared between a project's
// environments, and only learned from a manifest the key can read.
func TestKeyScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, env := range []string{"development", "preview", "staging", "qa", "pr-7", "br-0a1b2c3d"} {
		must(t, f.store.Put(ctx, delivery.ManifestPath(projectA, env), []byte(`{"environment":"`+env+`"}`), "application/json"))
	}
	production := f.scopedKey(t, []string{"production"}, false)
	preview := f.scopedKey(t, []string{"preview"}, true)
	names := map[string]string{production: "production key", preview: "preview key"}
	unknown, _ := delivery.NewKey()
	notFound := f.get(t, "/v1/"+unknown+"/production/manifest.json")

	for _, c := range []struct {
		key, env string
		want     int
	}{
		{production, "production", 200},
		{production, "staging", 404},
		{production, "preview", 404},
		{production, "pr-7", 404},
		{production, "br-0a1b2c3d", 404},
		{preview, "preview", 200},
		{preview, "pr-7", 200},
		{preview, "br-0a1b2c3d", 200},
		{preview, "production", 404},
		{preview, "qa", 404},
	} {
		rec := f.get(t, "/v1/"+c.key+"/"+c.env+"/manifest.json")
		if rec.Code != c.want {
			t.Errorf("%s, %s: %d, want %d", names[c.key], c.env, rec.Code, c.want)
			continue
		}
		if c.want == 404 && (rec.Body.String() != notFound.Body.String() ||
			rec.Header().Get("Cache-Control") != notFound.Header().Get("Cache-Control") ||
			rec.Header().Get("Content-Type") != notFound.Header().Get("Content-Type")) {
			t.Errorf("%s out of scope differs from an unknown key: %v %s", c.env, rec.Header(), rec.Body)
		}
	}
	for _, key := range []string{production, preview} {
		if rec := f.get(t, "/v1/"+key+"/a/"+f.digest+".json"); rec.Code != 200 {
			t.Errorf("%s, artifact: %d", names[key], rec.Code)
		}
	}
}

// Index objects written before scopes existed read as the default
// environments without branches, what their keys migrated to.
func TestLegacyKeyIndex(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	legacy := f.putKey(t, []byte(`{"schema":"glossa.delivery-key/v1","project":"`+projectA+`","key_id":"old"}`))
	for _, env := range []string{"development", "preview", "staging", "qa", "pr-7"} {
		must(t, f.store.Put(ctx, delivery.ManifestPath(projectA, env), []byte(`{}`), "application/json"))
	}
	for env, want := range map[string]int{
		"development": 200, "preview": 200, "staging": 200, "production": 200, "qa": 404, "pr-7": 404,
	} {
		if rec := f.get(t, "/v1/"+legacy+"/"+env+"/manifest.json"); rec.Code != want {
			t.Errorf("%s: %d, want %d", env, rec.Code, want)
		}
	}
}

// A narrowed scope reaches the edge once the key's cache entry expires,
// like a revocation.
func TestScopeChangesReachTheEdge(t *testing.T) {
	f := newFixture(t)
	must(t, f.store.Put(context.Background(), delivery.ManifestPath(projectA, "staging"), []byte(`{}`), "application/json"))
	if rec := f.get(t, "/v1/"+f.key+"/staging/manifest.json"); rec.Code != 200 {
		t.Fatalf("staging: %d", rec.Code)
	}
	narrowed, _ := delivery.NewScope([]string{"production"}, false)
	must(t, f.store.Put(context.Background(), delivery.KeyIndexPath(f.key), delivery.EncodeKeyIndex(projectA, "k1", narrowed), "application/json"))
	if rec := f.get(t, "/v1/"+f.key+"/staging/manifest.json"); rec.Code != 200 {
		t.Errorf("scope narrowed before the key's TTL: %d", rec.Code)
	}
	f.clock.Advance(31 * time.Second)
	if rec := f.get(t, "/v1/"+f.key+"/staging/manifest.json"); rec.Code != http.StatusNotFound {
		t.Errorf("narrowed scope: %d", rec.Code)
	}
	if rec := f.get(t, f.manifestPath()); rec.Code != 200 {
		t.Errorf("production: %d", rec.Code)
	}
}
