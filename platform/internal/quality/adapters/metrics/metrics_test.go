package metrics

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func TestCountsWhatItSees(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	m.FindingRecorded(domain.LayerParity, "missing-argument", domain.Error)
	m.FindingRecorded(domain.LayerParity, "missing-argument", domain.Error)
	m.CheckRunRecorded(domain.TriggerPullRequest, domain.ConclusionFailure)
	m.WaiverDecided(app.WaiverCreated)

	if got := testutil.ToFloat64(m.findings.WithLabelValues("parity", "missing-argument", "error")); got != 2 {
		t.Errorf("findings = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.runs.WithLabelValues("pull_request", "failure")); got != 1 {
		t.Errorf("runs = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.waivers.WithLabelValues("created")); got != 1 {
		t.Errorf("waivers = %v, want 1", got)
	}
}

// TestLabelsAreBounded: a layer, severity, trigger or outcome outside
// the vocabulary becomes "other", and codes stop being labels past
// MaxCodes — a dashboard that melts under one project's vocabulary is
// worse than one number fewer.
func TestLabelsAreBounded(t *testing.T) {
	m := New(nil)
	m.FindingRecorded(domain.Layer("invented"), "a-code", domain.Severity("loud"))
	if got := testutil.ToFloat64(m.findings.WithLabelValues("other", "a-code", "other")); got != 1 {
		t.Errorf("an unknown layer and severity = %v", got)
	}
	m.CheckRunRecorded(domain.Trigger("nope"), domain.Conclusion("maybe"))
	if got := testutil.ToFloat64(m.runs.WithLabelValues("other", "other")); got != 1 {
		t.Errorf("an unknown trigger and conclusion = %v", got)
	}
	m.WaiverDecided("whatever")
	if got := testutil.ToFloat64(m.waivers.WithLabelValues("other")); got != 1 {
		t.Errorf("an unknown outcome = %v", got)
	}

	for i := range MaxCodes + 50 {
		m.FindingRecorded(domain.LayerSource, "code-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26)), domain.Warning)
	}
	if got := testutil.ToFloat64(m.findings.WithLabelValues("source", "other", "warning")); got == 0 {
		t.Error("codes past MaxCodes should be bucketed as other")
	}
	if len(m.codes) > MaxCodes {
		t.Errorf("%d codes kept, want at most %d", len(m.codes), MaxCodes)
	}
}

// TestRegisteringTwiceReusesTheCollectors, so two services on one
// registry don't panic.
func TestRegisteringTwiceReusesTheCollectors(t *testing.T) {
	reg := prometheus.NewRegistry()
	a, b := New(reg), New(reg)
	a.WaiverDecided(app.WaiverRevoked)
	if got := testutil.ToFloat64(b.waivers.WithLabelValues("revoked")); got != 1 {
		t.Errorf("the second registration got its own collector: %v", got)
	}
}

// TestTheSeriesRFC0005Section11Names: the series this package publishes
// exist under exactly the names §11 spells, with exactly the labels it
// spells. A metric a dashboard cannot find is a metric nobody has.
func TestTheSeriesRFC0005Section11Names(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	m.FindingRecorded(domain.LayerLength, "expansion-excessive", domain.Warning)
	m.CheckRunRecorded(domain.TriggerCLI, domain.ConclusionSuccess)
	m.LayerChecked(domain.LayerLength, 250*time.Millisecond)
	m.WaiverDecided(app.WaiverExpired)
	m.VisualProbeRecorded("text-clipped", app.ProbeConfirmed)
	m.PolicyVersionRead(uuid.MustParse("0192f5a1-0000-0000-0000-000000000001"), 4)

	want := map[string][]string{
		"glossa_quality_findings_total":         {"code", "layer", "severity"},
		"glossa_quality_check_runs_total":       {"conclusion", "trigger"},
		"glossa_quality_check_duration_seconds": {"layer"},
		"glossa_quality_waivers_total":          {"outcome"},
		"glossa_quality_visual_probes_total":    {"code", "outcome"},
		"glossa_quality_policy_version":         {"project"},
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range families {
		labels, ok := want[f.GetName()]
		if !ok {
			t.Errorf("unexpected series %q", f.GetName())
			continue
		}
		seen[f.GetName()] = true
		var got []string
		for _, l := range f.GetMetric()[0].GetLabel() {
			got = append(got, l.GetName())
		}
		if !slices.Equal(got, labels) {
			t.Errorf("%s labels %v, want %v", f.GetName(), got, labels)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s was never published", name)
		}
	}
}

// The layer duration lands in the histogram under its layer, and an
// invented layer folds into "other" like everywhere else.
func TestTheLayerDurationIsObservedByLayer(t *testing.T) {
	m := New(nil)
	m.LayerChecked(domain.LayerVisual, 2*time.Second)
	m.LayerChecked(domain.Layer("hand-wringing"), time.Second)
	if got := testutil.CollectAndCount(m.duration); got != 2 {
		t.Errorf("%d series, want one per layer seen", got)
	}
}

// A probe outcome outside the three the ingest can reach folds to
// "other", and the code goes through the same MaxCodes bound the
// findings series uses — the codes come from the same vocabulary, so
// they must not have two different ways of being unbounded.
func TestVisualProbeLabelsAreBounded(t *testing.T) {
	m := New(nil)
	m.VisualProbeRecorded("text-clipped", "whatever")
	if got := testutil.ToFloat64(m.probes.WithLabelValues("text-clipped", "other")); got != 1 {
		t.Errorf("an unknown outcome = %v", got)
	}
	for i := range MaxCodes + 50 {
		m.VisualProbeRecorded("probe-"+string(rune('a'+i%26))+string(rune('a'+i/26)), app.ProbeFirstSighting)
	}
	if got := testutil.ToFloat64(m.probes.WithLabelValues("other", app.ProbeFirstSighting)); got == 0 {
		t.Error("codes past MaxCodes should be bucketed as other")
	}
	if len(m.codes) > MaxCodes {
		t.Errorf("%d codes kept, want at most %d", len(m.codes), MaxCodes)
	}
}

// The policy gauge is the one label a tenant can grow without limit, so
// it stops at MaxProjects rather than bucketing: "other" on a gauge
// would mean whichever project was read last, which is worse than not
// publishing it at all.
func TestThePolicyGaugeStopsAtMaxProjects(t *testing.T) {
	m := New(nil)
	first := uuid.New()
	m.PolicyVersionRead(first, 3)
	m.PolicyVersionRead(first, 4)
	if got := testutil.ToFloat64(m.policy.WithLabelValues(first.String())); got != 4 {
		t.Errorf("version = %v, want the latest read", got)
	}
	for range MaxProjects + 50 {
		m.PolicyVersionRead(uuid.New(), 1)
	}
	if got := testutil.CollectAndCount(m.policy); got > MaxProjects {
		t.Errorf("%d series, want at most %d", got, MaxProjects)
	}
}
