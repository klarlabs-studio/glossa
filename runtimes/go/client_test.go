package glossa

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var twoLocales = map[string]map[string]string{
	"en": {
		"hello":      "Hello {$name}!",
		"cart.items": ".input {$n :number} .match $n one {{one item}} * {{{$n} items}}",
		"empty":      "",
		"broken":     "Total: {$amount :number}",
	},
	"de": {"hello": "Hallo {$name}!"},
}

func TestNewValidatesConfig(t *testing.T) {
	for name, cfg := range map[string]Config{
		"nothing to load":   {},
		"edge without key":  {EdgeURL: fakeEdgeURL, Environment: fakeEnv},
		"edge without env":  {EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey},
		"bad edge URL":      {EdgeURL: "ftp://x", DeliveryKey: fakeKey, Environment: fakeEnv},
		"key without an ID": {EdgeURL: fakeEdgeURL, DeliveryKey: fakeKey, Environment: fakeEnv, PublicKeys: []PublicKey{{Key: make(ed25519.PublicKey, 32)}}},
	} {
		if c, err := New(cfg); err == nil {
			_ = c.Close()
			t.Errorf("%s: New succeeded", name)
		}
	}
}

func TestBundledOnlyClient(t *testing.T) {
	rel := buildRelease(t, "rel_b", 3, twoLocales, "en", "de")
	c := newTestClient(t, Config{Bundled: rel.fs()})
	if got := c.For("de-AT").T("hello", Args{"name": "Ada"}, BidiIsolation(false)); got != "Hallo Ada!" {
		t.Fatalf("T = %q", got)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh without an edge must be a no-op: %v", err)
	}
	if src := c.For("de").Explain("hello").Source; src != SourceBundled {
		t.Fatalf("source = %q, want bundled", src)
	}
	if got := c.Locales(); !slices.Equal(got, []string{"en", "de"}) {
		t.Fatalf("Locales = %v", got)
	}
}

func TestBundledArtifactsAreTrusted(t *testing.T) {
	rel := buildRelease(t, "rel_b", 1, map[string]map[string]string{"en": {"v": "one"}})
	fsys := rel.fs()
	for name, f := range fsys {
		if strings.HasPrefix(name, "a/") {
			f.Data = []byte(strings.Replace(string(f.Data), "one", "uno", 1))
		}
	}
	c := newTestClient(t, Config{Bundled: fsys})
	if got := c.For("en").T("v", nil); got != "uno" {
		t.Fatalf("T = %q; bundled artifacts ship like code and aren't re-hashed", got)
	}
}

func TestBundledManifestIsTrustedWithoutSignature(t *testing.T) {
	pub, _ := testKey(3)
	rel := buildRelease(t, "rel_b", 1, map[string]map[string]string{"en": {"v": "one"}})
	c := newTestClient(t, Config{Bundled: rel.fs(), PublicKeys: []PublicKey{{KeyID: "k", Key: pub}}})
	if got := c.For("en").T("v", nil); got != "one" {
		t.Fatalf("T = %q; bundled catalogs ship like code, signatures guard the network", got)
	}
}

func TestUnreadableMessageDegradesToFallback(t *testing.T) {
	broken := json.RawMessage(`{"type":"message","declarations":[],"pattern":[{"type":"nonsense"}]}`)
	rel := buildReleaseModels(t, "rel_1", 1, map[string]map[string]any{
		"en": {"a": broken, "b": json.RawMessage(`{"type":"message","declarations":[],"pattern":["B"]}`)},
	})
	log := &errorLog{}
	c := newTestClient(t, Config{Bundled: rel.fs(), OnError: log.handle})
	if got := c.For("en").T("b", nil); got != "B" {
		t.Fatalf("T(b) = %q; one bad message must not block the release", got)
	}
	errs := log.all()
	if len(errs) != 1 || errs[0].Type != ErrorSchema || errs[0].MessageID != "a" || errs[0].ReleaseID != "rel_1" {
		t.Fatalf("errors = %+v", errs)
	}
}

func TestUnlistedFallbackTargetsResolveAsMissing(t *testing.T) {
	rel := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}, "de": {}}, "en", "de")
	rel.manifest = mutateJSON(t, rel.manifest, func(m map[string]any) {
		m["fallback"] = map[string]any{"de": []any{"de-CH"}}
	})
	c := newTestClient(t, Config{Bundled: rel.fs()})
	e := c.For("de").Explain("v")
	want := []Step{{"de", OutcomeMissing}, {"de-CH", OutcomeMissing}, {"en", OutcomeFound}}
	if !slices.Equal(e.Steps, want) {
		t.Fatalf("steps = %+v, want %+v", e.Steps, want)
	}
}

func TestRenderingNeverFailsOrReturnsEmpty(t *testing.T) {
	log := &errorLog{}
	rel := buildRelease(t, "rel_1", 1, twoLocales, "en", "de")
	c := newTestClient(t, Config{Bundled: rel.fs(), OnError: log.handle})
	en := c.For("en")
	cases := []struct {
		id   string
		args Args
		opts []Option
		want string
	}{
		{"cart.items", Args{"n": 1}, nil, "one item"},
		{"cart.items", Args{"n": 5}, []Option{BidiIsolation(false)}, "5 items"},
		{"hello", nil, []Option{BidiIsolation(false)}, "Hello {$name}!"},
		{"broken", Args{"amount": "lots"}, []Option{BidiIsolation(false)}, "Total: {$amount}"},
		{"empty", nil, nil, "empty"},
		{"empty", nil, []Option{Default("Nothing here")}, "Nothing here"},
		{"nope", nil, nil, "nope"},
		{"nope", nil, []Option{Default("Fallback")}, "Fallback"},
	}
	for _, tc := range cases {
		if got := en.T(tc.id, tc.args, tc.opts...); got != tc.want {
			t.Errorf("T(%q, %v) = %q, want %q", tc.id, tc.args, got, tc.want)
		}
	}
	want := []ErrorType{ErrorFormat, ErrorFormat, ErrorMissingMessage}
	if got := log.types(); !slices.Equal(got, want) {
		t.Fatalf("errors = %v, want %v (repeats rate-limited)", got, want)
	}
	if e := log.all()[2]; e.MessageID != "nope" || e.Locale != "en" || e.ReleaseID != "rel_1" {
		t.Fatalf("missing-message error = %+v", e)
	}
}

func TestBidiIsolationDefault(t *testing.T) {
	rel := buildRelease(t, "rel_1", 1, twoLocales, "en", "de")
	on := newTestClient(t, Config{Bundled: rel.fs()})
	if got := on.For("en").T("hello", Args{"name": "Ada"}); got != "Hello ⁨Ada⁩!" {
		t.Fatalf("default isolation: %q", got)
	}
	off := newTestClient(t, Config{Bundled: rel.fs(), DisableBidiIsolation: true})
	if got := off.For("en").T("hello", Args{"name": "Ada"}); got != "Hello Ada!" {
		t.Fatalf("isolation disabled: %q", got)
	}
}

func TestTUsesLocalesFromContext(t *testing.T) {
	rel := buildRelease(t, "rel_1", 1, twoLocales, "en", "de")
	c := newTestClient(t, Config{Bundled: rel.fs(), DisableBidiIsolation: true})
	ctx := WithLocales(context.Background(), "de_CH", "en")
	if got := c.T(ctx, "hello", Args{"name": "Ada"}); got != "Hallo Ada!" {
		t.Fatalf("T = %q", got)
	}
	if e := c.Explain(ctx, "hello"); e.Locale != "de" || !slices.Equal(e.Requested, []string{"de-CH", "en"}) {
		t.Fatalf("Explain = %+v", e)
	}
	if got := c.T(context.Background(), "hello", Args{"name": "Ada"}); got != "Hello Ada!" {
		t.Fatalf("no locale on ctx must use the source locale: %q", got)
	}
}

func TestDirection(t *testing.T) {
	rel := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {}, "ar": {}}, "en", "ar")
	c := newTestClient(t, Config{Bundled: rel.fs()})
	if d := c.For("ar-EG").Direction(); d != RTL {
		t.Fatalf("ar-EG: %q", d)
	}
	if d := c.For("en").Direction(); d != LTR {
		t.Fatalf("en: %q", d)
	}
}

func TestNothingLoaded(t *testing.T) {
	f := newFakeEdge() // down
	log := &errorLog{}
	c := newTestClient(t, edgeConfig(f, log))
	_ = c.Refresh(context.Background())
	l := c.For("he")
	if got := l.T("hello", nil); got != "hello" {
		t.Fatalf("T = %q", got)
	}
	if l.Locale() != "he" || l.Direction() != RTL {
		t.Fatalf("locale %q direction %q", l.Locale(), l.Direction())
	}
	e := l.Explain("hello")
	if e.Release != nil || e.Source != SourceInline || e.ResolvedFrom != nil || e.Chain == nil || e.Steps == nil {
		t.Fatalf("Explain = %+v", e)
	}
	if _, ok := c.Release(); ok || c.Locales() != nil {
		t.Fatal("nothing should be active")
	}
	if got := log.types(); !slices.Equal(got, []ErrorType{ErrorNetwork}) {
		t.Fatalf("errors = %v; nothing loaded is not a missing message", got)
	}
}

func TestExplainHasNoSideEffects(t *testing.T) {
	log := &errorLog{}
	rel := buildRelease(t, "rel_1", 1, twoLocales, "en", "de")
	c := newTestClient(t, Config{Bundled: rel.fs(), OnError: log.handle})
	e := c.For("de").Explain("nope")
	if len(log.all()) != 0 {
		t.Fatalf("Explain reported %v", log.all())
	}
	if e.Release == nil || e.Release.ID != "rel_1" || e.Release.Version != 1 {
		t.Fatalf("release = %+v", e.Release)
	}
	if !slices.Equal(e.Steps, []Step{{"de", OutcomeMissing}, {"en", OutcomeMissing}}) {
		t.Fatalf("steps = %+v", e.Steps)
	}
}

func TestNewerBundledReleaseBeatsPersisted(t *testing.T) {
	dir := t.TempDir()
	f := newFakeEdge()
	old := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}})
	f.serve(old.response(`"1"`), old.artifacts)
	cfg := edgeConfig(f, &errorLog{})
	cfg.DisableCache, cfg.CacheDir = false, dir
	first := newTestClient(t, cfg)
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	cfg.Bundled = buildRelease(t, "rel_2", 2, map[string]map[string]string{"en": {"v": "two"}}).fs()
	c := newTestClient(t, cfg)
	if got := c.For("en").T("v", nil); got != "two" {
		t.Fatalf("T = %q, want the newer bundled release", got)
	}
	cfg.Bundled = buildRelease(t, "rel_0", 1, map[string]map[string]string{"en": {"v": "zero"}}).fs()
	c = newTestClient(t, cfg)
	if got := c.For("en").T("v", nil); got != "one" {
		t.Fatalf("T = %q, want the persisted release when it isn't older", got)
	}
}

func TestCorruptPersistedArtifactFallsThrough(t *testing.T) {
	dir := t.TempDir()
	f := newFakeEdge()
	rel := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}})
	f.serve(rel.response(`"1"`), rel.artifacts)
	log := &errorLog{}
	cfg := edgeConfig(f, log)
	cfg.DisableCache, cfg.CacheDir = false, dir
	first := newTestClient(t, cfg)
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	corruptCachedArtifacts(t, dir)

	c := newTestClient(t, cfg)
	if _, ok := c.Release(); ok {
		t.Fatal("a persisted release with corrupt artifacts must not activate")
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.For("en").T("v", nil); got != "one" {
		t.Fatalf("T = %q after refetching", got)
	}
}

func corruptCachedArtifacts(t *testing.T, dir string) {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "*", "a", "*.json"))
	if len(paths) == 0 {
		t.Fatal("nothing was cached")
	}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte(`{"tampered":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPersistedManifestIsVerifiedAgain(t *testing.T) {
	pub, priv := testKey(7)
	dir := t.TempDir()
	rel := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}})
	rel.manifest = signManifest(t, rel.manifest, "k", priv)
	f := newFakeEdge()
	f.serve(rel.response(`"1"`), rel.artifacts)
	cfg := edgeConfig(f, &errorLog{})
	cfg.DisableCache, cfg.CacheDir, cfg.PublicKeys = false, dir, []PublicKey{{KeyID: "k", Key: pub}}
	first := newTestClient(t, cfg)
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	statePath, _ := filepath.Glob(filepath.Join(dir, "*", stateFile))
	b, _ := os.ReadFile(statePath[0])
	if err := os.WriteFile(statePath[0], []byte(strings.Replace(string(b), "prj_test", "prj_evil", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &errorLog{}
	cfg.OnError = log.handle
	c := newTestClient(t, cfg)
	if _, ok := c.Release(); ok {
		t.Fatal("a tampered persisted manifest must not activate")
	}
	if got := log.types(); !slices.Equal(got, []ErrorType{ErrorSignature}) {
		t.Fatalf("errors = %v", got)
	}
}

func TestEnvironmentMismatchIsRejected(t *testing.T) {
	f := newFakeEdge()
	rel := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}})
	rel.manifest = mutateJSON(t, rel.manifest, func(m map[string]any) { m["environment"] = "staging" })
	f.serve(rel.response(`"1"`), rel.artifacts)
	log := &errorLog{}
	c := newTestClient(t, edgeConfig(f, log))
	_ = c.Refresh(context.Background())
	if _, ok := c.Release(); ok || !slices.Equal(log.types(), []ErrorType{ErrorSchema}) {
		t.Fatalf("release active: %v; errors %v", ok, log.types())
	}
}

func TestLocalesRestrictsWhatLoads(t *testing.T) {
	f := newFakeEdge()
	rel := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}, "de": {"v": "eins"}, "fr": {"v": "un"}}, "en", "de", "fr")
	f.serve(rel.response(`"1"`), rel.artifacts)
	cfg := edgeConfig(f, &errorLog{})
	cfg.Locales = []string{"de-DE"}
	c := newTestClient(t, cfg)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.recorded()); n != 3 {
		t.Fatalf("%d requests, want manifest + de + en", n)
	}
	if got := c.For("fr").T("v", nil); got != "one" {
		t.Fatalf("T = %q", got)
	}
	if steps := c.For("fr").Explain("v").Steps; steps[0].Outcome != OutcomeNotLoaded {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestRefreshReusesUnchangedArtifacts(t *testing.T) {
	f := newFakeEdge()
	r1 := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}, "de": {"v": "eins"}}, "en", "de")
	r2 := buildRelease(t, "rel_2", 2, map[string]map[string]string{"en": {"v": "two"}, "de": {"v": "eins"}}, "en", "de")
	f.serve(r1.response(`"1"`), r1.artifacts)
	c := newTestClient(t, edgeConfig(f, &errorLog{}))
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.serve(r2.response(`"2"`), r2.artifacts)
	before := len(f.recorded())
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.recorded()) - before; n != 2 {
		t.Fatalf("%d requests, want manifest + the changed en artifact", n)
	}
	if got := c.For("en").T("v", nil); got != "two" {
		t.Fatalf("T = %q", got)
	}
}

func TestBackgroundRefresh(t *testing.T) {
	f := newFakeEdge()
	r1 := buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}})
	f.serve(r1.response(`"1"`), r1.artifacts)
	cfg := edgeConfig(f, &errorLog{})
	cfg.RefreshInterval = 5 * time.Millisecond
	c := newTestClient(t, cfg)
	waitFor(t, func() bool { return c.For("en").T("v", nil) == "one" })

	r2 := buildRelease(t, "rel_2", 2, map[string]map[string]string{"en": {"v": "two"}})
	f.serve(r2.response(`"2"`), r2.artifacts)
	waitFor(t, func() bool { return c.For("en").T("v", nil) == "two" })

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	after := len(f.recorded())
	time.Sleep(30 * time.Millisecond)
	if len(f.recorded()) != after {
		t.Fatal("refresh continued after Close")
	}
	if got := c.For("en").T("v", nil); got != "two" {
		t.Fatalf("a closed client must keep rendering: %q", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConcurrentRenderingDuringRefresh(t *testing.T) {
	f := newFakeEdge()
	c := newTestClient(t, edgeConfig(f, &errorLog{}))
	releases := []testRelease{
		buildRelease(t, "rel_1", 1, map[string]map[string]string{"en": {"v": "one"}, "de": {"v": "eins"}}, "en", "de"),
		buildRelease(t, "rel_2", 2, map[string]map[string]string{"en": {"v": "two"}, "de": {"v": "zwei"}}, "en", "de"),
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				got := c.For("de").T("v", nil)
				if got != "v" && got != "eins" && got != "zwei" {
					t.Errorf("torn render: %q", got)
					return
				}
			}
		})
	}
	for i := range 20 {
		r := releases[i%2]
		f.serve(r.response(string(rune('a'+i))), r.artifacts)
		_ = c.Refresh(context.Background())
	}
	close(stop)
	wg.Wait()
}

func TestJitterStaysInBounds(t *testing.T) {
	for range 1000 {
		if d := jittered(time.Minute); d < 54*time.Second || d > 66*time.Second {
			t.Fatalf("jittered = %v", d)
		}
	}
}
