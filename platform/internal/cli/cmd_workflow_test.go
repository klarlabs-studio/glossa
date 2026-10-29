package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// reviewDoc is a small valid glossa.workflow/v1 document; extra
// top-level members change it without making it invalid.
func reviewDoc(name string, extra ...string) string {
	doc := `{
  "schema": "glossa.workflow/v1",
  "name": "` + name + `",
  "subject": "translation",
  "chart": {"id": "` + name + `", "initial": "current", "states": {
    "current": {"id": "current", "type": "atomic", "transitions": [{"event": "translation.revised", "target": "done"}]},
    "done": {"id": "done", "type": "final"}}},
  "guards": {"decided": {"use": "review_state_in", "states": ["approved", "rejected"]}},
  "actions": {"approve": {"use": "set_review_state", "state": "approved"}}`
	for _, e := range extra {
		doc += ",\n  " + e
	}
	return doc + "\n}\n"
}

func TestWorkflowLintPrintsEveryFindingAndFailsOnErrors(t *testing.T) {
	_, w := seeded(t)
	w.write("ok.json", reviewDoc("review", `"note": "consider a due date"`))
	var ok workflowLintDoc
	w.json(&ok, "workflow", "lint", "ok.json").want(t, ExitOK)
	if ok.Schema != "glossa.cli.workflow/v1" || ok.Action != "lint" || !ok.Valid || ok.Errors != 0 ||
		len(ok.Findings) != 1 || ok.Findings[0].Severity != "info" || ok.Findings[0].State != "current" {
		t.Fatalf("lint ok = %+v", ok)
	}

	w.write("bad.json", strings.Replace(reviewDoc("review"), `"use": "review_state_in"`, `"use": "legal_signed_off"`, 1))
	var bad workflowLintDoc
	w.json(&bad, "workflow", "lint", "bad.json").want(t, ExitCheckFailed)
	if bad.Valid || bad.Errors != 1 || len(bad.Findings) != 1 {
		t.Fatalf("lint bad = %+v", bad)
	}
	f := bad.Findings[0]
	if f.Rule != "unknown-primitive" || f.Severity != "error" || f.Path != "guards.decided" || !strings.Contains(f.Message, "legal_signed_off") {
		t.Fatalf("finding = %+v", f)
	}
	r := w.run("workflow", "lint", "bad.json")
	r.want(t, ExitCheckFailed)
	if !strings.Contains(r.stdout, "a save would be refused") ||
		!strings.Contains(r.stdout, "error   unknown-primitive  guards.decided: \"legal_signed_off\" is not a primitive") {
		t.Fatalf("human lint:\n%s", r.stdout)
	}

	// Not a document: refused before any request.
	w.write("list.json", `[1, 2]`)
	var e errorDoc
	w.json(&e, "workflow", "lint", "list.json").want(t, ExitUsage)
	if e.Error.Code != "invalid_workflow_file" {
		t.Fatalf("error = %+v", e)
	}
	w.json(&e, "workflow", "lint", "missing.json").want(t, ExitUsage)
	if e.Error.Code != "workflow_file_unreadable" {
		t.Fatalf("error = %+v", e)
	}
}

func TestWorkflowPushCreatesSavesAndIsIdempotent(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))

	var created workflowPushDoc
	w.json(&created, "workflow", "push", "review.json").want(t, ExitOK)
	d := created.Definition
	if created.Schema != workflowSchema || created.Action != "push" || created.Result != "created" || d == nil ||
		d.Name != "review" || d.Version != 1 || d.ProjectID != nil || d.Subject != "translation" {
		t.Fatalf("push = %+v (%+v)", created, d)
	}

	// The same document again — reformatted — saves nothing.
	var doc map[string]any
	_ = json.Unmarshal([]byte(reviewDoc("review")), &doc)
	compact, _ := json.Marshal(doc)
	w.write("review.json", string(compact))
	var again workflowPushDoc
	w.json(&again, "workflow", "push", "review.json").want(t, ExitOK)
	if again.Result != "unchanged" || again.Definition.Version != 1 || srv.wf.saves != 1 {
		t.Fatalf("second push = %+v (saves %d)", again, srv.wf.saves)
	}

	// A change is the next version, saved under If-Match on version 1.
	w.write("review.json", reviewDoc("review", `"note": "v2"`))
	var saved workflowPushDoc
	w.json(&saved, "workflow", "push", "review.json").want(t, ExitOK)
	if saved.Result != "saved" || saved.Definition.Version != 2 || len(saved.Findings) != 1 {
		t.Fatalf("third push = %+v", saved)
	}

	// --project makes it the project's own: another definition.
	var own workflowPushDoc
	w.json(&own, "workflow", "push", "review.json", "--project", "shop").want(t, ExitOK)
	if own.Result != "created" || own.Definition.ProjectID == nil || *own.Definition.ProjectID != "prj_1" {
		t.Fatalf("project push = %+v", own)
	}

	r := w.run("workflow", "push", "review.json")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "review is unchanged: version 2 is this document") {
		t.Fatalf("human push:\n%s", r.stdout)
	}
}

func TestWorkflowPushNeverOverwritesANewerVersion(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	w.write("review.json", reviewDoc("review", `"note": "theirs"`))
	w.run("workflow", "push", "review.json").want(t, ExitOK)

	// Edited from version 1, while someone saved version 2.
	w.write("mine.json", reviewDoc("review", `"note": "mine"`))
	var e errorDoc
	w.json(&e, "workflow", "push", "mine.json", "--if-version", "1").want(t, ExitNetwork)
	if e.Error.Code != "precondition_failed" || !strings.Contains(e.Error.Message, "version 1 is not the latest") ||
		!strings.Contains(e.Error.Why, "nothing was saved") || !strings.Contains(e.Error.Fix, "glossa workflow pull review") {
		t.Fatalf("stale push = %+v", e)
	}
	if n := len(srv.wf.defs[0].versions); n != 2 {
		t.Fatalf("versions = %d, want 2", n)
	}
	// Based on the latest, it saves.
	w.run("workflow", "push", "mine.json", "--if-version", "2").want(t, ExitOK)
	// --if-version on a definition that doesn't exist is the same refusal.
	w.write("new.json", reviewDoc("other"))
	w.json(&e, "workflow", "push", "new.json", "--if-version", "3").want(t, ExitNetwork)
	if e.Error.Code != "precondition_failed" {
		t.Fatalf("missing push = %+v", e)
	}
}

func TestWorkflowPushRefusedDocumentPrintsTheFindings(t *testing.T) {
	_, w := seeded(t)
	w.write("bad.json", strings.Replace(reviewDoc("review"), `"use": "set_review_state"`, `"use": "webhook"`, 1))
	var out workflowPushDoc
	w.json(&out, "workflow", "push", "bad.json").want(t, ExitUsage)
	if out.Result != "invalid" || out.Definition != nil || len(out.Findings) != 1 || out.Findings[0].Path != "actions.approve" {
		t.Fatalf("invalid push = %+v", out)
	}
	r := w.run("workflow", "push", "bad.json")
	r.want(t, ExitUsage)
	if !strings.Contains(r.stderr, "actions.approve: \"webhook\" is not a primitive") || !strings.Contains(r.stderr, "the server refused bad.json") {
		t.Fatalf("stderr:\n%s", r.stderr)
	}
	w.write("noname.json", `{"schema": "glossa.workflow/v1"}`)
	var e errorDoc
	w.json(&e, "workflow", "push", "noname.json").want(t, ExitUsage)
	if e.Error.Code != "invalid_workflow_file" {
		t.Fatalf("no name = %+v", e)
	}
}

func TestWorkflowTokenWithoutTheWorkflowsScopeIsToldSo(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	srv.wf.refuseManage = true

	for _, args := range [][]string{
		{"workflow", "push", "review.json", "--project", "shop"},
		{"workflow", "bind", "review"},
	} {
		var e errorDoc
		w.json(&e, args...).want(t, ExitNetwork)
		if e.Error.Code != "workflows_scope_required" || !strings.Contains(e.Error.Why, "lacks the `workflows` scope") ||
			!strings.Contains(e.Error.Why, "GitHub Actions credential never holds") ||
			!strings.Contains(e.Error.Fix, "API token with the `workflows` scope") {
			t.Fatalf("%v = %+v", args, e)
		}
	}
	// Reading still works.
	w.run("workflow", "list").want(t, ExitOK)
}

func TestWorkflowPullListAndShow(t *testing.T) {
	_, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	w.write("review.json", reviewDoc("review", `"note": "v2"`))
	w.run("workflow", "push", "review.json").want(t, ExitOK)

	// To stdout, the document itself; pushing it back changes nothing.
	r := w.run("workflow", "pull", "review", "--version", "1")
	r.want(t, ExitOK)
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || doc["name"] != "review" || doc["note"] != nil {
		t.Fatalf("pull stdout = %s (%v)", r.stdout, err)
	}
	var pulled workflowPullDoc
	w.json(&pulled, "workflow", "pull", "review", "-o", "defs/review.yaml").want(t, ExitOK)
	if pulled.Action != "pull" || pulled.Version != 2 || pulled.File != "defs/review.yaml" || pulled.Definition.Name != "review" {
		t.Fatalf("pull = %+v", pulled)
	}
	if !strings.Contains(w.read("defs/review.yaml"), "note: v2") {
		t.Fatalf("yaml = %s", w.read("defs/review.yaml"))
	}
	var round workflowPushDoc
	w.json(&round, "workflow", "push", "defs/review.yaml").want(t, ExitOK)
	if round.Result != "unchanged" {
		t.Fatalf("round trip = %+v", round)
	}

	var list workflowListDoc
	w.json(&list, "workflow", "list").want(t, ExitOK)
	if list.Action != "list" || list.ProjectID != "prj_1" || len(list.Definitions) != 1 || list.Definitions[0].Version != 2 {
		t.Fatalf("list = %+v", list)
	}

	w.run("workflow", "bind", "review", "--locales", "de").want(t, ExitOK)
	var show workflowShowDoc
	w.json(&show, "workflow", "show", "review").want(t, ExitOK)
	if show.Action != "show" || show.Version != 2 || len(show.Versions) != 2 || show.Versions[0].Version != 2 ||
		show.Document["note"] != "v2" || len(show.Bindings) != 1 || show.Bindings[0].Locales[0] != "de" {
		t.Fatalf("show = %+v", show)
	}

	var e errorDoc
	w.json(&e, "workflow", "show", "nope").want(t, ExitUsage)
	if e.Error.Code != "workflow_not_found" || !strings.Contains(e.Error.Fix, "review") {
		t.Fatalf("show nope = %+v", e)
	}
	w.json(&e, "workflow", "pull", "review", "--version", "9").want(t, ExitNetwork)
	if e.Error.Code != "not_found" {
		t.Fatalf("pull v9 = %+v", e)
	}

	// A name the tenant and the project both define needs the ID.
	w.run("workflow", "push", "review.json", "--project", "shop").want(t, ExitOK)
	w.json(&e, "workflow", "show", "review").want(t, ExitUsage)
	if e.Error.Code != "workflow_ambiguous" {
		t.Fatalf("ambiguous = %+v", e)
	}
	w.run("workflow", "show", "wfd_2").want(t, ExitOK)
}

func TestWorkflowBindAndUnbind(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.write("legal.json", reviewDoc("legal"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	w.run("workflow", "push", "legal.json").want(t, ExitOK)

	var bound workflowBindDoc
	w.json(&bound, "workflow", "bind", "review", "--locales", "fr,de", "--namespace", "checkout").want(t, ExitOK)
	b := bound.Binding
	if bound.Action != "bind" || bound.Result != "created" || b.Definition != "review" || strings.Join(b.Locales, ",") != "de,fr" ||
		b.Namespace != "checkout" || b.Position != 1 {
		t.Fatalf("bind = %+v", bound)
	}
	// Again: what a re-run CI job does. Nothing new.
	w.json(&bound, "workflow", "bind", "review", "--locales", "de", "--locales", "fr", "--namespace", "checkout").want(t, ExitOK)
	if bound.Result != "unchanged" || len(srv.wf.bindings) != 1 {
		t.Fatalf("bind again = %+v", bound)
	}
	// The same selector for another definition is a conflict.
	var e errorDoc
	w.json(&e, "workflow", "bind", "legal", "--locales", "de,fr", "--namespace", "checkout").want(t, ExitNetwork)
	if e.Error.Code != "workflow_binding_exists" || !strings.Contains(e.Error.Why, "wfd_1") || !strings.Contains(e.Error.Fix, "unbind") {
		t.Fatalf("conflict = %+v", e)
	}
	w.json(&e, "workflow", "bind", "legal", "--namespace", "no such").want(t, ExitUsage)
	if e.Error.Code != "invalid_workflow_binding" {
		t.Fatalf("invalid binding = %+v", e)
	}
	w.run("workflow", "bind", "legal").want(t, ExitOK)
	w.run("workflow", "bind", "review", "--locales", "ja").want(t, ExitOK)

	var list workflowBindingsDoc
	w.json(&list, "workflow", "bindings").want(t, ExitOK)
	if len(list.Bindings) != 3 || list.Bindings[1].Definition != "legal" || len(list.Bindings[1].Locales) != 0 {
		t.Fatalf("bindings = %+v", list)
	}

	// review is bound twice: unbind needs the selector.
	w.json(&e, "workflow", "unbind", "review").want(t, ExitUsage)
	if e.Error.Code != "binding_ambiguous" {
		t.Fatalf("ambiguous unbind = %+v", e)
	}
	var removed workflowBindDoc
	w.json(&removed, "workflow", "unbind", "review", "--locales", "ja").want(t, ExitOK)
	if removed.Action != "unbind" || removed.Result != "removed" || removed.Binding.Locales[0] != "ja" {
		t.Fatalf("unbind = %+v", removed)
	}
	w.json(&removed, "workflow", "unbind", "--binding", list.Bindings[1].ID).want(t, ExitOK)
	if removed.Binding.DefinitionID != "wfd_2" {
		t.Fatalf("unbind --binding = %+v", removed)
	}
	w.json(&e, "workflow", "unbind", "legal").want(t, ExitUsage)
	if e.Error.Code != "workflow_not_bound" {
		t.Fatalf("not bound = %+v", e)
	}
	if len(srv.wf.bindings) != 1 {
		t.Fatalf("bindings left = %d", len(srv.wf.bindings))
	}
}

func TestWorkflowInstancesAndLog(t *testing.T) {
	srv, w := seeded(t)
	w.write("review.json", reviewDoc("review"))
	w.run("workflow", "push", "review.json").want(t, ExitOK)
	srv.instance("wfi_1", "wfd_1", "done", "finished", "de", map[string]any{
		"seq": 1, "from": "current", "event": "translation.outdated", "to": "reviewing", "outcome": "applied",
		"guards": []any{}, "actions": []map[string]any{{"name": "back_to_review", "outcome": "done"}},
		"actor": "token:ci", "outbox_event_id": "evt_1", "at": fakeWorkflowTime,
	}, map[string]any{
		"seq": 2, "from": "reviewing", "event": "approval.granted", "to": "done", "outcome": "applied",
		"guards":  []map[string]any{{"guard": "one_approval", "passed": true}},
		"actions": []map[string]any{{"name": "approve", "outcome": "refused", "detail": "missing permission translations.review"}},
		"actor":   "person:rita", "at": fakeWorkflowTime,
	})
	srv.instance("wfi_2", "wfd_1", "reviewing", "active", "ja")

	var all workflowInstancesDoc
	w.json(&all, "workflow", "instances").want(t, ExitOK)
	if all.Action != "instances" || len(all.Instances) != 2 || all.Instances[0].Definition != "review" {
		t.Fatalf("instances = %+v", all)
	}
	var finished workflowInstancesDoc
	w.json(&finished, "workflow", "instances", "--status", "finished", "--definition", "review", "--locale", "de",
		"--message", "checkout.pay").want(t, ExitOK)
	if len(finished.Instances) != 1 || finished.Instances[0].ID != "wfi_1" || finished.Instances[0].SubjectID != "msg_checkout.pay" {
		t.Fatalf("finished = %+v", finished)
	}

	var log workflowLogDoc
	w.json(&log, "workflow", "log", "wfi_1").want(t, ExitOK)
	if log.Action != "log" || log.Instance.State != "done" || len(log.Transitions) != 2 {
		t.Fatalf("log = %+v", log)
	}
	t1, t2 := log.Transitions[0], log.Transitions[1]
	if t1.Event != "translation.outdated" || t1.Actions[0].Name != "back_to_review" || t1.OutboxEventID != "evt_1" ||
		t2.Guards[0].Guard != "one_approval" || !t2.Guards[0].Passed || t2.Actions[0].Outcome != "refused" || t2.Actor != "person:rita" {
		t.Fatalf("transitions = %+v", log.Transitions)
	}
	r := w.run("workflow", "log", "wfi_1")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "current --translation.outdated--> reviewing  applied  by token:ci") ||
		!strings.Contains(r.stdout, "action approve refused: missing permission translations.review") {
		t.Fatalf("human log:\n%s", r.stdout)
	}

	var e errorDoc
	w.json(&e, "workflow", "log", "wfi_9").want(t, ExitNetwork)
	if e.Error.Code != "not_found" || !strings.Contains(e.Error.Fix, "glossa workflow") {
		t.Fatalf("log 404 = %+v", e)
	}
	w.json(&e, "workflow", "instances", "--status", "running").want(t, ExitUsage)
	srv.wf.noInstances = true
	w.json(&e, "workflow", "instances").want(t, ExitNetwork)
	if e.Error.Code != "workflow_instances_unavailable" || !strings.Contains(e.Error.Fix, "instance store") {
		t.Fatalf("503 = %+v", e)
	}
}

func TestWorkflowUsageAndAuthErrors(t *testing.T) {
	_, w := seeded(t)
	var e errorDoc
	w.json(&e, "workflow").want(t, ExitUsage)
	w.json(&e, "workflow", "deploy").want(t, ExitUsage)
	if !strings.Contains(e.Error.Message, `unknown action "deploy"`) {
		t.Fatalf("unknown = %+v", e)
	}
	w.json(&e, "workflow", "push").want(t, ExitUsage)
	w.json(&e, "workflow", "list", "extra").want(t, ExitUsage)
	w.env["GLOSSA_TOKEN"] = "glossa_api_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	w.json(&e, "workflow", "list").want(t, ExitNetwork)
	if e.Error.Code != "unauthenticated" {
		t.Fatalf("401 = %+v", e)
	}
}
