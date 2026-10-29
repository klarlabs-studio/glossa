package edge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/releasetest"
)

// edgeFixture is runtimes/testdata/edge/*.json (runtimes/SPEC.md §2).
type edgeFixture struct {
	Description string `json:"description"`
	Project     string `json:"project"`
	Keys        map[string]struct {
		Key   string          `json:"key"`
		Index json.RawMessage `json:"index"`
	} `json:"keys"`
	Environments []string `json:"environments"`
	Cases        []struct {
		Key         string `json:"key"`
		Environment string `json:"environment"`
		Artifact    bool   `json:"artifact"`
		ExpStatus   int    `json:"expStatus"`
	} `json:"cases"`
}

// The edge passes the delivery contract's scope fixtures: every key,
// every environment, the answer SPEC §2 prescribes.
func TestConformance(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(releasetest.RepoRoot(), "runtimes", "testdata", "edge", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no edge fixtures: %v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file) //nolint:gosec // fixtures in the repository
			must(t, err)
			var fx edgeFixture
			must(t, json.Unmarshal(raw, &fx))
			runEdgeFixture(t, fx)
		})
	}
}

func runEdgeFixture(t *testing.T, fx edgeFixture) {
	f := newFixture(t)
	ctx := context.Background()
	for _, k := range fx.Keys {
		if string(k.Index) != "null" {
			releasetest.KeyIndex(t, k.Index)
			must(t, f.store.Put(ctx, delivery.KeyIndexPath(k.Key), k.Index, "application/json"))
		}
	}
	for _, env := range fx.Environments {
		must(t, f.store.Put(ctx, delivery.ManifestPath(fx.Project, env), []byte(`{"environment":"`+env+`"}`), "application/json"))
	}
	must(t, f.store.Put(ctx, delivery.ArtifactPath(fx.Project, f.digest), f.art, "application/json"))
	unknown, _ := delivery.NewKey()
	notFound := f.get(t, "/v1/"+unknown+"/production/manifest.json")
	for _, c := range fx.Cases {
		key := fx.Keys[c.Key].Key
		path := "/v1/" + key + "/" + c.Environment + "/manifest.json"
		if c.Artifact {
			path = "/v1/" + key + "/a/" + f.digest + ".json"
		}
		rec := f.get(t, path)
		if rec.Code != c.ExpStatus {
			t.Errorf("%s key, %s: %d, want %d", c.Key, path, rec.Code, c.ExpStatus)
			continue
		}
		if c.ExpStatus == http.StatusNotFound && rec.Body.String() != notFound.Body.String() {
			t.Errorf("%s key, %s: 404 differs from an unknown key's: %s", c.Key, path, rec.Body)
		}
	}
}

// What the control plane writes matches the index object's schema.
func TestEncodedKeyIndexMatchesItsSchema(t *testing.T) {
	for _, s := range []delivery.Scope{
		delivery.DefaultScope(),
		{Environments: delivery.DefaultEnvironments},
		{Environments: []string{"preview"}, Branches: true},
		{Branches: true},
	} {
		releasetest.KeyIndex(t, delivery.EncodeKeyIndex(projectA, "0192f5a0-7a4e-7cc3-9d1e-3a4b5c6d7e8f", s))
	}
}
