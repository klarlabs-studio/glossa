package metrics_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/metrics"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

func TestSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	open := 0
	m.TrackOpenSessions(metrics.TransportStreamableHTTP, func() int { return open })

	m.SessionOpened(metrics.TransportStreamableHTTP)
	m.ToolCalled("whoami", domain.ToolsetRead, domain.OutcomeOK)
	m.ToolCalled("message_upsert", domain.ToolsetWrite, domain.OutcomeDenied)
	open = 3

	want := `
# HELP glossa_mcp_sessions_open MCP sessions currently open, by transport.
# TYPE glossa_mcp_sessions_open gauge
glossa_mcp_sessions_open{transport="streamable-http"} 3
# HELP glossa_mcp_sessions_total MCP transport sessions opened, by transport. A client that probes the endpoint before initializing opens more than one, so this counts sessions rather than agent conversations; read it as load, and glossa_mcp_sessions_open for concurrency.
# TYPE glossa_mcp_sessions_total counter
glossa_mcp_sessions_total{transport="streamable-http"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"glossa_mcp_sessions_total", "glossa_mcp_sessions_open"); err != nil {
		t.Error(err)
	}
}

// A label set must stay bounded whatever a caller sends: an unknown
// outcome or transport folds into a known one rather than creating a
// time series nobody meant to create.
func TestLabelsAreBounded(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.ToolCalled("whoami", domain.Toolset("everything"), domain.Outcome("exploded"))
	m.SessionOpened("carrier pigeon")

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		for _, metric := range f.GetMetric() {
			for _, l := range metric.GetLabel() {
				switch l.GetName() {
				case "scope":
					if l.GetValue() != string(domain.ToolsetRead) {
						t.Errorf("scope = %q, want it folded to read", l.GetValue())
					}
				case "outcome":
					if l.GetValue() != string(domain.OutcomeError) {
						t.Errorf("outcome = %q, want it folded to error", l.GetValue())
					}
				case "transport":
					if l.GetValue() != metrics.TransportStreamableHTTP {
						t.Errorf("transport = %q, want it folded to the known one", l.GetValue())
					}
				}
			}
		}
	}
}

// Registering twice must reuse the collectors: the composition root
// builds one registry, but a test (or a second wiring) must not panic.
func TestRegisteringTwiceIsSafe(t *testing.T) {
	reg := prometheus.NewRegistry()
	first, second := metrics.New(reg), metrics.New(reg)
	first.SessionOpened(metrics.TransportStreamableHTTP)
	second.SessionOpened(metrics.TransportStreamableHTTP)
	if got := testutil.CollectAndCount(reg, "glossa_mcp_sessions_total"); got != 1 {
		t.Errorf("series = %d, want 1", got)
	}
}
