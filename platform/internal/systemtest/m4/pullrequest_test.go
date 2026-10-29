//go:build system

package m4_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The exit criterion (RFC 0005 §12.3): the same commit, through the
// fake GitHub, produces a check run whose conclusion, error count and
// per-layer counts equal the CLI's. Two surfaces, one verdict.
//
// Wave 4 proved this property at the unit level
// (integration/app/check_agreement_test.go). This is the same property
// end to end: the numbers on the left come out of `glossa check`
// against a real server, and the numbers on the right out of a check
// run a real webhook produced, read back off GitHub's own view of it.
//
// A note on how the check run is produced. Nothing creates a check run
// through `/v1`: `openCheck` is reached only from webhook processing
// (integration/app/github_webhook.go), which writes a queue row that a
// worker turns into a GitHub call. This test therefore does what a
// product does — it delivers a signed `pull_request.opened` — and
// assumes no API shortcut. Should the wave-7 slice that adds one land,
// the shortcut would be an additional way in, not a replacement: the
// property under test is about the check run, not about what triggered
// it.

// checkRunView is a Glossa check on the fake GitHub.
type checkRunView struct {
	Name, HeadSHA, Status, Conclusion, Title string
	Summary                                  string
	Annotations                              int
	Patches                                  int
}

// connectRepository runs the GitHub App's install flow and connects the
// repository to the project.
func (s *scenario) connectRepository() {
	t := s.t
	var intent struct {
		State      string `json:"state"`
		InstallURL string `json:"install_url"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/github/install-intents"), map[string]any{}, http.StatusCreated, &intent)
	var installation struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/github/installations"), map[string]any{
		"state": intent.State, "code": "code-m4", "installation_id": installationID,
	}, http.StatusCreated, &installation)
	if installation.State != "active" {
		t.Fatalf("installation = %+v", installation)
	}
	var connection struct {
		ID string `json:"id"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/github/connections"), map[string]any{
		"installation_id": installation.ID,
		"repository_id":   repositoryID,
		"project_id":      s.project,
		"application_id":  s.application,
		"default_branch":  defaultBranch,
	}, http.StatusCreated, &connection)
}

// deliver posts one signed webhook, the way GitHub does.
func (s *scenario) deliver(name, delivery string, edits map[string]any) {
	t := s.t
	body := githubtest.Fixture(name)
	if len(edits) > 0 {
		body = retarget(t, body, edits)
	}
	req, err := githubtest.WebhookRequest(s.d.base+"/v1/integrations/github/webhooks",
		[]byte(webhookSecret), githubtest.FixtureEvent(name), delivery, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("deliver %s: %v", name, err)
	}
	defer resp.Body.Close()
	var ack struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("deliver %s: HTTP %d (%v)", name, resp.StatusCode, err)
	}
	if !ack.Accepted {
		t.Fatalf("deliver %s (%s): the inbox had seen it already", name, delivery)
	}
}

// retarget rewrites dotted paths in a webhook fixture.
func retarget(t interface{ Fatal(...any) }, body []byte, edits map[string]any) []byte {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for path, v := range edits {
		parts := strings.Split(path, ".")
		at := doc
		for _, p := range parts[:len(parts)-1] {
			next, ok := at[p].(map[string]any)
			if !ok {
				t.Fatal(fmt.Errorf("webhook fixture has no %s", path))
			}
			at = next
		}
		at[parts[len(parts)-1]] = v
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func prEdits(branch, commit string, number int, openedAt time.Time) map[string]any {
	return map[string]any{
		"number":                  number,
		"pull_request.number":     number,
		"pull_request.head.ref":   branch,
		"pull_request.head.sha":   commit,
		"pull_request.created_at": openedAt.UTC().Format(time.RFC3339),
	}
}

// openPullRequest opens the pull request whose head commit is the one
// the CLI just graded.
func (s *scenario) openPullRequest() {
	t := s.t
	s.deliver("pull_request.opened", "m4-pr-opened", prEdits(prBranch, headCommit, prNumber, time.Now()))

	eventually(t, 60*time.Second, "the branch to open", func() (bool, string) {
		for _, b := range list[struct {
			Name     string `json:"name"`
			State    string `json:"state"`
			PRNumber int    `json:"pr_number"`
		}](s.owner, s.projectPath("/branches"), nil) {
			if b.Name == prBranch && b.State == "open" {
				return true, ""
			}
		}
		return false, "no open branch"
	})

	// The branch's CI: the same catalogs `main` holds, so the two
	// surfaces grade the same content, plus the build's usages at the
	// pull request's head commit.
	var out pushJSON
	s.ci.run("push", "--translations", "--branch", prBranch, "--pr", strconv.Itoa(prNumber),
		"--commit", headCommit, "--json")
	_ = out
	s.branchUsages(prBranch, headCommit, "usages.pr.json")
}

// greenPullRequest opens §12.4's pull request that passes today, and
// runs its CI the way the workflow does: its catalogs on its branch, its
// build's usages, and `glossa capture --check --upload` at two commits —
// the second sighting is what makes its clipped pay button evidence a
// policy can gate on (§5.2).
//
// It runs while `main` holds the default branch's catalogs, which is
// the whole of its shape: a pull request branched and checked before
// feature/checkout-copy's copy changes, whose newest run is green under
// v3 with the one visual warning v4 would turn into an error. Nothing
// here is contrived to make it pass; if its run has an error under v3,
// that is reported, and the preview then has nothing to newly fail.
func (s *scenario) greenPullRequest() {
	t := s.t
	s.deliver("pull_request.opened", "m4-green-opened", prEdits(greenBranch, greenCommit, greenPRNumber, time.Now()))
	eventually(t, 60*time.Second, "the green branch to open", func() (bool, string) {
		for _, b := range list[struct {
			Name  string `json:"name"`
			State string `json:"state"`
		}](s.owner, s.projectPath("/branches"), nil) {
			if b.Name == greenBranch && b.State == "open" {
				return true, ""
			}
		}
		return false, "not yet"
	})
	s.startApp()
	var out captureJSON
	code := 0
	// The workflow's steps, once per commit: the pull request's check
	// on a commit waits for that commit's push, its usages and its
	// recorded run, so a commit CI only captured would have a check
	// that waits out CheckWait for the other two.
	for _, commit := range []string{greenCommit, greenCommit2} {
		s.ci.run("push", "--translations", "--branch", greenBranch, "--pr", strconv.Itoa(greenPRNumber),
			"--commit", commit, "--json")
		s.branchUsages(greenBranch, commit, "usages.green.json")
		res := s.ci.run("capture", "--check", "--upload", "--no-coverage", "--base-url", s.appURL,
			"--commit", commit, "--branch", greenBranch, "--json")
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			s.gap("12.4", "the green pull request's `glossa capture --check` printed no document (exit %d): %s",
				res.code, res.stderr)
			return
		}
		code = res.code
	}
	if out.Check == nil {
		s.gap("12.4", "the green pull request's `glossa capture --check` produced no check document")
		return
	}
	s.greenRun = out.Check
	clipped := 0
	for _, f := range visualFindings(out.Check.Findings, "text-clipped") {
		if f.Locus.Key == keyButton && f.Locus.Locale == "ja" {
			clipped++
		}
	}
	var errs []string
	for _, f := range out.Check.Findings {
		if f.Severity == domain.Error {
			errs = append(errs, fmt.Sprintf("`%s/%s` on `%s` (%s)", f.Layer, f.Code, f.Locus.Key, f.Locus.Locale))
		}
	}
	switch {
	case !out.Check.Passed || len(errs) > 0:
		s.gap("12.4", "the pull request meant to pass under v%d does not: `glossa capture --check` exited %d, "+
			"conclusion `%s`, %d errors (%s). A policy change cannot *newly* fail a pull request that already "+
			"fails, so the preview has nothing to name", s.policyVersion, code, out.Check.Conclusion,
			out.Check.Errors, strings.Join(errs, "; "))
	case clipped == 0:
		s.gap("12.4", "the green pull request's run has no `text-clipped` on the Japanese pay button, so "+
			"promoting `visual` would change nothing about it")
	case out.Check.Record == nil || !out.Check.Record.Recorded:
		s.gap("12.4", "the green pull request's run was not recorded, so the preview cannot measure against it: %s",
			recordWhy(out.Check.Record))
	default:
		s.note("12.4", "Pull request #%d (`%s`) was opened and checked while `main` still held the default "+
			"branch's catalogs: its newest recorded run (`%s`) concluded `%s` under v%d with %d errors and %d "+
			"warnings, one of them the Japanese pay button's `text-clipped` — a warning under `visual: warn`, "+
			"an error under `enforce`.", greenPRNumber, greenBranch, short(greenCommit2), out.Check.Conclusion,
			s.policyVersion, out.Check.Errors, out.Check.Warnings)
	}
}

// branchUsages uploads the build's usages for one branch and commit.
func (s *scenario) branchUsages(branch, commit, name string) {
	t := s.t
	raw, err := os.ReadFile(filepath.Join(s.repo.dir, "usages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["commit"], doc["branch"] = commit, branch
	writeJSON(t, filepath.Join(s.repo.dir, name), doc)
	var pushed contextPushJSON
	s.ci.ok(&pushed, "context", "push", name)
}

// waitForCheck waits until the Glossa check for a commit has completed.
func (s *scenario) waitForCheck(commit string) (checkRunView, bool) {
	var got checkRunView
	ok, state := softly(3*time.Minute, func() (bool, string) {
		for _, r := range s.d.github.CheckRuns(repositoryID) {
			if r.HeadSHA != commit || r.Name != "Glossa" {
				continue
			}
			got = checkRunView{
				Name: r.Name, HeadSHA: r.HeadSHA, Status: r.Status, Conclusion: r.Conclusion,
				Title: r.Title, Summary: r.Summary, Annotations: len(r.Annotations), Patches: r.Patches,
			}
			if r.Status == "completed" && r.Conclusion != "" {
				return true, ""
			}
			return false, fmt.Sprintf("%s/%s after %d patches", r.Status, r.Conclusion, r.Patches)
		}
		return false, "no check run yet"
	})
	if !ok {
		s.t.Logf("the check run for %s never completed: %s", short(commit), state)
	}
	return got, ok
}

// agreement is the exit criterion. It compares the CLI's verdict with
// the pull request's, number by number.
func (s *scenario) agreement() {
	run, ok := s.waitForCheck(headCommit)
	if !ok {
		s.gap("12.3", "the pull request's Glossa check never completed, so there is nothing to compare "+
			"the CLI's verdict with")
		return
	}
	s.prCheck = run
	s.prLayers = parseLayerTable(run.Summary)

	// The CLI's side. The workflow runs two commands on one commit, and
	// the second — `glossa capture --check` — is the one that has every
	// layer the server has, because the visual layer needs a browser
	// the plain check has no access to. That is the run CI records for
	// this commit, and therefore the one the pull request renders; the
	// plain `glossa check` document is §12.2's artifact.
	cli := s.cliCheck
	s.agreedWith = "`glossa check`"
	switch {
	case s.cliCaptureHead != nil && s.cliCaptureHead.Check != nil:
		cli = *s.cliCaptureHead.Check
		s.agreedWith = "`glossa capture --check` on this commit"
		if rec := cli.Record; rec == nil || !rec.Recorded {
			s.gap("12.3", "`glossa capture --check` did not record its run for this commit (%v), so there "+
				"is no run for the pull request to render and the check can only show its own reduced view",
				recordWhy(rec))
		}
	case s.cliCapture.Check != nil:
		cli = *s.cliCapture.Check
		s.agreedWith = "`glossa capture --check`"
	}
	switch {
	case strings.Contains(run.Summary, "run CI recorded for this commit"):
		s.note("12.3", "The check run renders the `glossa check` run CI recorded for `%s` — one computation, "+
			"two presentations — rather than computing a second, narrower one of its own.", short(headCommit))
	case strings.Contains(run.Summary, "No `glossa check` run was recorded for this commit"):
		s.gap("12.3", "the check run says no `glossa check` run was recorded for this commit, so it is "+
			"showing Glossa's own reduced view of the branch and not the run the terminal produced. That is "+
			"the honest fallback, and it is not the exit criterion: §12.3 asks the two surfaces to agree, "+
			"which they can only do when the pull request renders CI's run")
	default:
		s.gap("12.3", "the check run's summary says neither which run it rendered nor that it had none to "+
			"render, so a reader cannot tell what was actually checked")
	}
	cliLayers := byLayer(cli.Findings)

	prErrors, prWarnings, prWaived := totalsOf(s.prLayers)
	s.agreeRows = []agreementRow{
		{What: "conclusion", CLI: string(cli.Conclusion), PR: run.Conclusion,
			Agrees: string(cli.Conclusion) == run.Conclusion},
		{What: "errors", CLI: strconv.Itoa(cli.Errors), PR: strconv.Itoa(prErrors),
			Agrees: cli.Errors == prErrors},
		{What: "warnings", CLI: strconv.Itoa(cli.Warnings), PR: strconv.Itoa(prWarnings),
			Agrees: cli.Warnings == prWarnings},
		{What: "waived", CLI: strconv.Itoa(cli.Waived), PR: strconv.Itoa(prWaived),
			Agrees: cli.Waived == prWaived},
	}
	layers := map[domain.Layer]bool{}
	for l := range cliLayers {
		layers[l] = true
	}
	for l := range s.prLayers {
		layers[l] = true
	}
	names := make([]string, 0, len(layers))
	for l := range layers {
		names = append(names, string(l))
	}
	sort.Strings(names)
	for _, n := range names {
		l := domain.Layer(n)
		a, b := cliLayers[l], s.prLayers[l]
		s.agreeRows = append(s.agreeRows, agreementRow{
			What:   "layer `" + n + "` (e/w/x)",
			CLI:    fmt.Sprintf("%d/%d/%d", a.Errors, a.Warnings, a.Waived),
			PR:     fmt.Sprintf("%d/%d/%d", b.Errors, b.Warnings, b.Waived),
			Agrees: a == b,
		})
	}
	if !allAgree(s.agreeRows) {
		// The cause, once, rather than the symptom per row. The two
		// surfaces are meant to be one computation rendered twice now —
		// CI records what `glossa check` found and the pull request
		// renders that run — so a disagreement can only be one of three
		// things, and the row that differs says which.
		s.gap("12.3", "the terminal and the pull request do not reach the same verdict. The check renders "+
			"the run CI recorded for the commit (`integration/adapters/sources.Checks.RecordedRun` → "+
			"`quality/app.ListFindings`), so the numbers can differ only where the rendering is not "+
			"faithful: the run the check found is not the run compared here (another trigger, another "+
			"commit, a later run of the same commit), the report adds or drops findings the run holds, or "+
			"the version grading the pull request is not the version that graded the run (a policy grace, "+
			"RFC 0005 §4.3 — the summary names the version it used). The rows above are where it shows.")
		for _, row := range s.agreeRows {
			if !row.Agrees {
				s.t.Logf("  %s: `glossa check` %s, the check run %s", row.What, row.CLI, row.PR)
			}
		}
	}
	if run.Conclusion == "failure" && run.Annotations == 0 {
		s.gap("12.3", "the failing check run carries no annotations")
	}
	// The located findings are the ones GitHub can annotate.
	located := 0
	for _, f := range cli.Findings {
		if f.Locus.File != "" && f.Locus.Line > 0 {
			located++
		}
	}
	if located > 0 && run.Annotations == 0 {
		s.gap("12.3", "%d findings name a file and a line and none of them was annotated", located)
	}
	s.stickyFound = len(s.stickyComments())
	if s.stickyFound != 1 {
		s.gap("12.3", "the pull request carries %d sticky comments, want exactly one", s.stickyFound)
	}
	if !strings.Contains(run.Summary, fmt.Sprintf("check policy v%d", s.policyVersion)) {
		s.gap("12.3", "the check run's summary does not name the policy version it graded against (v%d)", s.policyVersion)
	}
	s.note("12.3", "The check run on `%s` is `%s`/`%s` — %q — with %d annotations and one sticky comment, "+
		"and its per-layer table carries %d errors, %d warnings and %d waived.",
		short(headCommit), run.Status, run.Conclusion, run.Title, run.Annotations, prErrors, prWarnings, prWaived)
	if allAgree(s.agreeRows) {
		s.note("12.3", "Two surfaces, one verdict: every number above matches.")
	}
}

func allAgree(rows []agreementRow) bool {
	for _, r := range rows {
		if !r.Agrees {
			return false
		}
	}
	return true
}

func totalsOf(m map[domain.Layer]layerCount) (errors, warnings, waived int) {
	for _, c := range m {
		errors += c.Errors
		warnings += c.Warnings
		waived += c.Waived
	}
	return
}

// layerRow reads one line of the check run's "Findings by layer" table.
var layerRow = regexp.MustCompile(`(?m)^\| ([a-z]+) \| (\d+) \| (\d+) \| (\d+) \|$`)

// parseLayerTable reads the per-layer counts back out of the check
// run's own summary — the surface a person reads, not a second
// computation of it.
func parseLayerTable(summary string) map[domain.Layer]layerCount {
	out := map[domain.Layer]layerCount{}
	for _, m := range layerRow.FindAllStringSubmatch(summary, -1) {
		layer := domain.Layer(m[1])
		if !layer.Valid() {
			continue
		}
		e, _ := strconv.Atoi(m[2])
		w, _ := strconv.Atoi(m[3])
		x, _ := strconv.Atoi(m[4])
		out[layer] = layerCount{Errors: e, Warnings: w, Waived: x}
	}
	return out
}

type comment struct {
	ID    int64
	Body  string
	Login string
}

func (s *scenario) stickyComments() []comment {
	var out []comment
	for _, c := range s.d.github.Comments(repositoryID, prNumber) {
		if strings.Contains(c.Body, "<!-- glossa:sticky -->") {
			out = append(out, comment{ID: c.ID, Body: c.Body, Login: c.Login})
		}
	}
	return out
}
