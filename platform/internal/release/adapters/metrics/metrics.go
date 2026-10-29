// Package metrics records the Release context in Prometheus (RFC 0006
// §10.1): how many staged rollouts are active across the deployment.
package metrics

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/release/app"
)

// Prometheus implements app.RolloutMetrics.
type Prometheus struct {
	rollouts *prometheus.GaugeVec
}

var _ app.RolloutMetrics = (*Prometheus)(nil)

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Prometheus{
		rollouts: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_release_rollouts",
			Help: "Staged rollouts across the deployment, by state (active), as the rollout sweep last counted them. " +
				"Only the replica that leads the sweep updates it: read it beside glossa_scheduler_last_run_timestamp_seconds{job=\"release.rollouts\"}.",
		}, []string{"state"})),
	}
}

// ActiveRollouts implements app.RolloutMetrics.
func (p *Prometheus) ActiveRollouts(n int) {
	p.rollouts.WithLabelValues("active").Set(float64(n))
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
