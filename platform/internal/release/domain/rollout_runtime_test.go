package domain_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	glossa "go.klarlabs.de/glossa/runtimes/go"

	"go.klarlabs.de/glossa/platform/internal/release/domain"
	"go.klarlabs.de/glossa/platform/internal/release/releasetest"
)

// cohorts is runtimes/testdata/rollout/cohorts.json: SPEC §1.4's
// cohorts as runtimes/testdata/gen/generate.py computes them — a fourth
// implementation, none of the runtimes and not this platform.
type cohorts struct {
	Salt    string `json:"salt"`
	Vectors []struct {
		Salt   string `json:"salt"`
		Key    string `json:"key"`
		Cohort int    `json:"cohort"`
	} `json:"vectors"`
	Installations [][2]json.RawMessage `json:"installations"`
}

func loadCohorts(t *testing.T) cohorts {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(releasetest.RepoRoot(), "runtimes", "testdata", "rollout", "cohorts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c cohorts
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Salt != fixtureSalt || len(c.Installations) != 10000 {
		t.Fatalf("cohorts.json: salt %q, %d installations", c.Salt, len(c.Installations))
	}
	return c
}

// fakeEdge serves one environment's manifest and the artifacts it names
// at SPEC §2's paths, as glossa-edge serves them from storage.
func fakeEdge(t *testing.T, key, environment string, manifest []byte, built ...domain.Built) string {
	t.Helper()
	artifacts := map[string][]byte{}
	for _, b := range built {
		for _, a := range b.Artifacts {
			artifacts[a.Ref.SHA256] = a.Body
		}
	}
	prefix := "/v1/" + key + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		switch {
		case ok && rest == environment+"/manifest.json":
			_, _ = w.Write(manifest)
		case ok && strings.HasPrefix(rest, "a/") && artifacts[strings.TrimSuffix(strings.TrimPrefix(rest, "a/"), ".json")] != nil:
			_, _ = w.Write(artifacts[strings.TrimSuffix(strings.TrimPrefix(rest, "a/"), ".json")])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestTheGoRuntimePicksTheSideTheGeneratorExpects feeds the manifest
// the writer signs to the Go runtime (runtimes/go, which implements
// SPEC §1.4 since M5 wave 2), verified against the signing key, and
// asks it, for installation ids from cohorts.json, which release each
// one renders from. The expected side comes from the generator's
// cohort, never from this platform or the runtime.
func TestTheGoRuntimePicksTheSideTheGeneratorExpects(t *testing.T) {
	f := newRolloutFixture(t)
	c := loadCohorts(t)
	s, pubs := signer(t, "k_2026a")
	ro := f.start(t, 10)
	manifest, err := f.stable.Manifest("production").WithRollout(ro, f.candidate).Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	key := "glossa_pk_" + strings.Repeat("A", 32)
	pub, err := glossa.ParsePublicKey("k_2026a", base64.RawURLEncoding.EncodeToString(pubs[0]))
	if err != nil {
		t.Fatal(err)
	}
	client, err := glossa.New(glossa.Config{
		EdgeURL: fakeEdge(t, key, "production", manifest, f.stableBuilt, f.candBuilt), DeliveryKey: key,
		Environment: "production", PublicKeys: []glossa.PublicKey{pub}, PerRequestCohorts: true,
		DisableCache: true, RefreshInterval: -1, DisableBidiIsolation: true, Retry: glossa.RetryPolicy{MaxAttempts: 1},
		Logger:  slog.New(slog.DiscardHandler),
		OnError: func(e glossa.Error) { t.Errorf("runtime error: %+v", e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	cases := map[string]int{}
	for _, v := range c.Vectors {
		if v.Salt == fixtureSalt { // the boundaries: 0, 99, 100, 999, 1000, 9999, and non-hex keys
			cases[v.Key] = v.Cohort
		}
	}
	for _, inst := range c.Installations[:40] {
		var id string
		var cohort int
		if json.Unmarshal(inst[0], &id) != nil || json.Unmarshal(inst[1], &cohort) != nil {
			t.Fatalf("installation %s", inst)
		}
		cases[id] = cohort
	}
	in, out := 0, 0
	for id, cohort := range cases {
		ctx := glossa.WithCohortKey(context.Background(), id)
		want, text := f.stable.ID.String(), "3 bezahlen"
		if cohort < 10*100 {
			want, text = f.candidate.ID.String(), "Jetzt 3 zahlen"
			in++
		} else {
			out++
		}
		ex := client.Explain(glossa.WithLocales(ctx, "de"), "checkout.pay")
		if ex.Release == nil || ex.Release.ID != want || ex.Rollout == nil || ex.Rollout.ID != ro.ID.String() ||
			ex.Rollout.Cohort != cohort || ex.Rollout.Percent != 10 {
			t.Errorf("key %s (cohort %d): explain %+v, rollout %+v; want release %s", id, cohort, ex.Release, ex.Rollout, want)
		}
		if got := client.Localizer(glossa.WithLocales(ctx, "de")).T("checkout.pay", glossa.Args{"amount": 3}); got != text {
			t.Errorf("key %s (cohort %d) renders %q, want %q", id, cohort, got, text)
		}
	}
	if in == 0 || out == 0 {
		t.Fatalf("%d keys in the candidate, %d out: the cases don't straddle the boundary", in, out)
	}
}
