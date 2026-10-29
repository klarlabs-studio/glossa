package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/extract"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// extractWorkspace has a Vue file, a Go file and a Go template that use
// catalog keys, a key the catalog lacks (cart.totla), and leave
// cart.checkout unused.
func extractWorkspace(t *testing.T, srv *fakeServer) *workspace {
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	w.write("src/App.vue", `<template><p>{{ $t("cart.items", { count }) }} {{ t("cart.totla") }}</p></template>
<script setup lang="ts">
const m = useTypedMessages();
m.checkout.pay({ amount });
</script>`)
	w.write("internal/mail/mail.go", `package mail

func (s *Sender) Subject() string { return s.client.T(ctx, "cart.totla", nil) }`)
	w.write("templates/mail.gotmpl", `{{t "cart.items" "count" 1}}`)
	return w
}

func TestExtractPrintsTheUsagesDocument(t *testing.T) {
	w := extractWorkspace(t, nil)
	var doc extract.Document
	w.json(&doc, "extract", "--application", "web", "--commit", strings.ToUpper(testCommit), "--branch", "feat/copy").want(t, ExitOK)
	if doc.Schema != "glossa.usages/v1" || doc.Application != "web" || doc.Commit != testCommit || doc.Branch != "feat/copy" ||
		doc.Tool != (extract.Tool{Name: "glossa", Version: "0.0.0-dev"}) {
		t.Errorf("header = %+v", doc)
	}
	var got []string
	for _, u := range doc.Usages {
		got = append(got, u.Key+" "+u.File+" "+u.Kind+" "+u.Component)
	}
	want := []string{
		"cart.items src/App.vue t App",
		"cart.items templates/mail.gotmpl template ",
		"cart.totla internal/mail/mail.go t mail.(*Sender).Subject",
		"cart.totla src/App.vue t App",
		"checkout.pay src/App.vue accessor App",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("usages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExtractReportsUnknownAndUnused(t *testing.T) {
	w := extractWorkspace(t, nil)
	r := w.run("extract")
	r.want(t, ExitOK)
	for _, s := range []string{"5 usages of 3 messages in 3 files", "1 key not in the catalog", "cart.totla  internal/mail/mail.go:3 (+1 more)",
		"1 of 3 catalog messages look unused", "cart.checkout"} {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("output lacks %q:\n%s", s, r.stdout)
		}
	}
	w.run("extract", "--strict").want(t, ExitCheckFailed)
}

func TestExtractHeaderComesFromConfigEnvironmentAndGit(t *testing.T) {
	w := extractWorkspace(t, nil)
	var e errorDoc
	w.json(&e, "extract").want(t, ExitUsage)
	if e.Error.Code != "application_required" {
		t.Errorf("error = %+v", e.Error)
	}
	w.env["GLOSSA_APPLICATION"] = "web"
	w.json(&e, "extract").want(t, ExitUsage)
	if e.Error.Code != "commit_unknown" {
		t.Errorf("error = %+v", e.Error)
	}
	w.json(&e, "extract", "--commit", "abc123").want(t, ExitUsage)
	w.json(&e, "extract", "--commit", testCommit, "--branch", "feat/../x").want(t, ExitUsage)
	w.json(&e, "extract", "--application", "Web App").want(t, ExitUsage)

	// GitHub Actions on a pull request: the head commit and branch.
	event := filepath.Join(t.TempDir(), "event.json")
	head := strings.Repeat("b", 40)
	if err := os.WriteFile(event, []byte(`{"pull_request":{"head":{"sha":"`+head+`","ref":"feat/copy"}},"repository":{"default_branch":"trunk"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_SHA": strings.Repeat("c", 40), "GITHUB_REF": "refs/pull/7/merge",
		"GITHUB_HEAD_REF": "feat/copy", "GITHUB_EVENT_PATH": event} {
		w.env[k] = v
	}
	var doc extract.Document
	w.json(&doc, "extract").want(t, ExitOK)
	if doc.Commit != head || doc.Branch != "feat/copy" {
		t.Errorf("GitHub build = %s %s", doc.Commit, doc.Branch)
	}
}

func TestExtractReadsTheCommitFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	w := extractWorkspace(t, nil)
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = w.dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "feat/from-git")
	git("add", ".")
	git("commit", "-q", "--no-gpg-sign", "-m", "x")
	var doc extract.Document
	w.json(&doc, "extract", "--application", "web").want(t, ExitOK)
	if doc.Commit != git("rev-parse", "HEAD") || doc.Branch != "feat/from-git" {
		t.Errorf("git build = %s %s", doc.Commit, doc.Branch)
	}
}

func TestExtractUploadsTheDocument(t *testing.T) {
	srv := newFakeServer(t)
	w := extractWorkspace(t, srv)
	args := []string{"extract", "--upload", "--application", "web", "--commit", testCommit, "--branch", "main"}
	r := w.run(args...)
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "Uploaded the usages of web at 0123456789ab (main) as build bld_1") {
		t.Errorf("output:\n%s", r.stdout)
	}
	ups := srv.ctx.uploads
	if len(ups) != 1 || ups[0].source != "extract" || len(ups[0].doc.Usages) != 5 || ups[0].doc.Commit != testCommit ||
		ups[0].doc.Tool.Name != "glossa" {
		t.Fatalf("uploaded = %+v", ups)
	}
	var doc extract.Document
	w.json(&doc, args...).want(t, ExitOK)
	if doc.Application != "web" || len(doc.Usages) != 5 {
		t.Errorf("--json with --upload = %+v", doc)
	}
	if r := w.run(args...); !strings.Contains(r.stdout, "Already uploaded the usages of web") || !strings.Contains(r.stdout, "build bld_1") {
		t.Errorf("replayed upload:\n%s", r.stdout)
	}
}

func TestExtractUploadExplainsAPIErrors(t *testing.T) {
	srv := newFakeServer(t)
	w := extractWorkspace(t, srv)
	var e errorDoc
	// The server refusing the document is a usage error.
	w.json(&e, "extract", "--upload", "--application", "ios", "--commit", testCommit, "--branch", "main").want(t, ExitUsage)
	if e.Error.Code != "unknown_application" || !strings.Contains(e.Error.Why, "no application") {
		t.Errorf("error = %+v", e.Error)
	}
	w.env["GLOSSA_TOKEN"] = "glossa_api_" + strings.Repeat("B", 43)
	w.json(&e, "extract", "--upload", "--application", "web", "--commit", testCommit, "--branch", "main").want(t, ExitNetwork)
}
