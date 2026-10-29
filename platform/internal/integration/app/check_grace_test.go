package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Rolling out a stricter policy, end to end (RFC 0005 §4.3).
//
// The failure mode is the one the RFC names: someone tightens a rule
// and forty open pull requests go red for something their authors did
// not do. A policy saved with a grace pins the pull requests that
// predate it to the version they were opened under — which needs one
// fact nobody recorded until migration 0033, GitHub's own
// `pull_request.created_at`.
//
// These tests drive it through the fake GitHub: a real payload, a real
// queue row, a real check run.

// The fixture's recorded pull request was opened at this instant, and
// the fixture's clock stands at 2026-09-20 12:00 UTC.
var (
	fixtureOpenedAt = time.Date(2026, 9, 19, 9, 30, 0, 0, time.UTC)
	policySavedAt   = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
)

// rollout is the pair of documents a rollout has live at once: v7, and
// the stricter v8 that supersedes it with the default fortnight's
// grace. v7 only warns about an untranslated key; v8 fails on it, so
// which version graded a run is visible in the verdict and not only in
// the wording.
func rollout() (v7, v8 checkpolicy.Policy) {
	v7 = checkpolicy.Policy{Version: 7, MissingTranslations: checkpolicy.Warning}
	return v7, checkpolicy.Policy{}.Supersede(v7, policySavedAt, checkpolicy.DefaultGrace)
}

// openPRAt delivers a pull_request payload whose `created_at` is
// openedAt, as GitHub would for a pull request opened then.
func openPRAt(t *testing.T, f *fixture, deliveryID string, openedAt time.Time) {
	t.Helper()
	edits := prEdits()
	edits["pull_request.created_at"] = openedAt.UTC().Format(time.RFC3339)
	if _, err := f.deliverBody(t, "pull_request", deliveryID,
		retarget(t, "pull_request.opened", edits)); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
}

// untranslatedBranch is a branch whose one new key is untranslated in
// both locales, together with the `glossa check` run CI recorded for
// it: a warning under v7, an error under v8.
//
// The severity is the one the completeness layer emitted when CI ran,
// which is against the project's *current* document — v8. Whether this
// pull request is graded by that document or by the one it was opened
// under is the grace's whole question, and it is now asked of the run
// the check renders rather than of a second computation.
func (f *fixture) untranslatedBranch(policy checkpolicy.Policy) {
	f.sources.set(func(m *memSources) {
		m.policy = policy
		m.status.NewKeys = []string{"checkout.pay"}
		m.quality.Untranslated = map[string]int{"de": 1, "fr": 1}
	})
	var fs []quality.Finding
	for _, locale := range []string{"de", "fr"} {
		fs = append(fs, quality.New(quality.Finding{
			Layer: quality.LayerCompleteness, Code: checkpolicy.CodeMissingTranslation,
			Severity: policy.Severity(locale),
			Locus:    quality.Locus{Key: "checkout.pay", Locale: locale},
			Message:  "checkout.pay has no translation in " + locale,
		}))
	}
	f.recorded(headSHA, app.RecordedRun{
		Ref: branchName, Commit: headSHA, Trigger: string(quality.TriggerCLI),
		PolicyVersion: policy.Version, Layers: []quality.Layer{quality.LayerCompleteness}, Findings: fs,
	})
}

// TestAGraceGradesAnOlderPullRequestAgainstTheVersionItOpenedUnder is
// the whole mechanism: the pull request predates the save, so it keeps
// v7 and says so; merging is never blocked by a rule the branch
// predates.
func TestAGraceGradesAnOlderPullRequestAgainstTheVersionItOpenedUnder(t *testing.T) {
	_, v8 := rollout()
	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.untranslatedBranch(v8)
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Conclusion != app.ConclusionSuccess {
		t.Fatalf("conclusion = %q: this pull request predates v8 and v7 only warned\n%s",
			run.Conclusion, run.Summary)
	}
	for _, want := range []string{
		"Graded against the project's check policy v7",
		"the version this pull request was opened under",
		"v8 is the project's current policy and takes this pull request over on 2026-10-04",
	} {
		if !strings.Contains(run.Summary, want) {
			t.Fatalf("summary does not carry %q:\n%s", want, run.Summary)
		}
	}
}

// TestAPullRequestOpenedAfterTheSaveGetsTheNewVersionAtOnce: the grace
// is for the pull requests that predate the save, and for nobody else.
func TestAPullRequestOpenedAfterTheSaveGetsTheNewVersionAtOnce(t *testing.T) {
	_, v8 := rollout()
	f := newFixture(t)
	f.connected(t)
	openPRAt(t, f, "d-open-late", policySavedAt.Add(6*time.Hour))
	f.ci(headSHA)
	f.untranslatedBranch(v8)
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Conclusion != app.ConclusionFailure {
		t.Fatalf("conclusion = %q: this pull request was opened under v8, which fails on an untranslated key\n%s",
			run.Conclusion, run.Summary)
	}
	if !strings.Contains(run.Summary, "Graded against the project's check policy v8.") {
		t.Fatalf("summary does not say it used the current version:\n%s", run.Summary)
	}
	if strings.Contains(run.Summary, "opened under") {
		t.Fatalf("a pull request younger than the save was told it is pinned:\n%s", run.Summary)
	}
}

// TestThePinnedPullRequestMovesToTheCurrentVersionWhenTheGraceEnds: no
// pull request is pinned forever. The same row, the same commit, a
// clock past `grace_until` — and the current version grades it.
func TestThePinnedPullRequestMovesToTheCurrentVersionWhenTheGraceEnds(t *testing.T) {
	_, v8 := rollout()
	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.untranslatedBranch(v8)
	f.runCheck(t)
	if c := f.theCheck(t).Conclusion; c != app.ConclusionSuccess {
		t.Fatalf("conclusion = %q inside the grace, want success", c)
	}

	// A day past the fortnight. Nothing about the pull request changed.
	f.clock = policySavedAt.Add(checkpolicy.DefaultGrace).Add(24 * time.Hour)
	if _, err := f.checks.Wake(context.Background(), []int64{repoGitHubID}, branchName, f.clock); err != nil {
		t.Fatal(err)
	}
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Conclusion != app.ConclusionFailure {
		t.Fatalf("conclusion = %q after the grace ran out, want v8's verdict\n%s", run.Conclusion, run.Summary)
	}
	if !strings.Contains(run.Summary, "Graded against the project's check policy v8.") {
		t.Fatalf("summary still claims an older version:\n%s", run.Summary)
	}
}

// TestTheOpenedAtIsGitHubsAndSurvivesAPush: the grace is measured
// against when the pull request was opened, so it must come from the
// payload and it must not move.
//
// Reading it from a clock of ours would be wrong in exactly the case
// the grace exists for — a pull request opened before Glossa was
// installed, or before the policy was saved, would look new — and
// letting a `synchronize` refresh it would push the end of the grace a
// little further out with every commit.
func TestTheOpenedAtIsGitHubsAndSurvivesAPush(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")

	c, err := f.checks.Check(context.Background(), repoGitHubID, pullNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !c.OpenedAt.Equal(fixtureOpenedAt) {
		t.Fatalf("opened_at = %s, want GitHub's own %s (the clock said %s)",
			c.OpenedAt, fixtureOpenedAt, f.clock)
	}

	// A new commit. The wait for CI starts again; the pull request does
	// not get younger.
	edits := prEdits()
	edits["pull_request.head.sha"] = "0123456789abcdef0123456789abcdef01234567"
	f.clock = f.clock.Add(48 * time.Hour)
	if _, err := f.deliverBody(t, "pull_request", "d-sync",
		retarget(t, "pull_request.synchronize", edits)); err != nil {
		t.Fatal(err)
	}
	f.drain(t)

	c, err = f.checks.Check(context.Background(), repoGitHubID, pullNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !c.OpenedAt.Equal(fixtureOpenedAt) {
		t.Fatalf("opened_at = %s after a push, want it unmoved at %s", c.OpenedAt, fixtureOpenedAt)
	}
	if !c.RequestedAt.Equal(f.clock) {
		t.Fatalf("requested_at = %s, want the new commit's %s: the wait for CI does start again",
			c.RequestedAt, f.clock)
	}
}

// TestAPinnedPullRequestStillAgreesWithTheTerminal answers the question
// the grace raises against RFC 0005 §12.3.
//
// The two surfaces agree about a commit *given a policy version*. A
// grace deliberately puts two versions live at once, and a pinned pull
// request is graded by the older one while `glossa check` on a
// developer's machine fetches the project's current document — so for a
// pinned pull request the two can differ, by design, and that is
// precisely why the summary has to name the version it used.
//
// What must still hold, and what this pins, is that the pull request's
// numbers are the terminal's for the version that actually graded the
// run: same conclusion, same totals, same counts per layer.
func TestAPinnedPullRequestStillAgreesWithTheTerminal(t *testing.T) {
	_, v8 := rollout()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	effective := v8.Effective(fixtureOpenedAt, now)
	if effective.Version != 7 {
		t.Fatalf("the fixture's pull request is not pinned (v%d); the test would prove nothing",
			effective.Version)
	}
	commit := checkoutBranch(t)

	// The terminal, told which version graded this run.
	cli := qa.Run(commit, effective, append(qa.Default(),
		qa.Precomputed(quality.LayerTerminology, []quality.Finding{waivedTerm()}))...)

	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.sources.set(func(m *memSources) {
		serverView(m, commit, effective)
		m.policy = v8 // the project's current document, as the server holds it
	})
	// CI graded against the current document, because that is the one
	// the server hands `glossa check`. The pinned pull request renders
	// that run against v7 — which is the case emitAgainst exists for.
	byCI := qa.Run(commit, v8, append(qa.Default(),
		qa.Precomputed(quality.LayerTerminology, []quality.Finding{waivedTerm()}))...)
	f.recorded(headSHA, recordedRunOf(byCI, branchName))
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Conclusion != string(cli.Conclusion) {
		t.Fatalf("the pull request concluded %q and the terminal %q", run.Conclusion, cli.Conclusion)
	}
	if want := layerTable(cli); !strings.Contains(run.Summary, want) {
		t.Fatalf("a pinned run's counts per layer are not the terminal's.\nwant:\n%s\ngot:\n%s",
			want, run.Summary)
	}
	if !strings.Contains(run.Summary, "Graded against the project's check policy v7") {
		t.Fatalf("the summary does not name the version that graded the run:\n%s", run.Summary)
	}
}
