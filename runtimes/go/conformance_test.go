package glossa

// Runtime conformance (runtimes/SPEC.md §7): every case of
// runtimes/testdata/scenarios/*.json and every sequence of
// runtimes/testdata/loading/*.json, through the public API, a fake edge
// transport and a temporary cache directory. There are no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"
)

const testdataDir = "../testdata"

type scenarioFile struct {
	Description string            `json:"description"`
	Manifest    json.RawMessage   `json:"manifest"`
	Artifacts   map[string]string `json:"artifacts"`
	Cases       []scenarioCase    `json:"cases"`
}

type scenarioCase struct {
	Requested       []string       `json:"requested"`
	ID              string         `json:"id"`
	Values          map[string]any `json:"values"`
	Default         *string        `json:"default"`
	BidiIsolation   string         `json:"bidiIsolation"`
	Exp             string         `json:"exp"`
	ExpLocale       string         `json:"expLocale"`
	ExpChain        []string       `json:"expChain"`
	ExpResolvedFrom *string        `json:"expResolvedFrom"`
	ExpDirection    Direction      `json:"expDirection"`
}

type loadingFile struct {
	Description   string        `json:"description"`
	PublicKeys    []fixtureKey  `json:"publicKeys"`
	Steps         []loadingStep `json:"steps"`
	RestartBefore []int         `json:"restartBefore"`
	// InstallationID and RolloutSupport configure staged rollout (SPEC
	// §1.4). An absent RolloutSupport means on.
	InstallationID string `json:"installationId"`
	RolloutSupport *bool  `json:"rolloutSupport"`
}

type fixtureKey struct {
	KeyID string `json:"keyId"`
	Key   string `json:"key"`
}

type loadingStep struct {
	Description string `json:"description"`
	Edge        struct {
		Manifest struct {
			Status int             `json:"status"`
			ETag   string          `json:"etag"`
			Body   json.RawMessage `json:"body"`
		} `json:"manifest"`
		Artifacts map[string]string `json:"artifacts"`
	} `json:"edge"`
	Read struct {
		ID        string   `json:"id"`
		Requested []string `json:"requested"`
	} `json:"read"`
	Exp              string   `json:"exp"`
	ExpActiveRelease *string  `json:"expActiveRelease"`
	ExpSource        Source   `json:"expSource"`
	ExpErrors        []string `json:"expErrors"`
	// ExpRollout is explain().rollout. Absent: not checked; null: none.
	ExpRollout json.RawMessage `json:"expRollout"`
}

func loadFixtures[T any](t *testing.T, kind string) map[string]T {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(testdataDir, kind, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no %s fixtures: %v", kind, err)
	}
	out := map[string]T{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out[filepath.Base(p)] = v
	}
	return out
}

func blobs(artifacts map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(artifacts))
	for digest, body := range artifacts {
		out[digest] = []byte(body)
	}
	return out
}

// scenarioLoaders load a scenario's release into a fresh client: from the
// edge, and from bundled catalogs.
var scenarioLoaders = map[string]func(t *testing.T, sc scenarioFile) *Client{
	"edge": func(t *testing.T, sc scenarioFile) *Client {
		f := newFakeEdge()
		f.serve(fakeResponse{status: http.StatusOK, etag: `"s"`, body: sc.Manifest}, blobs(sc.Artifacts))
		c := newTestClient(t, Config{
			EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
			HTTPClient: &http.Client{Transport: f}, DisableCache: true, OnError: allowMissing(t),
		})
		if err := c.Refresh(context.Background()); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		return c
	},
	"bundled": func(t *testing.T, sc scenarioFile) *Client {
		fsys := fstest.MapFS{"manifest.json": {Data: sc.Manifest}}
		for digest, body := range sc.Artifacts {
			fsys["a/"+digest+".json"] = &fstest.MapFile{Data: []byte(body)}
		}
		return newTestClient(t, Config{Bundled: fsys, OnError: allowMissing(t)})
	},
}

// allowMissing fails the test on any runtime error but missing-message,
// which the missing-message scenario expects.
func allowMissing(t *testing.T) func(Error) {
	return func(e Error) {
		if e.Type != ErrorMissingMessage {
			t.Errorf("unexpected runtime error: %v", e)
		}
	}
}

// newTestClient creates a client that reports errors as test failures
// unless cfg.OnError is set, with background refresh off.
func newTestClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	if cfg.OnError == nil {
		cfg.OnError = func(e Error) { t.Errorf("unexpected runtime error: %v", e) }
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = -1
	}
	cfg.Retry = fastRetry
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestConformanceScenarios(t *testing.T) {
	passed := 0
	for name, sc := range loadFixtures[scenarioFile](t, "scenarios") {
		for loader, load := range scenarioLoaders {
			t.Run(name+"/"+loader, func(t *testing.T) {
				c := load(t, sc)
				for i, tc := range sc.Cases {
					checkScenarioCase(t, c, i, tc)
				}
				if !t.Failed() {
					passed += len(sc.Cases)
				}
			})
		}
	}
	t.Logf("%d scenario case runs passed", passed)
}

func checkScenarioCase(t *testing.T, c *Client, i int, tc scenarioCase) {
	t.Helper()
	loc := c.For(tc.Requested...)
	opts := []Option{BidiIsolation(tc.BidiIsolation != "none")}
	if tc.Default != nil {
		opts = append(opts, Default(*tc.Default))
	}
	if got := loc.T(tc.ID, tc.Values, opts...); got != tc.Exp {
		t.Errorf("case %d (%v %s): T = %q, want %q", i, tc.Requested, tc.ID, got, tc.Exp)
	}
	e := loc.Explain(tc.ID)
	if e.Locale != tc.ExpLocale || !slices.Equal(e.Chain, tc.ExpChain) || !sameLocale(e.ResolvedFrom, tc.ExpResolvedFrom) {
		t.Errorf("case %d (%v %s): explain = locale %q chain %v resolvedFrom %v; want %q %v %v",
			i, tc.Requested, tc.ID, e.Locale, e.Chain, deref(e.ResolvedFrom), tc.ExpLocale, tc.ExpChain, deref(tc.ExpResolvedFrom))
	}
	if tc.ExpDirection != "" && loc.Direction() != tc.ExpDirection {
		t.Errorf("case %d: direction %q, want %q", i, loc.Direction(), tc.ExpDirection)
	}
}

func sameLocale(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestConformanceLoading(t *testing.T) {
	for name, lf := range loadFixtures[loadingFile](t, "loading") {
		t.Run(name, func(t *testing.T) { runLoadingSequence(t, lf) })
	}
}

func runLoadingSequence(t *testing.T, lf loadingFile) {
	var keys []PublicKey
	for _, k := range lf.PublicKeys {
		pk, err := ParsePublicKey(k.KeyID, k.Key)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, pk)
	}
	var errs []string
	edge := newFakeEdge()
	dir := t.TempDir()
	start := func() *Client {
		return newTestClient(t, Config{
			EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv, PublicKeys: keys,
			HTTPClient: &http.Client{Transport: edge}, CacheDir: dir,
			InstallationID: lf.InstallationID, DisableRollout: lf.RolloutSupport != nil && !*lf.RolloutSupport,
			OnError: func(e Error) { errs = append(errs, string(e.Type)) },
		})
	}
	c := start()
	for i, step := range lf.Steps {
		errs = nil
		if slices.Contains(lf.RestartBefore, i) {
			_ = c.Close()
			c = start()
		}
		m := step.Edge.Manifest
		edge.serve(fakeResponse{status: m.Status, etag: m.ETag, body: m.Body}, blobs(step.Edge.Artifacts))
		_ = c.Refresh(context.Background())
		checkLoadingStep(t, c, i, step, errs)
	}
}

func checkLoadingStep(t *testing.T, c *Client, i int, step loadingStep, errs []string) {
	t.Helper()
	loc := c.For(step.Read.Requested...)
	if got := loc.T(step.Read.ID, nil); got != step.Exp {
		t.Errorf("step %d (%s): T = %q, want %q", i, step.Description, got, step.Exp)
	}
	var active *string
	if ref, ok := c.Release(); ok {
		active = &ref.ID
	}
	if !sameLocale(active, step.ExpActiveRelease) {
		t.Errorf("step %d (%s): active release %v, want %v", i, step.Description, deref(active), deref(step.ExpActiveRelease))
	}
	if got := loc.Explain(step.Read.ID).Source; got != step.ExpSource {
		t.Errorf("step %d (%s): source %q, want %q", i, step.Description, got, step.ExpSource)
	}
	if !slices.Equal(nonNil(errs), step.ExpErrors) {
		t.Errorf("step %d (%s): errors %v, want %v", i, step.Description, errs, step.ExpErrors)
	}
	if len(step.ExpRollout) > 0 {
		got, err := json.Marshal(loc.Explain(step.Read.ID).Rollout)
		if err != nil {
			t.Fatal(err)
		}
		var have, want any
		if err := json.Unmarshal(got, &have); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(step.ExpRollout, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(have, want) {
			t.Errorf("step %d (%s): explain().rollout %s, want %s", i, step.Description, got, step.ExpRollout)
		}
	}
}
