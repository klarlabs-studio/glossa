package metrics_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/audit/adapters/metrics"
)

func TestAuditSeriesMove(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.Appended(3)
	m.Appended(0)
	m.Appended(2)
	m.VerifyFailed()
	m.ExportJob("succeeded")
	m.ExportJob("succeeded")
	m.ExportJob("failed")
	m.ExportJob("tenant-42") // not an outcome: not a series
	metrics.New(reg)         // re-registering reuses the collectors
	want := `
# HELP glossa_audit_entries_total Audit entries appended to tenants' hash chains: projected events, sign-ins, MCP tool calls, backfilled and imported history.
# TYPE glossa_audit_entries_total counter
glossa_audit_entries_total 5
# HELP glossa_audit_chain_verify_failures_total Chain verifications that found an entry not following the one before it. Any increase is an incident: the stored trail was changed.
# TYPE glossa_audit_chain_verify_failures_total counter
glossa_audit_chain_verify_failures_total 1
# HELP glossa_audit_export_jobs_total Audit export jobs that ended, by outcome (succeeded, failed).
# TYPE glossa_audit_export_jobs_total counter
glossa_audit_export_jobs_total{outcome="failed"} 1
glossa_audit_export_jobs_total{outcome="succeeded"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"glossa_audit_entries_total", "glossa_audit_chain_verify_failures_total", "glossa_audit_export_jobs_total"); err != nil {
		t.Error(err)
	}
}
