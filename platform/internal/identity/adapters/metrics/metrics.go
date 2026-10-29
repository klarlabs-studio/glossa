// Package metrics is Identity's Prometheus adapter. It records the
// series of RFC 0004 §11 that belong to Identity: how CI authenticates.
package metrics

import (
	"errors"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

// CI records the GitHub Actions OIDC exchange (RFC 0004 §6.3): how many
// runs authenticated and how many did not, by why. Nothing here touches
// the ID token or the minted secret — an outcome label is the whole of
// what is recorded.
type CI struct{ exchanges *prometheus.CounterVec }

// outcomes is the label's closed set, so a mistyped outcome becomes
// "error" instead of a new time series nobody meant to create.
var outcomes = []string{
	"minted", "invalid_id_token", "repository_not_connected",
	"ambiguous_project", "project_not_connected", "error",
}

// NewCI registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func NewCI(reg prometheus.Registerer) *CI {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	c := &CI{exchanges: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "glossa_github_oidc_exchanges_total",
		Help: "GitHub Actions OIDC exchanges, by outcome: minted, invalid_id_token (issuer, audience, signature or expiry), repository_not_connected, ambiguous_project (the repository feeds several projects and the run named none), project_not_connected, error.",
	}, []string{"outcome"}))}
	// Publish every outcome at zero, so a dashboard shows "no failures"
	// rather than a gap until the first one happens.
	for _, o := range outcomes {
		c.exchanges.WithLabelValues(o)
	}
	return c
}

var _ app.CIMetrics = (*CI)(nil)

// Exchanged implements app.CIMetrics.
func (c *CI) Exchanged(outcome string) {
	if !slices.Contains(outcomes, outcome) {
		outcome = "error"
	}
	c.exchanges.WithLabelValues(outcome).Inc()
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
