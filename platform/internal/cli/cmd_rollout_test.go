package cli

import (
	"strings"
	"testing"
)

// withCandidate publishes v1 to production (the stable side) and v2 to
// staging (the candidate).
func withCandidate(t *testing.T) (*fakeServer, *workspace) {
	t.Helper()
	srv, w := seeded(t)
	publishJSON(t, w, "--environment", "production")
	publishJSON(t, w, "--environment", "staging")
	return srv, w
}

func (f *fakeServer) ifMatches() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.appr.ifMatch...)
}

func rolloutStart(t *testing.T, w *workspace, args ...string) rolloutDoc {
	t.Helper()
	var out rolloutDoc
	w.json(&out, append([]string{"release", "rollout", "start", "--environment", "production", "--release", "v2"}, args...)...).want(t, ExitOK)
	return out
}

func TestRolloutStartStatusAndList(t *testing.T) {
	srv, w := withCandidate(t)

	r := w.run("release", "rollout", "start", "--environment", "production", "--release", "v2", "--percent", "10",
		"--max-duration", "7d", "--idempotency-key", "ro-1", "--json")
	r.want(t, ExitOK)
	golden(t, "rollout-start.json", r.stdout)
	if srv.countRequests("POST /v1/tenants/ten_1/projects/prj_1/environments/production/rollouts ro-1") != 1 {
		t.Errorf("the Idempotency-Key wasn't sent: %v", srv.requests)
	}
	replay := rolloutStart(t, w, "--percent", "10", "--max-duration", "7d", "--idempotency-key", "ro-1")
	if !replay.Replayed || replay.Rollout.ID != "ro_1" || replay.Rollout.MaxDurationSeconds != 7*24*3600 {
		t.Fatalf("replay = %+v", replay)
	}
	if srv.serving("production") != "rel_1" {
		t.Fatal("starting a rollout moved the pointer")
	}

	var st rolloutDoc
	w.json(&st, "release", "rollout", "status", "--environment", "production").want(t, ExitOK)
	if st.Action != "status" || st.Rollout == nil || st.Rollout.ID != "ro_1" || st.ETag != `"1"` ||
		st.Rollout.Release.Version != 2 || st.Rollout.StableRelease.Version != 1 || st.Rollout.Percent != 10 {
		t.Fatalf("status = %+v", st)
	}
	human := w.run("release", "rollout", "show", "--environment", "production")
	for _, want := range []string{"Rollout ro_1 in production: active at 10 %", "candidate  v2", "stable     v1", "(max 7d)", `etag       "1"`} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, human.stdout)
		}
	}
	human = w.run("release", "rollout", "start", "--environment", "staging", "--release", "v1", "--percent", "5")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "Started rollout ro_2: staging serves v1 to 5 % of installations; the rest stay on v2") {
		t.Fatalf("start:\n%s", human.stdout)
	}

	var list rolloutListDoc
	w.json(&list, "release", "rollout", "list", "--environment", "production").want(t, ExitOK)
	if list.Schema != rolloutSchema || len(list.Rollouts) != 1 || list.Rollouts[0].ID != "ro_1" {
		t.Fatalf("list = %+v", list)
	}
	var none rolloutDoc
	w.json(&none, "release", "rollout", "status", "--environment", "development").want(t, ExitOK)
	if none.Rollout != nil {
		t.Fatalf("status without one = %+v", none)
	}
	if r := w.run("release", "rollout", "status", "--environment", "development"); !strings.Contains(r.stdout, "No active rollout in development.") {
		t.Fatalf("status without one:\n%s", r.stdout)
	}
}

func TestRolloutAdvanceSendsTheETagItReadAndExplains412(t *testing.T) {
	srv, w := withCandidate(t)
	rolloutStart(t, w, "--percent", "10")

	var adv rolloutDoc
	w.json(&adv, "release", "rollout", "advance", "--environment", "production", "--percent", "50").want(t, ExitOK)
	if adv.Rollout.Percent != 50 || adv.PreviousPercent == nil || *adv.PreviousPercent != 10 || adv.ETag != `"2"` {
		t.Fatalf("advance = %+v", adv)
	}
	if got := srv.ifMatches(); len(got) != 1 || got[0] != `advance "1"` {
		t.Fatalf("If-Match sent = %v, want the ETag read just before", got)
	}
	r := w.run("release", "rollout", "advance", "ro_1", "--environment", "production", "--percent", "25")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "Rollout ro_1 in production: 50 % → 25 %") {
		t.Fatalf("advance down:\n%s", r.stdout)
	}

	// A stale --if-match: someone changed it since.
	doc := wantError(t, w, ExitNetwork, "precondition_failed", "release", "rollout", "advance", "--environment", "production",
		"--percent", "60", "--if-match", `"1"`)
	if !strings.Contains(doc.Error.Message, "the rollout changed since it was read") ||
		!strings.Contains(doc.Error.Why, `--if-match "1" is not the rollout's current version, which is "3"`) ||
		!strings.Contains(doc.Error.Fix, "glossa release rollout status --environment production") {
		t.Fatalf("412 = %+v", doc.Error)
	}
	human := w.run("release", "rollout", "advance", "--environment", "production", "--percent", "60", "--if-match", `"2"`)
	human.want(t, ExitNetwork)
	if !strings.Contains(human.stderr, "the rollout changed since it was read") {
		t.Fatalf("412 human:\n%s", human.stderr)
	}

	wantError(t, w, ExitUsage, "invalid_percent", "release", "rollout", "advance", "--environment", "production", "--percent", "120")
	wantError(t, w, ExitUsage, "invalid_usage", "release", "rollout", "advance", "--environment", "production")
	wantError(t, w, ExitUsage, "invalid_usage", "release", "rollout", "advance", "--environment", "production", "--percent", "half")
}

func TestRolloutCompleteMovesThePointerAndAbortNeverWaitsOnATag(t *testing.T) {
	srv, w := withCandidate(t)
	rolloutStart(t, w, "--percent", "10")

	r := w.run("release", "rollout", "abort", "--environment", "production")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "Aborted rollout ro_1: every installation returns to v1 on its next refresh") {
		t.Fatalf("abort:\n%s", r.stdout)
	}
	if got := srv.ifMatches(); len(got) != 1 || got[0] != "abort " {
		t.Fatalf("abort sent If-Match %v; it must not wait on a tag", got)
	}
	doc := wantError(t, w, ExitNetwork, "no_active_rollout", "release", "rollout", "complete", "--environment", "production")
	if !strings.Contains(doc.Error.Fix, "glossa release rollout list --environment production") {
		t.Errorf("none active = %+v", doc.Error)
	}
	doc = wantError(t, w, ExitNetwork, "rollout_ended", "release", "rollout", "advance", "ro_1", "--environment", "production", "--percent", "20")
	if !strings.Contains(doc.Error.Fix, "already ended") {
		t.Errorf("ended = %+v", doc.Error)
	}

	rolloutStart(t, w, "--percent", "10")
	var done rolloutDoc
	w.json(&done, "release", "rollout", "complete", "--environment", "production").want(t, ExitOK)
	if done.Rollout.Status != "completed" || done.Rollout.End != "completed" || srv.serving("production") != "rel_2" {
		t.Fatalf("complete = %+v (serving %s)", done, srv.serving("production"))
	}
	if got := srv.ifMatches(); got[len(got)-1] != `complete "1"` {
		t.Fatalf("complete sent If-Match %v, want the ETag read just before", got)
	}
	var list rolloutListDoc
	w.json(&list, "release", "rollout", "list", "--environment", "production").want(t, ExitOK)
	if len(list.Rollouts) != 2 || list.Rollouts[0].Status != "completed" || list.Rollouts[1].End != "aborted" {
		t.Fatalf("list = %+v", list)
	}
	human := w.run("release", "rollout", "list", "--environment", "production")
	if !strings.Contains(human.stdout, "ro_2") || !strings.Contains(human.stdout, "aborted") {
		t.Fatalf("list:\n%s", human.stdout)
	}
}

func TestRolloutStartRefusalsAreExplained(t *testing.T) {
	srv, w := withCandidate(t)
	for _, c := range []struct {
		exit ExitCode
		code string
		args []string
		fix  string
	}{
		{ExitUsage, "invalid_percent", []string{"--percent", "101"}, "0 to 100"},
		{ExitUsage, "invalid_max_duration", []string{"--percent", "10", "--max-duration", "30m"}, "one hour to 90 days"},
		{ExitUsage, "force_reason_required", []string{"--percent", "10", "--force"}, "--force-reason"},
		{ExitUsage, "invalid_force_reason", []string{"--percent", "10", "--force-reason", "why"}, "goes with --force"},
		{ExitUsage, "invalid_usage", []string{"--percent", "10", "--max-duration", "soon"}, ""},
		{ExitUsage, "invalid_usage", []string{"--release", "v2"}, ""},
	} {
		doc := wantError(t, w, c.exit, c.code, append([]string{"release", "rollout", "start", "--environment", "production", "--release", "v2"}, c.args...)...)
		if !strings.Contains(doc.Error.Fix, c.fix) {
			t.Errorf("%s: fix = %q", c.code, doc.Error.Fix)
		}
	}
	wantError(t, w, ExitNetwork, "release_not_found", "release", "rollout", "start", "--environment", "production", "--release", "v9", "--percent", "10")
	wantError(t, w, ExitUsage, "invalid_usage", "release", "rollout", "start", "--release", "v2", "--percent", "10")
	wantError(t, w, ExitUsage, "invalid_usage", "release", "rollout", "spin", "--environment", "production")

	doc := wantError(t, w, ExitNetwork, "rollout_candidate_served", "release", "rollout", "start", "--environment", "production", "--release", "v1", "--percent", "10")
	if !strings.Contains(doc.Error.Fix, "production already serves that release") {
		t.Errorf("served = %+v", doc.Error)
	}
	wantError(t, w, ExitNetwork, "rollout_no_stable", "release", "rollout", "start", "--environment", "development", "--release", "v2", "--percent", "10")

	srv.mu.Lock()
	srv.appr.policyUnmet = true
	srv.mu.Unlock()
	doc = wantError(t, w, ExitNetwork, "policy_not_met", "release", "rollout", "start", "--environment", "production", "--release", "v2", "--percent", "10")
	if !strings.Contains(doc.Error.Fix, "--force --force-reason") || !strings.Contains(doc.Error.Why, "short of complete") {
		t.Errorf("gate = %+v", doc.Error)
	}
	forced := rolloutStart(t, w, "--percent", "10", "--force", "--force-reason", "launch day")
	if !forced.Rollout.Forced || forced.Rollout.ForceReason != "launch day" {
		t.Fatalf("forced = %+v", forced)
	}
	doc = wantError(t, w, ExitNetwork, "rollout_active", "release", "rollout", "start", "--environment", "production", "--release", "v2", "--percent", "10")
	if !strings.Contains(doc.Error.Fix, "production has one rollout at a time") {
		t.Errorf("active = %+v", doc.Error)
	}
	// A publish or promote under an active rollout is refused, and says how to get past it.
	doc = wantError(t, w, ExitNetwork, "rollout_active", "release", "publish", "--environment", "production")
	if !strings.Contains(doc.Error.Fix, "glossa release rollout status --environment") {
		t.Errorf("publish under a rollout = %+v", doc.Error)
	}
	wantError(t, w, ExitNetwork, "rollout_active", "release", "promote", "v2", "--to", "production")
	// A rollback is never refused: it aborts the rollout.
	w.run("release", "rollback", "--environment", "production", "--to", "v1").want(t, ExitOK)
	var st rolloutDoc
	w.json(&st, "release", "rollout", "status", "ro_1", "--environment", "production").want(t, ExitOK)
	if st.Rollout.End != "rolled_back" {
		t.Fatalf("after rollback = %+v", st.Rollout)
	}

	srv.mu.Lock()
	srv.rel.envs["production"].approvals = 2
	srv.mu.Unlock()
	doc = wantError(t, w, ExitNetwork, "rollout_needs_approval", "release", "rollout", "start", "--environment", "production", "--release", "v2", "--percent", "10")
	if !strings.Contains(doc.Error.Fix, "glossa release promote <release> --to production") || !strings.Contains(doc.Error.Fix, "glossa approve") {
		t.Errorf("needs approval = %+v", doc.Error)
	}
}
