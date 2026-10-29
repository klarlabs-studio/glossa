package outbox

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type metrics struct {
	settlements *prometheus.CounterVec
	claimErrors prometheus.Counter
	handlers    *prometheus.HistogramVec
}

// newMetrics registers the dispatcher's collectors on reg (a private
// registry when nil). Re-registering reuses the existing collectors, so
// several dispatchers can share one registry.
func newMetrics(reg prometheus.Registerer) *metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &metrics{
		settlements: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_outbox_settlements_total",
			Help: "Outbox claims settled, by outcome (delivered, retry, dead, release, lease_lost, error).",
		}, []string{"outcome"})),
		claimErrors: register(reg, prometheus.NewCounter(prometheus.CounterOpts{
			Name: "glossa_outbox_claim_errors_total",
			Help: "Failed attempts to claim a batch of outbox events.",
		})),
		handlers: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_outbox_handler_duration_seconds",
			Help:    "Outbox subscriber latency, inline retries included, by result.",
			Buckets: prometheus.DefBuckets,
		}, []string{"event_type", "subscriber", "result"})),
	}
}

func (m *metrics) observeHandler(eventType, subscriber string, err error, d time.Duration) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.handlers.WithLabelValues(eventType, subscriber, result).Observe(d.Seconds())
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
