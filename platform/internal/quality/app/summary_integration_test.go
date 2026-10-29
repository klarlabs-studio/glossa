//go:build integration

package app_test

import (
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The quality summary against a real Postgres: the two halves that are
// Quality's own — the per-layer breakdown of the newest run, and the
// findings-by-day rollup migration 0035 adds (RFC 0005 §8).

// TestSummaryCountsTheNewestRunByLayer: the second number. The
// breakdown sums to the run's counts, the waived are counted on their
// own, and `layers` still says what ran — which is what tells a clean
// layer from one nobody switched on.
func TestSummaryCountsTheNewestRunByLayer(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	parity := finding(domain.LayerParity, "missing-argument",
		domain.Locus{Key: "checkout.total", Locale: "fr"}, domain.Error, at(3))
	term := finding(domain.LayerTerminology, "term_forbidden",
		domain.Locus{Key: "checkout.pay", Locale: "de"}, domain.Warning, at(3))
	run := record(t, h, project, "main", parity, term)

	got, err := h.svc.QualitySummary(h.developer(), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Health.Findings == nil || got.Health.Run == nil {
		t.Fatal("the findings number is absent although a run was recorded")
	}
	if got.Health.Run.ID != run.ID {
		t.Errorf("run = %v, want the newest (%v)", got.Health.Run.ID, run.ID)
	}
	if *got.Health.Findings != (domain.Counts{Errors: 1, Warnings: 1}) {
		t.Errorf("counts = %+v", *got.Health.Findings)
	}
	byLayer := map[domain.Layer]domain.Counts{}
	for _, l := range got.Health.ByLayer {
		byLayer[l.Layer] = l.Counts
	}
	if byLayer[domain.LayerParity] != (domain.Counts{Errors: 1}) {
		t.Errorf("parity = %+v", byLayer[domain.LayerParity])
	}
	if byLayer[domain.LayerTerminology] != (domain.Counts{Warnings: 1}) {
		t.Errorf("terminology = %+v", byLayer[domain.LayerTerminology])
	}
	// The layers that ran are not the layers that found something: this
	// run ran three and reported from two, and the summary keeps both
	// facts, because a layer with no row is either clean or off.
	if len(got.Health.Run.Layers) != 3 || len(got.Health.ByLayer) != 2 {
		t.Errorf("layers = %v, by_layer = %v", got.Health.Run.Layers, got.Health.ByLayer)
	}

	// A waiver moves a finding to its own column without deleting it,
	// and the per-layer breakdown says so as plainly as the total does.
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: term.Fingerprint, Reason: "Login is the German term",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = h.svc.QualitySummary(h.developer(), project, app.SummaryQuery{Locale: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Locales) != 1 || got.Locales[0].Code != "de" {
		t.Fatalf("locales = %+v, want the German row alone", got.Locales)
	}
	de := got.Locales[0]
	if de.Findings == nil || *de.Findings != (domain.Counts{Waived: 1}) {
		t.Errorf("German counts = %+v, want the one waived warning", de.Findings)
	}
	// The German row's layers: terminology ran and reported the waived
	// finding; parity ran and found nothing *in German*, so it is
	// checked with a zero — which is a different fact from a layer
	// nobody ran, and the row has to keep them apart.
	layers := map[domain.Layer]app.LayerHealth{}
	for _, l := range de.Layers {
		layers[l.Layer] = l
	}
	if h := layers[domain.LayerTerminology]; !h.Available || !h.Checked ||
		h.Findings == nil || *h.Findings != (domain.Counts{Waived: 1}) {
		t.Errorf("terminology for German = %+v", h)
	}
	if h := layers[domain.LayerParity]; !h.Available || !h.Checked || h.Findings == nil ||
		*h.Findings != (domain.Counts{}) {
		t.Errorf("parity for German = %+v, want checked with no findings", h)
	}
	// A layer no run computed carries no counts at all, so it can never
	// be drawn as one that ran and passed (intent §41).
	if h := layers[domain.LayerVisual]; h.Available || h.Checked || h.Findings != nil ||
		h.Unavailable != app.UnavailableNoEvidence {
		t.Errorf("visual for German = %+v, want unavailable for want of evidence", h)
	}
}

// TestTheTrendCountsAProblemOncePerDay is what makes the rollup a
// trend rather than a measure of how busy CI was: the same finding seen
// by three runs on one day is one problem that day.
func TestTheTrendCountsAProblemOncePerDay(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerParity, "missing-argument",
		domain.Locus{Key: "checkout.total", Locale: "fr"}, domain.Error)
	other := finding(domain.LayerParity, "missing-argument",
		domain.Locus{Key: "checkout.pay", Locale: "fr"}, domain.Error)

	// Three runs on one day, the same problem in all three, plus a
	// second problem in the last.
	record(t, h, project, "main", f)
	record(t, h, project, "pr-1", f)
	record(t, h, project, "pr-2", f, other)

	got, err := h.svc.QualitySummary(h.developer(), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Trend == nil {
		t.Fatal("the trend is absent")
	}
	days := map[domain.Layer]int{}
	for _, d := range got.Trend.Days {
		days[d.Layer] += d.Findings
	}
	if days[domain.LayerParity] != 2 {
		t.Errorf("parity on the day = %d, want the two distinct problems", days[domain.LayerParity])
	}
	// A layer that ran and found nothing is a row with 0, and that is
	// the row that distinguishes "clean" from "never looked at".
	var completeness, found = 0, false
	for _, d := range got.Trend.Days {
		if d.Layer == domain.LayerCompleteness {
			completeness, found = d.Findings, true
		}
	}
	if !found || completeness != 0 {
		t.Errorf("completeness ran and found nothing; want a row with 0, got %d (present %v)", completeness, found)
	}
	// A layer no run switched on has no row at all.
	for _, d := range got.Trend.Days {
		if d.Layer == domain.LayerVisual {
			t.Errorf("a layer nothing ran has a row: %+v", d)
		}
	}
}

// TestTheTrendKeepsTheDaysApart: two days of runs are two days of rows,
// and re-running a day restates only that day.
func TestTheTrendKeepsTheDaysApart(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	first := finding(domain.LayerParity, "missing-argument",
		domain.Locus{Key: "checkout.total", Locale: "fr"}, domain.Error)
	second := finding(domain.LayerParity, "missing-argument",
		domain.Locus{Key: "checkout.pay", Locale: "fr"}, domain.Error)

	record(t, h, project, "main", first)
	h.clock.advance(48 * time.Hour)
	record(t, h, project, "main", first, second)

	got, err := h.svc.QualitySummary(h.developer(), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	byDay := map[string]int{}
	for _, d := range got.Trend.Days {
		if d.Layer == domain.LayerParity {
			byDay[d.Day.Format(time.DateOnly)] = d.Findings
		}
	}
	if len(byDay) != 2 {
		t.Fatalf("parity days = %v, want two", byDay)
	}
	var counts []int
	for _, d := range got.Trend.Days {
		if d.Layer == domain.LayerParity {
			counts = append(counts, d.Findings)
		}
	}
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 2 {
		t.Errorf("parity per day = %v (oldest first), want [1 2]", counts)
	}
	// The day between them has no row: nothing ran, and nothing is not
	// zero.
	middle := got.Trend.Days[0].Day.AddDate(0, 0, 1).Format(time.DateOnly)
	if _, present := byDay[middle]; present {
		t.Errorf("a day nothing ran on has a row (%s)", middle)
	}
}

// TestSummaryOfAnUncheckedProjectSaysSo: the numbers Quality owns are
// absent, with a reason, rather than a comfortable zero.
func TestSummaryOfAnUncheckedProjectSaysSo(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")

	got, err := h.svc.QualitySummary(h.developer(), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Health.Findings != nil || got.Trend != nil {
		t.Fatalf("an unchecked project reports findings: %+v / %+v", got.Health.Findings, got.Trend)
	}
	reasons := map[string]string{}
	for _, u := range got.Unmeasured {
		reasons[u.Number] = u.Reason
	}
	if reasons[app.NumberFindings] == "" || reasons[app.NumberFindingsByDay] == "" {
		t.Errorf("unmeasured = %+v, want both with a reason", got.Unmeasured)
	}
	// And every source the harness does not wire is named too, so the
	// page says what it could not see rather than showing sevens zeros.
	for _, number := range []string{
		app.NumberCoverage, app.NumberAI, app.NumberReviewQueue,
		app.NumberContextCoverage, app.NumberLeadTime, app.NumberCheckHealth,
	} {
		if reasons[number] == "" {
			t.Errorf("%s is not named as unmeasured", number)
		}
	}
}
