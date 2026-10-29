package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditFast polls the fake job in milliseconds.
var auditFast = []string{"--poll-interval", "1ms"}

func auditWorkspace(t *testing.T, n int) (*fakeServer, *fakeAuditTrail, *workspace) {
	t.Helper()
	srv, w := seeded(t)
	return srv, srv.auditTrail(t, n), w
}

func TestAuditListPagesAndFilters(t *testing.T) {
	_, tr, w := auditWorkspace(t, 250)

	var doc auditListJSON
	w.json(&doc, "audit", "list", "--limit", "0").want(t, ExitOK)
	if doc.Schema != "glossa.cli.audit.list/v1" || doc.Count != 250 || doc.More || len(doc.Entries) != 250 ||
		doc.Entries[0].Sequence != 250 || doc.Entries[249].Sequence != 1 {
		t.Fatalf("all = schema %s count %d more %v first %d", doc.Schema, doc.Count, doc.More, doc.Entries[0].Sequence)
	}
	if len(tr.queries) != 3 {
		t.Errorf("250 entries took %d requests, want 3 pages: %q", len(tr.queries), tr.queries)
	}

	// --limit stops paging and says there is more.
	tr.queries = nil
	w.json(&doc, "audit", "list", "--limit", "120").want(t, ExitOK)
	if doc.Count != 120 || !doc.More || doc.Entries[0].Sequence != 250 || len(tr.queries) != 2 {
		t.Errorf("limit 120 = count %d more %v requests %d", doc.Count, doc.More, len(tr.queries))
	}
	w.json(&doc, "audit", "list", "--limit", "250").want(t, ExitOK)
	if doc.Count != 250 || doc.More {
		t.Errorf("limit equal to the trail = count %d more %v", doc.Count, doc.More)
	}

	// Filters travel as the API's query.
	tr.queries = nil
	w.json(&doc, "audit", "list", "--actor", "person:0190a1b2-0000-7000-8000-000000000002", "--action", "identity.person.signed_in",
		"--source", "direct", "--from", "2026-10-01", "--to", "2026-10-02T00:00:00Z", "--project", "prj_1").want(t, ExitOK)
	q := tr.queries[0]
	for _, want := range []string{"actor=person%3A0190a1b2-0000-7000-8000-000000000002", "action=identity.person.signed_in",
		"source=direct", "from=2026-10-01T00%3A00%3A00Z", "to=2026-10-02T00%3A00%3A00Z", "project=prj_1"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %q", q, want)
		}
	}
	if doc.Count != 0 { // signed-in entries carry no project
		t.Errorf("project + direct = %d entries", doc.Count)
	}

	r := w.run("audit", "list", "--limit", "3")
	r.want(t, ExitOK)
	for _, want := range []string{"SEQ", "250", "localization.translation.revised", "translation:tr_249", "more entries match"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("table lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestAuditListUsageAndRefusals(t *testing.T) {
	srv, tr, w := auditWorkspace(t, 3)
	var e errorDoc
	for _, args := range [][]string{
		{"audit", "list", "--csv", "--json"},
		{"audit", "list", "--from", "yesterday"},
		{"audit", "list", "--source", "mars"},
		{"audit", "list", "--limit", "-1"},
		{"audit", "list", "--out", "x"},
		{"audit", "list", "extra"},
		{"audit", "export", "--actor", "x"},
	} {
		w.json(&e, args...).want(t, ExitUsage)
	}
	if e.Error.Code != "invalid_usage" {
		t.Errorf("code = %q", e.Error.Code)
	}

	tr.refuse = true
	w.json(&e, "audit", "list").want(t, ExitNetwork)
	if !strings.Contains(e.Error.Fix, "audit.read") {
		t.Errorf("403: %+v", e.Error)
	}
	srv.close()
	w.json(&e, "audit", "list").want(t, ExitNetwork)
}

// The CSV of `audit list` is the CSV of the export's lines: one column
// order, one spelling, and it is pinned by a golden file.
func TestAuditCSVGoldenAndConsistency(t *testing.T) {
	srv, tr, w := auditWorkspace(t, 5)

	listed := w.run("audit", "list", "--csv", "--limit", "0")
	listed.want(t, ExitOK)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		updateAuditGolden(t, listed.stdout)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "audit", "entries.csv"))
	if err != nil {
		t.Fatal(err)
	}
	// list is newest first; the export is chain order. Same rows.
	rows := strings.Split(strings.TrimSuffix(listed.stdout, "\n"), "\n")
	asc := []string{rows[0]}
	for i := len(rows) - 1; i >= 1; i-- {
		asc = append(asc, rows[i])
	}
	if got := strings.Join(asc, "\n") + "\n"; got != string(golden) {
		t.Errorf("list --csv differs from testdata/audit/entries.csv:\n%s", got)
	}

	w.run("audit", "export", "--first-sequence", "1", "--out", "ex", "--public-key", tr.publicPair()).want(t, ExitOK)
	for _, from := range []string{"ex", filepath.Join("ex", "entries.jsonl")} {
		r := w.run("audit", "csv", from)
		r.want(t, ExitOK)
		if r.stdout != string(golden) {
			t.Errorf("audit csv %s differs from the golden file:\n%s", from, r.stdout)
		}
	}

	var doc auditCSVJSON
	w.json(&doc, "audit", "csv", "ex", "--out", "ex.csv").want(t, ExitOK)
	if doc.Schema != "glossa.cli.audit.csv/v1" || doc.Rows != 5 || doc.Out != "ex.csv" || strings.Join(doc.Columns, ",") != strings.Join(auditCSVColumns, ",") {
		t.Errorf("csv json = %+v", doc)
	}
	if w.read("ex.csv") != string(golden) {
		t.Errorf("--out file differs")
	}

	// Cells hold identifiers and shapes, never text.
	if strings.Contains(string(golden), "Zur Kasse") || !strings.Contains(string(golden), `string(len=12)`) {
		t.Errorf("csv has text or lost the shape")
	}

	var e errorDoc
	w.json(&e, "audit", "csv").want(t, ExitUsage)
	w.json(&e, "audit", "csv", "nowhere", "--out", "n.csv").want(t, ExitUsage)
	if e.Error.Code != "export_unreadable" {
		t.Errorf("missing export: %+v", e.Error)
	}
	w.json(&e, "audit", "csv", "ex").want(t, ExitUsage) // --json without --out
	w.write("bad.jsonl", "not json\n")
	w.json(&e, "audit", "csv", "bad.jsonl", "--out", "bad.csv").want(t, ExitUsage)
	if e.Error.Code != "entries_unreadable" || !strings.Contains(e.Error.Message, "line 1") {
		t.Errorf("bad line: %+v", e.Error)
	}
	_ = srv
}

func TestAuditExportDownloadsAndVerifies(t *testing.T) {
	_, tr, w := auditWorkspace(t, 12)

	var doc auditExportJSON
	args := append([]string{"audit", "export", "--first-sequence", "3", "--last-sequence", "10", "--out", "ex", "--public-key", tr.publicPair()}, auditFast...)
	w.json(&doc, args...).want(t, ExitOK)
	if doc.Schema != "glossa.cli.audit.export/v1" || !doc.Waited || doc.Job.State != "succeeded" || doc.Job.KeyID != "audit-test" ||
		*doc.Job.FirstSequence != 3 || *doc.Job.LastSequence != 10 || *doc.Job.EntryCount != 8 || len(doc.Files) != 2 {
		t.Fatalf("export = %+v", doc)
	}
	if doc.Verification == nil || !doc.Verification.OK || doc.Verification.Verified != 8 || doc.Verification.FirstSequence != 3 {
		t.Fatalf("verification = %+v", doc.Verification)
	}
	for _, f := range doc.Files {
		if !f.Verified || f.SHA256 == "" || f.Size == 0 {
			t.Errorf("file = %+v", f)
		}
	}
	if len(tr.posts) != 1 || tr.posts[0] == "" {
		t.Errorf("job creation carried idempotency keys %q", tr.posts)
	}
	// What was downloaded verifies offline, with the verify command.
	w.run("audit", "verify", "ex", "--public-key", tr.publicPair()).want(t, ExitOK)

	// A time range; the human report without a key says how to trust it.
	r := w.run(append([]string{"audit", "export", "--from", "2026-10-01", "--to", "2026-10-02", "--out", "t"}, auditFast...)...)
	r.want(t, ExitOK)
	for _, want := range []string{": 12 entries, sequences 1–12", "nothing here says they are authentic", "/.well-known/glossa-audit-keys.json", "glossa audit verify t --public-key"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, r.stdout)
		}
	}

	// An export is never overwritten silently.
	var e errorDoc
	w.json(&e, append([]string{"audit", "export", "--first-sequence", "1", "--out", "t"}, auditFast...)...).want(t, ExitUsage)
	if e.Error.Code != "output_exists" {
		t.Errorf("existing: %+v", e.Error)
	}
	w.run(append([]string{"audit", "export", "--first-sequence", "1", "--out", "t", "--force"}, auditFast...)...).want(t, ExitOK)
}

// glossa.cli.audit.list/v1 is pinned: scripts parse it.
func TestAuditListJSONGolden(t *testing.T) {
	_, _, w := auditWorkspace(t, 3)
	r := w.run("audit", "list", "--limit", "2", "--json")
	r.want(t, ExitOK)
	path := filepath.Join("testdata", "audit", "list.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(r.stdout), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.stdout != string(golden) {
		t.Errorf("audit list --json differs from %s:\n%s", path, r.stdout)
	}
}

func TestAuditExportNoWaitAndJob(t *testing.T) {
	_, tr, w := auditWorkspace(t, 4)
	var doc auditExportJSON
	w.json(&doc, "audit", "export", "--first-sequence", "1", "--no-wait").want(t, ExitOK)
	if doc.Waited || doc.Job.State != "queued" || len(doc.Files) != 0 {
		t.Fatalf("no-wait = %+v", doc)
	}
	args := append([]string{"audit", "export", "--job", doc.Job.ID, "--out", "later", "--public-key", tr.publicPair()}, auditFast...)
	w.json(&doc, args...).want(t, ExitOK)
	if doc.Verification == nil || !doc.Verification.OK || len(tr.posts) != 1 {
		t.Errorf("--job = %+v posts %d", doc.Verification, len(tr.posts))
	}
}

func TestAuditExportFailures(t *testing.T) {
	_, tr, w := auditWorkspace(t, 6)
	var e errorDoc
	k := tr.publicPair()

	// A tampered chain downloads (the digests match) but does not verify: exit 1.
	tr.tamper = true
	var doc auditExportJSON
	w.json(&doc, append([]string{"audit", "export", "--first-sequence", "1", "--out", "bad", "--public-key", k}, auditFast...)...).want(t, ExitCheckFailed)
	if doc.Verification == nil || doc.Verification.OK || doc.Verification.Failure == nil {
		t.Errorf("tampered = %+v", doc.Verification)
	}
	tr.tamper = false

	// Corrupted in transit: nothing is written for it.
	tr.corrupt = true
	w.json(&e, append([]string{"audit", "export", "--first-sequence", "1", "--out", "corrupt"}, auditFast...)...).want(t, ExitNetwork)
	if e.Error.Code != "download_corrupted" {
		t.Errorf("corrupt: %+v", e.Error)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "corrupt", "entries.jsonl")); err == nil {
		t.Errorf("a corrupted download was kept")
	}
	tr.corrupt = false

	// A time range around imported history.
	tr.notContiguous = true
	w.json(&e, append([]string{"audit", "export", "--from", "2026-10-01", "--to", "2026-10-02", "--out", "nc"}, auditFast...)...).want(t, ExitUsage)
	if e.Error.Code != "range_not_contiguous" || !strings.Contains(e.Error.Fix, "--first-sequence") {
		t.Errorf("range_not_contiguous: %+v", e.Error)
	}
	tr.notContiguous = false

	tr.unavailable = true
	w.json(&e, "audit", "export", "--first-sequence", "1", "--out", "u").want(t, ExitNetwork)
	if e.Error.Code != "audit_export_unavailable" || !strings.Contains(e.Error.Fix, "GLOSSA_AUDIT_SIGNING_KEY") {
		t.Errorf("unavailable: %+v", e.Error)
	}
	tr.unavailable = false

	tr.refuse = true
	w.json(&e, "audit", "export", "--first-sequence", "1", "--out", "u").want(t, ExitNetwork)
	if !strings.Contains(e.Error.Fix, "audit.export") || !strings.Contains(e.Error.Fix, "owner") {
		t.Errorf("403: %+v", e.Error)
	}
	tr.refuse = false

	w.json(&e, "audit", "export", "--first-sequence", "99", "--out", "u").want(t, ExitUsage)
	if e.Error.Code != "sequence_out_of_range" {
		t.Errorf("past the head: %+v", e.Error)
	}
	w.json(&e, "audit", "export", "--from", "2026-01-01", "--to", "2026-06-01", "--out", "u").want(t, ExitUsage)
	if e.Error.Code != "range_too_long" || !strings.Contains(e.Error.Fix, "31 days") {
		t.Errorf("too long: %+v", e.Error)
	}
}

// updateAuditGolden rewrites testdata/audit/entries.csv (chain order)
// from `audit list --csv` (newest first): UPDATE_GOLDEN=1 go test -run TestAuditCSVGolden.
func updateAuditGolden(t *testing.T, listed string) {
	t.Helper()
	rows := strings.Split(strings.TrimSuffix(listed, "\n"), "\n")
	asc := []string{rows[0]}
	for i := len(rows) - 1; i >= 1; i-- {
		asc = append(asc, rows[i])
	}
	if err := os.WriteFile(filepath.Join("testdata", "audit", "entries.csv"), []byte(strings.Join(asc, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAuditExportUsage(t *testing.T) {
	_, tr, w := auditWorkspace(t, 2)
	var e errorDoc
	for _, args := range [][]string{
		{"audit", "export", "--out", "x"},
		{"audit", "export", "--first-sequence", "1"},
		{"audit", "export", "--from", "2026-10-01", "--out", "x"},
		{"audit", "export", "--from", "2026-10-02", "--to", "2026-10-01", "--out", "x"},
		{"audit", "export", "--from", "2026-10-01", "--to", "2026-10-02", "--first-sequence", "1", "--out", "x"},
		{"audit", "export", "--first-sequence", "5", "--last-sequence", "2", "--out", "x"},
		{"audit", "export", "--first-sequence", "1", "--no-wait", "--out", "x"},
		{"audit", "export", "--job", "0190a1b2-0000-7000-8000-00000000aaaa", "--from", "2026-10-01", "--out", "x"},
		{"audit", "export", "--first-sequence", "1", "--out", "x", "--public-key", "nonsense"},
		{"audit", "export", "--first-sequence", "1", "--out", "x", "--limit", "5"},
	} {
		w.json(&e, args...).want(t, ExitUsage)
	}
	if len(tr.posts) != 0 {
		t.Errorf("a usage mistake created %d jobs", len(tr.posts))
	}
}
