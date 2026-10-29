package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The rendered summary. What these pin is the edge's half of the one
// property the number exists for: a number that was not measured is
// absent from the JSON and named in `unmeasured`, and never serialized
// as a zero somebody could read as a measurement.

func renderSummary(t *testing.T, s app.Summary) map[string]any {
	t.Helper()
	b, err := json.Marshal(toQualitySummary(s))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func project(t *testing.T, got map[string]any) map[string]any {
	t.Helper()
	p, _ := got["project"].(map[string]any)
	if p == nil {
		t.Fatal("the document has no project health")
	}
	return p
}

// TestUnmeasuredNumbersAreAbsentNotZero: the whole point of the
// document. A dashboard that read `0` for "not measured" would report
// the absence of a check as the absence of problems.
func TestUnmeasuredNumbersAreAbsentNotZero(t *testing.T) {
	got := renderSummary(t, app.Summary{
		Schema: app.SummarySchema, Project: uuid.New(), Environment: "production",
		Since: time.Now().UTC().Add(-time.Hour), ComputedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Minute),
		Unmeasured: []app.Unmeasured{
			{Number: app.NumberLeadTime, Reason: `the environment "production" has published nothing`},
			{Number: app.NumberCheckHealth, Reason: "the caller may not read what it is computed from"},
		},
	})
	if got["schema"] != app.SummarySchema {
		t.Errorf("schema = %v", got["schema"])
	}
	health := project(t, got)
	for _, number := range []string{
		"coverage", "findings", "by_layer", "run", "ai", "queue", "context", "lead_time", "checks",
	} {
		if _, present := health[number]; present {
			t.Errorf("project.%s is serialized although it was not measured", number)
		}
	}
	if _, present := got["findings_by_day"]; present {
		t.Error("the trend is serialized although it was not measured")
	}
	list, _ := got["unmeasured"].([]any)
	if len(list) != 2 {
		t.Fatalf("unmeasured = %v, want the two that were not measured", got["unmeasured"])
	}
	first, _ := list[0].(map[string]any)
	if first["number"] != "lead_time" || first["reason"] == "" {
		t.Errorf("unmeasured[0] = %v, want the lead time with its reason", first)
	}
}

// TestAnEmptySampleHasNoPercentiles: a queue of depth 0 and a window
// with no concluded check each report the count they measured and no
// derived number at all.
func TestAnEmptySampleHasNoPercentiles(t *testing.T) {
	got := renderSummary(t, app.Summary{
		Schema: app.SummarySchema, Project: uuid.New(), Environment: "production",
		Health: app.ProjectHealth{
			Queue:   &app.Queue{Depth: 0},
			Context: &app.ContextCoverage{ActiveMessages: 4, WithUsage: 3, WithRegion: 1},
			Checks:  &app.CheckHealth{},
		},
	})
	health := project(t, got)
	queue, _ := health["queue"].(map[string]any)
	if queue["depth"] != float64(0) {
		t.Errorf("depth = %v, want a measured 0", queue["depth"])
	}
	if _, present := queue["age"]; present {
		t.Error("an empty queue reports an age")
	}
	checks, _ := health["checks"].(map[string]any)
	if checks["runs"] != float64(0) {
		t.Errorf("runs = %v, want a measured 0", checks["runs"])
	}
	if _, present := checks["pass_rate"]; present {
		t.Error("a window with no graded check reports a pass rate")
	}
	if _, present := checks["median_seconds"]; present {
		t.Error("a window with no concluded check reports a median")
	}
	coverage, _ := health["context"].(map[string]any)
	if coverage["with_region"] != float64(1) {
		t.Errorf("context coverage = %v", coverage)
	}
}

// TestMeasuredNumbersCarryTheirPercentiles: the counterpart — what is
// measured is rendered whole, in seconds, with the sample size beside
// it so a reader can weigh it.
func TestMeasuredNumbersCarryTheirPercentiles(t *testing.T) {
	lead := domain.NewSpread([]time.Duration{2 * time.Hour, 4 * time.Hour})
	got := renderSummary(t, app.Summary{
		Schema: app.SummarySchema, Project: uuid.New(), Environment: "staging",
		Health: app.ProjectHealth{
			LeadTime: &lead,
			Checks: &app.CheckHealth{
				Concluded: 5, Succeeded: 4, Failed: 1,
				Latency: domain.NewSpread([]time.Duration{time.Minute}),
			},
		},
		Locales: []app.LocaleHealth{
			{Code: "de", Direction: "ltr", LeadTime: &lead},
			{Code: "fr", Direction: "ltr"}, // nothing shipped: no lead at all
		},
	})
	health := project(t, got)
	overall, _ := health["lead_time"].(map[string]any)
	if overall["samples"] != float64(2) || overall["p50_seconds"] != float64(3*3600) {
		t.Errorf("lead time = %v, want two samples with a three-hour median", overall)
	}
	locales, _ := got["locales"].([]any)
	fr, _ := locales[1].(map[string]any)
	if _, present := fr["lead_time"]; present {
		t.Error("a locale that has shipped nothing reports a lead time")
	}
	checks, _ := health["checks"].(map[string]any)
	if checks["pass_rate"] != 0.8 || checks["median_seconds"] != float64(60) {
		t.Errorf("checks = %v, want a 0.8 pass rate and a one-minute median", checks)
	}
}

// TestALayerNobodyRanCarriesNoCounts is intent §41 on the wire: a
// locale says which layers are available for it, availability is
// answered before grading, and only a layer that was actually checked
// carries numbers — so an unavailable layer can never be drawn as one
// that ran and passed.
func TestALayerNobodyRanCarriesNoCounts(t *testing.T) {
	got := renderSummary(t, app.Summary{
		Schema: app.SummarySchema, Project: uuid.New(), Environment: "production",
		Locales: []app.LocaleHealth{{Code: "ja", Direction: "ltr", Layers: []app.LayerHealth{
			{Layer: domain.LayerParity, Available: true, Checked: true, Findings: &domain.Counts{Errors: 2}},
			{Layer: domain.LayerStyle, Available: true, Checked: false},
			{Layer: domain.LayerVisual, Unavailable: app.UnavailableUnsupportedLocale},
			{Layer: domain.LayerLinguistic, Unavailable: app.UnavailableNotConfigured},
		}}},
	})
	locales, _ := got["locales"].([]any)
	ja, _ := locales[0].(map[string]any)
	layers, _ := ja["layers"].([]any)
	if len(layers) != 4 {
		t.Fatalf("layers = %v", layers)
	}
	checked, _ := layers[0].(map[string]any)
	if checked["available"] != true || checked["checked"] != true {
		t.Errorf("the checked layer = %v", checked)
	}
	counts, _ := checked["findings"].(map[string]any)
	if counts["errors"] != float64(2) {
		t.Errorf("findings = %v", counts)
	}
	if _, present := checked["unavailable"]; present {
		t.Error("an available layer carries a reason for being unavailable")
	}
	notRun, _ := layers[1].(map[string]any)
	if notRun["available"] != true || notRun["checked"] != false {
		t.Errorf("the unchecked layer = %v", notRun)
	}
	if _, present := notRun["findings"]; present {
		t.Error("a layer nobody ran carries counts, which would read as a clean run")
	}
	unsupported, _ := layers[2].(map[string]any)
	if unsupported["available"] != false || unsupported["unavailable"] != "unsupported_locale" {
		t.Errorf("the unsupported layer = %v", unsupported)
	}
	if _, present := unsupported["findings"]; present {
		t.Error("an unavailable layer carries counts")
	}
	off, _ := layers[3].(map[string]any)
	if off["unavailable"] != "not_configured" {
		t.Errorf("the switched-off layer = %v", off)
	}
}

// TestTheTrendKeepsItsDays: the rollup renders as dates, oldest first,
// so a chart can plot it without parsing timestamps.
func TestTheTrendKeepsItsDays(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC) }
	got := renderSummary(t, app.Summary{
		Schema: app.SummarySchema, Project: uuid.New(), Environment: "production",
		Trend: &app.TrendSummary{From: day(1), To: day(3), Days: []domain.DailyFindings{
			{Day: day(1), Layer: domain.LayerParity, Findings: 3},
			{Day: day(2), Layer: domain.LayerParity, Findings: 0},
		}},
	})
	trend, _ := got["findings_by_day"].(map[string]any)
	if trend["from"] != "2026-03-01" || trend["to"] != "2026-03-03" {
		t.Errorf("window = %v..%v", trend["from"], trend["to"])
	}
	days, _ := trend["days"].([]any)
	if len(days) != 2 {
		t.Fatalf("days = %v", days)
	}
	// A layer that ran and found nothing is a 0, and a day with no run
	// has no row: the two must not be rendered the same way, and the
	// only way that holds is if the 0 is really there.
	second, _ := days[1].(map[string]any)
	if second["findings"] != float64(0) || second["day"] != "2026-03-02" {
		t.Errorf("days[1] = %v, want a measured zero on the second", second)
	}
}
