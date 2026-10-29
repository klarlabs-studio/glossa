package metrics_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/release/adapters/metrics"
)

func TestActiveRolloutsIsAGaugeByState(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.ActiveRollouts(3)
	m.ActiveRollouts(2)
	metrics.New(reg) // re-registering reuses the collector
	want := `
# HELP glossa_release_rollouts Staged rollouts across the deployment, by state (active), as the rollout sweep last counted them. Only the replica that leads the sweep updates it: read it beside glossa_scheduler_last_run_timestamp_seconds{job="release.rollouts"}.
# TYPE glossa_release_rollouts gauge
glossa_release_rollouts{state="active"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "glossa_release_rollouts"); err != nil {
		t.Error(err)
	}
}
