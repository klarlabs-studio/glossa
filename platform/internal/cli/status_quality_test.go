package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// `glossa status --quality` (RFC 0005 §8, §13 wave 6).

// summarized gives the fake the summary document to answer with.
func summarized(f *fakeServer, doc map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qa.summary = doc
}

// measuredSummary is a project where all seven numbers were computed.
func measuredSummary() map[string]any {
	s := baseSummary()
	s["project"] = map[string]any{
		"coverage": map[string]any{"messages": 100, "translated": 80, "outdated": 5, "missing": 20},
		"findings": map[string]any{"errors": 3, "warnings": 7, "waived": 2},
		"ai": map[string]any{"decisions": 40, "accepted": 30, "edited": 12, "rejected": 10,
			"acceptance_rate": 0.75, "mean_edit_distance": 0.18},
		"queue": map[string]any{"depth": 4, "age": map[string]any{
			"samples": 4, "p50_seconds": 3600.0, "p90_seconds": 86400.0}},
		"context":   map[string]any{"active_messages": 100, "with_usage": 90, "with_region": 40},
		"lead_time": map[string]any{"samples": 11, "p50_seconds": 7200.0, "p90_seconds": 172800.0},
		"checks": map[string]any{"runs": 20, "succeeded": 17, "failed": 3, "neutral": 1,
			"pass_rate": 0.85, "median_seconds": 42.0,
			"latency": map[string]any{"samples": 20, "p50_seconds": 42.0, "p90_seconds": 90.0}},
		"by_layer": []map[string]any{
			{"layer": "terminology", "counts": map[string]any{"errors": 3, "warnings": 1, "waived": 2}},
		},
		"run": map[string]any{"id": "run_1", "ref": "main", "commit": "abc1234def", "conclusion": "failure",
			"policy_version": 7, "layers": []string{"structure", "parity"},
			"started_at": "2026-09-30T08:00:00Z"},
	}
	return s
}

func TestStatusQualityPrintsTheSevenNumbers(t *testing.T) {
	srv := newFakeServer(t)
	summarized(srv, measuredSummary())
	w := newWorkspace(t).withProject(srv, nil)

	var out statusQualityJSON
	w.json(&out, "status", "--quality").want(t, ExitOK)
	if out.Schema != statusQualitySchema || out.ProjectID != "prj_1" || out.Environment != "production" {
		t.Fatalf("status = %+v", out)
	}
	n := out.Numbers
	switch {
	case n.Coverage == nil || n.Coverage.Translated != 80:
		t.Fatalf("1 coverage = %+v", n.Coverage)
	case n.Findings == nil || n.Findings.Errors != 3 || n.Findings.Waived != 2:
		t.Fatalf("2 findings = %+v", n.Findings)
	case n.AI == nil || n.AI.AcceptanceRate != 0.75:
		t.Fatalf("3 ai = %+v", n.AI)
	case n.Queue == nil || n.Queue.Depth != 4 || n.Queue.Age == nil:
		t.Fatalf("4 queue = %+v", n.Queue)
	case n.Context == nil || n.Context.WithRegion != 40:
		t.Fatalf("5 context = %+v", n.Context)
	case n.LeadTime == nil || n.LeadTime.Samples != 11:
		t.Fatalf("6 lead time = %+v", n.LeadTime)
	case n.Checks == nil || n.Checks.PassRate == nil || *n.Checks.PassRate != 0.85:
		t.Fatalf("7 checks = %+v", n.Checks)
	}
	if out.Run == nil || out.Run.PolicyVersion != 7 {
		t.Errorf("run = %+v", out.Run)
	}

	h := w.run("status", "--quality")
	h.want(t, ExitOK)
	for _, want := range []string{
		"1 coverage", "80.0% translated", "2 findings", "3 errors", "3 AI acceptance",
		"4 review queue", "5 context coverage", "6 lead time", "7 check health", "85% pass",
	} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("the output has no %q:\n%s", want, h.stdout)
		}
	}
}

// The property the command exists to keep: a number nobody measured is
// absent from the document and named with its reason — never a zero, in
// either output.
func TestStatusQualityNeverPrintsZeroForNotMeasured(t *testing.T) {
	srv := newFakeServer(t)
	s := baseSummary()
	s["project"] = map[string]any{
		"coverage": map[string]any{"messages": 100, "translated": 80, "outdated": 0, "missing": 20},
	}
	s["unmeasured"] = []map[string]any{
		{"number": "findings", "reason": "nothing has been checked yet"},
		{"number": "ai", "reason": "no AI provider is configured"},
		{"number": "queue", "reason": "the review queue is empty"},
		{"number": "context", "reason": "the project has no current context build"},
		{"number": "lead_time", "reason": "nothing has been published to production"},
		{"number": "checks", "reason": "no pull-request check has concluded in the window"},
	}
	summarized(srv, s)
	w := newWorkspace(t).withProject(srv, nil)

	// In the document: the members are absent, not zero.
	r := w.run("status", "--quality", "--json")
	r.want(t, ExitOK)
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &raw); err != nil {
		t.Fatal(err)
	}
	numbers, _ := raw["numbers"].(map[string]any)
	for _, absent := range []string{"findings", "ai", "queue", "context", "lead_time", "checks"} {
		if v, present := numbers[absent]; present {
			t.Errorf("numbers.%s = %v, want it absent: 0 is an answer and this has none", absent, v)
		}
	}
	if numbers["coverage"] == nil {
		t.Error("numbers.coverage is absent, but it was measured")
	}
	// And the reasons travel with it, so a reader can tell "clean" from
	// "not looked at" without the dashboard.
	var out statusQualityJSON
	w.json(&out, "status", "--quality").want(t, ExitOK)
	if len(out.Unmeasured) != 6 {
		t.Fatalf("unmeasured = %+v", out.Unmeasured)
	}

	// In the human output: "not measured" and the reason, where a number
	// would go. A CLI that printed 0 % here would be telling the same
	// lie a dashboard would, somewhere harder to notice.
	h := w.run("status", "--quality")
	h.want(t, ExitOK)
	for _, want := range []string{
		"not measured — nothing has been checked yet",
		"not measured — no AI provider is configured",
		"not measured — no pull-request check has concluded in the window",
	} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("the output has no %q:\n%s", want, h.stdout)
		}
	}
	for _, line := range strings.Split(h.stdout, "\n") {
		if strings.Contains(line, "not measured") && strings.Contains(line, " 0 ") {
			t.Errorf("an unmeasured number printed a zero: %q", line)
		}
	}
}

// One level down, the same rule: a percentile over an empty sample and
// a pass rate over no graded check are absent, not 0.
func TestStatusQualityLeavesOutWhatHadNoSample(t *testing.T) {
	srv := newFakeServer(t)
	s := baseSummary()
	s["project"] = map[string]any{
		"queue":  map[string]any{"depth": 0},
		"checks": map[string]any{"runs": 0, "succeeded": 0, "failed": 0, "neutral": 2},
	}
	summarized(srv, s)
	w := newWorkspace(t).withProject(srv, nil)

	var out statusQualityJSON
	w.json(&out, "status", "--quality").want(t, ExitOK)
	if out.Numbers.Queue == nil || out.Numbers.Queue.Depth != 0 {
		t.Fatalf("queue = %+v, want a measured depth of 0", out.Numbers.Queue)
	}
	if out.Numbers.Queue.Age != nil {
		t.Errorf("queue.age = %+v, want none: an empty queue has no wait, which is not a wait of zero", out.Numbers.Queue.Age)
	}
	if out.Numbers.Checks == nil || out.Numbers.Checks.PassRate != nil {
		t.Errorf("checks.pass_rate = %+v, want none: nothing concluded", out.Numbers.Checks)
	}
	h := w.run("status", "--quality")
	for _, want := range []string{"age not measured", "pass rate not measured"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("the output has no %q:\n%s", want, h.stdout)
		}
	}
}

// A layer a locale cannot run must never read as a clean one (intent
// §41).
func TestStatusQualityNamesTheLayersALocaleCantRun(t *testing.T) {
	srv := newFakeServer(t)
	s := baseSummary()
	s["locales"] = []map[string]any{{
		"code": "ja", "direction": "ltr", "is_source": false,
		"coverage": map[string]any{"messages": 10, "translated": 10, "outdated": 0, "missing": 0},
		"findings": map[string]any{"errors": 0, "warnings": 0, "waived": 0},
		"layers": []map[string]any{
			{"layer": "structure", "available": true, "checked": true},
			{"layer": "visual", "available": false, "checked": false, "unavailable": "no_evidence"},
			{"layer": "linguistic", "available": false, "checked": false, "unavailable": "not_configured"},
		},
	}}
	summarized(srv, s)
	w := newWorkspace(t).withProject(srv, nil)

	var out statusQualityJSON
	w.json(&out, "status", "--quality").want(t, ExitOK)
	if len(out.Locales) != 1 || len(out.Locales[0].Layers) != 3 {
		t.Fatalf("locales = %+v", out.Locales)
	}
	h := w.run("status", "--quality")
	for _, want := range []string{"ja: visual (no_evidence), linguistic (not_configured)",
		"— is not measured, and is not a zero"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("the output has no %q:\n%s", want, h.stdout)
		}
	}
}

func TestStatusQualityIsTheServersAnswer(t *testing.T) {
	srv := newFakeServer(t)
	summarized(srv, measuredSummary())
	w := newWorkspace(t).withProject(srv, nil)

	// The seven numbers come from six contexts' tables; the local
	// catalogs know about one of them, so --offline is a different,
	// smaller question and is refused rather than answered.
	var out errorDoc
	w.json(&out, "status", "--quality", "--offline").want(t, ExitUsage)
	if !strings.Contains(out.Error.Message, "--offline") {
		t.Errorf("error = %+v", out.Error)
	}
	w.json(&out, "status", "--quality", "--since", "whenever").want(t, ExitUsage)
	if !strings.Contains(out.Error.Message, "--since") {
		t.Errorf("error = %+v", out.Error)
	}
	w.json(&out, "status", "--quality", "--locale", "not a locale").want(t, ExitUsage)
	if !strings.Contains(out.Error.Message, "--locale") {
		t.Errorf("error = %+v", out.Error)
	}
	// And `glossa status` without it still prints coverage, unchanged.
	var coverage statusJSON
	w.json(&coverage, "status").want(t, ExitOK)
	if coverage.Schema != "glossa.cli.status/v1" {
		t.Errorf("status = %+v", coverage)
	}
}
