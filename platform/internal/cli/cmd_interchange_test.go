package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fast polls the fake's jobs without waiting.
var fast = []string{"--poll-interval", "1ms"}

func interchange(t *testing.T) (*fakeServer, *workspace) {
	t.Helper()
	srv := newFakeServer(t)
	srv.locales = []string{"de", "en"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	return srv, w
}

func args(a ...string) []string { return append(a, fast...) }

func TestImportFileIsADryRunByDefault(t *testing.T) {
	srv, w := interchange(t)
	body := `{"checkout.pay": "Zahle {amount, number}"}`
	w.write("locales/de.json", body)

	var out fileImportJSON
	w.json(&out, args("import", "--format", "json", "locales/de.json", "--locale", "de", "--namespace", "shop")...).want(t, ExitOK)
	if out.Schema != "glossa.cli.import.job/v1" || !out.DryRun || out.Mode != "dry_run" || !out.Waited || out.Scope != "project" ||
		out.File.Path != "locales/de.json" || out.File.Size != int64(len(body)) || out.File.SHA256 != sha([]byte(body)) ||
		out.Job.State != "succeeded" || out.Job.Direction != "import" || out.Job.Summary.Created != 1 || len(out.Results) != 0 ||
		out.ResultsFilter != "problems" {
		t.Fatalf("import = %+v", out)
	}
	req := srv.io.bodies[0]
	opts := req["options"].(map[string]any)
	if req["mode"] != "dry_run" || req["format"] != "json" || req["project_id"] != "prj_1" || req["file_name"] != "de.json" ||
		opts["locale"] != "de" || opts["namespace"] != "shop" || opts["syntax"] != nil {
		t.Errorf("create body = %v", req)
	}
	j := srv.io.jobs[0]
	if string(j.upload) != body || j.uploadType != "application/octet-stream" {
		t.Errorf("upload = %q (%s)", j.upload, j.uploadType)
	}
	if srv.countRequests("POST /v1/tenants/ten_1/import-jobs") != 1 {
		t.Errorf("requests = %v", srv.requests)
	}

	h := w.run(args("import", "--format", "json", "locales/de.json", "--locale", "de")...)
	h.want(t, ExitOK)
	if !strings.Contains(h.stdout, "Dry run: locales/de.json (json, 42 B) into shop — nothing was changed") ||
		!strings.Contains(h.stdout, "messages: would be 1 created") || !strings.Contains(h.stdout, "--apply") {
		t.Errorf("human output:\n%s", h.stdout)
	}
	if !strings.Contains(h.stderr, "uploaded de.json (42 B)") || !strings.Contains(h.stderr, "succeeded") {
		t.Errorf("progress:\n%s", h.stderr)
	}
}

func TestImportFileAppliesAndListsConflictsWithLocations(t *testing.T) {
	srv, w := interchange(t)
	w.write("de.xlf", "<xliff/>")
	srv.io.importResults = []map[string]any{
		{"seq": 1, "kind": "message", "key": "checkout.pay", "status": "unchanged", "line": 4, "column": 7},
		{"seq": 2, "kind": "translation", "key": "checkout.pay", "locale": "de", "status": "conflict",
			"code": "approved_translation_conflict", "detail": "de is approved with other text", "line": 9, "column": 11},
		{"seq": 3, "kind": "translation", "key": "cart.items", "locale": "de", "status": "invalid", "code": "structural_qa_failed",
			"detail": "missing argument count", "line": 14},
	}
	var out fileImportJSON
	w.json(&out, args("import", "--format", "xliff", "de.xlf", "--apply")...).want(t, ExitCheckFailed)
	if out.Mode != "merge" || out.DryRun || len(out.Results) != 2 || out.Results[0].Location != "de.xlf:9:11" ||
		out.Results[0].Code != "approved_translation_conflict" || out.Results[1].Location != "de.xlf:14" ||
		out.Job.Summary.Conflict != 1 || out.Job.Summary.ByKind["translation"].Invalid != 1 {
		t.Fatalf("import --apply = %+v", out)
	}
	h := w.run(args("import", "--format", "xliff", "de.xlf", "--apply", "--syntax", "mf1")...)
	h.want(t, ExitCheckFailed)
	for _, want := range []string{
		"de.xlf:9:11: conflict translation de checkout.pay  approved_translation_conflict: de is approved with other text",
		"de.xlf:14: invalid translation de cart.items  structural_qa_failed: missing argument count",
		"translations: 1 conflict · 1 invalid",
	} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, h.stdout)
		}
	}
	// The same file applied again reuses the first result.
	if !strings.Contains(h.stdout, "the same file and options were imported by job") {
		t.Errorf("reuse not reported:\n%s", h.stdout)
	}

	var all fileImportJSON
	w.json(&all, args("import", "--format", "xliff", "de.xlf", "--all-results")...).want(t, ExitCheckFailed)
	if all.ResultsFilter != "all" || len(all.Results) != 3 || all.Results[0].Location != "de.xlf:4:7" {
		t.Errorf("--all-results = %+v", all.Results)
	}

	w.json(&out, args("import", "--format", "xliff", "de.xlf", "--overwrite")...).want(t, ExitCheckFailed)
	if out.Mode != "overwrite" {
		t.Errorf("--overwrite mode = %q", out.Mode)
	}
}

func TestImportFileReportsReuse(t *testing.T) {
	_, w := interchange(t)
	w.write("m.tmx", "<tmx/>")
	var first, second fileImportJSON
	w.json(&first, args("tm", "import", "m.tmx", "--apply")...).want(t, ExitOK)
	w.json(&second, args("tm", "import", "m.tmx", "--apply")...).want(t, ExitOK)
	if first.Job.ReusedJobID != "" || second.Job.ReusedJobID != first.Job.ID || second.Format != "tmx" {
		t.Fatalf("reuse = %+v / %+v", first.Job, second.Job)
	}
}

func TestImportFilePagesThroughAllResults(t *testing.T) {
	srv, w := interchange(t)
	w.write("en.json", `{}`)
	for i := 1; i <= 250; i++ {
		srv.io.importResults = append(srv.io.importResults, map[string]any{"seq": i, "kind": "message", "key": "k", "status": "unchanged"})
	}
	var out fileImportJSON
	w.json(&out, args("import", "--format", "json", "en.json", "--all-results")...).want(t, ExitOK)
	if len(out.Results) != 250 || out.Results[249].Seq != 250 {
		t.Fatalf("results = %d", len(out.Results))
	}
	if n := srv.countRequests("GET /v1/tenants/ten_1/import-jobs/" + out.Job.ID + "/results"); n != 3 {
		t.Errorf("result pages = %d, want 3", n)
	}
}

func TestImportFileJobFailureExitsFour(t *testing.T) {
	srv, w := interchange(t)
	w.write("broken.po", "msgid")
	srv.io.importFailure = "malformed_file"
	srv.io.importResults = []map[string]any{{"seq": 1, "kind": "message", "key": "", "status": "invalid", "code": "malformed_file",
		"detail": "unexpected end of file", "line": 1, "column": 6}}
	var out fileImportJSON
	w.json(&out, args("import", "--format", "po", "broken.po", "--locale", "de", "--plural-variable", "n")...).want(t, ExitPartial)
	if out.Job.State != "failed" || out.Job.FailureCode != "malformed_file" || len(out.Results) != 1 || out.Results[0].Location != "broken.po:1:6" {
		t.Fatalf("failed import = %+v", out)
	}
	h := w.run(args("import", "--format", "po", "broken.po")...)
	h.want(t, ExitPartial)
	if !strings.Contains(h.stdout, "failed: malformed_file") || !strings.Contains(h.stdout, "broken.po:1:6: invalid") {
		t.Errorf("human output:\n%s", h.stdout)
	}
}

func TestImportFileRefusalsAndServerErrors(t *testing.T) {
	srv, w := interchange(t)
	w.write("t.tbx", "<tbx/>")
	srv.io.refuseOverwrite = true
	var e errorDoc
	w.json(&e, args("terms", "import", "t.tbx", "--overwrite")...).want(t, ExitNetwork)
	if e.Error.Code != "forbidden" || !strings.Contains(e.Error.Fix, "integration.manage") {
		t.Errorf("refused overwrite = %+v", e)
	}
	// The upload stored something else than was sent.
	srv.io.corruptUpload = true
	w.json(&e, args("terms", "import", "t.tbx", "--scope", "tenant")...).want(t, ExitNetwork)
	if e.Error.Code != "upload_corrupted" {
		t.Errorf("corrupted upload = %+v", e)
	}
	last := srv.io.jobs[len(srv.io.jobs)-1]
	if last.state != "cancelled" {
		t.Errorf("the corrupted job wasn't cancelled: %s", last.state)
	}
	if last.project != nil || last.format != "tbx" {
		t.Errorf("--scope tenant job = %+v", last)
	}
	srv.io.corruptUpload = false
	w.write("empty.json", "")
	w.json(&e, args("import", "--format", "json", "empty.json")...).want(t, ExitUsage)
	if e.Error.Code != "empty_file" {
		t.Errorf("empty file = %+v", e)
	}
}

func TestImportFileUsage(t *testing.T) {
	_, w := interchange(t)
	w.write("f.json", "{}")
	for _, c := range [][]string{
		{"import"},
		{"import", "--format", "csv", "f.json"},
		{"import", "--format", "json"},
		{"import", "--format", "json", "f.json", "g.json"},
		{"import", "--format", "json", "f.json", "--from", "v0"},
		{"import", "--format", "json", "f.json", "--v0-url", "http://x"},
		{"import", "--from", "v0", "--v0-url", "http://x", "--apply"},
		{"import", "--from", "v0", "--v0-url", "http://x", "f.json"},
		{"import", "--format", "json", "f.json", "--scope", "tenant"},
		{"import", "--format", "xliff", "f.json", "--syntax", "mf2"},
		{"import", "--format", "json", "f.json", "--syntax", "icu"},
		{"import", "--format", "json", "f.json", "--state", "final"},
		{"import", "--format", "json", "f.json", "--locale", "nöt"},
		{"import", "--format", "json", "f.json", "--dry-run", "--apply"},
		{"import", "--format", "json", "missing.json"},
		{"import", "--format", "json", "f.json", "--timeout", "0s"},
		{"tm", "import", "f.json", "--format", "json"},
		{"terms", "import"},
	} {
		var e errorDoc
		w.json(&e, c...).want(t, ExitUsage)
		if e.Error.Code == "" {
			t.Errorf("%v: no error code", c)
		}
	}
}

func TestImportFileNoWaitThenJobsShowWait(t *testing.T) {
	_, w := interchange(t)
	w.write("en.json", `{"a": "b"}`)
	var out fileImportJSON
	w.json(&out, "import", "--format", "json", "en.json", "--apply", "--no-wait").want(t, ExitOK)
	if out.Waited || out.Job.State != "queued" || len(out.Results) != 0 {
		t.Fatalf("--no-wait = %+v", out)
	}
	var show jobsShowJSON
	w.json(&show, args("jobs", "show", out.Job.ID, "--wait")...).want(t, ExitOK)
	if show.Schema != "glossa.cli.jobs.show/v1" || show.Job.State != "succeeded" || show.Job.Direction != "import" {
		t.Fatalf("jobs show --wait = %+v", show)
	}
}

func TestImportFileWaitTimeout(t *testing.T) {
	srv, w := interchange(t)
	srv.io.readsToFinish = 1000
	w.write("en.json", `{}`)
	var e errorDoc
	w.json(&e, "import", "--format", "json", "en.json", "--timeout", "5ms", "--poll-interval", "1ms").want(t, ExitNetwork)
	if e.Error.Code != "wait_timeout" || !strings.Contains(e.Error.Fix, "glossa jobs show") {
		t.Errorf("timeout = %+v", e)
	}
}

func TestExportDownloadsAndVerifiesTheFile(t *testing.T) {
	srv, w := interchange(t)
	var out exportJSON
	w.json(&out, args("export", "--format", "json", "--locale", "de", "--state", "approved,needs_review", "--layout", "nested", "-o", "out/de.json")...).want(t, ExitOK)
	if out.Schema != "glossa.cli.export/v1" || out.Job.State != "succeeded" || out.File == nil || !out.File.Verified ||
		out.File.Path != filepath.Join("out", "de.json") || out.File.SHA256 != sha(srv.io.exportFile) || len(out.Extracted) != 0 {
		t.Fatalf("export = %+v", out)
	}
	if got := w.read("out/de.json"); got != string(srv.io.exportFile) {
		t.Errorf("file = %q", got)
	}
	req := srv.io.bodies[0]
	opts := req["options"].(map[string]any)
	if req["project_id"] != "prj_1" || opts["layout"] != "nested" || len(opts["locales"].([]any)) != 1 || len(opts["states"].([]any)) != 2 {
		t.Errorf("create body = %v", req)
	}

	// Without -o the file takes its name in the working directory; an
	// existing directory takes it by name too.
	h := w.run(args("export", "--format", "xliff", "--locale", "de")...)
	h.want(t, ExitOK)
	if !strings.Contains(h.stdout, "to shop.xliff") || !strings.Contains(h.stdout, "SHA-256 verified") {
		t.Errorf("human output:\n%s", h.stdout)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "shop.xliff")); err != nil {
		t.Error(err)
	}
	w.run(args("export", "--format", "xliff", "-o", "out")...).want(t, ExitOK)
	if _, err := os.Stat(filepath.Join(w.dir, "out", "shop.xliff")); err != nil {
		t.Error(err)
	}
}

func TestExportRefusesACorruptedDownload(t *testing.T) {
	srv, w := interchange(t)
	srv.io.corruptDownload = true
	var e errorDoc
	w.json(&e, args("export", "--format", "json", "-o", "out/en.json")...).want(t, ExitNetwork)
	if e.Error.Code != "download_corrupted" || !strings.Contains(e.Error.Fix, "export --job") {
		t.Fatalf("corrupted = %+v", e)
	}
	entries, _ := os.ReadDir(filepath.Join(w.dir, "out"))
	if len(entries) != 0 {
		t.Errorf("a corrupted download left files: %v", entries)
	}
	// Downloaded again intact, by job.
	srv.io.corruptDownload = false
	var out exportJSON
	w.json(&out, args("export", "--job", srv.io.jobs[0].id, "-o", "out/en.json")...).want(t, ExitOK)
	if !out.File.Verified || w.read("out/en.json") != string(srv.io.exportFile) || srv.io.downloads != 2 {
		t.Errorf("export --job = %+v", out)
	}
}

func TestExportUnzipsSeveralLocales(t *testing.T) {
	srv, w := interchange(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"de.json", "en.json"} {
		f, _ := zw.Create(name)
		_, _ = f.Write([]byte(`{"k": "` + name + `"}`))
	}
	_ = zw.Close()
	srv.io.exportFile, srv.io.exportType = buf.Bytes(), "application/zip"
	var out exportJSON
	w.json(&out, args("export", "--format", "json", "--locale", "de,en", "--unzip", "-o", "locales/out")...).want(t, ExitOK)
	if len(out.Extracted) != 2 || out.Extracted[0].Path != filepath.Join("locales", "out", "de.json") || out.File.Path != "" || !out.File.Verified {
		t.Fatalf("unzip = %+v", out)
	}
	if got := w.read("locales/out/en.json"); got != `{"k": "en.json"}` {
		t.Errorf("en.json = %q", got)
	}
	// Without --unzip the zip is written as it is.
	w.run(args("export", "--format", "json", "--locale", "de,en")...).want(t, ExitOK)
	if _, err := zip.OpenReader(filepath.Join(w.dir, "shop.json.zip")); err != nil {
		t.Error(err)
	}
}

func TestExportJobFailureExitsFour(t *testing.T) {
	srv, w := interchange(t)
	srv.io.exportFailure = "not_representable"
	var out exportJSON
	w.json(&out, args("export", "--format", "json")...).want(t, ExitPartial)
	if out.Job.State != "failed" || out.Job.FailureCode != "not_representable" || out.File != nil {
		t.Fatalf("failed export = %+v", out)
	}
	h := w.run(args("export", "--format", "json")...)
	h.want(t, ExitPartial)
	if !strings.Contains(h.stdout, "--syntax mf2") {
		t.Errorf("human output:\n%s", h.stdout)
	}
}

func TestExportUsage(t *testing.T) {
	_, w := interchange(t)
	for _, c := range [][]string{
		{"export"},
		{"export", "--format", "po"},
		{"export", "--format", "xliff", "--layout", "nested"},
		{"export", "--format", "tbx", "--locale", "de"},
		{"export", "--format", "json", "--scope", "tenant"},
		{"export", "--format", "json", "--layout", "tree"},
		{"export", "--format", "json", "--unzip"},
		{"export", "--format", "json", "--no-wait", "-o", "x"},
		{"export", "--format", "json", "--state", "final"},
		{"export", "--format", "json", "extra"},
		{"export", "--job", "nope"},
		{"export", "--job", "00000000-0000-4000-9000-000000000001", "--format", "json"},
		{"tm", "export", "--format", "tbx"},
	} {
		var e errorDoc
		w.json(&e, c...).want(t, ExitUsage)
	}
}

func TestTMAndTermsExportAliases(t *testing.T) {
	srv, w := interchange(t)
	var out exportJSON
	w.json(&out, args("tm", "export", "--locale", "de", "--source-locale", "en", "-o", "memory.tmx")...).want(t, ExitOK)
	req := srv.io.bodies[0]
	opts := req["options"].(map[string]any)
	if out.Format != "tmx" || req["format"] != "tmx" || req["project_id"] != "prj_1" || opts["source_locale"] != "en" {
		t.Errorf("tm export = %+v, %v", out, req)
	}
	w.json(&out, args("terms", "export", "--scope", "tenant", "-o", "terms.tbx")...).want(t, ExitOK)
	if req := srv.io.bodies[1]; req["format"] != "tbx" || req["project_id"] != nil || out.Scope != "tenant" {
		t.Errorf("terms export = %v", req)
	}
}

func TestJobsListShowCancel(t *testing.T) {
	srv, w := interchange(t)
	w.write("en.json", `{}`)
	var imp fileImportJSON
	w.json(&imp, "import", "--format", "json", "en.json", "--no-wait").want(t, ExitOK)
	var exp exportJSON
	w.json(&exp, args("export", "--format", "json")...).want(t, ExitOK)

	var list jobsListJSON
	w.json(&list, "jobs", "list").want(t, ExitOK)
	if list.Schema != "glossa.cli.jobs.list/v1" || len(list.Jobs) != 2 || list.Jobs[0].ID != exp.Job.ID || list.Jobs[1].ID != imp.Job.ID {
		t.Fatalf("jobs list = %+v", list)
	}
	w.json(&list, "jobs", "list", "--direction", "import").want(t, ExitOK)
	if len(list.Jobs) != 1 || list.Jobs[0].Direction != "import" {
		t.Errorf("--direction import = %+v", list)
	}
	h := w.run("jobs", "list")
	if !strings.Contains(h.stdout, "3 written") || !strings.Contains(h.stdout, "queued") {
		t.Errorf("human list:\n%s", h.stdout)
	}

	var show jobsShowJSON
	w.json(&show, "jobs", "show", exp.Job.ID).want(t, ExitOK)
	if show.Job.Direction != "export" || show.Job.Written == nil || *show.Job.Written != 3 {
		t.Errorf("jobs show = %+v", show)
	}
	if h := w.run("jobs", "show", exp.Job.ID); !strings.Contains(h.stdout, "Export json job") {
		t.Errorf("human show:\n%s", h.stdout)
	}

	var cancelled jobsCancelJSON
	w.json(&cancelled, "jobs", "cancel", imp.Job.ID).want(t, ExitOK)
	if cancelled.Schema != "glossa.cli.jobs.cancel/v1" || cancelled.Job.State != "cancelled" {
		t.Errorf("cancel = %+v", cancelled)
	}
	var e errorDoc
	w.json(&e, "jobs", "cancel", exp.Job.ID).want(t, ExitNetwork)
	if e.Error.Code != "job_not_cancellable" {
		t.Errorf("cancel finished = %+v", e)
	}
	w.json(&e, "jobs", "show", "00000000-0000-4000-9000-999999999999").want(t, ExitNetwork)
	if e.Error.Code != "not_found" {
		t.Errorf("show unknown = %+v", e)
	}
	w.run("jobs", "show", "abc").want(t, ExitUsage)
	w.run("jobs", "list", "--direction", "both").want(t, ExitUsage)
	w.run("jobs", "list", "--state", "done").want(t, ExitUsage)
	w.run("jobs", "purge").want(t, ExitUsage)
	w.run("jobs").want(t, ExitUsage)
	if srv.countRequests("GET /v1/tenants/ten_1/import-jobs?") != 0 {
		t.Error("query strings leaked into paths")
	}
}
