package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// Checks records the PR-check series of RFC 0004 §11: check latency
// from the pull-request event to the completed check, checks by
// conclusion, and the sticky comment's upserts.
type Checks struct {
	completed *prometheus.CounterVec
	latency   *prometheus.HistogramVec
	handled   *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	comments  *prometheus.CounterVec
	depth     prometheus.Gauge
}

// NewChecks registers the collectors on reg (a private registry when
// nil). Re-registering reuses the existing collectors.
func NewChecks(reg prometheus.Registerer) *Checks {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Checks{
		completed: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_github_checks_total",
			Help: "Glossa check runs completed, by conclusion: success, failure, neutral (no Glossa CI run for the commit).",
		}, []string{"conclusion"})),
		latency: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "glossa_github_check_latency_seconds",
			Help: "Seconds from the pull-request event to the completed check, by conclusion. It spans CI's own upload, so the neutral bucket sits at the 30-minute wait.",
			// From a fast CI run to the 30-minute wait and beyond.
			Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1200, 1800, 2400},
		}, []string{"conclusion"})),
		handled: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_github_check_jobs_total",
			Help: "Attempts by the check worker, by outcome: done, waiting (this commit's CI has not uploaded yet), ignored (the repository is no longer connected), retry, failed.",
		}, []string{"outcome"})),
		duration: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_github_check_job_duration_seconds",
			Help:    "How long one check-worker attempt took, by outcome.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"outcome"})),
		comments: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_github_comment_upserts_total",
			Help: "Writes of a pull request's one sticky comment, by outcome (ok, failed). A pull request has one comment however often it is written.",
		}, []string{"outcome"})),
		depth: register(reg, prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "glossa_github_check_queue_depth",
			Help: "Checks waiting to run. The queue is shared, so every replica reports the same number: take the max across instances, not the sum.",
		})),
	}
}

var _ app.CheckMetrics = (*Checks)(nil)

// CheckCompleted implements app.CheckMetrics.
func (c *Checks) CheckCompleted(conclusion string, latency time.Duration) {
	l := conclusionLabel(conclusion)
	c.completed.WithLabelValues(l).Inc()
	c.latency.WithLabelValues(l).Observe(latency.Seconds())
}

// CheckHandled implements app.CheckMetrics.
func (c *Checks) CheckHandled(outcome string, d time.Duration) {
	o := outcomeLabel(outcome)
	c.handled.WithLabelValues(o).Inc()
	c.duration.WithLabelValues(o).Observe(d.Seconds())
}

// CommentUpserted implements app.CheckMetrics.
func (c *Checks) CommentUpserted(outcome string) {
	c.comments.WithLabelValues(outcomeLabel(outcome)).Inc()
}

// QueueDepth implements app.CheckMetrics.
func (c *Checks) QueueDepth(n int) { c.depth.Set(float64(n)) }

// conclusionLabel keeps the label set to the three conclusions Glossa
// writes.
func conclusionLabel(s string) string {
	switch s {
	case app.ConclusionSuccess, app.ConclusionFailure, app.ConclusionNeutral:
		return s
	}
	return "unknown"
}

func outcomeLabel(s string) string {
	switch s {
	case "done", "waiting", "ignored", "retry", "failed", "ok":
		return s
	}
	return "unknown"
}
