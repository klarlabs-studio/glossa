package metrics_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/context/adapters/metrics"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestPrometheusRecordsUploadsAndCoverage(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	if metrics.New(reg) == nil {
		t.Fatal("registering twice reuses the collectors")
	}
	m.BuildIngested(domain.SourcePlugin, 120, 3, false)
	m.BuildIngested(domain.SourcePlugin, 120, 3, true)
	m.BuildIngested(domain.SourceExtract, 7, 0, false)
	tenant, project := tenancy.NewID(), uuid.New()
	m.Coverage(tenant, project, 4, 3)
	empty := uuid.New()
	m.Coverage(tenant, empty, 0, 0)

	want := `
# HELP glossa_context_usages_ingested_total Usages stored by uploads, by source.
# TYPE glossa_context_usages_ingested_total counter
glossa_context_usages_ingested_total{source="extract"} 7
glossa_context_usages_ingested_total{source="plugin"} 120
# HELP glossa_context_builds_total Usage uploads, by source (plugin, extract, runtime, capture) and outcome (stored, replayed).
# TYPE glossa_context_builds_total counter
glossa_context_builds_total{outcome="replayed",source="plugin"} 1
glossa_context_builds_total{outcome="stored",source="extract"} 1
glossa_context_builds_total{outcome="stored",source="plugin"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"glossa_context_usages_ingested_total", "glossa_context_builds_total"); err != nil {
		t.Error(err)
	}
	g, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	ratios := map[string]float64{}
	for _, f := range g {
		if f.GetName() != "glossa_context_coverage_ratio" {
			continue
		}
		for _, s := range f.GetMetric() {
			for _, l := range s.GetLabel() {
				if l.GetName() == "project" {
					ratios[l.GetValue()] = s.GetGauge().GetValue()
				}
			}
		}
	}
	if ratios[project.String()] != 0.75 || ratios[empty.String()] != 1 {
		t.Errorf("coverage = %v", ratios)
	}
}

func TestPrometheusRecordsCaptureUploadsAndCaptureCoverage(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	tenant, project := tenancy.NewID(), uuid.New()
	m.CapturesIngested(tenant, 3, 40, 2, 1, 5000)
	m.CapturesIngested(tenant, 1, 2, 0, 1, 0)
	m.CaptureCoverage(tenant, project, 4, 1)
	tl := `tenant="` + tenant.String() + `"`
	want := `
# HELP glossa_context_capture_bytes_stored_total Bytes of re-encoded capture images written to object storage, per tenant (retention deletes some later).
# TYPE glossa_context_capture_bytes_stored_total counter
glossa_context_capture_bytes_stored_total{` + tl + `} 5000
# HELP glossa_context_capture_images_total Capture images uploaded, per tenant, by outcome (stored, deduplicated: the same pixels were stored already).
# TYPE glossa_context_capture_images_total counter
glossa_context_capture_images_total{outcome="deduplicated",` + tl + `} 2
glossa_context_capture_images_total{outcome="stored",` + tl + `} 2
# HELP glossa_context_captures_ingested_total Captures stored by capture uploads, per tenant.
# TYPE glossa_context_captures_ingested_total counter
glossa_context_captures_ingested_total{` + tl + `} 4
# HELP glossa_context_regions_ingested_total Regions stored by capture uploads, per tenant.
# TYPE glossa_context_regions_ingested_total counter
glossa_context_regions_ingested_total{` + tl + `} 42
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "glossa_context_capture_bytes_stored_total",
		"glossa_context_capture_images_total", "glossa_context_captures_ingested_total", "glossa_context_regions_ingested_total"); err != nil {
		t.Error(err)
	}
	if n, err := testutil.GatherAndCount(reg, "glossa_context_capture_coverage_ratio"); err != nil || n != 1 {
		t.Errorf("capture coverage series = %d, %v", n, err)
	}
}

func TestPrometheusReportsCaptureStorageAgainstTheQuota(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	tenant := tenancy.NewID()
	m.StorageUsed(tenant, 5000, 2<<30)
	// Retention frees space: the gauge falls, unlike the ingested bytes.
	m.StorageUsed(tenant, 1200, 2<<30)

	want := `
# HELP glossa_context_capture_bytes_used Bytes of capture images a tenant holds in object storage now, measured after each capture upload and each retention run. Unlike the ingested bytes, this falls again when retention deletes images.
# TYPE glossa_context_capture_bytes_used gauge
glossa_context_capture_bytes_used{tenant="` + tenant.String() + `"} 1200
# HELP glossa_context_capture_quota_bytes The per-tenant capture storage quota this deployment enforces (GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES). An upload past it is refused with storage_quota_exceeded.
# TYPE glossa_context_capture_quota_bytes gauge
glossa_context_capture_quota_bytes 2.147483648e+09
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"glossa_context_capture_bytes_used", "glossa_context_capture_quota_bytes"); err != nil {
		t.Error(err)
	}
}

func TestPrometheusCountsPurgeDeletionsByKind(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	m.Purged(app.Purged{
		Builds:         []uuid.UUID{uuid.New(), uuid.New()},
		Captures:       5,
		OrphanedImages: []domain.Digest{"a", "b", "c"},
		// One delete failed, so only two images actually went.
		ImagesDeleted: 2,
	})
	// A run that deleted nothing keeps the series and adds nothing.
	m.Purged(app.Purged{})

	want := `
# HELP glossa_context_purge_deletions_total What the daily retention run deleted, by kind (build, capture, image: an image no capture referenced any more, gone from object storage).
# TYPE glossa_context_purge_deletions_total counter
glossa_context_purge_deletions_total{kind="build"} 2
glossa_context_purge_deletions_total{kind="capture"} 5
glossa_context_purge_deletions_total{kind="image"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "glossa_context_purge_deletions_total"); err != nil {
		t.Error(err)
	}
}
