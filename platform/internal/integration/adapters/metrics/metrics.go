// Package metrics records the Integration context in Prometheus
// (RFC 0004 §11): the GitHub App's calls, by status, with the rate
// limit the API last left the installation, and the webhook side —
// deliveries by event and outcome, the inbox's depth, and how long a
// handler took.
//
// The PR check's own series (§6.4) live in checks.go: check latency
// from the pull-request event to the completed check, checks by
// conclusion, and the sticky comment's upserts.
package metrics

import (
	"errors"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
)

// GitHub records one collector set for the GitHub App's HTTP calls.
// Give OnCall to github.Options.
type GitHub struct {
	calls     *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	remaining *prometheus.GaugeVec
}

// NewGitHub registers the collectors on reg (a private registry when
// nil). Re-registering reuses the existing collectors.
func NewGitHub(reg prometheus.Registerer) *GitHub {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &GitHub{
		calls: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_github_calls_total",
			Help: "Calls to the GitHub App API, by operation and HTTP status (0: no answer came back).",
		}, []string{"op", "status"})),
		duration: register(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "glossa_github_call_duration_seconds",
			Help:    "How long a GitHub App API call took, by operation, retries and waiting excluded.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op"})),
		remaining: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_github_rate_limit_remaining",
			Help: "Calls GitHub last said were left in the window, by resource (core, search, graphql). Each instance reports what it saw last; take the min across instances.",
		}, []string{"resource"})),
	}
}

// OnCall is the github.Options hook: it records one exchange. It never
// blocks and never panics on a call GitHub didn't answer.
func (g *GitHub) OnCall(info github.CallInfo) {
	op := info.Op
	if op == "" {
		op = "unknown"
	}
	g.calls.WithLabelValues(op, strconv.Itoa(info.Status)).Inc()
	g.duration.WithLabelValues(op).Observe(info.Duration.Seconds())
	// A call GitHub answered without rate-limit headers (or one it never
	// answered) says nothing about the window: leave the gauge alone.
	if info.RateLimit.Limit > 0 {
		resource := info.RateLimit.Resource
		if resource == "" {
			resource = "core"
		}
		g.remaining.WithLabelValues(resource).Set(float64(info.RateLimit.Remaining))
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
		panic(err) // a name clash with an unrelated collector is a programming error
	}
	return c
}
