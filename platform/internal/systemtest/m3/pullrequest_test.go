//go:build system

package m3_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

// event is one step of the pull request's timeline, for the report.
type event struct {
	// What happened, and what the platform did about it.
	What, Then string
	// At is how long after the pull request opened.
	At time.Duration
}

// checkRun is a Glossa check on the fake GitHub, as the report names it.
type checkRun struct {
	Name, HeadSHA, Status, Conclusion, Title string
	Annotations                              int
	Patches                                  int
}

// comment is the sticky pull-request comment.
type comment struct {
	ID    int64
	Body  string
	Login string
}

// grant is the in-context grant the overlay would hold.
type grant struct {
	Token       string `json:"token"`
	ExpiresAt   string `json:"expires_at"`
	ProjectID   string `json:"project_id"`
	Origin      string `json:"origin"`
	PersonID    string `json:"person_id"`
	Permissions []struct {
		Permission string `json:"permission"`
	} `json:"permissions"`
}

// edgeRead is one read of the branch environment through glossa-edge.
type edgeRead struct {
	Key, Environment string
	Status           int
	When             string
}

func (s *scenario) started() time.Time { return s.prOpened }

// connectRepository runs the GitHub App's install flow and connects the
// repository to the project (RFC 0004 §6.1).
func (s *scenario) connectRepository() {
	t := s.t
	var intent struct {
		State      string `json:"state"`
		InstallURL string `json:"install_url"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/github/install-intents"), map[string]any{}, http.StatusCreated, &intent)
	if !strings.Contains(intent.InstallURL, "/apps/"+appSlug+"/installations/new") {
		t.Fatalf("install URL %q does not point at the App", intent.InstallURL)
	}
	var installation struct {
		ID             string `json:"id"`
		InstallationID int64  `json:"installation_id"`
		AccountLogin   string `json:"account_login"`
		State          string `json:"state"`
		Repositories   []struct {
			RepositoryID int64  `json:"repository_id"`
			FullName     string `json:"full_name"`
		} `json:"repositories"`
		RepositoriesUnavailable bool `json:"repositories_unavailable"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/github/installations"), map[string]any{
		"state": intent.State, "code": "code-m3", "installation_id": installationID,
	}, http.StatusCreated, &installation)
	if installation.State != "active" || installation.InstallationID != installationID {
		t.Fatalf("installation = %+v", installation)
	}
	// The repositories come from GitHub when the installation is listed,
	// not from the callback that stored it.
	listed := list[struct {
		InstallationID int64 `json:"installation_id"`
		Repositories   []struct {
			RepositoryID int64  `json:"repository_id"`
			FullName     string `json:"full_name"`
		} `json:"repositories"`
		RepositoriesUnavailable bool `json:"repositories_unavailable"`
	}](s.owner, s.tenantPath("/github/installations"), nil)
	if len(listed) != 1 || listed[0].InstallationID != installationID {
		t.Fatalf("the tenant has %+v, want the one installation", listed)
	}
	if listed[0].RepositoriesUnavailable {
		t.Fatal("the installation could not list its repositories")
	}
	if len(listed[0].Repositories) != 1 || listed[0].Repositories[0].FullName != repositoryName {
		t.Fatalf("the installation covers %+v, want %s", listed[0].Repositories, repositoryName)
	}
	var connection struct {
		ID             string `json:"id"`
		RepositoryName string `json:"repository_name"`
		DefaultBranch  string `json:"default_branch"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/github/connections"), map[string]any{
		"installation_id": installation.ID,
		"repository_id":   repositoryID,
		"project_id":      s.project,
		"application_id":  s.application,
		"default_branch":  fixture.DefaultBranch,
	}, http.StatusCreated, &connection)
	s.connection = connection.ID
}

// deliver posts one signed webhook, the way GitHub does, and waits for
// the inbox worker to have taken it.
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

// retarget rewrites dotted paths in a webhook fixture, so the pull
// request names this test's branch and commit.
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

// prEdits point a pull-request fixture at this test's branch and one of
// its two commits.
func prEdits(commit string) map[string]any {
	return map[string]any{
		"pull_request.head.ref": fixture.PRBranch,
		"pull_request.head.sha": commit,
	}
}

// openPullRequest delivers `pull_request.opened` and waits for what it
// has to produce: the branch, the pr-7 environment and a queued check.
func (s *scenario) openPullRequest() {
	t := s.t
	s.prOpened = time.Now()
	s.deliver("pull_request.opened", "m3-pr-opened", prEdits(fixture.BranchCommit))

	eventually(t, 60*time.Second, "the branch to open", func() (bool, string) {
		b, ok := s.branch()
		if ok && b.State == "open" && b.PRNumber == fixture.PRNumber {
			s.branchID = b.ID
			return true, ""
		}
		return false, fmt.Sprintf("%+v", b)
	})
	eventually(t, 60*time.Second, "the "+fixture.PREnvironment+" environment", func() (bool, string) {
		envs := list[environment](s.owner, s.projectPath("/environments"), nil)
		names := []string{}
		for _, e := range envs {
			names = append(names, e.Name)
			if e.Name == fixture.PREnvironment {
				if e.Kind != "branch" {
					return false, "kind " + e.Kind
				}
				return true, ""
			}
		}
		return false, strings.Join(names, ", ")
	})
	eventually(t, 60*time.Second, "a queued Glossa check", func() (bool, string) {
		runs := s.d.github.CheckRuns(repositoryID)
		for _, r := range runs {
			if r.HeadSHA == fixture.BranchCommit && r.Name == "Glossa" {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%d check runs", len(runs))
	})
	s.record("pull_request.opened", "branch "+fixture.PRBranch+" opened, "+fixture.PREnvironment+" created, Glossa check queued")
}

type environment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Branch string `json:"branch"`
}

type branchDoc struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	PRNumber   int    `json:"pr_number"`
	HeadCommit string `json:"head_commit"`
}

// branch is the pull request's branch, if the platform has it.
func (s *scenario) branch() (branchDoc, bool) {
	for _, b := range list[branchDoc](s.owner, s.projectPath("/branches"), url.Values{"name": {fixture.PRBranch}}) {
		if b.Name == fixture.PRBranch {
			return b, true
		}
	}
	return branchDoc{}, false
}

// record adds a step to the timeline the report prints.
func (s *scenario) record(what, then string) {
	s.timeline = append(s.timeline, event{What: what, Then: then, At: time.Since(s.started()).Round(time.Second)})
}

// branchRunner is the CI of the pull request: the app's tree with the
// branch's catalogs and its usages document, in a directory of its own,
// so the committed fixture is never written to.
func (s *scenario) branchRunner(commit string, invalid bool) *runner {
	t := s.t
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.CopyFS(dir, os.DirFS(appDir())); err != nil {
		t.Fatal(err)
	}
	messages := append(slices.Clone(s.f.Messages), s.f.NewKeys...)
	for _, locale := range append([]string{s.f.SourceLocale}, s.f.Locales...) {
		catalog := s.f.Catalog(locale, messages)
		if locale != "de" && locale != "en" {
			// The pull request ships no translation for its new keys:
			// that is what the check has to report per locale, and what
			// the in-product editor then writes.
			catalog = s.f.Catalog(locale, s.f.Messages)
		}
		if locale == "de" && invalid {
			catalog[s.f.InvalidKey] = s.f.InvalidSource
		}
		write(t, filepath.Join(dir, "locales", locale+".json"), catalog)
	}
	// The build of this commit: the committed branch document, at the
	// commit CI is running on.
	raw, err := os.ReadFile(filepath.Join(appDir(), "usages.branch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["commit"] = commit
	write(t, filepath.Join(dir, "usages.branch.json"), doc)

	env := map[string]string{}
	for k, v := range s.ci.env {
		env[k] = v
	}
	return &runner{t: t, dir: dir, env: env}
}

func write(t interface{ Fatal(...any) }, path string, v any) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pushBranch runs one CI run of the pull request: push the branch's
// copy, then upload the build's usages for its head commit (§6.3).
func (s *scenario) pushBranch(commit string, invalid bool, want int) pushJSON {
	t := s.t
	r := s.branchRunner(commit, invalid)
	var out pushJSON
	res := r.run("push", "--translations", "--branch", fixture.PRBranch,
		"--pr", strconv.Itoa(fixture.PRNumber), "--commit", commit, "--json")
	if res.code != want {
		t.Fatalf("glossa push --branch: exit %d, want %d\n%s\n%s", res.code, want, res.stdout, res.stderr)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("glossa push --branch --json: %v\n%s", err, res.stdout)
	}
	if out.Branch == nil || out.Branch.Name != fixture.PRBranch {
		t.Fatalf("push --branch = %+v", out)
	}
	var pushed contextPushJSON
	r.ok(&pushed, "context", "push", "usages.branch.json")
	if pushed.Build.Branch != fixture.PRBranch || pushed.Build.OnDefault || pushed.Build.Commit != commit {
		t.Fatalf("the branch's usages landed as %+v", pushed.Build)
	}
	s.branchUsages = pushed.Build
	return out
}

// firstPush is the CI run of the pull request as it was opened: five new
// keys, one of them a message that does not parse. `glossa push` refuses
// that one — it never reaches the platform — and the build's usages
// carry it as an unknown key with the file and line it is used on.
func (s *scenario) firstPush() {
	t := s.t
	out := s.pushBranch(fixture.BranchCommit, true, 4) // ExitPartial: one item failed
	failed := 0
	for _, it := range out.Messages {
		if it.Status != "failed" {
			continue
		}
		failed++
		if it.Key != s.f.InvalidKey {
			t.Errorf("the push refused %s, want only %s", it.Key, s.f.InvalidKey)
		}
		if it.Error == nil || it.Error.Code != "invalid_message" {
			t.Errorf("%s was refused as %+v, want invalid_message", it.Key, it.Error)
		}
	}
	if failed != 1 {
		states := map[string]int{}
		for _, it := range append(slices.Clone(out.Messages), out.Translations...) {
			states[it.Status]++
		}
		t.Fatalf("the push refused %d messages, want the one broken plural (%s); %d messages and %d translations: %v",
			failed, s.f.InvalidKey, len(out.Messages), len(out.Translations), states)
	}
	if len(out.Branch.NewKeys) != len(s.f.NewKeys)-1 {
		t.Fatalf("the push proposed %d keys, want the four that parse: %v", len(out.Branch.NewKeys), out.Branch.NewKeys)
	}
	if s.branchUsages.UnknownKeys != 1 {
		t.Fatalf("the branch's usages hold %d unknown keys, want the one for %s", s.branchUsages.UnknownKeys, s.f.InvalidKey)
	}
	s.record("CI push (`"+short(fixture.BranchCommit)+"`): 5 new keys, 1 invalid",
		fmt.Sprintf("%d keys proposed; `%s` refused (`invalid_message`), %d usages uploaded with it as an unknown key",
			len(out.Branch.NewKeys), s.f.InvalidKey, s.branchUsages.Usages))
}

// waitForCheck waits until the Glossa check for the branch's head commit
// has completed with a conclusion, and returns it.
func (s *scenario) waitForCheck(commit, want string, minPatches int) checkRun {
	t := s.t
	var got checkRun
	eventually(t, 3*time.Minute, "the Glossa check on "+short(commit)+" to be "+want, func() (bool, string) {
		for _, r := range s.d.github.CheckRuns(repositoryID) {
			if r.HeadSHA != commit || r.Name != "Glossa" {
				continue
			}
			got = checkRun{
				Name: r.Name, HeadSHA: r.HeadSHA, Status: r.Status, Conclusion: r.Conclusion,
				Title: r.Title, Annotations: len(r.Annotations), Patches: r.Patches,
			}
			if r.Status == "completed" && r.Conclusion == want && r.Patches >= minPatches {
				return true, ""
			}
			return false, fmt.Sprintf("%s/%s after %d patches", r.Status, r.Conclusion, r.Patches)
		}
		return false, "no check run yet"
	})
	s.checks = append(s.checks, got)
	return got
}

// stickyComments are the Glossa comments on the pull request.
func (s *scenario) stickyComments() []comment {
	var out []comment
	for _, c := range s.d.github.Comments(repositoryID, fixture.PRNumber) {
		if strings.Contains(c.Body, "<!-- glossa:sticky -->") {
			out = append(out, comment{ID: c.ID, Body: c.Body, Login: c.Login})
		}
	}
	return out
}

// checkFails is the verdict on the first CI run: a failure that names
// the invalid message at the line the product's own source uses it on,
// and one sticky comment.
func (s *scenario) checkFails() {
	t := s.t
	run := s.waitForCheck(fixture.BranchCommit, "failure", 1)
	annotations := s.annotations(fixture.BranchCommit)
	want := fmt.Sprintf("%s:%d", s.f.InvalidFile, s.f.InvalidLine)
	located := map[string]string{}
	for _, a := range annotations {
		located[fmt.Sprintf("%s:%d", a.Path, a.StartLine)] = a.Message
	}
	if _, ok := located[want]; !ok {
		t.Errorf("no annotation at %s; the check annotated %v", want, keysOfMap(located))
	}
	named := false
	for _, a := range annotations {
		if a.StartLine == s.f.InvalidLine && a.Path == s.f.InvalidFile && strings.Contains(a.Message, s.f.InvalidKey) {
			named = true
		}
	}
	if !named {
		t.Errorf("no annotation names %s at %s; annotations: %+v", s.f.InvalidKey, want, annotations)
	}
	s.noDuplicateAnnotations(fixture.BranchCommit)
	comments := s.stickyComments()
	if len(comments) != 1 {
		t.Fatalf("%d sticky comments, want exactly one", len(comments))
	}
	if comments[0].Login != "glossa[bot]" {
		t.Errorf("the sticky comment was written by %s", comments[0].Login)
	}
	for _, l := range []string{"es", "fr", "ja"} {
		if !strings.Contains(comments[0].Body, "| "+l+" |") {
			t.Errorf("the sticky comment has no row for %s:\n%s", l, comments[0].Body)
		}
	}
	s.comments = comments
	s.record("Glossa check on `"+short(fixture.BranchCommit)+"`",
		fmt.Sprintf("failure — %q, %s at `%s`, one sticky comment", run.Title, plural(run.Annotations, "annotation"), want))
}

// annotations are the Glossa check run's annotations on a commit.
func (s *scenario) annotations(commit string) []githubtest.Annotation {
	for _, r := range s.d.github.CheckRuns(repositoryID) {
		if r.HeadSHA == commit && r.Name == "Glossa" {
			return r.Annotations
		}
	}
	return nil
}

// noDuplicateAnnotations checks that the worker sent each annotation
// once, however often the report was re-rendered: GitHub appends
// annotations, it never replaces them (RFC 0004 §6.4).
func (s *scenario) noDuplicateAnnotations(commit string) {
	seen := map[string]int{}
	for _, a := range s.annotations(commit) {
		seen[fmt.Sprintf("%s:%d:%s", a.Path, a.StartLine, a.Message)]++
	}
	var twice []string
	for k, n := range seen {
		if n > 1 {
			twice = append(twice, fmt.Sprintf("%s (×%d)", k, n))
		}
	}
	if len(twice) > 0 {
		slices.Sort(twice)
		s.t.Errorf("%d annotations were sent more than once on %s: %v", len(twice), short(commit), twice)
	}
}

func keysOfMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// repair fixes the invalid message in the product's source (a second CI
// push) and writes the missing Spanish, French and Japanese through an
// in-context grant, the way the in-product editor does from the shared
// preview deployment (RFC 0004 §5.2, §12.2).
func (s *scenario) repair() {
	t := s.t
	// The fix is a second commit on the branch, the way it is in a real
	// pull request: GitHub says `synchronize`, CI runs again.
	s.deliver("pull_request.synchronize", "m3-pr-synchronize", prEdits(fixture.BranchCommit2))
	s.pushBranch(fixture.BranchCommit2, false, 0)
	eventually(t, 60*time.Second, s.f.InvalidKey+" to be proposed", func() (bool, string) {
		var m struct {
			State string `json:"state"`
		}
		if _, err := s.owner.try(http.MethodGet, s.projectPath("/messages/"+s.f.InvalidKey), nil, http.StatusOK, &m); err != nil {
			return false, err.Error()
		}
		return m.State == "proposed", m.State
	})

	// Studio registers the preview deployment's origin, then mints the
	// grant the overlay's popup returns. The origin is this test's own
	// fixture deployment, on loopback.
	origin := strings.TrimSuffix(s.appURL, "/")
	s.owner.do(http.MethodPost, s.projectPath("/preview-origins"),
		map[string]string{"origin": origin, "label": "m3 preview deployment"}, http.StatusCreated, nil)
	s.owner.do(http.MethodPost, s.projectPath("/in-context-grants"),
		map[string]string{"origin": origin}, http.StatusCreated, &s.grant)
	if !strings.HasPrefix(s.grant.Token, "glossa_ctx_") || s.grant.Origin != origin || s.grant.ProjectID != s.project {
		t.Fatalf("grant = %+v", s.grant)
	}
	var can []string
	for _, p := range s.grant.Permissions {
		can = append(can, p.Permission)
	}
	if !slices.Contains(can, "translations.write") {
		t.Fatalf("the grant may not write translations: %v", can)
	}

	editor := &client{t: t, base: s.d.base, bearer: s.grant.Token, origin: origin,
		http: &http.Client{Timeout: 60 * time.Second}}
	type edit struct{ key, locale, text string }
	var edits []edit
	for _, m := range s.f.NewKeys {
		for _, l := range []string{"es", "fr", "ja"} {
			edits = append(edits, edit{m.Key, l, m.Translations[l]})
		}
	}
	errs := parallel(edits, 4, func(e edit) error {
		_, err := editor.try(http.MethodPut,
			s.projectPath("/messages/"+e.key+"/translations/"+e.locale),
			map[string]any{
				"text": e.text, "syntax": "mf2", "origin": "human",
				"origin_detail": map[string]any{"in_context": map[string]any{
					"route": "/kasse", "viewport": map[string]int{"width": 1280, "height": 800},
				}},
			}, http.StatusCreated, nil)
		if err != nil {
			return fmt.Errorf("%s %s: %w", e.key, e.locale, err)
		}
		return nil
	})
	for _, err := range errs {
		t.Error(err)
	}
	s.inContextEdits = len(edits)

	// The grant reaches only the editor's endpoints, and only from its
	// own origin: the same token is refused elsewhere.
	if _, err := editor.try(http.MethodGet, s.tenantPath("/tokens"), nil, http.StatusOK, nil); err == nil {
		t.Error("the in-context grant could list the tenant's API tokens")
	}
	elsewhere := &client{t: t, base: s.d.base, bearer: s.grant.Token, origin: "https://evil.example.com",
		http: &http.Client{Timeout: 30 * time.Second}}
	if _, err := elsewhere.try(http.MethodGet, s.projectPath("/messages/"+s.f.InvalidKey), nil, http.StatusOK, nil); err == nil {
		t.Error("the in-context grant worked from an origin it is not bound to")
	}
	// The origin is a loopback port of this run, so it stays out of the
	// report: the report has to read the same after every run.
	s.record("second CI push (`"+short(fixture.BranchCommit2)+"`) + in-context edits",
		fmt.Sprintf("`%s` fixed; %d translations written with a `glossa_ctx_…` grant bound to the preview deployment's origin",
			s.f.InvalidKey, len(edits)))
}

// checkSucceeds is the verdict after the repair: the same check run, now
// green, and the same comment, updated in place.
func (s *scenario) checkSucceeds() {
	t := s.t
	run := s.waitForCheck(fixture.BranchCommit2, "success", 1)
	if n := len(s.annotations(fixture.BranchCommit2)); n != 0 {
		t.Errorf("the green check still carries %d annotations", n)
	}
	s.noDuplicateAnnotations(fixture.BranchCommit2)
	comments := s.stickyComments()
	if len(comments) != 1 {
		t.Fatalf("%d sticky comments after the repair, want the first one updated", len(comments))
	}
	if comments[0].ID != s.comments[0].ID {
		t.Errorf("the sticky comment was replaced (%d → %d), not updated", s.comments[0].ID, comments[0].ID)
	}
	if comments[0].Body == s.comments[0].Body {
		t.Errorf("the sticky comment still says what it said when the check failed")
	}
	s.comments = comments
	s.record("Glossa check on `"+short(fixture.BranchCommit2)+"`",
		fmt.Sprintf("success — %q, the same comment updated in place", run.Title))
}

// deliveryKey is a key the edge resolves.
type deliveryKey struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Scope struct {
		Environments []string `json:"environments"`
		Branches     bool     `json:"branches"`
	} `json:"scope"`
}

// readBranchEnvironment proves what a branch environment is for: a
// preview key reads pr-7 through glossa-edge, and a production key never
// does (RFC 0004 §4.3, §12.2).
func (s *scenario) readBranchEnvironment() {
	t := s.t
	var preview, production deliveryKey
	s.owner.do(http.MethodPost, s.projectPath("/delivery-keys"), map[string]any{
		"name": "preview", "scope": map[string]any{"environments": []string{}, "branches": true},
	}, http.StatusCreated, &preview)
	s.owner.do(http.MethodPost, s.projectPath("/delivery-keys"), map[string]any{
		"name": "production", "scope": map[string]any{"environments": []string{"production"}, "branches": false},
	}, http.StatusCreated, &production)
	s.previewKey, s.productionKey = preview.Key, production.Key

	// The branch environment publishes on its own, debounced.
	eventually(t, 3*time.Minute, fixture.PREnvironment+" to be published", func() (bool, string) {
		code := s.edgeGet(preview.Key, fixture.PREnvironment)
		return code == http.StatusOK, fmt.Sprintf("HTTP %d", code)
	})
	s.edgeReads = append(s.edgeReads, edgeRead{"preview", fixture.PREnvironment, http.StatusOK, "while the pull request is open"})

	if code := s.edgeGet(production.Key, fixture.PREnvironment); code != http.StatusNotFound {
		t.Errorf("a production delivery key read %s: HTTP %d", fixture.PREnvironment, code)
	}
	s.edgeReads = append(s.edgeReads, edgeRead{"production", fixture.PREnvironment, http.StatusNotFound, "while the pull request is open"})
	s.record("branch environment", fmt.Sprintf("%s published; the preview key reads it, the production key gets 404",
		fixture.PREnvironment))
}

// edgeGet reads a manifest through glossa-edge and returns the status.
func (s *scenario) edgeGet(key, environment string) int {
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(s.d.edgeURL + "/v1/" + key + "/" + environment + "/manifest.json")
	if err != nil {
		s.t.Fatalf("read %s through the edge: %v", environment, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// merge is the default-branch push that follows a merge: the proposed
// messages become active (RFC 0004 §4.1).
func (s *scenario) merge() {
	t := s.t
	r := s.branchRunner(fixture.BranchCommit2, false)
	var out pushJSON
	r.ok(&out, "push", "--translations")
	if out.Branch != nil {
		t.Fatalf("a default-branch push reported a branch: %+v", out.Branch)
	}
	errs := parallel(s.f.NewKeys, 4, func(m fixture.Message) error {
		var doc struct {
			State string `json:"state"`
		}
		if _, err := s.owner.try(http.MethodGet, s.projectPath("/messages/"+m.Key), nil, http.StatusOK, &doc); err != nil {
			return err
		}
		if doc.State != "active" {
			return fmt.Errorf("%s is %s after the default-branch push, want active", m.Key, doc.State)
		}
		return nil
	})
	for _, err := range errs {
		t.Error(err)
	}
	s.activated = len(s.f.NewKeys)
	s.record("merge + default-branch push", fmt.Sprintf("%d proposed messages activated", s.activated))
}

// closePullRequest delivers `pull_request.closed` (merged) and checks
// that the branch environment is destroyed: the edge answers 404 for the
// key that could read it a moment ago.
func (s *scenario) closePullRequest() {
	t := s.t
	s.deliver("pull_request.closed", "m3-pr-closed", prEdits(fixture.BranchCommit2))
	eventually(t, 60*time.Second, "the branch to be merged", func() (bool, string) {
		b, ok := s.branch()
		if !ok {
			return false, "gone"
		}
		return b.State == "merged", b.State
	})
	eventually(t, 2*time.Minute, "the edge to stop serving "+fixture.PREnvironment, func() (bool, string) {
		code := s.edgeGet(s.previewKey, fixture.PREnvironment)
		return code == http.StatusNotFound, fmt.Sprintf("HTTP %d", code)
	})
	s.edgeReads = append(s.edgeReads, edgeRead{"preview", fixture.PREnvironment, http.StatusNotFound, "after the pull request closed"})
	envs := list[environment](s.owner, s.projectPath("/environments"), nil)
	for _, e := range envs {
		if e.Name == fixture.PREnvironment {
			t.Errorf("%s still exists after the pull request closed", fixture.PREnvironment)
		}
	}
	s.record("pull_request.closed (merged)", fixture.PREnvironment+" destroyed; the edge answers 404")
}
