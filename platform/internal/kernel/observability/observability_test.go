package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestLoggerAddsCorrelationFields(t *testing.T) {
	var buf bytes.Buffer
	tenantAttr := func(context.Context) []slog.Attr { return []slog.Attr{slog.String("tenant_id", "t-1")} }
	logger := observability.NewLogger(&buf, slog.LevelInfo, tenantAttr)

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()
	ctx = observability.ContextWithRequestID(ctx, "req-42")

	logger.InfoContext(ctx, "hello", slog.Int("n", 1))
	logger.DebugContext(ctx, "filtered out")

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (debug filtered): %v", len(lines), lines)
	}
	got := lines[0]
	want := map[string]any{
		"message":    "hello",
		"trace_id":   span.SpanContext().TraceID().String(),
		"span_id":    span.SpanContext().SpanID().String(),
		"request_id": "req-42",
		"tenant_id":  "t-1",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (line %v)", k, got[k], v, got)
		}
	}
	// bolt writes the trace fields itself since v1.7.0; the kernel must
	// not add them a second time (duplicate JSON keys confuse log shippers).
	raw := buf.String()
	for _, key := range []string{`"trace_id"`, `"span_id"`} {
		if n := strings.Count(raw, key); n != 1 {
			t.Errorf("%s appears %d times, want 1: %s", key, n, raw)
		}
	}
}

func TestLoggerWithoutContextFields(t *testing.T) {
	var buf bytes.Buffer
	observability.NewLogger(&buf, slog.LevelInfo).Info("plain")
	line := decodeLines(t, &buf)[0]
	for _, k := range []string{"trace_id", "span_id", "request_id"} {
		if _, ok := line[k]; ok {
			t.Errorf("%s present without context", k)
		}
	}
}

func TestTracerProviderDisabledIsNoop(t *testing.T) {
	tp, shutdown, err := observability.NewTracerProvider(context.Background(), config.OTel{ServiceName: "glossa-server"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	_, span := tp.Tracer("x").Start(context.Background(), "op")
	if span.SpanContext().IsValid() {
		t.Error("disabled tracer produced a recording span")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestTracerProviderEnabled(t *testing.T) {
	tp, shutdown, err := observability.NewTracerProvider(context.Background(),
		config.OTel{Endpoint: "http://127.0.0.1:1", ServiceName: "glossa-server"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	_, span := tp.Tracer("x").Start(context.Background(), "op")
	if !span.SpanContext().IsValid() {
		t.Error("enabled tracer produced an invalid span")
	}
	span.End()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // don't wait on the unreachable collector
	_ = shutdown(ctx)
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name    string
		inbound string
		reuse   bool
	}{
		{"generated when absent", "", false},
		{"propagated when valid", "abc-123_x.y", true},
		{"replaced when invalid", "bad id\nwith newline", false},
		{"replaced when too long", strings.Repeat("a", 129), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			h := observability.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen, _ = observability.RequestIDFromContext(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.inbound != "" {
				req.Header.Set(observability.RequestIDHeader, tc.inbound)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			echoed := rec.Header().Get(observability.RequestIDHeader)
			if seen == "" || echoed != seen {
				t.Fatalf("context id %q, header %q", seen, echoed)
			}
			if (seen == tc.inbound) != tc.reuse {
				t.Errorf("id = %q, inbound %q, reuse want %t", seen, tc.inbound, tc.reuse)
			}
		})
	}
}

func TestAccessLogRecordsRouteStatusAndMetrics(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, slog.LevelDebug)
	reg := observability.NewRegistry()
	httpMetrics := observability.NewHTTPMetrics(reg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	})
	h := observability.AccessLog(logger, httpMetrics)(mux)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/projects/p-123", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope", nil))

	lines := decodeLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines", len(lines))
	}
	first := lines[0]
	if first["route"] != "GET /v1/projects/{id}" || first["status"] != float64(418) || first["bytes"] != float64(15) {
		t.Errorf("access log = %v", first)
	}
	if lines[1]["route"] != "unmatched" {
		t.Errorf("unmatched route logged as %v", lines[1]["route"])
	}

	// Metrics are labelled by route pattern, never the raw path.
	if n := testutil.ToFloat64(httpMetrics.Requests.WithLabelValues("GET", "GET /v1/projects/{id}", "418")); n != 1 {
		t.Errorf("request counter = %v, want 1", n)
	}
	if n := testutil.ToFloat64(httpMetrics.Requests.WithLabelValues("GET", "unmatched", "404")); n != 1 {
		t.Errorf("unmatched counter = %v, want 1", n)
	}
}
