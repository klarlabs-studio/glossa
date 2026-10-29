// Package metrics records the Context context in Prometheus (RFC 0004
// §11): usage uploads and the usages they carried, by collector;
// capture uploads — captures, regions, images stored or deduplicated
// and the bytes stored, per tenant; and each project's context
// coverage — the share of its active messages with a current usage,
// and with a visible region on a current capture, on the default
// branch.
package metrics

import (
	"errors"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Prometheus implements app.Metrics.
type Prometheus struct {
	builds   *prometheus.CounterVec
	usages   *prometheus.CounterVec
	unknown  *prometheus.CounterVec
	coverage *prometheus.GaugeVec
	captures *prometheus.CounterVec
	regions  *prometheus.CounterVec
	images   *prometheus.CounterVec
	bytes    *prometheus.CounterVec
	used     *prometheus.GaugeVec
	quota    prometheus.Gauge
	captured *prometheus.GaugeVec
	purged   *prometheus.CounterVec
}

var _ app.Metrics = (*Prometheus)(nil)

// New registers the collectors on reg (a private registry when nil).
// Re-registering reuses the existing collectors.
func New(reg prometheus.Registerer) *Prometheus {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Prometheus{
		builds: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_builds_total",
			Help: "Usage uploads, by source (plugin, extract, runtime, capture) and outcome (stored, replayed).",
		}, []string{"source", "outcome"})),
		usages: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_usages_ingested_total",
			Help: "Usages stored by uploads, by source.",
		}, []string{"source"})),
		unknown: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_unknown_keys_total",
			Help: "Stored usages whose key the catalog didn't know, by source.",
		}, []string{"source"})),
		coverage: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_context_coverage_ratio",
			Help: "Share of a project's active messages with a current usage on the default branch (1 with none active), measured after each default-branch upload. Each instance reports what it measured last; take the max across instances.",
		}, []string{"tenant", "project"})),
		captures: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_captures_ingested_total",
			Help: "Captures stored by capture uploads, per tenant.",
		}, []string{"tenant"})),
		regions: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_regions_ingested_total",
			Help: "Regions stored by capture uploads, per tenant.",
		}, []string{"tenant"})),
		images: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_capture_images_total",
			Help: "Capture images uploaded, per tenant, by outcome (stored, deduplicated: the same pixels were stored already).",
		}, []string{"tenant", "outcome"})),
		bytes: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_capture_bytes_stored_total",
			Help: "Bytes of re-encoded capture images written to object storage, per tenant (retention deletes some later).",
		}, []string{"tenant"})),
		used: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_context_capture_bytes_used",
			Help: "Bytes of capture images a tenant holds in object storage now, measured after each capture upload and each retention run. Unlike the ingested bytes, this falls again when retention deletes images.",
		}, []string{"tenant"})),
		quota: register(reg, prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "glossa_context_capture_quota_bytes",
			Help: "The per-tenant capture storage quota this deployment enforces (GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES). An upload past it is refused with storage_quota_exceeded.",
		})),
		captured: register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "glossa_context_capture_coverage_ratio",
			Help: "Share of a project's active messages with a visible region on a current default-branch capture (1 with none active), measured after each default-branch upload. Each instance reports what it measured last; take the max across instances.",
		}, []string{"tenant", "project"})),
		purged: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "glossa_context_purge_deletions_total",
			Help: "What the daily retention run deleted, by kind (build, capture, image: an image no capture referenced any more, gone from object storage).",
		}, []string{"kind"})),
	}
}

// BuildIngested implements app.Metrics.
func (p *Prometheus) BuildIngested(source domain.Source, usages, unknownKeys int, replayed bool) {
	if replayed {
		p.builds.WithLabelValues(string(source), "replayed").Inc()
		return
	}
	p.builds.WithLabelValues(string(source), "stored").Inc()
	p.usages.WithLabelValues(string(source)).Add(float64(usages))
	p.unknown.WithLabelValues(string(source)).Add(float64(unknownKeys))
}

// Coverage implements app.Metrics.
func (p *Prometheus) Coverage(tenant tenancy.ID, project uuid.UUID, active, used int) {
	p.coverage.WithLabelValues(tenant.String(), project.String()).Set(ratio(active, used))
}

// CapturesIngested implements app.Metrics.
func (p *Prometheus) CapturesIngested(tenant tenancy.ID, captures, regions, stored, deduplicated int, bytes int64) {
	t := tenant.String()
	p.captures.WithLabelValues(t).Add(float64(captures))
	p.regions.WithLabelValues(t).Add(float64(regions))
	p.images.WithLabelValues(t, "stored").Add(float64(stored))
	p.images.WithLabelValues(t, "deduplicated").Add(float64(deduplicated))
	p.bytes.WithLabelValues(t).Add(float64(bytes))
}

// StorageUsed implements app.Metrics.
func (p *Prometheus) StorageUsed(tenant tenancy.ID, used, quota int64) {
	p.used.WithLabelValues(tenant.String()).Set(float64(used))
	p.quota.Set(float64(quota))
}

// CaptureCoverage implements app.Metrics.
func (p *Prometheus) CaptureCoverage(tenant tenancy.ID, project uuid.UUID, active, captured int) {
	p.captured.WithLabelValues(tenant.String(), project.String()).Set(ratio(active, captured))
}

// Purged implements app.Metrics. A run that deleted nothing still
// touches the series, so the counters exist before the first purge.
func (p *Prometheus) Purged(got app.Purged) {
	p.purged.WithLabelValues("build").Add(float64(len(got.Builds)))
	p.purged.WithLabelValues("capture").Add(float64(got.Captures))
	p.purged.WithLabelValues("image").Add(float64(got.ImagesDeleted))
}

func ratio(active, n int) float64 {
	if active == 0 {
		return 1
	}
	return float64(n) / float64(active)
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
