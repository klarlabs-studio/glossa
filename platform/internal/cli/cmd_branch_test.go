package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// The CI commands of RFC 0004 §6.3: glossa push --branch --pr,
// glossa preview register and glossa branch status|close.

type pushDoc struct {
	Schema string `json:"schema"`
	Branch *struct {
		Name            string         `json:"name"`
		State           string         `json:"state"`
		PR              *int           `json:"pr_number"`
		HeadCommit      string         `json:"head_commit"`
		NewKeys         []string       `json:"new_keys"`
		SourceProposals []string       `json:"source_proposals"`
		Removed         []string       `json:"removed"`
		Outdated        map[string]int `json:"outdated"`
		Conflicts       []struct {
			Key      string   `json:"key"`
			Branches []string `json:"branches"`
		} `json:"conflicts"`
	} `json:"branch"`
	Messages []struct {
		Key    string `json:"key"`
		Status string `json:"status"`
	} `json:"messages"`
}

type branchDoc struct {
	Schema string `json:"schema"`
	Action string `json:"action"`
	Branch struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		State      string   `json:"state"`
		PR         *int     `json:"pr_number"`
		PreviewURL string   `json:"preview_url"`
		NewKeys    []string `json:"new_keys"`
	} `json:"branch"`
}

// live adds a message to the project's live catalog.
func (f *fakeServer) live(t *testing.T, key, text string) {
	t.Helper()
	c, err := mfcontent.Parse(mfcontent.MF1, text, bcp47.MustParse(f.sourceLocale))
	if err != nil {
		t.Fatal(err)
	}
	f.messages[key] = &fakeMessage{key: key, content: c, revision: 1, state: "active"}
}

func TestPushBranchProposesAndReportsWhatMergingWouldDo(t *testing.T) {
	srv := newFakeServer(t)
	srv.live(t, "cart.checkout", "Checkout")
	srv.live(t, "cart.items", "{count, plural, one {# item} other {# items}}")
	srv.live(t, "checkout.pay", "Pay {amount, number}")
	srv.live(t, "home.title", "Welcome") // not in the local catalog: removed
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{
  "cart": {"checkout": "Checkout", "items": "{count, plural, one {# item} other {# items}}"},
  "checkout.pay": "Pay securely {amount, number}",
  "checkout.tip": "Add a tip"
}`})

	var out pushDoc
	w.json(&out, "push", "--branch", "feature/checkout-copy", "--pr", "42", "--commit", "0a1b2c3d").want(t, ExitOK)
	if out.Branch == nil || out.Branch.Name != "feature/checkout-copy" || out.Branch.State != "open" ||
		out.Branch.PR == nil || *out.Branch.PR != 42 || out.Branch.HeadCommit != "0a1b2c3d" {
		t.Fatalf("branch = %+v", out.Branch)
	}
	if strings.Join(out.Branch.NewKeys, ",") != "checkout.tip" ||
		strings.Join(out.Branch.SourceProposals, ",") != "checkout.pay" ||
		strings.Join(out.Branch.Removed, ",") != "home.title" || out.Branch.Outdated["de"] != 1 {
		t.Errorf("report = %+v", out.Branch)
	}
	statuses := map[string]string{}
	for _, m := range out.Messages {
		statuses[m.Key] = m.Status
	}
	if statuses["checkout.tip"] != "new_key" || statuses["checkout.pay"] != "source_proposal" ||
		statuses["cart.items"] != "unchanged" {
		t.Errorf("items = %v", statuses)
	}
	// The live catalog is untouched: a branch proposes, it doesn't write.
	if srv.messages["checkout.pay"].content.Text != "Pay {amount, number}" || srv.messages["checkout.tip"] != nil {
		t.Errorf("a branch push changed the live catalog")
	}

	// The human output names the branch and the counts.
	r := w.run("push", "--branch", "feature/checkout-copy", "--pr", "42")
	r.want(t, ExitOK)
	for _, want := range []string{"on feature/checkout-copy", "1 new key", "1 source proposal", "1 removed key"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestPushBranchTakesTheBranchAndCommitFromCI(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	w.env["GITHUB_HEAD_REF"] = "feature/from-ci"
	w.env["GITHUB_SHA"] = "0123456789ab"
	w.env["PR_NUMBER"] = "7"

	var out pushDoc
	w.json(&out, "push", "--branch", "").want(t, ExitOK)
	if out.Branch != nil {
		t.Fatalf("an empty --branch pushed a branch: %+v", out.Branch)
	}
	w.json(&out, "push", "--pr", "7").want(t, ExitOK)
	if out.Branch == nil || out.Branch.Name != "feature/from-ci" || out.Branch.HeadCommit != "0123456789ab" ||
		out.Branch.PR == nil || *out.Branch.PR != 7 {
		t.Fatalf("branch = %+v", out.Branch)
	}
	if r := w.run("push", "--branch", "feature/x", "--dry-run"); r.code != int(ExitUsage) {
		t.Errorf("--dry-run with --branch: exit %d\n%s", r.code, r.stderr)
	}
}

func TestBranchStatusCloseAndPreviewRegister(t *testing.T) {
	srv := newFakeServer(t)
	srv.branches.conflicts["checkout.tip"] = []string{"feature/other"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{"checkout.tip": "Add a tip"}`})
	w.run("push", "--branch", "feature/checkout-copy", "--pr", "42").want(t, ExitOK)

	// status exits 1 on a key conflict, and says which branch holds it.
	r := w.run("branch", "status", "feature/checkout-copy")
	r.want(t, ExitCheckFailed)
	if !strings.Contains(r.stdout, "pull request #42") || !strings.Contains(r.stdout, "feature/other") {
		t.Errorf("status output:\n%s", r.stdout)
	}

	// preview register records the deployment.
	var reg struct {
		Schema string `json:"schema"`
		URL    string `json:"url"`
		Branch struct {
			PreviewURL string `json:"preview_url"`
		} `json:"branch"`
	}
	w.json(&reg, "preview", "register", "--url", "https://pr-42.preview.example.com", "--branch", "feature/checkout-copy").want(t, ExitOK)
	if reg.Schema != "glossa.cli.preview/v1" || reg.Branch.PreviewURL != "https://pr-42.preview.example.com" {
		t.Errorf("preview = %+v", reg)
	}
	if r := w.run("preview", "register", "--url", "not-a-url", "--branch", "feature/checkout-copy"); r.code == int(ExitOK) {
		t.Errorf("an invalid URL was accepted:\n%s", r.stdout)
	}

	// close, by the branch the CI environment names.
	w.env["GITHUB_HEAD_REF"] = "feature/checkout-copy"
	var out branchDoc
	w.json(&out, "branch", "close").want(t, ExitOK)
	if out.Schema != "glossa.cli.branch/v1" || out.Action != "close" || out.Branch.State != "closed" {
		t.Errorf("close = %+v", out)
	}

	// An unknown branch is a usage error, not a crash.
	if r := w.run("branch", "status", "feature/nope"); r.code != int(ExitUsage) {
		t.Errorf("unknown branch: exit %d\n%s", r.code, r.stderr)
	}
	if r := w.run("branch", "list"); r.code != int(ExitUsage) {
		t.Errorf("unknown action: exit %d", r.code)
	}
}
