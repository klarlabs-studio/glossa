package domain_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
	"go.klarlabs.de/glossa/platform/internal/release/releasetest"
)

// fixtureSalt is runtimes/testdata/rollout/cohorts.json's salt.
const fixtureSalt = "c3RhZ2VkLXJvbGxvdXQtMQ"

var t0 = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

// rolloutFixture is production serving stable, with candidate — a later
// release of the same project, built under production's policy — ready
// to roll out.
type rolloutFixture struct {
	env                    domain.Environment
	stable, candidate      domain.Release
	stableBuilt, candBuilt domain.Built
}

func newRolloutFixture(t *testing.T) rolloutFixture {
	t.Helper()
	project := uuid.New()
	env, err := domain.NewEnvironment(project, "production", domain.DefaultPolicy("production"), t0)
	if err != nil {
		t.Fatal(err)
	}
	stableBuilt, err := domain.Build(shop(t), env.Policy)
	if err != nil {
		t.Fatal(err)
	}
	next := shop(t)
	next.Translations["de"][idPay] = domain.Translation{Model: model(t, "de", "Jetzt {amount, number} zahlen")}
	candBuilt, err := domain.Build(next, env.Policy)
	if err != nil {
		t.Fatal(err)
	}
	stable, err := domain.NewRelease(uuid.New(), project, 1, uuid.Nil, env, stableBuilt, "", "person:x", t0)
	if err != nil {
		t.Fatal(err)
	}
	env.Point(stable.ID, t0)
	candidate, err := domain.NewRelease(uuid.New(), project, 2, stable.ID, env, candBuilt, "", "person:x", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return rolloutFixture{env: env, stable: stable, candidate: candidate, stableBuilt: stableBuilt, candBuilt: candBuilt}
}

func (f rolloutFixture) start(t *testing.T, percent int) domain.Rollout {
	t.Helper()
	ro, err := domain.StartRollout(uuid.New(), f.env, f.stable, f.candidate,
		domain.RolloutStart{Percent: percent, Salt: fixtureSalt}, "person:a", t0)
	if err != nil {
		t.Fatal(err)
	}
	return ro
}

func TestSaltsAre22RandomBase64URLCharacters(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		s := domain.NewSalt()
		if len(s) != domain.SaltLen || !domain.ValidSalt(s) {
			t.Fatalf("salt %q", s)
		}
		if seen[s] {
			t.Fatalf("salt %q twice", s)
		}
		seen[s] = true
	}
	for _, bad := range []string{"", "short", fixtureSalt + "A", "c3RhZ2VkLXJvbGxvdXQtM=", "c3RhZ2VkLXJvbGxvdXQtM+", "c3RhZ2VkLXJvbGxvdXQtM/"} {
		if domain.ValidSalt(bad) {
			t.Errorf("ValidSalt(%q)", bad)
		}
	}
}

func TestStartingARollout(t *testing.T) {
	f := newRolloutFixture(t)
	ro := f.start(t, 10)
	if !ro.Active() || ro.Status != domain.RolloutActive || ro.Percent != 10 || ro.Salt != fixtureSalt ||
		ro.Candidate != f.candidate.ID || ro.Stable != f.stable.ID || ro.Environment != "production" ||
		ro.ProjectID != f.env.ProjectID || ro.StartedBy != "person:a" || ro.Version != 1 {
		t.Fatalf("%+v", ro)
	}
	if ro.MaxDuration != domain.DefaultRolloutMaxDuration || !ro.ExpiresAt.Equal(t0.Add(14*24*time.Hour)) {
		t.Errorf("max_duration %s, expires %s; want 14 days", ro.MaxDuration, ro.ExpiresAt)
	}
	if ro.Expired(ro.ExpiresAt.Add(-time.Second)) || !ro.Expired(ro.ExpiresAt) {
		t.Error("a rollout expires exactly at its max_duration")
	}
}

func TestPercentIsAnIntegerFrom0To100(t *testing.T) {
	f := newRolloutFixture(t)
	for _, p := range []int{0, 1, 50, 100} {
		if _, err := domain.StartRollout(uuid.New(), f.env, f.stable, f.candidate, domain.RolloutStart{Percent: p, Salt: fixtureSalt}, "person:a", t0); err != nil {
			t.Errorf("start at %d%%: %v", p, err)
		}
	}
	for _, p := range []int{-1, 101, 1000} {
		if _, err := domain.StartRollout(uuid.New(), f.env, f.stable, f.candidate, domain.RolloutStart{Percent: p, Salt: fixtureSalt}, "person:a", t0); !errors.Is(err, domain.ErrInvalidPercent) {
			t.Errorf("start at %d%% = %v", p, err)
		}
		ro := f.start(t, 10)
		if _, err := ro.Advance(p, t0); !errors.Is(err, domain.ErrInvalidPercent) || ro.Percent != 10 {
			t.Errorf("advance to %d%% = %v, percent %d", p, err, ro.Percent)
		}
	}
}

func TestMaxDurationBounds(t *testing.T) {
	f := newRolloutFixture(t)
	for d, ok := range map[time.Duration]bool{
		time.Hour: true, 90 * 24 * time.Hour: true, 59 * time.Minute: false, 91 * 24 * time.Hour: false, -time.Hour: false,
	} {
		ro, err := domain.StartRollout(uuid.New(), f.env, f.stable, f.candidate,
			domain.RolloutStart{Percent: 5, MaxDuration: d, Salt: fixtureSalt}, "person:a", t0)
		if ok != (err == nil) || (!ok && !errors.Is(err, domain.ErrInvalidMaxDuration)) {
			t.Errorf("max_duration %s: %v", d, err)
		}
		if ok && !ro.ExpiresAt.Equal(t0.Add(d)) {
			t.Errorf("max_duration %s expires at %s", d, ro.ExpiresAt)
		}
	}
	if _, err := domain.StartRollout(uuid.New(), f.env, f.stable, f.candidate, domain.RolloutStart{Percent: 5, Salt: "nope"}, "person:a", t0); !errors.Is(err, domain.ErrInvalidSalt) {
		t.Errorf("a malformed salt: %v", err)
	}
}

func TestWhatCanBeRolledOutWhere(t *testing.T) {
	f := newRolloutFixture(t)
	start := func(env domain.Environment, stable, candidate domain.Release, approval bool) error {
		_, err := domain.StartRollout(uuid.New(), env, stable, candidate,
			domain.RolloutStart{Percent: 10, Salt: fixtureSalt, ApprovalRequired: approval}, "person:a", t0)
		return err
	}
	if err := start(f.env, f.stable, f.stable, false); !errors.Is(err, domain.ErrRolloutCandidateServed) {
		t.Errorf("rolling out what is served: %v", err)
	}
	empty := f.env
	empty.Current = uuid.Nil
	if err := start(empty, domain.Release{}, f.candidate, false); !errors.Is(err, domain.ErrRolloutNoStable) {
		t.Errorf("an environment serving nothing: %v", err)
	}
	if err := start(f.env, f.stable, f.candidate, true); !errors.Is(err, domain.ErrRolloutNeedsApproval) {
		t.Errorf("an environment requiring approvals: %v", err)
	}
	branch, _ := domain.NewBranchEnvironment(f.env.ProjectID, "feature/x", 7, t0)
	branch.Current = f.stable.ID
	if err := start(branch, f.stable, f.candidate, false); !errors.Is(err, domain.ErrRolloutBranchEnvironment) {
		t.Errorf("a branch environment: %v", err)
	}
	// A preview release ships drafts; production's policy doesn't.
	preview, _ := domain.NewEnvironment(f.env.ProjectID, "preview", domain.DefaultPolicy("preview"), t0)
	drafts, _ := domain.NewRelease(uuid.New(), f.env.ProjectID, 3, uuid.Nil, preview, f.candBuilt, "", "person:x", t0)
	if err := start(f.env, f.stable, drafts, false); !errors.Is(err, domain.ErrIneligible) {
		t.Errorf("a release the policy doesn't cover: %v", err)
	}
	other := f.candidate
	other.ProjectID = uuid.New()
	if err := start(f.env, f.stable, other, false); err == nil {
		t.Error("a release of another project was rolled out")
	}
	relocated := f.candidate
	relocated.Content.SourceLocale = "de"
	if err := start(f.env, f.stable, relocated, false); !errors.Is(err, domain.ErrRolloutSourceLocale) {
		t.Errorf("a candidate with another source locale: %v", err)
	}
}

func TestRolloutLifecycle(t *testing.T) {
	f := newRolloutFixture(t)
	ro := f.start(t, 10)

	// Steps are free: up, down, and the same percent changes nothing.
	for _, step := range []struct {
		percent int
		changed bool
	}{{50, true}, {50, false}, {5, true}, {0, true}, {100, true}} {
		v := ro.Version
		changed, err := ro.Advance(step.percent, t0.Add(time.Hour))
		if err != nil || changed != step.changed || ro.Percent != step.percent || (ro.Version > v) != step.changed {
			t.Fatalf("advance to %d%% = %v, %v; %+v", step.percent, changed, err, ro)
		}
		if ro.Salt != fixtureSalt {
			t.Fatal("advancing changed the salt")
		}
	}
	if err := ro.Complete("person:b", t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ro.Active() || ro.Status != domain.RolloutCompleted || ro.End != domain.EndCompleted || ro.EndedBy != "person:b" ||
		!ro.EndedAt.Equal(t0.Add(2*time.Hour)) || ro.Expired(t0.Add(365*24*time.Hour)) {
		t.Fatalf("completed: %+v", ro)
	}
	if _, err := ro.Advance(20, t0); !errors.Is(err, domain.ErrRolloutEnded) {
		t.Errorf("advancing a completed rollout: %v", err)
	}
	if err := ro.Abort(domain.EndAborted, "person:b", t0); !errors.Is(err, domain.ErrRolloutEnded) {
		t.Errorf("aborting a completed rollout: %v", err)
	}

	for _, why := range []domain.RolloutEnd{domain.EndAborted, domain.EndExpired, domain.EndRolledBack} {
		ro := f.start(t, 10)
		if err := ro.Abort(why, "system:x", t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if ro.Status != domain.RolloutAborted || ro.End != why || ro.EndedBy != "system:x" {
			t.Errorf("aborted (%s): %+v", why, ro)
		}
		if err := ro.Complete("person:b", t0); !errors.Is(err, domain.ErrRolloutEnded) {
			t.Errorf("completing an aborted rollout: %v", err)
		}
	}
	ro = f.start(t, 10)
	if err := ro.Abort(domain.EndCompleted, "person:b", t0); err == nil || !ro.Active() {
		t.Error("an abort that claims to complete")
	}
}

// TestManifestCarriesTheRolloutNested is SPEC §1.4's shape, validated
// against the schema the runtimes use.
func TestManifestCarriesTheRolloutNested(t *testing.T) {
	f := newRolloutFixture(t)
	ro := f.start(t, 10)
	s, _ := signer(t, "k_2026a")
	body, err := f.stable.Manifest("production").WithRollout(ro, f.candidate).Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	releasetest.Manifest(t, body)
	if canonical, _ := jcs.Canonicalize(body); string(canonical) != string(body) {
		t.Error("served manifest bytes are not canonical")
	}
	var m struct {
		Release      domain.ReleaseRef `json:"release"`
		SourceLocale string            `json:"sourceLocale"`
		Rollout      map[string]json.RawMessage
		Signatures   []domain.Signature `json:"signatures"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Release.ID != f.stable.ID.String() || len(m.Signatures) != 1 {
		t.Errorf("the top level is not the stable release: %s", body)
	}
	var cand map[string]json.RawMessage
	_ = json.Unmarshal(m.Rollout["candidate"], &cand)
	if string(m.Rollout["id"]) != `"`+ro.ID.String()+`"` || string(m.Rollout["percent"]) != "10" ||
		string(m.Rollout["salt"]) != `"`+fixtureSalt+`"` || len(m.Rollout) != 4 {
		t.Errorf("rollout member %s", body)
	}
	if len(cand) != 4 || cand["release"] == nil || cand["locales"] == nil || cand["fallback"] == nil || cand["artifacts"] == nil {
		t.Errorf("candidate has exactly release, locales, fallback and artifacts: %s", m.Rollout["candidate"])
	}
	var ref domain.ReleaseRef
	_ = json.Unmarshal(cand["release"], &ref)
	if ref.ID != f.candidate.ID.String() || ref.Version != 2 {
		t.Errorf("candidate release %+v", ref)
	}

	// An ended rollout leaves the manifest the stable one, byte for byte.
	plain, _ := f.stable.Manifest("production").Encode(s)
	for _, end := range []func(*domain.Rollout) error{
		func(r *domain.Rollout) error { return r.Abort(domain.EndAborted, "person:a", t0) },
		func(r *domain.Rollout) error { return r.Complete("person:a", t0) },
	} {
		ended := ro
		if err := end(&ended); err != nil {
			t.Fatal(err)
		}
		got, _ := f.stable.Manifest("production").WithRollout(ended, f.candidate).Encode(s)
		if string(got) != string(plain) {
			t.Errorf("an ended rollout is still in the manifest: %s", got)
		}
	}
}
