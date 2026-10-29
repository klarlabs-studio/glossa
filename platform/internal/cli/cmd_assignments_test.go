package cli

import (
	"strings"
	"testing"
	"time"
)

func TestAssignmentsListsMyWorkByDefault(t *testing.T) {
	srv, w := seeded(t)
	srv.assignment("asg_1", "role:translator", "open", "checkout.pay@de", "cart.items@de")
	srv.assignment("asg_2", "vendor:ven_9", "open", "cart.checkout@ja")
	srv.assignment("asg_3", "role:translator", "done", "cart.checkout@de")

	var mine assignmentsListDoc
	w.json(&mine, "assignments").want(t, ExitOK)
	if mine.Schema != "glossa.cli.assignments/v1" || mine.Action != "list" || !mine.Mine || len(mine.Assignments) != 2 ||
		srv.wf.lastListMine != "true" {
		t.Fatalf("my work = %+v (mine=%q)", mine, srv.wf.lastListMine)
	}
	a := mine.Assignments[0]
	if a.ID != "asg_1" || a.Assignee.Kind != "role" || a.Assignee.Role != "translator" || len(a.Units) != 2 ||
		a.Units[0].MessageID != "msg_checkout.pay" || a.Units[0].Locale != "de" || a.Permission != "translations.write" {
		t.Fatalf("assignment = %+v", a)
	}

	var open assignmentsListDoc
	w.json(&open, "assignments", "list", "--state", "open").want(t, ExitOK)
	if len(open.Assignments) != 1 {
		t.Fatalf("open = %+v", open)
	}
	var all assignmentsListDoc
	w.json(&all, "assignments", "--all").want(t, ExitOK)
	if all.Mine || len(all.Assignments) != 3 || srv.wf.lastListMine != "" {
		t.Fatalf("all = %+v", all)
	}
	r := w.run("assignments")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "asg_1") || !strings.Contains(r.stdout, "role translator") {
		t.Fatalf("human list:\n%s", r.stdout)
	}

	srv.wf.mine = "member:nobody"
	r = w.run("assignments")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "Nothing is assigned to you.") || !strings.Contains(r.stdout, "an API token is none of them") {
		t.Fatalf("empty list:\n%s", r.stdout)
	}

	var e errorDoc
	w.json(&e, "assignments", "--state", "late").want(t, ExitUsage)
	w.json(&e, "assignments", "--locale", "not a locale").want(t, ExitUsage)
}

func TestAssignmentsShowAcceptCompleteDecline(t *testing.T) {
	srv, w := seeded(t)
	srv.assignment("asg_1", "role:translator", "open", "checkout.pay@de")
	srv.assignment("asg_2", "role:translator", "open", "cart.items@ja")
	srv.assignment("asg_3", "vendor:ven_9", "open", "cart.checkout@ja")

	var shown assignmentDoc
	w.json(&shown, "assignments", "show", "asg_1").want(t, ExitOK)
	if shown.Action != "show" || shown.Assignment.Units[0].Message != "checkout.pay" {
		t.Fatalf("show = %+v", shown)
	}

	var accepted assignmentDoc
	w.json(&accepted, "assignments", "accept", "asg_1").want(t, ExitOK)
	if accepted.Action != "accept" || accepted.Assignment.State != "accepted" {
		t.Fatalf("accept = %+v", accepted)
	}
	var e errorDoc
	w.json(&e, "assignments", "accept", "asg_1").want(t, ExitNetwork)
	if e.Error.Code != "assignment_state" || !strings.Contains(e.Error.Why, "accepted") || !strings.Contains(e.Error.Fix, "only an open assignment") {
		t.Fatalf("accept twice = %+v", e)
	}
	var done assignmentDoc
	w.json(&done, "assignments", "complete", "asg_1").want(t, ExitOK)
	if done.Assignment.State != "done" || done.Assignment.ClosedBy != "person:vera" || done.Assignment.ClosedAt == nil {
		t.Fatalf("complete = %+v", done)
	}
	r := w.run("assignments", "complete", "asg_1")
	r.want(t, ExitNetwork)
	if !strings.Contains(r.stderr, "can't complete assignment asg_1") {
		t.Fatalf("complete twice:\n%s", r.stderr)
	}

	var declined assignmentDoc
	w.json(&declined, "assignments", "decline", "asg_2", "--reason", "not my language").want(t, ExitOK)
	if declined.Assignment.State != "declined" || declined.Assignment.Reason != "not my language" {
		t.Fatalf("decline = %+v", declined)
	}

	// Someone else's is not there, and the message says why a token
	// never has any.
	w.json(&e, "assignments", "accept", "asg_3").want(t, ExitNetwork)
	if e.Error.Code != "not_found" || !strings.Contains(e.Error.Why, "does not exist to you") ||
		!strings.Contains(e.Error.Fix, "an API token is none of them") {
		t.Fatalf("someone else's = %+v", e)
	}
	w.json(&e, "assignments", "show").want(t, ExitUsage)
}

func TestAssignmentsCreate(t *testing.T) {
	srv, w := seeded(t)
	srv.member("vera@lingua.example", "translator")

	var created assignmentDoc
	w.json(&created, "assignments", "create", "--to", "vendor:Lingua", "--units", "checkout.pay@de,cart.items@de",
		"--due", "2026-10-09T17:00:00Z").want(t, ExitOK)
	x := created.Assignment
	if created.Action != "create" || x.State != "open" || x.Assignee.Kind != "vendor" || x.Assignee.ID != "Lingua" ||
		len(x.Units) != 2 || x.Units[0].Message != "checkout.pay" || x.DueAt == nil || !x.DueAt.Equal(time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("create = %+v", created)
	}
	if n := srv.countRequests("POST /v1/tenants/ten_1/assignments glossa-cli-"); n != 1 {
		t.Fatalf("create sent %d requests with an Idempotency-Key", n)
	}

	// A member by address.
	w.json(&created, "assignments", "create", "--to", "member:vera@lingua.example", "--units", "cart.checkout@ja",
		"--units", "cart.items@ja", "--permission", "translations.review").want(t, ExitOK)
	if created.Assignment.Assignee.Kind != "member" || created.Assignment.Assignee.ID != "mem_1" ||
		created.Assignment.Permission != "translations.review" || len(created.Assignment.Units) != 2 {
		t.Fatalf("create for a member = %+v", created)
	}

	var e errorDoc
	w.json(&e, "assignments", "create", "--to", "group:nobody", "--units", "checkout.pay@de").want(t, ExitUsage)
	if e.Error.Code != "unknown_party" || !strings.Contains(e.Error.Fix, "check --to") {
		t.Fatalf("unknown party = %+v", e)
	}
	w.json(&e, "assignments", "create", "--to", "role:translator", "--units", "no.such@de").want(t, ExitUsage)
	if e.Error.Code != "invalid_assignment" || !strings.Contains(e.Error.Why, "no.such") {
		t.Fatalf("unknown key = %+v", e)
	}
	w.json(&e, "assignments", "create", "--to", "member:ghost@example.com", "--units", "checkout.pay@de").want(t, ExitUsage)
	if e.Error.Code != "unknown_party" {
		t.Fatalf("unknown member = %+v", e)
	}
	for _, args := range [][]string{
		{"assignments", "create", "--units", "checkout.pay@de"},
		{"assignments", "create", "--to", "vendor:Lingua"},
		{"assignments", "create", "--to", "Lingua", "--units", "checkout.pay@de"},
		{"assignments", "create", "--to", "team:x", "--units", "checkout.pay@de"},
		{"assignments", "create", "--to", "vendor:Lingua", "--units", "checkout.pay"},
		{"assignments", "create", "--to", "vendor:Lingua", "--units", "checkout.pay@de", "--due", "tomorrow"},
	} {
		w.json(&e, args...).want(t, ExitUsage)
	}

	// No token scope grants assignments.manage: the refusal says so.
	srv.wf.refuseAssign = true
	w.json(&e, "assignments", "create", "--to", "vendor:Lingua", "--units", "checkout.pay@de").want(t, ExitNetwork)
	if e.Error.Code != "assignments_manage_required" || !strings.Contains(e.Error.Why, "no API token scope grants") {
		t.Fatalf("token create = %+v", e)
	}
}

func reportRow(assignee, locale string, assignments, onTime, late, units, words, approved int, ratio float64) map[string]any {
	return map[string]any{"assignee": assignee, "locale": locale, "assignments": assignments, "on_time": onTime, "late": late,
		"no_due": 0, "units": units, "unavailable": 0, "source_words": words, "tm_words": map[string]int{"exact": words / 4},
		"approved": approved, "rejected": 0, "needs_review": 0, "draft": 0, "unreviewed": units - approved,
		"changed_after_delivery": 1, "mean_edit_distance": 3.5, "mean_edit_ratio": ratio,
		"findings": map[string]int{"terminology": 1}, "findings_per_unit": 0.25, "reworked": 0, "rework_rate": 0.0, "on_time_rate": 0.5}
}

func TestAssignmentsReportIsTheServersNumbers(t *testing.T) {
	srv, w := seeded(t)
	srv.wf.reportRows = []map[string]any{
		reportRow("vendor:ven_9", "ja", 2, 1, 1, 8, 120, 6, 0.125),
		reportRow("vendor:ven_2", "de", 1, 1, 0, 4, 40, 4, 0),
	}

	r := w.run("assignments", "report", "--json")
	r.want(t, ExitOK)
	golden(t, "assignments-report.json", r.stdout)

	var doc assignmentReportDoc
	w.json(&doc, "assignments", "report", "--vendor", "ven_9", "--since", "2026-09-01T00:00:00Z").want(t, ExitOK)
	if doc.Action != "report" || len(doc.Rows) != 1 || doc.Rows[0].Assignee != "vendor:ven_9" || doc.Vendor != "ven_9" ||
		!strings.Contains(srv.wf.lastReportQuery, "vendor=ven_9") || !strings.Contains(srv.wf.lastReportQuery, "since=2026-09-01T00%3A00%3A00Z") {
		t.Fatalf("report = %+v (query %q)", doc, srv.wf.lastReportQuery)
	}

	w.json(&doc, "assignments", "report", "--project", "prj_1", "--since", "720h").want(t, ExitOK)
	if !strings.Contains(srv.wf.lastReportQuery, "project=prj_1") || !strings.Contains(srv.wf.lastReportQuery, "since=") {
		t.Fatalf("query %q", srv.wf.lastReportQuery)
	}

	human := w.run("assignments", "report")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "vendor:ven_9") || !strings.Contains(human.stdout, "FINDINGS/UNIT") || strings.Contains(human.stdout, "bound") {
		t.Fatalf("human report:\n%s", human.stdout)
	}
	srv.wf.reportTruncated = true
	if r := w.run("assignments", "report"); !strings.Contains(r.stdout, "hit a bound") {
		t.Fatalf("truncated report:\n%s", r.stdout)
	}
	srv.wf.reportRows = nil
	if r := w.run("assignments", "report"); !strings.Contains(r.stdout, "No completed assignments") {
		t.Fatalf("empty report:\n%s", r.stdout)
	}
}

func TestAssignmentsReportRefusals(t *testing.T) {
	srv, w := seeded(t)
	var e errorDoc
	w.json(&e, "assignments", "report", "--since", "yesterday").want(t, ExitUsage)
	w.json(&e, "assignments", "report", "--vendor", "Lingua").want(t, ExitUsage)
	w.json(&e, "assignments", "report", "extra").want(t, ExitUsage)

	srv.wf.refuseAssign = true
	w.json(&e, "assignments", "report").want(t, ExitNetwork)
	if e.Error.Code != "forbidden" || !strings.Contains(e.Error.Fix, "assignments.read") || !strings.Contains(e.Error.Fix, "assigned") {
		t.Fatalf("refused report = %+v", e)
	}
}

func TestParseReportSince(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if d, err := parseReportSince("720h", now); err != nil || !d.Equal(now.Add(-720*time.Hour)) {
		t.Fatalf("720h = %v, %v", d, err)
	}
	if d, err := parseReportSince("2026-09-01T00:00:00+02:00", now); err != nil || !d.Equal(time.Date(2026, 8, 31, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("rfc3339 = %v, %v", d, err)
	}
	if _, err := parseReportSince("-1h", now); err == nil {
		t.Fatal("a negative duration parsed")
	}
}

func TestParseDue(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if d, err := parseDue("72h", now); err != nil || !d.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("72h = %v, %v", d, err)
	}
	if _, err := parseDue("-1h", now); err == nil {
		t.Fatal("a due date in the past parsed")
	}
}
