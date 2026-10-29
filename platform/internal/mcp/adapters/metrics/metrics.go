// Package metrics is MCP's Prometheus adapter: the two series of
// RFC 0005 §11 that belong to this context, plus the open-session
// gauge that makes a leaked session visible.
//
// Label values are bounded on purpose. `tool` comes from the registry,
// so it is a closed set by construction; `scope` and `outcome` are
// closed vocabularies in the domain, and anything else is folded into
// "error" rather than becoming a time series nobody meant to create.
package metrics

import (
	"errors"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Transports are the transports a session can arrive on. Today there is
// one; the stdio proxy of RFC 0005 §7.1 speaks to this same endpoint,
// so it does not add a second.
const TransportStreamableHTTP = "streamable-http"

var transports = []string{TransportStreamableHTTP}

// Metrics implements app.Metrics.
type Metrics struct {
	reg      prometheus.Registerer
	calls    *prometheus.CounterVec
	sessions *prometheus.CounterVec
}

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	m := &Metrics{
		reg: reg,
		calls: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_mcp_tool_calls_total",
			Help: "MCP tool calls, by tool, the session's toolset (read, write or publish) and outcome: ok, denied (authorization, the session's toolset, or the rate limit), invalid (unknown tool or bad arguments), error.",
		}, []string{"tool", "scope", "outcome"})),
		sessions: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_mcp_sessions_total",
			Help: "MCP transport sessions opened, by transport. A client that probes the endpoint before initializing opens more than one, so this counts sessions rather than agent conversations; read it as load, and glossa_mcp_sessions_open for concurrency.",
		}, []string{"transport"})),
	}
	// Publish the session counter at zero, so a dashboard shows "no
	// agents" rather than a gap until the first one connects. The call
	// counter is left alone: its tool label set is the registry's, and
	// pre-publishing it would pin tools a deployment may not serve.
	for _, t := range transports {
		m.sessions.WithLabelValues(t)
	}
	return m
}

// TrackOpenSessions publishes glossa_mcp_sessions_open for a transport,
// read from count on every scrape.
//
// It is a gauge function rather than a gauge the service increments and
// decrements, because the transport — not this context — owns when a
// session ends: a client that disconnects without saying so, or a
// session the idle timeout closes, would leave a counted gauge drifting
// upwards forever, and a monitoring number that only ever grows is
// worse than none.
func (m *Metrics) TrackOpenSessions(transport string, count func() int) {
	if count == nil {
		return
	}
	transport = known(transport)
	register(m.reg, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name:        "glossa_mcp_sessions_open",
		Help:        "MCP sessions currently open, by transport.",
		ConstLabels: prometheus.Labels{"transport": transport},
	}, func() float64 { return float64(count()) }))
}

var _ app.Metrics = (*Metrics)(nil)

// ToolCalled implements app.Metrics.
func (m *Metrics) ToolCalled(tool string, toolset domain.Toolset, outcome domain.Outcome) {
	if !slices.Contains(domain.Outcomes(), outcome) {
		outcome = domain.OutcomeError
	}
	if !slices.Contains(domain.Toolsets(), toolset) {
		toolset = domain.ToolsetRead
	}
	m.calls.WithLabelValues(tool, toolset.String(), outcome.String()).Inc()
}

// SessionOpened implements app.Metrics.
func (m *Metrics) SessionOpened(transport string) { m.sessions.WithLabelValues(known(transport)).Inc() }

func known(transport string) string {
	if !slices.Contains(transports, transport) {
		return TransportStreamableHTTP
	}
	return transport
}

func register[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			if existing, ok := are.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic(err) // a name clash with an unrelated collector is a programming error
	}
	return c
}
