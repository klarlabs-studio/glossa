// Package httpserver is glossa-server's HTTP edge: a net/http ServeMux
// (Go 1.22+ method and wildcard patterns) behind the kernel's middleware
// chain, plus the operational endpoints /livez, /readyz and /metrics.
//
// Bounded contexts register their routes through Deps.Routes; they get
// tracing, correlation IDs, access logs, RED metrics, panic recovery and
// body limits without doing anything.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

const maxHeaderBytes = 64 << 10

// Deps are the server's collaborators.
type Deps struct {
	Logger *slog.Logger
	// TracerProvider and Propagator default to no-op / W3C when nil.
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
	// Registry backs /metrics and receives the HTTP metrics.
	Registry *prometheus.Registry
	// Readiness checks gate /readyz (e.g. a Postgres ping).
	Readiness []Check
	// Routes registers the bounded contexts' handlers.
	Routes func(mux *http.ServeMux)
	// LargeBodies names the requests that stream files (uploads,
	// downloads) and the policy they get instead of the defaults; nil
	// means none.
	LargeBodies func(r *http.Request) (BodyPolicy, bool)
}

// BodyPolicy replaces the default body limit and read/write timeouts
// for a request that streams a file.
type BodyPolicy struct {
	// MaxBytes bounds the request body; 0 leaves it to the handler,
	// which enforces its own limit while it streams.
	MaxBytes int64
	// Timeout is the read and write deadline, from the request's start.
	Timeout time.Duration
}

// Server is the configured HTTP server.
type Server struct {
	http     *http.Server
	logger   *slog.Logger
	checks   []Check
	draining atomic.Bool
}

// New builds the server; it doesn't listen yet.
func New(cfg config.HTTP, deps Deps) *Server {
	s := &Server{logger: deps.Logger, checks: deps.Readiness}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", s.livez)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(deps.Registry, promhttp.HandlerOpts{Registry: deps.Registry}))
	mux.HandleFunc("/", notFound)
	if deps.Routes != nil {
		deps.Routes(mux)
	}

	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.chain(mux, cfg, deps),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(deps.Logger.Handler(), slog.LevelWarn),
	}
	return s
}

// chain wraps mux, outermost first. AccessLog must sit directly on the
// path to the mux (no request copies below it) to read the route.
func (s *Server) chain(mux http.Handler, cfg config.HTTP, deps Deps) http.Handler {
	tp, prop := deps.TracerProvider, deps.Propagator
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	if prop == nil {
		prop = observability.NewPropagator()
	}
	tracing := otelhttp.NewMiddleware("http.server",
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(prop),
		otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
		otelhttp.WithFilter(func(r *http.Request) bool { return !isOperational(r.URL.Path) }),
	)
	metrics := observability.NewHTTPMetrics(deps.Registry)

	var h http.Handler = mux
	h = limitBody(cfg.MaxBodyBytes, deps.LargeBodies)(h)
	h = securityHeaders(h)
	h = recoverPanics(s.logger)(h)
	h = observability.AccessLog(s.logger, metrics)(h)
	h = observability.RequestID(h)
	return tracing(h)
}

// Handler returns the full handler chain (for tests).
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Listen binds the configured address.
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return nil, fmt.Errorf("httpserver: listen on %s: %w", s.http.Addr, err)
	}
	return ln, nil
}

// Serve serves on ln until Shutdown; a graceful stop returns nil.
func (s *Server) Serve(ln net.Listener) error {
	if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("httpserver: serve: %w", err)
	}
	return nil
}

// StartDraining makes /readyz fail so load balancers stop routing here
// while in-flight requests finish.
func (s *Server) StartDraining() { s.draining.Store(true) }

// Shutdown drains and stops the server, waiting for in-flight requests
// until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	s.StartDraining()
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}
	return nil
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	problem.Write(w, http.StatusNotFound, "no such resource")
}

func isOperational(path string) bool {
	return path == "/livez" || path == "/readyz" || path == "/metrics"
}
