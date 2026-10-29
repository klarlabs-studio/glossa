package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/httpserver"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func httpConfig() config.HTTP {
	return config.HTTP{
		Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second, MaxBodyBytes: 16,
	}
}

func newServer(t *testing.T, checks []httpserver.Check, routes func(*http.ServeMux)) *httpserver.Server {
	t.Helper()
	return httpserver.New(httpConfig(), httpserver.Deps{
		Logger:    slog.New(slog.DiscardHandler),
		Registry:  observability.NewRegistry(),
		Readiness: checks,
		Routes:    routes,
	})
}

func do(t *testing.T, h http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, body))
	return rec
}

func TestLivez(t *testing.T) {
	rec := do(t, newServer(t, nil, nil).Handler(), http.MethodGet, "/livez", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Header().Get(observability.RequestIDHeader) == "" {
		t.Error("no request id on response")
	}
}

func TestReadyz(t *testing.T) {
	failing := errors.New("connection refused to 10.0.0.5")
	tests := []struct {
		name   string
		check  error
		drain  bool
		status int
	}{
		{"ready", nil, false, http.StatusOK},
		{"dependency down", failing, false, http.StatusServiceUnavailable},
		{"draining", nil, true, http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, []httpserver.Check{{
				Name:  "postgres",
				Probe: func(context.Context) error { return tc.check },
			}}, nil)
			if tc.drain {
				srv.StartDraining()
			}
			rec := do(t, srv.Handler(), http.MethodGet, "/readyz", nil)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Error("readiness body leaks the dependency error")
			}
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := newServer(t, nil, nil).Handler()
	do(t, h, http.MethodGet, "/livez", nil)
	rec := do(t, h, http.MethodGet, "/metrics", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `glossa_http_requests_total{method="GET",route="GET /livez",status="200"} 1`) {
		t.Errorf("metrics missing request counter:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Error("runtime collectors missing")
	}
}

func TestPanicRecovery(t *testing.T) {
	h := newServer(t, nil, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("nil pointer") })
	}).Handler()

	rec := do(t, h, http.MethodGet, "/boom", nil)
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != problem.ContentType {
		t.Errorf("status = %d, content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.Contains(rec.Body.String(), "nil pointer") {
		t.Error("panic value leaked to the client")
	}
	if rec := do(t, h, http.MethodGet, "/livez", nil); rec.Code != http.StatusOK {
		t.Error("server unusable after a panic")
	}
}

func TestBodyLimit(t *testing.T) {
	h := newServer(t, nil, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(r.Body); err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					problem.Write(w, http.StatusRequestEntityTooLarge, "too big")
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}).Handler()

	if rec := do(t, h, http.MethodPost, "/echo", strings.NewReader("small")); rec.Code != http.StatusNoContent {
		t.Errorf("small body: status %d", rec.Code)
	}
	// Declared oversize: rejected before the handler runs.
	if rec := do(t, h, http.MethodPost, "/echo", strings.NewReader(strings.Repeat("x", 64))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize body: status %d", rec.Code)
	}
	// Undeclared (chunked) oversize: cut off while reading.
	req := httptest.NewRequest(http.MethodPost, "/echo", io.NopCloser(strings.NewReader(strings.Repeat("x", 64))))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked oversize body: status %d", rec.Code)
	}
}

// Routes that stream files (uploads) get their own body limit.
func TestLargeBodyRoutes(t *testing.T) {
	h := httpserver.New(httpConfig(), httpserver.Deps{
		Logger: slog.New(slog.DiscardHandler), Registry: observability.NewRegistry(),
		Routes: func(mux *http.ServeMux) {
			mux.HandleFunc("PUT /files/{name}", func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.ReadAll(r.Body); err != nil {
					problem.Write(w, http.StatusRequestEntityTooLarge, "too big")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		},
		LargeBodies: func(r *http.Request) (httpserver.BodyPolicy, bool) {
			if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/files/") {
				max := int64(100)
				if r.URL.Path == "/files/streamed" {
					max = 0 // the handler enforces its own
				}
				return httpserver.BodyPolicy{MaxBytes: max, Timeout: time.Minute}, true
			}
			return httpserver.BodyPolicy{}, false
		},
	}).Handler()
	if rec := do(t, h, http.MethodPut, "/files/streamed", strings.NewReader(strings.Repeat("x", 1000))); rec.Code != http.StatusNoContent {
		t.Errorf("a route that enforces its own limit: status %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/files/a", strings.NewReader(strings.Repeat("x", 64))); rec.Code != http.StatusNoContent {
		t.Errorf("64 bytes under a 100-byte route limit: status %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/files/a", strings.NewReader(strings.Repeat("x", 101))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over the route limit: status %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/files/a", strings.NewReader(strings.Repeat("x", 64))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("other routes keep the default limit: status %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := do(t, newServer(t, nil, nil).Handler(), http.MethodGet, "/livez", nil)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
}

func TestNotFoundIsProblemJSON(t *testing.T) {
	rec := do(t, newServer(t, nil, nil).Handler(), http.MethodGet, "/nope", nil)
	var p problem.Details
	if rec.Code != http.StatusNotFound || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Status != 404 {
		t.Errorf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestServeAndShutdown(t *testing.T) {
	srv := newServer(t, nil, nil)
	errc := make(chan error, 1)
	ln, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go func() { errc <- srv.Serve(ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/livez")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-errc; err != nil {
		t.Errorf("Serve returned %v after graceful shutdown", err)
	}
}
