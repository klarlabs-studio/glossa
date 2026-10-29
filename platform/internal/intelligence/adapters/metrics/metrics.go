// Package metrics records the Intelligence context in Prometheus (RFC
// 0003 §3.3, intent §70): jobs by trigger and outcome with their
// duration, spend by tenant, provider and model, suggestions by action
// and confidence, decisions (acceptance) and the edit distance of
// accepted edits, and the queue depth.
package metrics

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Prometheus implements app.Metrics.
type Prometheus struct {
	jobs        *prometheus.CounterVec
	jobSeconds  *prometheus.HistogramVec
	cost        *prometheus.CounterVec
	suggestions *prometheus.CounterVec
	confidence  *prometheus.HistogramVec
	decisions   *prometheus.CounterVec
	editRatio   *prometheus.HistogramVec
	queue       *prometheus.GaugeVec
}

var _ app.Metrics = (*Prometheus)(nil)

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Prometheus{
		jobs: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_intelligence_jobs_total",
			Help: "AI translation jobs finished, by trigger, state (succeeded, skipped, failed, dead, queued for retry) and failure code.",
		}, []string{"trigger", "state", "code"})),
		jobSeconds: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_intelligence_job_duration_seconds",
			Help:    "Time from claiming a job to settling it, by state.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"state"})),
		cost: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_intelligence_cost_micro_usd_total",
			Help: "Provider spend in micro-USD, by tenant, provider and model.",
		}, []string{"tenant", "provider", "model"})),
		suggestions: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_intelligence_suggestions_total",
			Help: "Suggestions created, by target locale, origin (ai, translation_memory) and routed action.",
		}, []string{"locale", "origin", "action"})),
		confidence: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_intelligence_suggestion_confidence",
			Help:    "Confidence score of created suggestions, by origin.",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95, 1},
		}, []string{"origin"})),
		decisions: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_intelligence_suggestion_decisions_total",
			Help: "Decisions on suggestions, by locale and status (accepted, rejected, auto_applied): the acceptance rate is accepted / (accepted + rejected).",
		}, []string{"locale", "status"})),
		editRatio: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_intelligence_edit_ratio",
			Help:    "Normalized character edit distance of accepted suggestions (0 = accepted as is), by locale.",
			Buckets: []float64{0, 0.02, 0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1},
		}, []string{"locale"})),
		queue: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_intelligence_queue_jobs",
			Help: "Jobs queued and running across tenants.",
		}, []string{"state"})),
	}
}

// JobFinished implements app.Metrics.
func (p *Prometheus) JobFinished(trigger domain.Trigger, state domain.JobState, code string, d time.Duration) {
	p.jobs.WithLabelValues(string(trigger), string(state), code).Inc()
	p.jobSeconds.WithLabelValues(string(state)).Observe(d.Seconds())
}

// Spent implements app.Metrics.
func (p *Prometheus) Spent(tenant uuid.UUID, provider, model string, cost domain.MicroUSD) {
	if cost > 0 {
		p.cost.WithLabelValues(tenant.String(), provider, model).Add(float64(cost))
	}
}

// SuggestionCreated implements app.Metrics.
func (p *Prometheus) SuggestionCreated(locale string, origin domain.Origin, action domain.Action, score float64) {
	p.suggestions.WithLabelValues(locale, string(origin), string(action)).Inc()
	p.confidence.WithLabelValues(string(origin)).Observe(score)
}

// SuggestionDecided implements app.Metrics.
func (p *Prometheus) SuggestionDecided(locale string, status domain.SuggestionStatus, editRatio float64) {
	p.decisions.WithLabelValues(locale, string(status)).Inc()
	if status == domain.StatusAccepted {
		p.editRatio.WithLabelValues(locale).Observe(editRatio)
	}
}

// QueueDepth implements app.Metrics.
func (p *Prometheus) QueueDepth(depth map[domain.JobState]int) {
	for _, s := range []domain.JobState{domain.JobQueued, domain.JobRunning} {
		p.queue.WithLabelValues(string(s)).Set(float64(depth[s]))
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
