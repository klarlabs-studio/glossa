package httpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// recoverPanics turns a handler panic into a logged 500 so one bad
// request can't take the connection down with a half-written response.
// http.ErrAbortHandler is re-panicked: it's net/http's deliberate abort.
func recoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if p == http.ErrAbortHandler { // identity comparison, as net/http itself does
					panic(p)
				}
				logger.ErrorContext(r.Context(), "handler panicked",
					slog.String("panic", fmt.Sprint(p)), slog.String("stack", string(debug.Stack())))
				problem.Write(w, http.StatusInternalServerError, "internal error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// limitBody caps request bodies: a declared oversize body is refused
// up front; an undeclared one fails with *http.MaxBytesError on read.
// Requests large names get their policy's limit (none when it is 0:
// the handler enforces its own while it streams), and read and write
// deadlines in place of the server's timeouts (best effort: a writer
// that can't set deadlines keeps the server's).
func limitBody(def int64, large func(*http.Request) (BodyPolicy, bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := def
			if large != nil {
				if p, ok := large(r); ok {
					rc, deadline := http.NewResponseController(w), time.Now().Add(p.Timeout)
					_ = rc.SetReadDeadline(deadline)
					_ = rc.SetWriteDeadline(deadline)
					if p.MaxBytes <= 0 {
						next.ServeHTTP(w, r)
						return
					}
					limit = p.MaxBytes
				}
			}
			if r.ContentLength > limit {
				problem.Write(w, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("request body exceeds %d bytes", limit))
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeaders sets headers every API response should carry.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
