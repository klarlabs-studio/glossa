package scheduler

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus implements Metrics.
type Prometheus struct {
	runs      *prometheus.CounterVec
	skips     *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	lastRunAt *prometheus.GaugeVec
}

var _ Metrics = (*Prometheus)(nil)

// NewMetrics registers the scheduler's collectors on reg (a private
// registry when nil). Re-registering reuses the existing collectors.
func NewMetrics(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Prometheus{
		runs: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_scheduler_runs_total",
			Help: "Periodic job runs this replica led, by job and outcome (ok, error).",
		}, []string{"job", "outcome"})),
		skips: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_scheduler_skips_total",
			Help: "Polls where a job was not due, or another replica held its lease, by job.",
		}, []string{"job"})),
		duration: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_scheduler_run_duration_seconds",
			Help:    "How long a periodic job's run took, by job and outcome.",
			Buckets: []float64{.1, .5, 1, 5, 15, 60, 300, 900, 1800},
		}, []string{"job", "outcome"})),
		lastRunAt: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_scheduler_last_run_timestamp_seconds",
			Help: "When this replica last led a run of the job (Unix seconds). Take the max across replicas: another one may have led the most recent run.",
		}, []string{"job"})),
	}
}

// Ran implements Metrics.
func (p *Prometheus) Ran(job string, d time.Duration, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	p.runs.WithLabelValues(job, outcome).Inc()
	p.duration.WithLabelValues(job, outcome).Observe(d.Seconds())
	p.lastRunAt.WithLabelValues(job).SetToCurrentTime()
}

// Skipped implements Metrics.
func (p *Prometheus) Skipped(job string) { p.skips.WithLabelValues(job).Inc() }

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
