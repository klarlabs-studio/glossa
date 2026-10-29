package app_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Where the pull request's findings come from (RFC 0005 §12.3): the run
// CI recorded for the commit, or — said out loud — Glossa's own reduced
// view of the branch.

// recordedRunOf is the run the server stores when CI records what
// `glossa check` found (`createCheckRun`), read back as the check reads
// it: the findings at the severity the policy gave them, with the
// project's waivers applied, and the layers that actually ran.
//
// It carries no counts and no conclusion, because a stored run's counts
// are history and the report grades again — against the version that
// applies to *this* pull request, which is not always the version that
// recorded the run (RFC 0005 §4.3).
func recordedRunOf(r qa.Report, ref string) app.RecordedRun {
	return app.RecordedRun{
		ID: uuid.New(), Ref: ref, Commit: headSHA, Trigger: string(quality.TriggerCLI),
		PolicyVersion: r.PolicyVersion, Layers: r.Layers, Findings: r.Findings,
	}
}

// TestTheCheckSaysWhenItIsShowingItsOwnReducedView is the other half of
// the exit criterion, and the half that keeps it honest.
//
// A repository that pushes its catalogs and uploads its usages but
// never runs `glossa check` has no run to render. The check still
// reports — a pull request with no verdict helps nobody — but what it
// reports is a much weaker claim than "`glossa check` found nothing",
// and it looks exactly the same. So it says so, in the check run and in
// the comment. Falling back quietly would put the divergence this
// milestone exists to remove back where nobody could see it.
func TestTheCheckSaysWhenItIsShowingItsOwnReducedView(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.withoutACheckRun()
	f.sources.set(func(m *memSources) {
		m.status.NewKeys = []string{"checkout.pay"}
		m.quality.Untranslated = map[string]int{"de": 1, "fr": 1}
	})
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Status != app.CheckCompleted {
		t.Fatalf("check run = %+v: a repository with no `glossa check` still gets a verdict", run)
	}
	if !strings.Contains(run.Summary, app.ReducedViewNotice) {
		t.Fatalf("the summary does not say no run was recorded:\n%s", run.Summary)
	}
	for _, want := range []string{"reduced view", "`length`", "`visual`", "Add `glossa check`"} {
		if !strings.Contains(run.Summary, want) {
			t.Fatalf("the summary does not say %q, so a reader cannot tell what was checked:\n%s",
				want, run.Summary)
		}
	}
	if strings.Contains(run.Summary, "run CI recorded for this commit") {
		t.Fatalf("a reduced view claimed to be the run CI recorded:\n%s", run.Summary)
	}
	// The comment says it too: a reader who only ever opens the comment
	// must not be left thinking this was the check CI ran.
	if !strings.Contains(f.theComment(t).Body, app.ReducedViewNotice) {
		t.Fatalf("the sticky comment does not say which verdict this is:\n%s", f.theComment(t).Body)
	}
}

// TestTheCheckWaitsForTheRunBeforeItReports: the recorded run is the
// third thing CI uploads, and the check waits for it as it waits for
// the other two.
//
// Reporting the reduced view first and the real verdict a minute later
// would mean a pull request that goes green and then red — the worst
// possible order, because the green one is the one people merge on.
func TestTheCheckWaitsForTheRunBeforeItReports(t *testing.T) {
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	commit := checkoutBranch(t)
	cli := qa.Run(commit, policy, qa.Default()...)

	f := newFixture(t)
	f.connected(t)
	f.recordsRuns()
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.sources.set(func(m *memSources) { serverView(m, commit, policy) })
	f.runCheck(t)
	if run := f.theCheck(t); run.Status != app.CheckQueued {
		t.Fatalf("check run = %+v: the push and the usages are in, but no `glossa check` run is", run)
	}

	// The run lands, which is what quality.check_run.recorded wakes the
	// check for.
	f.recorded(headSHA, recordedRunOf(cli, branchName))
	if err := f.wakeCheck(); err != nil {
		t.Fatal(err)
	}
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Status != app.CheckCompleted || run.Conclusion != string(cli.Conclusion) {
		t.Fatalf("check run = %+v, want the terminal's %q", run, cli.Conclusion)
	}
}

// TestAProjectThatRecordsRunsWaitsForThisCommitsRun: the check waits
// for a recorded run only where the project is known to record them —
// it has recorded one before, of any commit. Such a project's CI runs
// `glossa check`, so a commit whose run has not landed yet is a commit
// whose verdict is on its way, and reporting the reduced view first
// would be the green-then-red the wait exists to prevent.
func TestAProjectThatRecordsRunsWaitsForThisCommitsRun(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	// An earlier commit's run: the project records them.
	f.recorded("0123456789abcdef0123456789abcdef01234567", app.RecordedRun{
		ID: uuid.New(), Ref: branchName, Trigger: string(quality.TriggerCLI),
	})
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.runCheck(t)

	if run := f.theCheck(t); run.Status != app.CheckQueued {
		t.Fatalf("check run = %+v: this project records `glossa check` runs, and this commit's has not landed", run)
	}
}

// TestAProjectThatHasNeverRecordedARunReportsAtOnce closes the
// regression the third readiness condition introduced.
//
// A project whose CI pushes and uploads usages but has never recorded a
// `glossa check` run — every product that adopted Glossa before RFC
// 0005 — must not wait thirty minutes for a run that is not coming. It
// reports as soon as the push and the usages are in, as it did before
// the check rendered recorded runs, and the summary's first line says
// this is Glossa's reduced view and not the check CI ran.
//
// It also pins the edge honestly: the first pull request after a
// repository adds `glossa check` to its CI is such a project. It gets
// the labelled reduced view first, and when its run lands the check is
// re-rendered from it.
func TestAProjectThatHasNeverRecordedARunReportsAtOnce(t *testing.T) {
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	commit := checkoutBranch(t)
	cli := qa.Run(commit, policy, qa.Default()...)

	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.sources.set(func(m *memSources) { serverView(m, commit, policy) })
	f.runCheck(t) // the clock does not move: no wait

	run := f.theCheck(t)
	if run.Status != app.CheckCompleted {
		t.Fatalf("check run = %+v: a project that has never recorded a run waited for one", run)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(run.Summary), "\n")
	if !strings.Contains(first, app.ReducedViewNotice) {
		t.Fatalf("the summary's first line does not say this is the reduced view:\n%s", run.Summary)
	}
	if !strings.Contains(f.theComment(t).Body, app.ReducedViewNotice) {
		t.Fatalf("the sticky comment does not say which verdict this is:\n%s", f.theComment(t).Body)
	}

	// The project's first run lands: the check renders it, reduced no
	// longer.
	f.recorded(headSHA, recordedRunOf(cli, branchName))
	if err := f.wakeCheck(); err != nil {
		t.Fatal(err)
	}
	f.runCheck(t)
	run = f.theCheck(t)
	if strings.Contains(run.Summary, app.ReducedViewNotice) || run.Conclusion != string(cli.Conclusion) {
		t.Fatalf("check run = %+v: once the run is recorded the check renders it, want %q",
			run, cli.Conclusion)
	}
}

// TestTheCheckAsksAboutHistoryOnlyWhenItDecidesSomething: whether the
// project records runs is asked on the readiness path, so it is asked
// only when the answer can change something — the push and the usages
// are in, and this commit has no run of its own.
func TestTheCheckAsksAboutHistoryOnlyWhenItDecidesSomething(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.runCheck(t) // nothing uploaded yet
	f.recorded(headSHA, app.RecordedRun{ID: uuid.New(), Ref: branchName, Trigger: string(quality.TriggerCLI)})
	f.ci(headSHA)
	if err := f.wakeCheck(); err != nil {
		t.Fatal(err)
	}
	f.runCheck(t) // this commit's own run is in
	f.sources.set(func(m *memSources) {
		if m.historyAsked != 0 {
			t.Errorf("asked whether the project records runs %d times; the answer changed nothing", m.historyAsked)
		}
	})
}

// TestARenderedRunIsAnnotatedWhereTheProductUsesTheKey: a finding
// `glossa check` recorded carries no file, and the pull request puts it
// on the diff anyway.
//
// A catalog is not a file, so nothing the terminal computes knows where
// a message is rendered. Context does — that is what a usages upload is
// — and filling the locus in at report time is what makes a check run
// something a reviewer can act on rather than a list to cross-reference
// by hand. It must not touch the numbers: the same run, located or not,
// reaches the same verdict.
func TestARenderedRunIsAnnotatedWhereTheProductUsesTheKey(t *testing.T) {
	run := app.RecordedRun{
		ID: uuid.New(), Ref: branchName, Commit: headSHA, Trigger: string(quality.TriggerCLI),
		PolicyVersion: 7, Layers: []quality.Layer{quality.LayerParity},
		Findings: []quality.Finding{quality.New(quality.Finding{
			Layer: quality.LayerParity, Code: "missing-argument", Severity: checkpolicy.Error,
			Locus:   quality.Locus{Key: "checkout.pay", Locale: "fr"},
			Message: "{$amount} is gone",
		})},
	}
	in := app.CheckInput{
		Policy: checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}, Recorded: &run,
		Status: app.BranchStatus{Name: branchName, Outdated: map[string]int{}},
		Usages: app.BranchUsages{
			Where: map[string]app.UsageSite{"checkout.pay": {File: "src/Pay.vue", Line: 12}},
		},
	}
	located := app.BuildCheckReport(in)
	if len(located.Annotations) != 1 {
		t.Fatalf("annotations = %+v, want the one the usages placed", located.Annotations)
	}
	if a := located.Annotations[0]; a.Path != "src/Pay.vue" || a.StartLine != 12 || a.Level != "failure" {
		t.Fatalf("annotation = %+v, want a failure on src/Pay.vue:12", a)
	}

	// The same run with nowhere to put it: fewer annotations, the same
	// verdict and the same arithmetic.
	in.Usages.Where = nil
	bare := app.BuildCheckReport(in)
	if len(bare.Annotations) != 0 {
		t.Fatalf("annotations = %+v, want none: nothing said where the key is used", bare.Annotations)
	}
	if bare.Conclusion != located.Conclusion || bare.Errors != located.Errors ||
		bare.Warnings != located.Warnings || bare.Waived != located.Waived {
		t.Fatalf("locating a finding moved the verdict: %q %d/%d/%d against %q %d/%d/%d",
			bare.Conclusion, bare.Errors, bare.Warnings, bare.Waived,
			located.Conclusion, located.Errors, located.Warnings, located.Waived)
	}
}

// TestAnOrphanedTranslationIsAnnotatedWhereTheCodeStillUsesItsKey is
// the case orphaned translations are reported for: the product still
// asks for a key whose message the catalog has obsoleted. `glossa check`
// finds the translation left behind (the completeness layer's
// `unknown-key`, by the obsolete message's ID); Context knows the line
// that still asks for the key; the pull request puts the one on the
// other — and changes nothing else about the run it renders.
func TestAnOrphanedTranslationIsAnnotatedWhereTheCodeStillUsesItsKey(t *testing.T) {
	s := &snapshot.Snapshot{
		Origin: snapshot.FromServerOrigin, SourceLocale: "de",
		Locales:      []snapshot.Locale{{Code: "de", IsSource: true}, {Code: "fr"}},
		Translations: map[string]map[string]snapshot.Translation{"fr": {}},
		Orphans: []snapshot.Orphan{{
			MessageID: "0192f5a1-0000-0000-0000-00000000000b", Key: "help.legacy.title", Namespace: "help",
			Locale: "fr", Revision: "tr-fr",
		}},
	}
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	cli := qa.Run(s, policy, qa.Default()...)
	run := recordedRunOf(cli, branchName)
	var orphan quality.Finding
	for _, f := range cli.Findings {
		if f.Code == checkpolicy.CodeUnknownKey {
			orphan = f
		}
	}
	if orphan.Locus.Message != s.Orphans[0].MessageID || orphan.Severity != checkpolicy.Warning {
		t.Fatalf("the terminal's unknown-key = %+v, want the obsolete message's, as a warning", orphan)
	}
	rep := app.BuildCheckReport(app.CheckInput{
		Policy: policy, Recorded: &run,
		Status: app.BranchStatus{Name: branchName, Outdated: map[string]int{}},
		Usages: app.BranchUsages{
			Where: map[string]app.UsageSite{"help.legacy.title": {File: "src/pages/HelpPage.vue", Line: 23}},
		},
	})
	var located *quality.Finding
	for i, f := range rep.Findings {
		if f.Code == checkpolicy.CodeUnknownKey {
			located = &rep.Findings[i]
		}
	}
	if located == nil || located.Locus.File != "src/pages/HelpPage.vue" || located.Locus.Line != 23 {
		t.Fatalf("rendered unknown-key = %+v, want it at src/pages/HelpPage.vue:23", located)
	}
	if located.Fingerprint != orphan.Fingerprint {
		t.Errorf("fingerprint %s on the pull request, %s in the terminal: locating a finding re-identified it",
			located.Fingerprint, orphan.Fingerprint)
	}
	var annotated bool
	for _, a := range rep.Annotations {
		if a.Path == "src/pages/HelpPage.vue" && a.StartLine == 23 && a.Level == "warning" {
			annotated = true
		}
	}
	if !annotated {
		t.Errorf("annotations = %+v, want a warning on src/pages/HelpPage.vue:23", rep.Annotations)
	}
	if rep.Errors != cli.Counts.Errors || rep.Warnings != cli.Counts.Warnings || rep.Conclusion != string(cli.Conclusion) {
		t.Errorf("pull request %s %d/%d, terminal %s %d/%d", rep.Conclusion, rep.Errors, rep.Warnings,
			cli.Conclusion, cli.Counts.Errors, cli.Counts.Warnings)
	}
}

// TestTheAnnotationBudgetIsSpentOnDistinctAnnotations: GitHub is sent
// each annotation once (UnsentAnnotations), so an annotation repeated on
// a line — a missing translation in six locales reads the same on the
// key's usage — is one annotation. The cap counts what is sent; spending
// it on repeats would silently drop the distinct ones behind them.
func TestTheAnnotationBudgetIsSpentOnDistinctAnnotations(t *testing.T) {
	var fs []quality.Finding
	for i := range app.MaxAnnotations + 10 {
		fs = append(fs, quality.New(quality.Finding{
			Layer: quality.LayerCompleteness, Code: checkpolicy.CodeMissingTranslation, Severity: checkpolicy.Warning,
			Locus:   quality.Locus{Key: "home.title", Locale: fmt.Sprintf("x%03d", i)},
			Message: "missing translation",
		}))
	}
	fs = append(fs, quality.New(quality.Finding{
		Layer: quality.LayerCompleteness, Code: checkpolicy.CodeUnknownKey, Severity: checkpolicy.Warning,
		Locus:   quality.Locus{Message: "m-legacy", Key: "help.legacy.title", Locale: "fr"},
		Message: "translation of a message the catalog has obsoleted",
	}))
	run := app.RecordedRun{ID: uuid.New(), Ref: branchName, Commit: headSHA, Trigger: string(quality.TriggerCLI),
		PolicyVersion: 7, Layers: []quality.Layer{quality.LayerCompleteness}, Findings: fs}
	rep := app.BuildCheckReport(app.CheckInput{
		Policy: checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}, Recorded: &run,
		Status: app.BranchStatus{Name: branchName, Outdated: map[string]int{}},
		Usages: app.BranchUsages{Where: map[string]app.UsageSite{
			"home.title": {File: "src/Home.vue", Line: 3}, "help.legacy.title": {File: "src/Help.vue", Line: 23},
		}},
	})
	if len(rep.Annotations) != 2 || rep.Annotations[1].Path != "src/Help.vue" {
		t.Errorf("%d annotations, want the repeated one once and the orphan's", len(rep.Annotations))
	}
}

// TestARunOfAnotherCommitIsNotThisCommitsVerdict: the check matches on
// the commit and never on the branch.
//
// A branch moves under a pull request with every push. A run belongs to
// the commit it graded, and grading a new commit with an older commit's
// findings would be the same untruth as computing a second thing — it
// would just be harder to notice.
func TestARunOfAnotherCommitIsNotThisCommitsVerdict(t *testing.T) {
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	commit := checkoutBranch(t)
	cli := qa.Run(commit, policy, qa.Default()...)
	if cli.Conclusion != quality.ConclusionFailure {
		t.Fatalf("the fixture passes `glossa check` (%+v); the test would prove nothing", cli.Counts)
	}

	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.withoutACheckRun()
	f.sources.set(func(m *memSources) { m.policy = policy })
	// The same branch, a commit ago. It must not grade this one.
	f.recorded("0123456789abcdef0123456789abcdef01234567", recordedRunOf(cli, branchName))
	f.runCheck(t)

	run := f.theCheck(t)
	if !strings.Contains(run.Summary, app.ReducedViewNotice) {
		t.Fatalf("the check rendered a run of another commit:\n%s", run.Summary)
	}
	if run.Conclusion == app.ConclusionFailure {
		t.Fatalf("this commit was failed by the run of a commit it is not: %+v", run)
	}
}

// TestTheRenderedRunIsTheWholeReport: nothing the read model can see is
// added to a recorded run.
//
// It is tempting to keep the unknown keys and the invalid messages —
// the pull request is the only surface that has the usages document, so
// they are real and they are useful. But adding them makes the numbers
// differ from the terminal's again, and a roll-up per locale on top of
// one finding per message counts the same gap twice. Whatever else the
// pull request grows, it may not grow that.
func TestTheRenderedRunIsTheWholeReport(t *testing.T) {
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	run := app.RecordedRun{
		ID: uuid.New(), Ref: branchName, Commit: headSHA, Trigger: string(quality.TriggerCLI),
		PolicyVersion: 7, Layers: []quality.Layer{quality.LayerParity},
		Findings: []quality.Finding{quality.New(quality.Finding{
			Layer: quality.LayerParity, Code: "missing-argument", Severity: checkpolicy.Warning,
			Locus:   quality.Locus{Key: "checkout.pay", Locale: "fr"},
			Message: "{$amount} is gone",
		})},
	}
	rep := app.BuildCheckReport(app.CheckInput{
		Policy: policy, Recorded: &run,
		Status: app.BranchStatus{
			Name: branchName, Outdated: map[string]int{"fr": 3},
			Invalid: []app.InvalidMessage{{Key: "checkout.total", Code: "invalid_content", Detail: "unmatched"}},
		},
		Quality: app.BranchQuality{Locales: []string{"de", "fr"}, Untranslated: map[string]int{"de": 4}},
		Usages:  app.BranchUsages{Unknown: []app.UnknownKey{{Key: "stray", File: "src/A.vue", Line: 1}}},
	})
	if len(rep.Findings) != 1 || rep.Findings[0].Code != "missing-argument" {
		t.Fatalf("findings = %+v, want exactly the recorded run's one", rep.Findings)
	}
	if rep.Errors != 0 || rep.Warnings != 1 {
		t.Fatalf("counts = %d errors, %d warnings; the branch status leaked into the verdict",
			rep.Errors, rep.Warnings)
	}
	if rep.Conclusion != app.ConclusionSuccess {
		t.Fatalf("conclusion = %q: nothing the recorded run did not find may fail this commit", rep.Conclusion)
	}
}
