package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// RFC 0006 §13 wave 6 on the CLI: a definition exported as its
// portable document and imported elsewhere, and an instance rebased.

func TestWorkflowExportAndImport(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	w.write("review.json", reviewDoc("review", `"note": "v2"`))
	w.run("workflow", "push", "review.json").want(t, ExitOK)

	// To stdout, the document alone: no id, no tenant, no version.
	r := w.run("workflow", "export", "review", "--version", "1")
	r.want(t, ExitOK)
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || doc["schema"] != "glossa.workflow/v1" ||
		doc["name"] != "review" || doc["note"] != nil || doc["id"] != nil {
		t.Fatalf("export stdout = %s (%v)", r.stdout, err)
	}
	var exported workflowPullDoc
	w.json(&exported, "workflow", "export", "review", "-o", "out/review.yaml").want(t, ExitOK)
	if exported.Action != "export" || exported.Version != 2 || exported.File != "out/review.yaml" {
		t.Fatalf("export = %+v", exported)
	}
	human := w.run("workflow", "export", "review", "-o", "out/review.json")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "glossa workflow import out/review.json") {
		t.Fatalf("export says:\n%s", human.stdout)
	}

	// Imported into the project: a new definition, its findings shown.
	var imported workflowPushDoc
	w.json(&imported, "workflow", "import", "out/review.yaml", "--project", "shop").want(t, ExitOK)
	if imported.Action != "import" || imported.Result != "created" || imported.Definition == nil ||
		imported.Definition.ProjectID == nil || len(imported.Findings) != 1 {
		t.Fatalf("import = %+v", imported)
	}
	// Imported again where it came from: it is the latest, so nothing.
	var again workflowPushDoc
	w.json(&again, "workflow", "import", "out/review.yaml").want(t, ExitOK)
	if again.Result != "unchanged" || again.Definition.Version != 2 {
		t.Fatalf("re-import = %+v", again)
	}
	// An edited one is the next version, under --if-version.
	w.write("out/edited.json", reviewDoc("review", `"note": "v3"`))
	var next workflowPushDoc
	w.json(&next, "workflow", "import", "out/edited.json", "--if-version", "2").want(t, ExitOK)
	if next.Result != "saved" || next.Definition.Version != 3 || srv.wf.defs[0].versions[2]["note"] != "v3" {
		t.Fatalf("import as v3 = %+v", next)
	}
	// A document the server refuses prints why and saves nothing.
	w.write("out/bad.json", strings.Replace(reviewDoc("broken"), "review_state_in", "legal_signed_off", 1))
	var refused workflowPushDoc
	w.json(&refused, "workflow", "import", "out/bad.json").want(t, ExitUsage)
	if refused.Action != "import" || refused.Result != "invalid" || len(refused.Findings) == 0 {
		t.Fatalf("refused import = %+v", refused)
	}
	var e errorDoc
	w.json(&e, "workflow", "import").want(t, ExitUsage)
	w.json(&e, "workflow", "export").want(t, ExitUsage)
}

// reviewDocWithout is reviewDoc whose waiting state is called state.
func reviewDocWithout(name, state string) string {
	return strings.ReplaceAll(reviewDoc(name), `"current"`, `"`+state+`"`)
}

func TestWorkflowRebase(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	srv.instance("wfi_1", "wfd_1", "current", "active", "de")
	srv.instance("wfi_2", "wfd_1", "done", "finished", "de")
	w.write("review.json", reviewDoc("review", `"note": "v2"`))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	w.write("review.json", reviewDocWithout("review", "drafting"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)

	// The latest (v3) lacks the state: refused, with what to do.
	var e errorDoc
	w.json(&e, "workflow", "rebase", "wfi_1").want(t, ExitUsage)
	if e.Error.Code != "workflow_rebase_state_missing" || !strings.Contains(e.Error.Fix, "current") {
		t.Fatalf("onto v3 = %+v", e)
	}
	var out workflowRebaseDoc
	w.json(&out, "workflow", "rebase", "wfi_1", "--version", "2").want(t, ExitOK)
	if out.Action != "rebase" || out.FromVersion != 1 || out.Instance.DefinitionVersion != 2 ||
		out.Instance.State != "current" || out.Instance.Definition != "review" {
		t.Fatalf("rebase = %+v", out)
	}
	var log workflowLogDoc
	w.json(&log, "workflow", "log", "wfi_1").want(t, ExitOK)
	if last := log.Transitions[len(log.Transitions)-1]; last.Event != "rebase" || last.Actions[0].Detail != "version 1 → 2" {
		t.Fatalf("log = %+v", log.Transitions)
	}
	// Not newer, finished, unknown: each says so.
	w.json(&e, "workflow", "rebase", "wfi_1", "--version", "2").want(t, ExitUsage)
	if e.Error.Code != "invalid_workflow_rebase" {
		t.Fatalf("same version = %+v", e)
	}
	w.json(&e, "workflow", "rebase", "wfi_2").want(t, ExitUsage)
	if e.Error.Code != "workflow_instance_finished" {
		t.Fatalf("finished = %+v", e)
	}
	w.json(&e, "workflow", "rebase", "wfi_9").want(t, ExitNetwork)
	if e.Error.Code != "not_found" {
		t.Fatalf("unknown = %+v", e)
	}
	r := w.run("workflow", "rebase", "wfi_1", "--version", "3")
	if r.code == int(ExitOK) {
		t.Fatal("rebased onto a version without its state")
	}
	srv.wf.refuseManage = true
	w.json(&e, "workflow", "rebase", "wfi_1", "--version", "3").want(t, ExitNetwork)
	if e.Error.Code != "workflows_scope_required" {
		t.Fatalf("without workflows.manage = %+v", e)
	}
}
