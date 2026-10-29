package domain_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
	"go.klarlabs.de/glossa/platform/internal/release/releasetest"
)

func model(t *testing.T, locale, mf1 string) json.RawMessage {
	t.Helper()
	c, err := mfcontent.Parse(mfcontent.MF1, mf1, bcp47.MustParse(locale))
	if err != nil {
		t.Fatalf("parse %q: %v", mf1, err)
	}
	return c.ModelJSON()
}

var (
	idPay   = uuid.MustParse("0192f5a0-0000-7000-8000-000000000001")
	idItems = uuid.MustParse("0192f5a0-0000-7000-8000-000000000002")
	idTitle = uuid.MustParse("0192f5a0-0000-7000-8000-000000000003")
)

func shop(t *testing.T) domain.Snapshot {
	t.Helper()
	return domain.Snapshot{
		SourceLocale: "en",
		Locales:      []domain.Locale{{Code: "en", Direction: "ltr"}, {Code: "de", Direction: "ltr"}, {Code: "ar", Direction: "rtl"}},
		Fallback:     map[string][]string{"*": {"en"}},
		Messages: []domain.SourceMessage{
			{ID: idItems, Key: "cart.items", Namespace: "default", Model: model(t, "en", "{count, plural, one {# item} other {# items}}")},
			{ID: idPay, Key: "checkout.pay", Namespace: "default", Model: model(t, "en", "Pay {amount, number}")},
			{ID: idTitle, Key: "home.title", Namespace: "marketing", Model: model(t, "en", "Welcome <b>home</b> & more")},
		},
		Translations: map[string]map[uuid.UUID]domain.Translation{
			"de": {
				idPay:   {Model: model(t, "de", "{amount, number} bezahlen")},
				idTitle: {Model: model(t, "de", "Willkommen"), Outdated: true},
			},
			// A translation of a message the catalog no longer releases.
			"ar": {uuid.New(): {Model: model(t, "ar", "x")}},
		},
	}
}

func artifactsByPath(b domain.Built) map[string]string {
	out := map[string]string{}
	for _, a := range b.Artifacts {
		out[a.Locale+"/"+a.Namespace] = string(a.Body)
	}
	return out
}

func TestBuildShipsWhatEachLocaleHas(t *testing.T) {
	b, err := domain.Build(shop(t), domain.DefaultPolicy("preview"))
	if err != nil {
		t.Fatal(err)
	}
	got := artifactsByPath(b)
	want := []string{"ar/default", "de/default", "de/marketing", "en/default", "en/marketing"}
	if keys := slices.Sorted(func(yield func(string) bool) {
		for k := range got {
			if !yield(k) {
				return
			}
		}
	}); !slices.Equal(keys, want) {
		t.Fatalf("artifacts %v, want %v", keys, want)
	}
	if got["ar/default"] != `{"locale":"ar","messages":{},"namespace":"default","schema":"glossa.artifact/v1"}` {
		t.Errorf("empty locale artifact = %s", got["ar/default"])
	}
	if strings.Contains(got["de/default"], "cart.items") {
		t.Error("an untranslated message was padded into de")
	}
	for _, a := range b.Artifacts {
		releasetest.Artifact(t, a.Body)
		if a.Ref.SHA256 != delivery.Digest(a.Body) || a.Ref.Size != int64(len(a.Body)) {
			t.Errorf("%s/%s ref %+v doesn't describe its bytes", a.Locale, a.Namespace, a.Ref)
		}
		if b.Content.Artifacts[a.Locale][a.Namespace] != a.Ref {
			t.Errorf("content lacks %s/%s", a.Locale, a.Namespace)
		}
	}
	if codes := []string{b.Content.Locales[0].Code, b.Content.Locales[1].Code, b.Content.Locales[2].Code}; !slices.Equal(codes, []string{"en", "ar", "de"}) {
		t.Errorf("locale order %v: source first, then by code", codes)
	}
	st := b.Stats
	if st.Messages != 3 || st.Locales["en"].Messages != 3 || st.Locales["de"] != (domain.LocaleStats{Messages: 2, Outdated: 1}) ||
		st.Locales["ar"].Messages != 0 || st.Artifacts != 5 {
		t.Errorf("stats %+v", st)
	}
}

// RFC 0004 §4.2: every environment but a branch's excludes proposed
// messages and source proposals. That keeps release eligibility intact:
// no text that exists only on a branch reaches a release.
func TestBuildExcludesTheBranchOverlay(t *testing.T) {
	s := shop(t)
	idTip := uuid.MustParse("0192f5a0-0000-7000-8000-000000000004")
	s.Messages = append(s.Messages, domain.SourceMessage{
		ID: idTip, Key: "checkout.tip", Namespace: "default", Model: model(t, "en", "Add a tip"), Proposed: true,
	})
	s.Translations["de"][idTip] = domain.Translation{Model: model(t, "de", "Trinkgeld geben")}
	s.Messages[1].Proposal = model(t, "en", "Pay securely {amount, number}")
	b, err := domain.Build(s, domain.DefaultPolicy("preview"))
	if err != nil {
		t.Fatal(err)
	}
	got := artifactsByPath(b)
	for path, body := range got {
		if strings.Contains(body, "checkout.tip") || strings.Contains(body, "Trinkgeld") {
			t.Errorf("%s ships a proposed message: %s", path, body)
		}
		if strings.Contains(body, "securely") {
			t.Errorf("%s ships a source proposal: %s", path, body)
		}
	}
	if !strings.Contains(got["en/default"], "checkout.pay") || b.Stats.Messages != 3 || b.Stats.Locales["de"].Messages != 2 {
		t.Errorf("live messages: stats %+v, en %s", b.Stats, got["en/default"])
	}
	want, _ := domain.Build(shop(t), domain.DefaultPolicy("preview"))
	if b.Content.Artifacts["en"]["default"] != want.Content.Artifacts["en"]["default"] {
		t.Error("the overlay changed the live catalog's artifact")
	}
}

// RFC 0004 §4.2: a branch environment's build is the main catalog plus
// that branch's overlay: its proposed messages (translated like any
// other) and its source proposals, rendered in the source locale only —
// translations stay against the live source.
func TestBuildBranchAddsItsOverlay(t *testing.T) {
	s := shop(t)
	idTip := uuid.MustParse("0192f5a0-0000-7000-8000-000000000004")
	s.Translations["de"][idTip] = domain.Translation{Model: model(t, "de", "Trinkgeld geben")}
	branch := s.WithOverlay(domain.Overlay{
		Proposed: []domain.SourceMessage{{ID: idTip, Key: "checkout.tip", Namespace: "default", Model: model(t, "en", "Add a tip")}},
		Changes:  map[uuid.UUID]json.RawMessage{idPay: model(t, "en", "Pay securely {amount, number}")},
	})
	b, err := domain.BuildBranch(branch, domain.BranchPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got := artifactsByPath(b)
	if !strings.Contains(got["en/default"], "checkout.tip") || !strings.Contains(got["en/default"], "securely") {
		t.Errorf("en lacks the overlay: %s", got["en/default"])
	}
	if !strings.Contains(got["de/default"], "Trinkgeld") || !strings.Contains(got["de/default"], "bezahlen") {
		t.Errorf("de: %s", got["de/default"])
	}
	if b.Stats.Messages != 4 || b.Stats.Locales["de"].Messages != 3 {
		t.Errorf("stats %+v", b.Stats)
	}
	// The overlay is only ever the branch build's: the snapshot it came
	// from is unchanged, and the main build of the overlaid snapshot
	// still leaves it out.
	if len(s.Messages) != 3 || s.Messages[1].Proposal != nil {
		t.Errorf("WithOverlay changed its receiver: %+v", s.Messages)
	}
	main, err := domain.Build(branch, domain.BranchPolicy())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := domain.Build(shop(t), domain.BranchPolicy())
	if !slices.Equal(main.Artifacts[0].Body, want.Artifacts[0].Body) || main.Stats.Messages != 3 {
		t.Errorf("the main build of an overlaid snapshot ships the overlay: %s", main.Artifacts[0].Body)
	}
}

func TestBuildPolicyDropsOutdated(t *testing.T) {
	b, err := domain.Build(shop(t), domain.Policy{States: []string{"approved"}, IncludeOutdated: false})
	if err != nil {
		t.Fatal(err)
	}
	got := artifactsByPath(b)
	if _, ok := got["de/marketing"]; ok || strings.Contains(got["de/default"], "home.title") {
		t.Errorf("outdated translation shipped: %v", got)
	}
	if b.Stats.Locales["de"] != (domain.LocaleStats{Messages: 1}) {
		t.Errorf("de stats %+v", b.Stats.Locales["de"])
	}
}

// The artifact serialization, pinned: RFC 8785 canonical JSON of
// {schema, locale, namespace, messages}, messages as MF2 data model.
func TestArtifactBytesAreCanonical(t *testing.T) {
	s := domain.Snapshot{
		SourceLocale: "de", Locales: []domain.Locale{{Code: "de", Direction: "ltr"}},
		Messages: []domain.SourceMessage{{ID: idPay, Key: "a.b", Namespace: "default",
			Model: json.RawMessage(`{"type":"message", "pattern":["Hallo <&> é"],"declarations":[]}`)}},
	}
	b, err := domain.Build(s, domain.DefaultPolicy("preview"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"locale":"de","messages":{"a.b":{"declarations":[],"pattern":["Hallo <&> é"],"type":"message"}},"namespace":"default","schema":"glossa.artifact/v1"}`
	if got := string(b.Artifacts[0].Body); got != want {
		t.Fatalf("artifact bytes\n got %s\nwant %s", got, want)
	}
	if b.Artifacts[0].Ref.SHA256 != delivery.Digest([]byte(want)) {
		t.Error("digest is not over the exact bytes")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	first, err := domain.Build(shop(t), domain.DefaultPolicy("preview"))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := first.Content.Digest()
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 20 {
		s := shop(t)
		rng.Shuffle(len(s.Messages), func(i, j int) { s.Messages[i], s.Messages[j] = s.Messages[j], s.Messages[i] })
		rng.Shuffle(len(s.Locales), func(i, j int) { s.Locales[i], s.Locales[j] = s.Locales[j], s.Locales[i] })
		b, err := domain.Build(s, domain.DefaultPolicy("preview"))
		if err != nil {
			t.Fatal(err)
		}
		if d, _ := b.Content.Digest(); d != digest {
			t.Fatalf("run %d: content digest %s != %s", i, d, digest)
		}
		for j, a := range b.Artifacts {
			if string(a.Body) != string(first.Artifacts[j].Body) {
				t.Fatalf("run %d: artifact %s/%s differs", i, a.Locale, a.Namespace)
			}
		}
	}
}

func TestBuildRefusesWhatArtifactsCantCarry(t *testing.T) {
	cases := map[string]func(*domain.Snapshot){
		"64-character namespace": func(s *domain.Snapshot) { s.Messages[0].Namespace = strings.Repeat("n", 64) },
		"duplicate key":          func(s *domain.Snapshot) { s.Messages[1].Key = s.Messages[0].Key },
		"source locale unlisted": func(s *domain.Snapshot) { s.SourceLocale = "fr" },
		"model not an object":    func(s *domain.Snapshot) { s.Messages[0].Model = json.RawMessage(`"x"`) },
		"duplicate locale":       func(s *domain.Snapshot) { s.Locales = append(s.Locales, s.Locales[1]) },
	}
	for name, mutate := range cases {
		s := shop(t)
		mutate(&s)
		if _, err := domain.Build(s, domain.DefaultPolicy("preview")); !errors.Is(err, domain.ErrNotReleasable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Every problem is reported, not only the first: a dry run lists what
// to fix in one pass.
func TestBuildReportsEveryProblem(t *testing.T) {
	s := shop(t)
	s.Messages[0].Namespace = strings.Repeat("n", 64)
	s.Messages[2].Model = json.RawMessage(`"x"`)
	_, err := domain.Build(s, domain.DefaultPolicy("preview"))
	var nr *domain.NotReleasableError
	if !errors.As(err, &nr) || !errors.Is(err, domain.ErrNotReleasable) {
		t.Fatalf("err = %v", err)
	}
	var keys []string
	for _, p := range nr.Problems {
		keys = append(keys, p.Key)
		if p.Detail == "" {
			t.Errorf("problem without detail: %+v", p)
		}
	}
	if !slices.Equal(keys, []string{"cart.items", "home.title"}) {
		t.Errorf("problems %+v", nr.Problems)
	}
	if !strings.Contains(err.Error(), "cart.items") || !strings.Contains(err.Error(), "home.title") {
		t.Errorf("message %q doesn't name every problem", err)
	}
}

func signer(t *testing.T, ids ...string) (*domain.Signer, []ed25519.PublicKey) {
	t.Helper()
	var keys []domain.SigningKey
	var pubs []ed25519.PublicKey
	for i, id := range ids {
		seed := make([]byte, ed25519.SeedSize)
		seed[0] = byte(i + 1)
		k, err := domain.ParseSigningKey(id, base64.StdEncoding.EncodeToString(seed))
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
		pubs = append(pubs, k.Key.Public().(ed25519.PublicKey))
	}
	s, err := domain.NewSigner(keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, pubs
}

func TestManifestIsSignedCanonicalJSON(t *testing.T) {
	b, err := domain.Build(shop(t), domain.DefaultPolicy("production"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 8, 0, 0, 123456789, time.UTC)
	project := uuid.New()
	production, err := domain.NewEnvironment(project, "production", domain.DefaultPolicy("production"), now)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := domain.NewRelease(uuid.New(), project, 42, uuid.Nil, production, b, "", "person:x", now)
	if err != nil || rel.Environment != "production" || !rel.Policy.Equal(production.Policy) || rel.Branch != "" {
		t.Fatalf("%+v, %v", rel, err)
	}
	s, pubs := signer(t, "k_2026a", "k_2026b")
	body, err := rel.Manifest("staging").Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	releasetest.Manifest(t, body)
	if canonical, _ := jcs.Canonicalize(body); string(canonical) != string(body) {
		t.Error("served manifest bytes are not canonical")
	}
	var m domain.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Environment != "staging" || m.Release.Version != 42 || m.Release.CreatedAt != "2026-09-19T08:00:00Z" ||
		m.Project != rel.ProjectID.String() || m.SourceLocale != "en" || len(m.Signatures) != 2 {
		t.Fatalf("manifest %s", body)
	}
	signed, err := jcs.Without(body, "signatures")
	if err != nil {
		t.Fatal(err)
	}
	for i, sig := range m.Signatures {
		raw, _ := base64.RawURLEncoding.DecodeString(sig.Sig)
		if sig.Alg != "Ed25519" || !ed25519.Verify(pubs[i], signed, raw) {
			t.Errorf("signature %s doesn't verify", sig.KeyID)
		}
	}
	again, _ := rel.Manifest("staging").Encode(s)
	if string(again) != string(body) {
		t.Error("encoding the same manifest twice gave different bytes")
	}
	if rel.Digest != mustDigest(t, b.Content) || len(rel.Digest) != 64 {
		t.Errorf("release digest %s", rel.Digest)
	}
}

func mustDigest(t *testing.T, c domain.Content) string {
	t.Helper()
	d, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSigningKeys(t *testing.T) {
	seed := make([]byte, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	for _, enc := range []string{
		base64.StdEncoding.EncodeToString(seed), base64.RawURLEncoding.EncodeToString(seed),
		base64.StdEncoding.EncodeToString(priv),
	} {
		k, err := domain.ParseSigningKey("k1", enc)
		if err != nil || !k.Key.Equal(priv) {
			t.Errorf("ParseSigningKey(%s) = %v", enc, err)
		}
	}
	for id, enc := range map[string]string{"k1": "c2hvcnQ", "bad id!": base64.StdEncoding.EncodeToString(seed), "k2": "!!!"} {
		if _, err := domain.ParseSigningKey(id, enc); !errors.Is(err, domain.ErrInvalidSigningKey) {
			t.Errorf("ParseSigningKey(%q, %q) = %v", id, enc, err)
		}
	}
	pub, err := domain.ParsePublicKey("old", base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
	if err != nil {
		t.Fatal(err)
	}
	active, _ := domain.ParseSigningKey("new", base64.StdEncoding.EncodeToString(append([]byte{1}, seed[1:]...)))
	s, err := domain.NewSigner([]domain.SigningKey{active}, []domain.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	keys := s.PublicKeys()
	if len(keys) != 2 || keys[0].ID != "new" || !keys[0].Active || keys[1].ID != "old" || keys[1].Active {
		t.Errorf("published keys %+v", keys)
	}
	if _, err := domain.NewSigner(nil, nil); err == nil {
		t.Error("a signer without keys")
	}
	if _, err := domain.NewSigner([]domain.SigningKey{active}, []domain.PublicKey{{ID: "new", Key: pub.Key}}); err == nil {
		t.Error("duplicate key IDs accepted")
	}
}

func TestDiff(t *testing.T) {
	base := map[string]domain.LocaleMessages{
		"en": {"a": json.RawMessage(`{"x":1}`), "b": json.RawMessage(`{"x":2}`), "c": json.RawMessage(`{"x":3}`)},
		"fr": {"a": json.RawMessage(`{"x":1}`)},
	}
	head := map[string]domain.LocaleMessages{
		"en": {"a": json.RawMessage(`{"x":1}`), "b": json.RawMessage(`{"x":9}`), "d": json.RawMessage(`{"x":4}`)},
		"de": {"a": json.RawMessage(`{"x":1}`)},
	}
	got := domain.Diff([]string{"en", "fr"}, []string{"en", "de"}, base, head)
	want := []domain.LocaleDiff{
		{Locale: "en", Added: []string{"d"}, Changed: []string{"b"}, Removed: []string{"c"}},
		{Locale: "de", Added: []string{"a"}, Changed: []string{}, Removed: []string{}},
		{Locale: "fr", Added: []string{}, Changed: []string{}, Removed: []string{"a"}},
	}
	if len(got) != len(want) {
		t.Fatalf("diff %+v", got)
	}
	for i := range want {
		if got[i].Locale != want[i].Locale || !slices.Equal(got[i].Added, want[i].Added) ||
			!slices.Equal(got[i].Changed, want[i].Changed) || !slices.Equal(got[i].Removed, want[i].Removed) {
			t.Errorf("locale %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestArtifactMessages(t *testing.T) {
	b, err := domain.Build(shop(t), domain.DefaultPolicy("preview"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range b.Artifacts {
		if a.Locale == "en" && a.Namespace == "default" {
			msgs, err := domain.ArtifactMessages(a.Body)
			if err != nil || len(msgs) != 2 || msgs["checkout.pay"] == nil {
				t.Fatalf("messages %v, %v", msgs, err)
			}
		}
	}
	if _, err := domain.ArtifactMessages([]byte(`{"schema":"glossa.artifact/v2"}`)); err == nil {
		t.Error("unknown artifact schema accepted")
	}
}
