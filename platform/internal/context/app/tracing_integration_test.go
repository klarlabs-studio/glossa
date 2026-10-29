//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"image/png"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.klarlabs.de/glossa/platform/internal/context/app"
)

// recordedSpans collects the spans a run produced.
func recordedSpans(t *testing.T) (trace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp, rec
}

func spanNamed(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func attrs(s sdktrace.ReadOnlySpan) map[string]string {
	out := map[string]string{}
	for _, kv := range s.Attributes() {
		out[string(kv.Key)] = kv.Value.Emit()
	}
	return out
}

// TestACIUploadIsOneTrace covers RFC 0004 §11: a CI upload is one trace
// from ingest to the events it raises. The outbox row carries the
// span's trace context, so the delivery joins the upload's trace
// instead of starting its own.
func TestACIUploadIsOneTrace(t *testing.T) {
	tp, rec := recordedSpans(t)
	h, _ := withImages(t, app.WithTracerProvider(tp))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")

	out := h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 7}, use{"gone.key", "b.vue", 2})

	span := spanNamed(rec.Ended(), "context.ingest_usages")
	if span == nil {
		t.Fatalf("no ingest span; recorded %d spans", len(rec.Ended()))
	}
	got := attrs(span)
	for key, want := range map[string]string{
		"glossa.project_id":   f.project.String(),
		"glossa.source":       "plugin",
		"glossa.build_id":     out.Build.ID.String(),
		"glossa.branch":       "main",
		"glossa.usages":       "2",
		"glossa.unknown_keys": "1",
		"glossa.replayed":     "false",
	} {
		if got[key] != want {
			t.Errorf("span attribute %s = %q, want %q", key, got[key], want)
		}
	}

	// The event the ingest published carries that span's trace.
	var raw []byte
	if err := env.Super.QueryRow(context.Background(),
		"SELECT trace_context FROM outbox_events WHERE event_type = 'context.build.ingested'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var carrier map[string]string
	if err := json.Unmarshal(raw, &carrier); err != nil {
		t.Fatal(err)
	}
	if tp := carrier["traceparent"]; !strings.Contains(tp, span.SpanContext().TraceID().String()) {
		t.Errorf("event traceparent = %q, want the upload's trace %s", tp, span.SpanContext().TraceID())
	}

	// A capture upload is traced the same way.
	img := pngOf(t, 16, 12, 9, png.BestSpeed)
	h.uploadCaptures(t, f, manifestOf(t, "bcdef12", "main", capture{"/checkout", "de", img, []string{"checkout.pay"}}), partsOf(img))
	shot := spanNamed(rec.Ended(), "context.ingest_captures")
	if shot == nil {
		t.Fatal("no capture upload span")
	}
	if a := attrs(shot); a["glossa.captures"] != "1" || a["glossa.images_stored"] != "1" {
		t.Errorf("capture span attributes = %v", a)
	}
}
