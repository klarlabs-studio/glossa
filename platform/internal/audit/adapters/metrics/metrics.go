// Package metrics records the Audit context in Prometheus (RFC 0006
// §10.1): entries appended, chain verifications that found a break, and
// export jobs by outcome. No tenant, actor or action is a label.
package metrics

import (
	"errors"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/audit/app"
)

// outcomes are the allowlisted export job outcomes.
var outcomes = []string{app.ExportSucceeded, app.ExportFailed}

// Prometheus implements app.Metrics.
type Prometheus struct {
	entries  prometheus.Counter
	failures prometheus.Counter
	exports  *prometheus.CounterVec
}

var _ app.Metrics = (*Prometheus)(nil)

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	p := &Prometheus{
		entries: register(reg, prometheus.NewCounter(prometheus.CounterOpts{
			Name: "glossa_audit_entries_total",
			Help: "Audit entries appended to tenants' hash chains: projected events, sign-ins, MCP tool calls, backfilled and imported history.",
		})),
		failures: register(reg, prometheus.NewCounter(prometheus.CounterOpts{
			Name: "glossa_audit_chain_verify_failures_total",
			Help: "Chain verifications that found an entry not following the one before it. Any increase is an incident: the stored trail was changed.",
		})),
		exports: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_audit_export_jobs_total",
			Help: "Audit export jobs that ended, by outcome (succeeded, failed).",
		}, []string{"outcome"})),
	}
	for _, o := range outcomes {
		p.exports.WithLabelValues(o)
	}
	return p
}

// Appended implements app.Metrics.
func (p *Prometheus) Appended(n int) {
	if n > 0 {
		p.entries.Add(float64(n))
	}
}

// VerifyFailed implements app.Metrics.
func (p *Prometheus) VerifyFailed() { p.failures.Inc() }

// ExportJob implements app.Metrics. An outcome outside the allowlist is
// not recorded.
func (p *Prometheus) ExportJob(outcome string) {
	if slices.Contains(outcomes, outcome) {
		p.exports.WithLabelValues(outcome).Inc()
	}
}

func register[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			if existing, ok := are.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic(err)
	}
	return c
}
