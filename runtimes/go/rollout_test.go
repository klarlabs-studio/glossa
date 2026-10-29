package glossa

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// cohortTable is runtimes/testdata/rollout/cohorts.json, which
// runtimes/testdata/gen/generate.py computes from SPEC §1.4's formula
// with code shared with no runtime.
type cohortTable struct {
	Salt          string         `json:"salt"`
	ExpCandidates map[string]int `json:"expCandidates"`
	Vectors       []struct {
		Salt   string `json:"salt"`
		Key    string `json:"key"`
		Cohort int    `json:"cohort"`
		Note   string `json:"note"`
	} `json:"vectors"`
	Installations [][2]json.RawMessage `json:"installations"`
}

func loadCohortTable(t *testing.T) cohortTable {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdataDir, "rollout", "cohorts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table cohortTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	return table
}

// TestCohortsAgreeWithTheGenerator checks the cohort function against
// every installation id, every vector and every candidate count of the
// shared table.
func TestCohortsAgreeWithTheGenerator(t *testing.T) {
	table := loadCohortTable(t)
	if len(table.Installations) != 10000 {
		t.Fatalf("the table has %d installations, want 10000", len(table.Installations))
	}
	for _, v := range table.Vectors {
		if got := cohortOf(v.Salt, v.Key); got != v.Cohort {
			t.Errorf("cohort(%q, %q) = %d, want %d (%s)", v.Salt, v.Key, got, v.Cohort, v.Note)
		}
	}
	cohorts := make([]int, 0, len(table.Installations))
	for _, row := range table.Installations {
		var id string
		var want int
		if err := json.Unmarshal(row[0], &id); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(row[1], &want); err != nil {
			t.Fatal(err)
		}
		got := cohortOf(table.Salt, id)
		if got != want {
			t.Errorf("installation %s: cohort %d, want %d", id, got, want)
		}
		cohorts = append(cohorts, got)
	}
	for p, want := range table.ExpCandidates {
		percent, err := strconv.Atoi(p)
		if err != nil {
			t.Fatal(err)
		}
		r := rollout{percent: percent}
		in := 0
		for _, c := range cohorts {
			if r.in(c) {
				in++
			}
		}
		if in != want {
			t.Errorf("at %d %%: %d installations in the candidate, want %d", percent, in, want)
		}
	}
}

// rolloutFixture is the first step of a shared rollout loading sequence:
// a manifest carrying a rollout of rel_2 and every artifact both sides
// need.
func rolloutFixture(t *testing.T, name string) (fakeResponse, map[string][]byte, cohortTable) {
	t.Helper()
	lf := loadFixtures[loadingFile](t, "loading")[name+".json"]
	step := lf.Steps[0]
	m := step.Edge.Manifest
	return fakeResponse{status: m.Status, etag: m.ETag, body: m.Body}, blobs(step.Edge.Artifacts), loadCohortTable(t)
}

// keysOnBothSides returns an installation id of the table in the
// candidate at percent and one outside it.
func keysOnBothSides(table cohortTable, percent int) (in, out string) {
	for _, row := range table.Installations {
		var id string
		var cohort int
		_ = json.Unmarshal(row[0], &id)
		_ = json.Unmarshal(row[1], &cohort)
		if cohort < percent*100 && in == "" {
			in = id
		}
		if cohort >= percent*100 && out == "" {
			out = id
		}
	}
	return in, out
}

func TestPerRequestCohortKeys(t *testing.T) {
	manifest, artifacts, table := rolloutFixture(t, "rollout-candidate-side")
	in, out := keysOnBothSides(table, 10)
	edge := newFakeEdge()
	edge.serve(manifest, artifacts)
	c := newTestClient(t, Config{
		EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
		HTTPClient: &http.Client{Transport: edge}, DisableCache: true,
		InstallationID: out, PerRequestCohorts: true,
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := WithLocales(context.Background(), "en")

	if got := c.T(ctx, "hello", nil); got != "Hello v1" {
		t.Errorf("without a key (the installation, outside): %q, want the stable release", got)
	}
	if got := c.T(WithCohortKey(ctx, in), "hello", nil); got != "Hello v2" {
		t.Errorf("a key inside 10 %%: %q, want the candidate", got)
	}
	if got := c.T(WithCohortKey(ctx, out), "hello", nil); got != "Hello v1" {
		t.Errorf("a key outside 10 %%: %q, want the stable release", got)
	}
	if got := c.For("en").WithCohortKey(in).T("hello", nil); got != "Hello v2" {
		t.Errorf("Localizer.WithCohortKey inside: %q, want the candidate", got)
	}

	e := c.Explain(WithCohortKey(ctx, in), "hello")
	want := RolloutInfo{ID: "ro_fixture", Percent: 10, Cohort: cohortOf(table.Salt, in), Side: SideCandidate}
	if e.Rollout == nil || *e.Rollout != want || e.Release == nil || e.Release.ID != "rel_2" {
		t.Errorf("explain for a candidate key: rollout %+v release %+v, want %+v on rel_2", e.Rollout, e.Release, want)
	}
	if ref, _ := c.Release(); ref.ID != "rel_1" {
		t.Errorf("Release() = %s, want the installation's side, rel_1", ref.ID)
	}
}

func TestCohortKeyWithoutPerRequestCohortsKeepsTheInstallationsSide(t *testing.T) {
	manifest, artifacts, table := rolloutFixture(t, "rollout-candidate-side")
	in, out := keysOnBothSides(table, 10)
	edge := newFakeEdge()
	edge.serve(manifest, artifacts)
	c := newTestClient(t, Config{
		EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
		HTTPClient: &http.Client{Transport: edge}, DisableCache: true, InstallationID: out,
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range edge.recorded() {
		if r.path != "/v1/"+fakeKey+"/"+fakeEnv+"/manifest.json" && !stableArtifact(t, manifest, r.path) {
			t.Errorf("the stable side fetched %s, which isn't the stable release's", r.path)
		}
	}
	ctx := WithCohortKey(WithLocales(context.Background(), "en"), in)
	if got := c.T(ctx, "hello", nil); got != "Hello v1" {
		t.Errorf("the candidate isn't loaded, so a candidate key renders the stable side: got %q", got)
	}
	if e := c.Explain(ctx, "hello"); e.Rollout == nil || e.Rollout.Side != SideStable {
		t.Errorf("explain().rollout = %+v, want the stable side", e.Rollout)
	}
}

func stableArtifact(t *testing.T, manifest fakeResponse, path string) bool {
	t.Helper()
	m, err := parseManifest(manifest.body)
	if err != nil {
		t.Fatal(err)
	}
	for _, namespaces := range m.Artifacts {
		for _, ref := range namespaces {
			if path == "/v1/"+fakeKey+"/a/"+ref.SHA256+".json" {
				return true
			}
		}
	}
	return false
}

func TestDisableRolloutIgnoresTheRollout(t *testing.T) {
	manifest, artifacts, table := rolloutFixture(t, "rollout-candidate-side")
	in, _ := keysOnBothSides(table, 10)
	edge := newFakeEdge()
	edge.serve(manifest, artifacts)
	c := newTestClient(t, Config{
		EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
		HTTPClient: &http.Client{Transport: edge}, DisableCache: true,
		InstallationID: in, PerRequestCohorts: true, DisableRollout: true,
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := WithCohortKey(WithLocales(context.Background(), "en"), in)
	if got := c.T(ctx, "hello", nil); got != "Hello v1" {
		t.Errorf("rollout support off: %q, want the stable release", got)
	}
	if e := c.Explain(ctx, "hello"); e.Rollout != nil {
		t.Errorf("rollout support off: explain().rollout = %+v, want nil", e.Rollout)
	}
}

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestInstallationIDIsCreatedOnceAndPersisted(t *testing.T) {
	manifest, artifacts, table := rolloutFixture(t, "rollout-candidate-side")
	dir := t.TempDir()
	edge := newFakeEdge()
	edge.serve(manifest, artifacts)
	start := func() *Client {
		return newTestClient(t, Config{
			EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
			HTTPClient: &http.Client{Transport: edge}, CacheDir: dir,
			// Either side is fine here; a failed candidate fetch isn't.
			OnError: func(e Error) { t.Errorf("unexpected runtime error: %v", e) },
		})
	}
	c := start()
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := c.installationID()
	if !hex32.MatchString(id) {
		t.Fatalf("installation id %q is not 32 lowercase hex digits", id)
	}
	e := c.For("en").Explain("hello")
	if e.Rollout == nil || e.Rollout.Cohort != cohortOf(table.Salt, id) {
		t.Errorf("explain().rollout = %+v, want the cohort of %s", e.Rollout, id)
	}
	_ = c.Close()

	again := start() // restores the persisted manifest, which carries the rollout
	if got := again.installationID(); got != id {
		t.Errorf("after a restart the installation id is %q, want %q", got, id)
	}
	if e2 := again.For("en").Explain("hello"); e2.Rollout == nil || *e2.Rollout != *e.Rollout {
		t.Errorf("after a restart explain().rollout = %+v, want %+v", e2.Rollout, e.Rollout)
	}
}

func TestNoInstallationIDWithoutARollout(t *testing.T) {
	lf := loadFixtures[loadingFile](t, "loading")["last-good.json"]
	m := lf.Steps[0].Edge.Manifest
	dir := t.TempDir()
	edge := newFakeEdge()
	edge.serve(fakeResponse{status: m.Status, etag: m.ETag, body: m.Body}, blobs(lf.Steps[0].Edge.Artifacts))
	c := newTestClient(t, Config{
		EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv,
		HTTPClient: &http.Client{Transport: edge}, CacheDir: dir,
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*", installationIDFile))
	if len(matches) != 0 || c.id != "" {
		t.Errorf("an installation id was created without a rollout: %v %q", matches, c.id)
	}
}

func TestParseRolloutRejectsInvalidMembers(t *testing.T) {
	const candidate = `"candidate":{"release":{"id":"rel_2","version":2},"locales":[],"fallback":{},"artifacts":{}}`
	const salt = `"salt":"c3RhZ2VkLXJvbGxvdXQtMQ"`
	for name, ro := range map[string]string{
		"fractional percent":           `{"id":"ro","percent":10.5,` + salt + `,` + candidate + `}`,
		"percent with a fraction part": `{"id":"ro","percent":10.0,` + salt + `,` + candidate + `}`,
		"percent as exponent":          `{"id":"ro","percent":1e1,` + salt + `,` + candidate + `}`,
		"percent above 100":            `{"id":"ro","percent":101,` + salt + `,` + candidate + `}`,
		"negative percent":             `{"id":"ro","percent":-1,` + salt + `,` + candidate + `}`,
		"percent as a string":          `{"id":"ro","percent":"10",` + salt + `,` + candidate + `}`,
		"short salt":                   `{"id":"ro","percent":10,"salt":"abc",` + candidate + `}`,
		"no id":                        `{"percent":10,` + salt + `,` + candidate + `}`,
		"candidate without artifacts": `{"id":"ro","percent":10,` + salt +
			`,"candidate":{"release":{"id":"rel_2","version":2},"locales":[],"fallback":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseRollout(&manifest{Rollout: json.RawMessage(ro)})
			if err == nil || got != nil {
				t.Errorf("parseRollout = %+v, %v; want a schema error", got, err)
			}
		})
	}
	got, err := parseRollout(&manifest{Rollout: json.RawMessage(`{"id":"ro","percent":100,` + salt + `,` + candidate + `}`)})
	if err != nil || got == nil || got.percent != 100 || got.candidate.Release.ID != "rel_2" {
		t.Errorf("a valid rollout: %+v, %v", got, err)
	}
}
