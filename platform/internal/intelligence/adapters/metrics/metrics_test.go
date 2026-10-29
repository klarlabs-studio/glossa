package metrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/metrics"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func TestPrometheusRecordsTheContext(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	if metrics.New(reg) == nil {
		t.Fatal("registering twice reuses the collectors")
	}
	tenant := uuid.New()
	m.JobFinished(domain.TriggerFill, domain.JobSucceeded, "", time.Second)
	m.JobFinished(domain.TriggerFill, domain.JobFailed, domain.FailureBudgetExceeded, time.Millisecond)
	m.Spent(tenant, "anthropic", "claude-sonnet-5", 1500)
	m.SuggestionCreated("de", domain.OriginAI, domain.ActionReviewRequired, 0.6)
	m.SuggestionDecided("de", domain.StatusAccepted, 0.1)
	m.SuggestionDecided("de", domain.StatusRejected, 0)
	m.QueueDepth(map[domain.JobState]int{domain.JobQueued: 3})

	want := `
# HELP glossa_intelligence_cost_micro_usd_total Provider spend in micro-USD, by tenant, provider and model.
# TYPE glossa_intelligence_cost_micro_usd_total counter
glossa_intelligence_cost_micro_usd_total{model="claude-sonnet-5",provider="anthropic",tenant="` + tenant.String() + `"} 1500
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "glossa_intelligence_cost_micro_usd_total"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(reg, "glossa_intelligence_jobs_total", "glossa_intelligence_suggestion_decisions_total",
		"glossa_intelligence_edit_ratio", "glossa_intelligence_queue_jobs"); n != 7 { // 2 jobs, 2 decisions, 1 edit ratio, queued + running

		t.Errorf("series = %d", n)
	}
}
