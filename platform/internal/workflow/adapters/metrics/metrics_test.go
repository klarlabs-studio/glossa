package metrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/metrics"
)

func TestTransitionsCountByOutcome(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.Transition("applied")
	m.Transition("applied")
	m.Transition("refused")
	m.Transition("legal_review") // not an outcome: not a series
	metrics.New(reg)             // re-registering reuses the collectors
	got := gather(t, reg, "glossa_workflow_transitions_total")
	want := map[string]float64{"outcome=applied": 2, "outcome=ignored": 0, "outcome=refused": 1}
	equal(t, got, want)
}

func TestGaugesAreSetNotAdded(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.Instances("active", 7)
	m.Instances("active", 4)
	m.Instances("finished", 9)
	m.Instances("stranded", 1) // not a status
	m.OpenAssignments(true, 2)
	m.OpenAssignments(false, 5)
	m.OpenAssignments(true, 1)
	equal(t, gather(t, reg, "glossa_workflow_instances"), map[string]float64{"status=active": 4, "status=finished": 9})
	equal(t, gather(t, reg, "glossa_assignments_open"), map[string]float64{"overdue=true": 1, "overdue=false": 5})
}

func TestDecisionsCountBySubjectAndVerdict(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.Decision("translation", "granted")
	m.Decision("release_request", "granted")
	m.Decision("release_request", "denied")
	m.Decision("release_request", "denied")
	m.Decision("vendor:acme", "granted") // not a subject
	equal(t, gather(t, reg, "glossa_approvals_decisions_total"), map[string]float64{
		"decision=granted,subject=translation":     1,
		"decision=denied,subject=translation":      0,
		"decision=granted,subject=release_request": 1,
		"decision=denied,subject=release_request":  2,
	})
}

// gather reads one family as "label=value[,label=value]" → value, the
// labels in name order as Prometheus sorts them.
func gather(t *testing.T, reg *prometheus.Registry, name string) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			key := ""
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += ","
				}
				key += l.GetName() + "=" + l.GetValue()
			}
			switch {
			case m.GetCounter() != nil:
				out[key] = m.GetCounter().GetValue()
			case m.GetGauge() != nil:
				out[key] = m.GetGauge().GetValue()
			}
		}
	}
	if n := testutil.CollectAndCount(reg, name); n != len(out) {
		t.Fatalf("%s: %d series collected, %d read", name, n, len(out))
	}
	return out
}

func equal(t *testing.T, got, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("series = %v, want %v", got, want)
		return
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v (present %t), want %v; all: %v", k, g, ok, v, got)
		}
	}
}
