package cli

import (
	"strings"
	"testing"
)

// --locale names an XLIFF file's target locale.
func TestImportXLIFFLocale(t *testing.T) {
	srv, w := interchange(t)
	w.write("vendor.xlf", "<xliff/>")
	var out fileImportJSON
	w.json(&out, args("import", "--format", "xliff", "vendor.xlf", "--locale", "de_de")...).want(t, ExitOK)
	opts, _ := srv.io.bodies[0]["options"].(map[string]any)
	if opts["locale"] != "de-DE" || srv.io.bodies[0]["format"] != "xliff" {
		t.Errorf("create body = %v", srv.io.bodies[0])
	}
	// A file in a locale the project lacks fails; the CLI says what to do.
	srv.io.importFailure = "target_locale_mismatch"
	h := w.run(args("import", "--format", "xliff", "vendor.xlf")...)
	h.want(t, ExitPartial)
	if !strings.Contains(h.stdout, "failed: target_locale_mismatch") || !strings.Contains(h.stdout, "with --locale") {
		t.Errorf("human output:\n%s", h.stdout)
	}
}

// Every conflict and invalid item is listed as file:line:column, with
// the item's reference in the format's own terms in --json.
func TestImportResultsCarryRefs(t *testing.T) {
	srv, w := interchange(t)
	w.write("de.json", "{}")
	srv.io.importResults = []map[string]any{
		{"seq": 1, "kind": "translation", "key": "checkout.pay", "locale": "de", "status": "conflict",
			"code": "approved_translation_conflict", "line": 3, "column": 5, "ref": "/checkout/pay"},
		{"seq": 2, "kind": "translation", "key": "nope", "locale": "de", "status": "invalid",
			"code": "message_not_found", "line": 5, "column": 3, "ref": "/nope"},
	}
	var out fileImportJSON
	w.json(&out, args("import", "--format", "json", "de.json", "--locale", "de", "--apply")...).want(t, ExitCheckFailed)
	if len(out.Results) != 2 || out.Results[0].Location != "de.json:3:5" || out.Results[0].Ref != "/checkout/pay" ||
		out.Results[1].Location != "de.json:5:3" || out.Results[1].Ref != "/nope" {
		t.Fatalf("results = %+v", out.Results)
	}
	h := w.run(args("import", "--format", "json", "de.json", "--locale", "de", "--apply")...)
	if !strings.Contains(h.stdout, "de.json:3:5: conflict translation de checkout.pay  approved_translation_conflict") ||
		!strings.Contains(h.stdout, "de.json:5:3: invalid translation de nope  message_not_found") {
		t.Errorf("human output:\n%s", h.stdout)
	}
}

// Tenant-wide TMX and TBX go through the workspace's own routes, and
// `jobs list` shows the workspace's jobs beside the project's.
func TestWorkspaceKnowledgeRoutes(t *testing.T) {
	srv, w := interchange(t)
	w.write("m.tmx", "<tmx/>")
	w.write("t.tbx", "<tbx/>")
	var imp fileImportJSON
	w.json(&imp, args("tm", "import", "m.tmx", "--scope", "tenant", "--apply")...).want(t, ExitOK)
	if imp.Scope != "tenant" || srv.countRequests("POST /v1/tenants/ten_1/tm-import-jobs") != 1 ||
		srv.countRequests("POST /v1/tenants/ten_1/import-jobs") != 0 {
		t.Fatalf("tenant TMX import = %+v, requests %v", imp, srv.requests)
	}
	if req := srv.io.bodies[0]; req["mode"] != "merge" || req["file_name"] != "m.tmx" {
		t.Errorf("tm-import-jobs body = %v", req)
	}
	w.json(&imp, args("import", "--format", "tbx", "t.tbx", "--scope", "tenant")...).want(t, ExitOK)
	if srv.countRequests("POST /v1/tenants/ten_1/termbase-import-jobs") != 1 || imp.Job.ProjectID != nil {
		t.Errorf("tenant TBX import = %+v", imp)
	}

	var exp exportJSON
	w.json(&exp, args("tm", "export", "--scope", "tenant", "--source-locale", "en", "-o", "memory.tmx")...).want(t, ExitOK)
	req := srv.io.bodies[2]
	opts, _ := req["options"].(map[string]any)
	if exp.Scope != "tenant" || srv.countRequests("POST /v1/tenants/ten_1/tm-export-jobs") != 1 || opts["source_locale"] != "en" {
		t.Errorf("tenant TMX export = %+v, body %v", exp, req)
	}
	w.json(&exp, args("terms", "export", "--scope", "tenant", "-o", "terms.tbx")...).want(t, ExitOK)
	if srv.countRequests("POST /v1/tenants/ten_1/termbase-export-jobs") != 1 {
		t.Errorf("tenant TBX export requests = %v", srv.requests)
	}

	// A project's own job, and the list: the project's and the
	// workspace's, newest first.
	w.write("en.json", "{}")
	w.json(&imp, args("import", "--format", "json", "en.json")...).want(t, ExitOK)
	var list jobsListJSON
	w.json(&list, "jobs", "list").want(t, ExitOK)
	if len(list.Jobs) != 5 || list.Jobs[0].ID != imp.Job.ID || list.Jobs[len(list.Jobs)-1].Format != "tmx" {
		t.Fatalf("jobs list = %d jobs: %+v", len(list.Jobs), list.Jobs)
	}
	w.json(&list, "jobs", "list", "--limit", "2").want(t, ExitOK)
	if len(list.Jobs) != 2 || list.Jobs[0].ID != imp.Job.ID {
		t.Errorf("--limit 2 = %+v", list.Jobs)
	}
	if h := w.run("jobs", "list"); !strings.Contains(h.stdout, "workspace") {
		t.Errorf("human list doesn't mark the workspace's jobs:\n%s", h.stdout)
	}
}

func TestNamespaces(t *testing.T) {
	srv, w := interchange(t)
	srv.messages["checkout.pay"] = &fakeMessage{key: "checkout.pay", revision: 1, state: "active"}
	srv.messages["checkout.old"] = &fakeMessage{key: "checkout.old", revision: 1, state: "obsolete"}
	var out namespacesJSON
	w.json(&out, "namespaces").want(t, ExitOK)
	if out.Schema != "glossa.cli.namespaces/v1" || len(out.Namespaces) != 1 || out.Namespaces[0].Name != "default" ||
		out.Namespaces[0].Active != 1 || out.Namespaces[0].Obsolete != 1 {
		t.Fatalf("namespaces = %+v", out)
	}
	h := w.run("namespaces")
	if !strings.Contains(h.stdout, "NAMESPACE") || !strings.Contains(h.stdout, "default") || !strings.Contains(h.stdout, "1 namespace") {
		t.Errorf("human output:\n%s", h.stdout)
	}
	w.run("namespaces", "extra").want(t, ExitUsage)
}
