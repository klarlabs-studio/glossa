//go:build system

package m4_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Waivers and the policy rollout (RFC 0005 §12.4).

// waivers waives the `term_missing`, watches the counts move and the
// conclusion stay, then moves the German source under it and watches it
// come back.
func (s *scenario) waivers() {
	var target *domain.Finding
	for i, f := range s.cliCheck.Findings {
		if f.Layer == domain.LayerTerminology && f.Code == "term_missing" {
			target = &s.cliCheck.Findings[i]
			break
		}
	}
	if target == nil {
		s.gap("12.4", "there is no `term_missing` finding to waive (the terminology layer produced %s)",
			codeList(codesOf(s.cliCheck.Findings, domain.LayerTerminology)))
		return
	}
	before := s.cliCheck

	// A waiver without a reason is a 400; that is the whole mechanism.
	_, err := s.owner.try(http.MethodPost, s.projectPath("/waivers"),
		map[string]any{"fingerprint": target.Fingerprint, "reason": "   "}, http.StatusCreated, nil)
	var ae *apiError
	switch {
	case err == nil:
		s.gap("12.4", "a waiver with a blank reason was accepted")
	case asAPIError(err, &ae) && ae.code() != "waiver_reason_required":
		s.gap("12.4", "a blank reason was refused as %q, want waiver_reason_required", ae.code())
	default:
		s.waiverSteps = append(s.waiverSteps, step{
			What: "a waiver with a blank reason",
			Then: "400 `" + ae.code() + "` — the reason is required and non-empty"})
	}

	const reason = "“cesta” is what Brotwerk's Spanish customers call it; the termbase's word is the newer one."
	var waiver struct {
		ID             string `json:"id"`
		Fingerprint    string `json:"fingerprint"`
		Reason         string `json:"reason"`
		Scope          string `json:"scope"`
		SourceRevision int    `json:"source_revision"`
		Active         bool   `json:"active"`
	}
	// The waiver is made against the source revision of the finding the
	// person is looking at. It is passed explicitly because the default
	// — the newest *stored* finding with this fingerprint — is not
	// available here: `glossa check` computes live and stores nothing,
	// so a waiver made from the terminal has no stored finding to take
	// a revision from.
	create := map[string]any{"fingerprint": target.Fingerprint, "reason": reason}
	if target.SourceRevision != nil {
		create["source_revision"] = *target.SourceRevision
	}
	s.owner.do(http.MethodPost, s.projectPath("/waivers"), create, http.StatusCreated, &waiver)
	if target.SourceRevision != nil && waiver.SourceRevision != *target.SourceRevision {
		s.gap("12.4", "the waiver was recorded against source revision %d, want the finding's %d",
			waiver.SourceRevision, *target.SourceRevision)
	}
	s.waiverSteps = append(s.waiverSteps, step{
		What: fmt.Sprintf("waive `%s` (`%s`) with a reason", target.Code, short(target.Fingerprint)),
		Then: fmt.Sprintf("waiver `%s`, scope `%s`, against source revision %d",
			short(waiver.ID), waiver.Scope, waiver.SourceRevision)})

	after := s.check()
	waived := findingByFingerprint(after.Findings, target.Fingerprint)
	switch {
	case waived == nil:
		s.gap("12.4", "the waived finding vanished from the check: a waived finding is still computed and still reported")
	case waived.Severity != domain.Waived:
		s.gap("12.4", "the waived finding is still `%s` in `glossa check`, so the terminal and the pull "+
			"request disagree about a finding the project has accepted. `glossa check` reads the project's "+
			"waivers (`cli/check_waivers.go`) and applies `domain.Waivers`, the same matcher "+
			"`quality/app.RecordRun` applies — so either the fetch did not happen (the run's `waivers` block "+
			"says why) or the two ends computed different fingerprints for this finding", waived.Severity)
	case waived.Waiver != waiver.ID:
		s.gap("12.4", "the waived finding names waiver %q, want %q", waived.Waiver, waiver.ID)
	}
	switch {
	case after.Waived != before.Waived+1:
		s.gap("12.4", "waived went from %d to %d, want one more", before.Waived, after.Waived)
	case after.Warnings != before.Warnings-1:
		s.gap("12.4", "warnings went from %d to %d, want one fewer", before.Warnings, after.Warnings)
	}
	if after.Conclusion != before.Conclusion {
		s.gap("12.4", "the conclusion moved from `%s` to `%s`: a waiver may not change a verdict",
			before.Conclusion, after.Conclusion)
	}
	got := "gone from the run"
	if waived != nil {
		got = "`" + string(waived.Severity) + "`"
	}
	s.waiverSteps = append(s.waiverSteps, step{
		What: "re-run `glossa check`",
		Then: fmt.Sprintf("the finding is %s; %d→%d warnings, %d→%d waived, conclusion `%s`→`%s`",
			got, before.Warnings, after.Warnings, before.Waived, after.Waived, before.Conclusion, after.Conclusion)})

	// The German the translator waived is not the German that now
	// ships: move the source revision under it.
	s.repo.head["de"][keyTermMissing] = "Warenkorb dauerhaft speichern"
	s.repo.write(s.t, s.repo.head)
	s.ci.run("push", "--json")

	back := s.check()
	reborn := findingByFingerprint(back.Findings, target.Fingerprint)
	switch {
	case reborn == nil:
		s.gap("12.4", "the finding did not come back after its source revision changed")
	case reborn.Severity == domain.Waived && target.SourceRevision == nil:
		// Precisely why, not merely that.
		s.gap("12.4", "the waived `term_missing` does not come back when its German source changes, and it "+
			"cannot: the terminology layer's findings carry no `source_revision` at all "+
			"(`glossa check` reported this one with none), and `domain.Waiver.Stale` is false whenever the "+
			"finding's revision is absent — nothing to disagree with. A waiver on a terminology finding "+
			"therefore never expires. The rule itself works, on a layer that does record the revision: see "+
			"the second waiver below")
		s.waiverSteps = append(s.waiverSteps, step{
			What: "change the German source behind it",
			Then: "still `waived` — a terminology finding carries no `source_revision`, so its waiver has " +
				"nothing to go stale against"})
	case reborn.Severity == domain.Waived:
		s.gap("12.4", "the finding is still waived after its source revision changed (waiver at revision %d, "+
			"finding at %v)", waiver.SourceRevision, reborn.SourceRevision)
	default:
		s.waiverSteps = append(s.waiverSteps, step{
			What: "change the German source behind it",
			Then: fmt.Sprintf("the finding is `%s` again: the waiver was made against source revision %d and the "+
				"finding is now at %d", reborn.Severity, waiver.SourceRevision, deref(reborn.SourceRevision))})
	}

	// The rule §12.4 is really about, on a layer that pins its findings
	// to a source revision: waive it, move the German under it, and it
	// comes back.
	s.expiringWaiver(back)

	// `glossa waive --list` is the other surface on the same waiver.
	var listed struct {
		Waivers []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
			Active bool   `json:"active"`
		} `json:"waivers"`
	}
	s.ci.ok(&listed, "waive", "--list")
	found := false
	for _, w := range listed.Waivers {
		if w.Reason == reason && w.Active {
			found = true
		}
	}
	if !found {
		s.gap("12.4", "`glossa waive --list` shows %d waivers and none of them is the active one this test "+
			"made with its reason", len(listed.Waivers))
	}
}

// expiringWaiver is the second half of §12.4's waiver rule: a finding
// that records the source revision it was made against, waived, and
// then reopened by a source change. The locale layer's findings carry
// one, so this is where the rule can be shown rather than described.
func (s *scenario) expiringWaiver(before checkJSON) {
	var target *domain.Finding
	for i, f := range before.Findings {
		if f.Layer == domain.LayerLocale && f.Code == "number-convention" &&
			f.Locus.Key == keyNumberFormat && f.SourceRevision != nil {
			target = &before.Findings[i]
			break
		}
	}
	if target == nil {
		s.gap("12.4", "no `number-convention` finding on `%s` carries a source revision, so the waiver's "+
			"expiry rule cannot be shown on any layer", keyNumberFormat)
		return
	}
	const reason = "The French price list is generated from the ERP and writes its own separators."
	var waiver struct {
		ID             string `json:"id"`
		SourceRevision int    `json:"source_revision"`
	}
	s.owner.do(http.MethodPost, s.projectPath("/waivers"), map[string]any{
		"fingerprint": target.Fingerprint, "reason": reason, "source_revision": *target.SourceRevision,
	}, http.StatusCreated, &waiver)

	waived := s.check()
	got := findingByFingerprint(waived.Findings, target.Fingerprint)
	if got == nil || got.Severity != domain.Waived {
		s.gap("12.4", "the `number-convention` waiver did not take: the finding is %s", severityOf(got))
		return
	}
	s.waiverSteps = append(s.waiverSteps, step{
		What: fmt.Sprintf("waive the `number-convention` on `%s` (`%s`)", keyNumberFormat, short(target.Fingerprint)),
		Then: fmt.Sprintf("`waived`, against source revision %d", waiver.SourceRevision)})

	// The German the translator waived is not the German that now ships.
	s.repo.head["de"][keyNumberFormat] = "Gesamtsumme inklusive Versand: 1.234,50 €"
	s.repo.write(s.t, s.repo.head)
	s.ci.run("push", "--translations", "--json")

	after := s.check()
	reborn := findingByFingerprint(after.Findings, target.Fingerprint)
	switch {
	case reborn == nil:
		s.gap("12.4", "the `number-convention` finding vanished instead of coming back")
	case reborn.Severity == domain.Waived:
		s.gap("12.4", "the `number-convention` finding is still waived after its source revision moved from "+
			"%d to %d", waiver.SourceRevision, deref(reborn.SourceRevision))
	default:
		s.waiverSteps = append(s.waiverSteps, step{
			What: "change the German source behind **that** one",
			Then: fmt.Sprintf("`%s` again: the waiver was made against source revision %d and the finding is "+
				"now at %d — waived against a German that no longer ships", reborn.Severity,
				waiver.SourceRevision, deref(reborn.SourceRevision))})
	}
}

// storedVisual says what the server holds for a branch's visual layer,
// so a gap can tell "nothing was promoted" from "nothing reads it".
func (s *scenario) storedVisual(branch string) string {
	fs := queryFindings(s.owner, s.projectPath("/findings"),
		url.Values{"layer": {string(domain.LayerVisual)}, "branch": {branch}})
	if len(fs) == 0 {
		return "no stored visual finding"
	}
	counts := map[string]int{}
	for _, f := range fs {
		counts[f.Code+" "+string(f.Severity)]++
	}
	return fmt.Sprintf("%d stored visual findings (%s)", len(fs), counts2(counts))
}

func counts2(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d × %s", m[k], k))
	}
	return strings.Join(parts, ", ")
}

func severityOf(f *domain.Finding) string {
	if f == nil {
		return "gone from the run"
	}
	return "`" + string(f.Severity) + "`"
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func asAPIError(err error, out **apiError) bool {
	e, ok := err.(*apiError)
	if ok {
		*out = e
	}
	return ok
}

func findingByFingerprint(fs []domain.Finding, fp string) *domain.Finding {
	for i, f := range fs {
		if f.Fingerprint == fp {
			return &fs[i]
		}
	}
	return nil
}

// check re-runs the workflow's check command and returns its document.
func (s *scenario) check() checkJSON {
	var out checkJSON
	res := s.ci.run("check", "--terminology", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		s.t.Fatalf("glossa check --json: %v\n%s\n%s", err, res.stdout, res.stderr)
	}
	return out
}

// policyRollout saves policy v4 — the one that promotes the visual
// layer from `warn` to `enforce` — first as a dry run, then with a
// grace, and watches the open pull request keep its version while a new
// one gets the new rule.
func (s *scenario) policyRollout() {
	// The preview, before the save.
	preview := s.previewPolicy(policyV4(), 14)
	if !preview.DryRun {
		s.gap("12.4", "a dry-run save reported dry_run=false")
	}
	// The preview shows the policy as it *would* stand; nothing may be
	// stored, which only the stored document can say.
	var stored policyState
	s.owner.do(http.MethodGet, s.projectPath("/check-policy"), nil, http.StatusOK, &stored)
	if stored.Version != s.policyVersion {
		s.gap("12.4", "the dry run moved the stored policy to v%d", stored.Version)
	}
	im := preview.Impact
	s.policySteps = append(s.policySteps, step{
		What: "`POST …/check-policy` with `dry_run: true`",
		Then: fmt.Sprintf("%d stored findings examined over %d runs: %d raised, %d lowered, %d silenced; "+
			"%d findings newly failing, on %s; **%d open pull requests** would newly fail: %s",
			im.Findings, im.Runs, im.Raised, im.Lowered, im.Silenced,
			im.NewlyFailing, codeList(im.NewlyFailingRefs), im.OpenPullRequests, namedPullRequests(im))})
	// "Newly" is the whole of it: the preview compares each ref's newest
	// stored run under v3 with the same run under v4. The green pull
	// request passes today and has the clipped button; the workflow's
	// own pull request already fails under v3 (§12.2 seeds it to), so
	// v4 cannot newly fail it and the preview must not say it does.
	var green *struct {
		Ref    string `json:"ref"`
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	for i, pr := range im.PullRequests {
		if pr.Number == greenPRNumber {
			green = &im.PullRequests[i]
		}
	}
	switch {
	case s.greenRun == nil || !s.greenRun.Passed:
		s.gap("12.4", "the impact preview has no green pull request to name: the one meant to pass under v%d "+
			"did not (see above), so %d open pull requests would newly fail (%s)",
			s.policyVersion, im.OpenPullRequests, namedPullRequests(im))
	case !contains(im.NewlyFailingRefs, greenBranch):
		s.gap("12.4", "the impact preview's newly-failing refs are %v, want `%s` — green under v%d, with the "+
			"clipped pay button v4 turns into an error — among them. It examined %d stored findings over %d "+
			"runs and raised %d", im.NewlyFailingRefs, greenBranch, s.policyVersion, im.Findings, im.Runs, im.Raised)
	case green == nil:
		s.gap("12.4", "`%s` newly fails and the preview does not name its pull request #%d: %s",
			greenBranch, greenPRNumber, namedPullRequests(im))
	case green.Ref != greenBranch:
		s.gap("12.4", "the preview names #%d on `%s`, want `%s`", green.Number, green.Ref, greenBranch)
	}
	if contains(im.NewlyFailingRefs, prBranch) {
		s.gap("12.4", "the preview says `%s` would newly fail, and it already fails under v%d", prBranch, s.policyVersion)
	}
	if im.OpenPullRequests != 1 {
		s.gap("12.4", "the preview counts %d open pull requests that would newly fail, want exactly the green "+
			"one: %s", im.OpenPullRequests, namedPullRequests(im))
	}
	// §12.4 asks the preview to *name* the pull request, not count it:
	// every one it counts is named, by number, with where it is on the
	// repository the project is connected to.
	if len(im.PullRequests) != im.OpenPullRequests {
		s.gap("12.4", "the impact preview counts %d open pull requests and names %d: %s",
			im.OpenPullRequests, len(im.PullRequests), namedPullRequests(im))
	}
	for _, pr := range im.PullRequests {
		want := fmt.Sprintf("/%s/pull/%d", repositoryName, pr.Number)
		if !strings.HasSuffix(pr.URL, want) {
			s.gap("12.4", "the impact preview names pull request #%d (`%s`) at %q, want its address on `%s` "+
				"(…%s)", pr.Number, pr.Ref, pr.URL, repositoryName, want)
		}
	}

	// Now the save, with a grace that pins what is already open.
	saved := s.savePolicy(policyV4(), 14)
	if saved.Version != s.policyVersion+1 {
		s.gap("12.4", "the save produced v%d, want v%d", saved.Version, s.policyVersion+1)
	}
	if saved.GraceUntil == "" || saved.PinnedVersion == 0 {
		s.gap("12.4", "the save recorded no grace (grace_until %q, pinned_version %d), so nothing pins the "+
			"open pull request", saved.GraceUntil, saved.PinnedVersion)
	} else {
		s.policySteps = append(s.policySteps, step{
			What: fmt.Sprintf("save v%d with a 14-day grace", saved.Version),
			Then: fmt.Sprintf("v%d keeps grading the pull requests opened before it, until %s",
				saved.PinnedVersion, saved.GraceUntil)})
	}
	newVersion := saved.Version

	// The open pull request: a new commit on it, and the check has to
	// say which version it used.
	s.deliver("pull_request.synchronize", "m4-pr-sync",
		prEdits(prBranch, headCommit, prNumber, time.Now().Add(-2*time.Hour)))
	pinned, ok := softly(2*time.Minute, func() (bool, string) {
		run, done := s.waitForCheck(headCommit)
		if !done {
			return false, "no completed run"
		}
		if strings.Contains(run.Summary, fmt.Sprintf("check policy v%d", s.policyVersion)) &&
			strings.Contains(run.Summary, "the version this pull request was opened under") {
			return true, ""
		}
		return false, firstLineContaining(run.Summary, "check policy v")
	})
	if !pinned {
		s.gap("12.4", "the open pull request's check does not say it is graded against v%d: %s", s.policyVersion, ok)
	} else {
		s.policySteps = append(s.policySteps, step{
			What: fmt.Sprintf("the open pull request (`%s`, opened under v%d)", prBranch, s.policyVersion),
			Then: fmt.Sprintf("still graded against v%d, and its summary says so: “Graded against the project's "+
				"check policy v%d — the version this pull request was opened under.”", s.policyVersion, s.policyVersion)})
	}

	// The pull request the preview named: opened under v3, a new commit
	// after the save, and it stays green — the grace is what stops v4
	// failing it for something its author did not do.
	s.deliver("pull_request.synchronize", "m4-green-sync",
		prEdits(greenBranch, greenCommit2, greenPRNumber, time.Now().Add(-3*time.Hour)))
	kept, done := s.waitForCheck(greenCommit2)
	switch {
	case !done:
		s.gap("12.4", "the green pull request's check never completed after the save")
	case !strings.Contains(kept.Summary, fmt.Sprintf("check policy v%d", s.policyVersion)):
		s.gap("12.4", "the green pull request, opened under v%d, was graded against %q after the save",
			s.policyVersion, firstLineContaining(kept.Summary, "check policy v"))
	case kept.Conclusion != "success":
		s.gap("12.4", "the green pull request concluded `%s` after the save, under v%d: the grace did not "+
			"keep it green", kept.Conclusion, s.policyVersion)
	default:
		s.policySteps = append(s.policySteps, step{
			What: fmt.Sprintf("the pull request the preview named (#%d, `%s`, opened under v%d)",
				greenPRNumber, greenBranch, s.policyVersion),
			Then: fmt.Sprintf("a new commit after the save: still graded against v%d, and still `%s` — the "+
				"grace keeps it green until it closes or the grace runs out", s.policyVersion, kept.Conclusion)})
	}

	// A new pull request gets v4 immediately, and the visual layer now
	// gates. It needs the visual evidence on its own commit, which is
	// what its CI captures.
	s.repo.write(s.t, s.repo.head)
	// Opened after the save, and said so in whole seconds: GitHub's
	// `created_at` is RFC 3339 to the second and `effective_from` is
	// not, so a pull request opened in the same second as the save
	// would round to just before it and be pinned.
	s.deliver("pull_request.opened", "m4-pr2-opened",
		prEdits(laterBranch, laterCommit, laterPRNumber, time.Now().Add(time.Minute)))
	eventually(s.t, 60*time.Second, "the second branch to open", func() (bool, string) {
		for _, b := range list[struct {
			Name  string `json:"name"`
			State string `json:"state"`
		}](s.owner, s.projectPath("/branches"), nil) {
			if b.Name == laterBranch && b.State == "open" {
				return true, ""
			}
		}
		return false, "not yet"
	})
	s.ci.run("push", "--translations", "--branch", laterBranch, "--pr", strconv.Itoa(laterPRNumber),
		"--commit", laterCommit, "--json")
	s.branchUsages(laterBranch, laterCommit, "usages.pr2.json")
	// Two sightings, at two commits: a visual finding is evidence only
	// when the same fingerprint comes back in the next capture of the
	// same scope, and the upload replays a commit it has already seen.
	for _, commit := range []string{laterCommit, laterCommit2} {
		s.ci.run("capture", "--check", "--upload", "--no-coverage", "--base-url", s.appURL,
			"--commit", commit, "--branch", laterBranch, "--json")
	}

	later, done := s.waitForCheck(laterCommit)
	switch {
	case !done:
		s.gap("12.4", "the second pull request's check never completed")
	case !strings.Contains(later.Summary, fmt.Sprintf("check policy v%d", newVersion)):
		s.gap("12.4", "the new pull request was graded against %q, want v%d",
			firstLineContaining(later.Summary, "check policy v"), newVersion)
	case later.Conclusion != "failure":
		s.gap("12.4", "the new pull request was graded against v%d, as it should be, and still concluded `%s`. "+
			"The server has %s for this branch. The check renders the run CI recorded for the commit, so "+
			"either `glossa capture --check` recorded no run for `%s` (its `record` block says why), or the "+
			"run it recorded holds no `visual` finding at error — which under `visual: enforce` means the "+
			"finding was still provisional: the two-sighting rule (§5.2) clamps a first sighting to a "+
			"warning, and a policy may not raise it.",
			newVersion, later.Conclusion, s.storedVisual(laterBranch), short(laterCommit))
	default:
		visual := parseLayerTable(later.Summary)[domain.LayerVisual]
		s.policySteps = append(s.policySteps, step{
			What: fmt.Sprintf("a pull request opened after the save (`%s`)", laterBranch),
			Then: fmt.Sprintf("graded against v%d and `%s`: the visual layer carries %d errors",
				newVersion, later.Conclusion, visual.Errors)})
	}
	s.policyVersion = newVersion
}

// namedPullRequests is the preview's pull requests as a reader gets
// them: number, branch and address.
func namedPullRequests(im policyImpact) string {
	if len(im.PullRequests) == 0 {
		return "none named"
	}
	parts := make([]string, 0, len(im.PullRequests))
	for _, pr := range im.PullRequests {
		link := "no address"
		if pr.URL != "" {
			link = "<" + pr.URL + ">"
		}
		parts = append(parts, fmt.Sprintf("#%d `%s` %s", pr.Number, pr.Ref, link))
	}
	return strings.Join(parts, ", ")
}

func contains(items []string, want string) bool {
	for _, s := range items {
		if s == want {
			return true
		}
	}
	return false
}

func firstLineContaining(text, want string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, want) {
			return strings.TrimSpace(l)
		}
	}
	return "(the summary names no policy version)"
}

var _ = cli.ExitOK
