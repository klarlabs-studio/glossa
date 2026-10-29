package cli

import (
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// `glossa policy show | diff | export | import` (RFC 0005 §4, §13 wave 6).

// storedPolicy gives the fake a saved policy document, as the API
// renders one.
func storedPolicy(f *fakeServer, version int, doc map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qa.policyVersion = version
	f.policyDoc = map[string]any{
		"version": version, "document": doc, "created_by": "person:ada",
		"created_at": "2026-09-01T10:00:00Z",
	}
}

// aPolicy is a document with a rule and an environment: enough that
// every part of the command has something to show.
func aPolicy() map[string]any {
	return map[string]any{
		"schema": "glossa.check-policy/v1", "fail_on": "error", "missing_translations": "warning",
		"require_complete": "listed", "locales": []string{"de", "en"},
		"rules": []map[string]any{
			{"layer": "source", "severity": "off"},
			{"layer": "terminology", "namespace": "legal", "severity": "error"},
			{"layer": "visual", "severity": "warning", "mode": "warn"},
		},
		"environments": map[string]any{
			"production": map[string]any{"require_complete": "listed", "locales": []string{"de", "en", "fr"},
				"require_review": "approved"},
		},
	}
}

func TestPolicyShowPrintsTheDocumentInForce(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 7, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)

	var out policyShowJSON
	w.json(&out, "policy", "show").want(t, ExitOK)
	if out.Schema != policyShowSchema || out.Version != 7 || out.CreatedBy != "person:ada" {
		t.Fatalf("show = %+v", out)
	}
	if strings.Join(out.Policy.RequireComplete, ",") != "de,en" || out.Policy.FailOn != checkpolicy.Error ||
		out.Policy.MissingTranslations != checkpolicy.Warning {
		t.Fatalf("document = %+v", out.Policy)
	}
	if len(out.Policy.Rules) != 3 || out.Policy.Rules[2].Mode != checkpolicy.ModeWarn {
		t.Fatalf("rules = %+v", out.Policy.Rules)
	}
	if e, ok := out.Policy.Environments["production"]; !ok || e.RequireReview != "approved" {
		t.Fatalf("environments = %+v", out.Policy.Environments)
	}
	// The version, the selectors and what each rule is worth are what a
	// person needs to argue with the policy.
	h := w.run("policy", "show")
	for _, want := range []string{"check policy v7", "de, en", "layer=terminology, namespace=legal", "warn"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human show has no %q:\n%s", want, h.stdout)
		}
	}
}

// A server too old to have the endpoint says that, rather than sending
// somebody to edit a glossa.yaml that is correct.
func TestPolicyShowExplainsAServerWithoutTheEndpoint(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)

	var out errorDoc
	w.json(&out, "policy", "show").want(t, ExitNetwork)
	if out.Error.Code != "no_check_policy" || !strings.Contains(out.Error.Fix, "Studio") {
		t.Errorf("error = %+v", out.Error)
	}
}

// Export and import are one round trip: what `export` writes is exactly
// what `import` reads, and the document reaches the server unchanged.
func TestPolicyExportsAndImportsTheSameDocument(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 7, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)

	var exported policyExportJSON
	w.json(&exported, "policy", "export", "--file", "policy.yaml").want(t, ExitOK)
	if exported.Schema != policyExportSchema || exported.File != "policy.yaml" {
		t.Fatalf("export = %+v", exported)
	}
	file := w.read("policy.yaml")
	// The file is the document, in the document's own words — not a
	// second spelling of it.
	for _, want := range []string{"schema: glossa.check-policy/v1", "fail_on: error", "missing_translations: warning"} {
		if !strings.Contains(file, want) {
			t.Errorf("policy.yaml has no %q:\n%s", want, file)
		}
	}

	var imported policyImportJSON
	w.json(&imported, "policy", "import", "--file", "policy.yaml").want(t, ExitOK)
	if imported.Schema != policyImportSchema || imported.DryRun || imported.Version != 8 {
		t.Fatalf("import = %+v", imported)
	}
	srv.mu.Lock()
	sent := srv.qa.saved
	srv.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("documents sent = %d, want 1", len(sent))
	}
	got := sent[0]
	if got["fail_on"] != "error" || got["missing_translations"] != "warning" || got["require_complete"] != "listed" {
		t.Fatalf("the document the server got = %+v", got)
	}
	if rules, ok := got["rules"].([]any); !ok || len(rules) != 3 {
		t.Fatalf("rules sent = %+v", got["rules"])
	}
	if envs, ok := got["environments"].(map[string]any); !ok || envs["production"] == nil {
		t.Fatalf("environments sent = %+v", got["environments"])
	}
}

// `export` without --file writes the document itself, which is what a
// pipeline reads back.
func TestPolicyExportWritesTheDocumentToStdout(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 2, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)

	yamlOut := w.run("policy", "export")
	yamlOut.want(t, ExitOK)
	if !strings.Contains(yamlOut.stdout, "fail_on: error") {
		t.Fatalf("export to stdout:\n%s", yamlOut.stdout)
	}
	var doc checkpolicy.Policy
	jsonOut := w.json(&doc, "policy", "export")
	jsonOut.want(t, ExitOK)
	if doc.Schema != checkpolicy.Schema || doc.FailOn != checkpolicy.Error {
		t.Fatalf("export --json = %+v", doc)
	}
	// And the YAML it wrote is a document `import` takes.
	w.write("from-stdout.yaml", yamlOut.stdout)
	w.run("policy", "import", "--file", "from-stdout.yaml").want(t, ExitOK)
}

// A policy file says what the policy says, and none of the bookkeeping
// about when it said it. Dropping it quietly would leave the author
// believing they had pinned a version.
func TestPolicyImportRefusesTheServersBookkeeping(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 1, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)

	w.write("policy.yaml", "schema: glossa.check-policy/v1\nversion: 7\nfail_on: error\n")
	var out errorDoc
	w.json(&out, "policy", "import", "--file", "policy.yaml").want(t, ExitUsage)
	if out.Error.Code != "invalid_policy_file" || !strings.Contains(out.Error.Why, "version") {
		t.Fatalf("error = %+v", out.Error)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.saved) != 0 {
		t.Errorf("a document was sent anyway")
	}
}

func TestPolicyImportRefusesAFileItCantRead(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 1, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)

	var out errorDoc
	w.json(&out, "policy", "import", "--file", "nothing.yaml").want(t, ExitUsage)
	if out.Error.Code != "policy_file_unreadable" {
		t.Fatalf("missing file = %+v", out.Error)
	}
	w.write("policy.yaml", "fail_onn: error\n")
	w.json(&out, "policy", "import", "--file", "policy.yaml").want(t, ExitUsage)
	if out.Error.Code != "invalid_policy_file" || !strings.Contains(out.Error.Why, "fail_onn") {
		t.Fatalf("a misspelt field = %+v", out.Error)
	}
	w.write("policy.yaml", "")
	w.json(&out, "policy", "import", "--file", "policy.yaml").want(t, ExitUsage)
	if !strings.Contains(out.Error.Why, "empty") {
		t.Fatalf("an empty file = %+v", out.Error)
	}
	w.run("policy", "import").want(t, ExitUsage)
	w.run("policy", "nonsense").want(t, ExitUsage)
	w.run("policy").want(t, ExitUsage)
}

// `diff` is §4.3's answer to the failure mode: it says how many open
// pull requests would newly fail *before* anyone saves anything.
func TestPolicyDiffPrintsTheImpactPreviewAndStoresNothing(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 7, aPolicy())
	srv.mu.Lock()
	srv.qa.impact = map[string]any{
		"findings": 210, "runs": 12, "raised": 34, "lowered": 2, "silenced": 5,
		"newly_failing": 34, "no_longer_failing": 0, "open_pull_requests": 40,
		"newly_failing_refs": []string{"feature/a", "feature/b"}, "no_longer_failing_refs": []string{},
		"rules": []map[string]any{
			{"rule": 1, "matched": 51, "changed": 34, "newly_failing": 34},
		},
	}
	srv.mu.Unlock()
	w := newWorkspace(t).withProject(srv, nil)

	w.write("policy.yaml", `schema: glossa.check-policy/v1
fail_on: error
missing_translations: error
require_complete: [de, en, fr]
rules:
  - {layer: source, severity: off}
  - {layer: terminology, severity: error}
`)
	var out policyDiffJSON
	w.json(&out, "policy", "diff", "--file", "policy.yaml").want(t, ExitOK)
	if out.Schema != policyDiffSchema || out.FromVersion != 7 {
		t.Fatalf("diff = %+v", out)
	}
	if out.Impact.OpenPullRequests != 40 || out.Impact.NewlyFailing != 34 || out.Impact.Findings != 210 {
		t.Fatalf("impact = %+v", out.Impact)
	}
	// Per rule, as §4.3 asks: which rule is doing it.
	if len(out.Impact.Rules) != 1 || out.Impact.Rules[0].Rule != 1 ||
		out.Impact.Rules[0].Selector != "layer=terminology" || out.Impact.Rules[0].NewlyFailing != 34 {
		t.Fatalf("rule impact = %+v", out.Impact.Rules)
	}
	// And the document diff a reviewer reads first.
	changes := map[string]string{}
	for _, c := range out.Changes {
		changes[c.What] = c.From + " → " + c.To
	}
	if changes["require_complete"] != "de, en → de, en, fr" {
		t.Errorf("changes = %+v", changes)
	}
	if changes["missing_translations"] != "warning → error" {
		t.Errorf("changes = %+v", changes)
	}
	if changes["environments.production"] == "" {
		t.Errorf("a dropped environment isn't in the diff: %+v", changes)
	}

	h := w.run("policy", "diff", "--file", "policy.yaml")
	for _, want := range []string{"40 open pull requests would newly fail", "mode: warn", "--grace-days"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human diff has no %q:\n%s", want, h.stdout)
		}
	}
	// Nothing was stored: the version in force is the one it was.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.qa.policyVersion != 7 {
		t.Errorf("version = %d, want 7: a preview stores nothing", srv.qa.policyVersion)
	}
}

// `diff` names the pull requests it would newly fail, not only their
// number: a reader who is told "2 open pull requests would newly fail"
// has to go and find them, and one who is given #41 and its address can
// go and tell its author (RFC 0005 §4.3, §12.4).
func TestPolicyDiffNamesThePullRequestsThatWouldNewlyFail(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 7, aPolicy())
	srv.mu.Lock()
	srv.qa.impact = map[string]any{
		"findings": 12, "runs": 3, "raised": 2, "lowered": 0, "silenced": 0,
		"newly_failing": 2, "no_longer_failing": 0, "open_pull_requests": 2,
		"newly_failing_refs": []string{"feature/cart", "feature/pay", "wip"},
		"newly_failing_pull_requests": []map[string]any{
			{"ref": "feature/cart", "number": 42},
			{"ref": "feature/pay", "number": 41, "url": "https://github.com/acme/shop/pull/41"},
		},
		"rules": []map[string]any{{"rule": 0, "matched": 2, "changed": 2, "newly_failing": 2}},
	}
	srv.mu.Unlock()
	w := newWorkspace(t).withProject(srv, nil)
	w.write("policy.yaml", "schema: glossa.check-policy/v1\nfail_on: error\nrules:\n  - {layer: visual, severity: error}\n")

	var out policyDiffJSON
	w.json(&out, "policy", "diff", "--file", "policy.yaml").want(t, ExitOK)
	want := []policyPullRequestJSON{
		{Ref: "feature/cart", Number: 42},
		{Ref: "feature/pay", Number: 41, URL: "https://github.com/acme/shop/pull/41"},
	}
	if !slices.Equal(out.Impact.PullRequests, want) {
		t.Fatalf("pull requests = %+v, want %+v", out.Impact.PullRequests, want)
	}

	h := w.run("policy", "diff", "--file", "policy.yaml")
	for _, line := range []string{
		"2 open pull requests would newly fail",
		"#41 feature/pay  https://github.com/acme/shop/pull/41",
		"#42 feature/cart",
		// A ref with no pull request is still somebody's branch going red.
		"other refs that turn red: wip",
	} {
		if !strings.Contains(h.stdout, line) {
			t.Errorf("human diff has no %q:\n%s", line, h.stdout)
		}
	}
}

func TestPolicyImportDryRunStoresNothing(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 3, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)
	w.write("policy.yaml", "schema: glossa.check-policy/v1\nfail_on: warning\nmissing_translations: error\nrequire_complete: null\n")

	var out policyImportJSON
	w.json(&out, "policy", "import", "--file", "policy.yaml", "--dry-run").want(t, ExitOK)
	if !out.DryRun {
		t.Fatalf("import --dry-run = %+v", out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.qa.policyVersion != 3 {
		t.Errorf("version = %d, want 3", srv.qa.policyVersion)
	}
}

// Wave 5 made policy-as-code work from CI, so the import has to work
// with the credential a GitHub Actions job gets: no secret in the
// repository, nothing to rotate.
func TestPolicyImportWorksWithACITokenInGitHubActions(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 1, aPolicy())
	runner := newFakeRunner(t)
	w := newWorkspace(t).withProject(srv, nil).inActions(runner)
	w.write("policy.yaml", "schema: glossa.check-policy/v1\nfail_on: error\nmissing_translations: error\nrequire_complete: [de]\n")

	w.run("login").want(t, ExitOK)
	if w.env["GLOSSA_TOKEN"] != "" {
		t.Fatal("the workspace still has an API token")
	}
	var out policyImportJSON
	w.json(&out, "policy", "import", "--file", "policy.yaml", "--grace-days", "30").want(t, ExitOK)
	if out.Version != 2 {
		t.Fatalf("import = %+v", out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.saved) != 1 || srv.qa.saved[0]["require_complete"] != "listed" {
		t.Fatalf("the document the CI job sent = %+v", srv.qa.saved)
	}
}

// A file read from stdin is the other CI shape: `cat policy.yaml |
// glossa policy import --file -`.
func TestPolicyImportReadsStdin(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 1, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)
	w.stdin = "fail_on: never\nmissing_translations: warning\nrequire_complete: []\n"

	var out policyImportJSON
	w.json(&out, "policy", "import", "--file", "-").want(t, ExitOK)
	if out.Policy.FailOn != checkpolicy.Never {
		t.Fatalf("import = %+v", out.Policy)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	// require_complete: [] is "no locale", not "every locale": the two
	// are opposite policies and must not collapse on the way to the wire.
	if srv.qa.saved[0]["require_complete"] != "listed" {
		t.Fatalf("sent = %+v", srv.qa.saved[0])
	}
	if locales, ok := srv.qa.saved[0]["locales"].([]any); !ok || len(locales) != 0 {
		t.Fatalf("locales sent = %+v", srv.qa.saved[0]["locales"])
	}
}

// The grace a save starts is what protects the people who did not cause
// the change, so a save that started one says so.
func TestPolicyImportReportsTheRolloutItStarted(t *testing.T) {
	srv := newFakeServer(t)
	storedPolicy(srv, 4, aPolicy())
	w := newWorkspace(t).withProject(srv, nil)
	w.write("policy.yaml", "fail_on: error\nmissing_translations: error\nrequire_complete: null\n")

	var out policyImportJSON
	w.json(&out, "policy", "import", "--file", "policy.yaml").want(t, ExitOK)
	if out.Version != 5 {
		t.Fatalf("import = %+v", out)
	}
	// The server answered no grace, so none is claimed: a rollout nobody
	// started is not printed, and a CLI that invented one would tell
	// forty pull-request authors they were safe when they were not.
	if out.Grace != nil {
		t.Errorf("grace = %+v, want none", out.Grace)
	}
}
