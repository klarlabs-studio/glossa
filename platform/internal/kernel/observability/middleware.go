package observability

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RequestIDHeader carries the correlation ID in and out.
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// ContextWithRequestID returns ctx carrying the correlation ID.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the request's correlation ID.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey{}).(string)
	return id, ok && id != ""
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestID assigns each request a correlation ID. A well-formed inbound
// X-Request-ID (from the ingress or a calling service) is kept so logs
// line up across hops; anything else is replaced. The ID is echoed in
// the response. It's for correlation only and never trusted for auth.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !requestIDPattern.MatchString(id) {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ContextWithRequestID(r.Context(), id)))
	})
}

// AccessLog logs each request and records its RED metrics. It must wrap
// the ServeMux directly (nothing in between may copy the request): the
// mux writes the matched pattern into the request, and that pattern is
// the route label and the span name.
func AccessLog(logger *slog.Logger, m *HTTPMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start)

			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			status := rec.statusCode()
			span := trace.SpanFromContext(r.Context())
			span.SetName(r.Method + " " + strings.TrimPrefix(route, r.Method+" "))
			span.SetAttributes(attribute.String("http.route", route))

			m.Requests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
			m.Duration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
			logger.Log(r.Context(), accessLevel(route, status), "http request",
				slog.String("method", r.Method), slog.String("route", route),
				slog.String("path", r.URL.Path), slog.Int("status", status),
				slog.Int64("bytes", rec.bytes), slog.Duration("duration", elapsed))
		})
	}
}

// accessLevel keeps probe and scrape noise at debug and surfaces 5xx.
func accessLevel(route string, status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case strings.HasSuffix(route, "/livez"), strings.HasSuffix(route, "/readyz"), strings.HasSuffix(route, "/metrics"):
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// statusRecorder captures the status and size. Unwrap lets
// http.ResponseController reach Flush/Hijack on the real writer.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) statusCode() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}
