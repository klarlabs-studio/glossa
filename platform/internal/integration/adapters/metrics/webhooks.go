package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// Webhooks records the webhook series of RFC 0004 §11: deliveries by
// event and outcome, the inbox's depth, and how long a handler took.
type Webhooks struct {
	deliveries *prometheus.CounterVec
	handled    *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	depth      *prometheus.GaugeVec

	// seen remembers the events the depth gauge has published, so an
	// event whose backlog drains is set to zero rather than left at its
	// last value.
	mu   sync.Mutex
	seen map[string]bool
}

// NewWebhooks registers the collectors on reg (a private registry when
// nil). Re-registering reuses the existing collectors.
func NewWebhooks(reg prometheus.Registerer) *Webhooks {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Webhooks{
		seen: map[string]bool{},
		deliveries: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_github_webhook_deliveries_total",
			Help: "Webhook deliveries at the endpoint, by GitHub event and outcome: accepted, duplicate (a delivery ID seen inside the replay window), rejected (the signature did not verify), too_large, unconfigured.",
		}, []string{"event", "outcome"})),
		handled: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_github_webhook_handled_total",
			Help: "Attempts by the inbox worker, by GitHub event and outcome: done, ignored, retry, failed. At-least-once delivery means one delivery can count several attempts.",
		}, []string{"event", "outcome"})),
		duration: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_github_webhook_handler_duration_seconds",
			Help:    "How long one inbox-worker attempt took, by GitHub event and outcome.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"event", "outcome"})),
		depth: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_github_webhook_inbox_depth",
			Help: "Deliveries waiting in the webhook inbox, by GitHub event. The inbox is shared, so every replica reports the same number: take the max across instances, not the sum.",
		}, []string{"event"})),
	}
}

var _ app.GitHubMetrics = (*Webhooks)(nil)

// DeliveryReceived implements app.GitHubMetrics.
func (w *Webhooks) DeliveryReceived(event, outcome string) {
	w.deliveries.WithLabelValues(eventLabel(event), outcome).Inc()
}

// DeliveryHandled implements app.GitHubMetrics.
func (w *Webhooks) DeliveryHandled(event, outcome string, d time.Duration) {
	e := eventLabel(event)
	w.handled.WithLabelValues(e, outcome).Inc()
	w.duration.WithLabelValues(e, outcome).Observe(d.Seconds())
}

// InboxDepth implements app.GitHubMetrics.
func (w *Webhooks) InboxDepth(depth map[string]int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for event, n := range depth {
		e := eventLabel(event)
		w.seen[e] = true
		w.depth.WithLabelValues(e).Set(float64(n))
	}
	for e := range w.seen {
		if _, ok := depth[e]; !ok {
			w.depth.WithLabelValues(e).Set(0)
		}
	}
}

// eventLabel keeps the label set bounded: an event name is lowercase
// letters and underscores, and anything else is "unknown".
func eventLabel(event string) string {
	if event == "" || len(event) > 64 {
		return "unknown"
	}
	for _, r := range event {
		if (r < 'a' || r > 'z') && r != '_' {
			return "unknown"
		}
	}
	return event
}
