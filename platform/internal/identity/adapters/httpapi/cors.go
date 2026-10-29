package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
)

// CORS for the in-product editor (RFC 0004 §5.2, §5.3).
//
// glossa-server has exactly one cross-origin caller. Studio is served
// beside /v1 and is same-origin, so it needs none of this; the overlay
// runs on the product's own preview deployment and needs all of it.
// Three things keep the hole small:
//
//   - The surface is the operations that accept an in-context grant, and
//     nothing else. Both come from one `security:` line in the contract
//     (apiv1.InContextRoutes), so the editor's reach and what CORS
//     answers can't drift apart.
//   - The origin must be a registered preview origin of some project.
//     There is no wildcard, and no pattern matching: an origin is on the
//     list or it is not. Unregistering one stops the answers with the
//     same request — nothing is cached here.
//   - Credentials are never allowed. The editor holds a bearer grant and
//     sends `credentials: "omit"`; a browser that tried to send cookies
//     would have the response refused for it, which is the point.
//
// A preflight is answered before any credential is seen — it carries
// none — so the origin is checked against every project's registrations.
// The grant's own project binding does the rest, later, in the Guard.

// preflightMaxAge is how long a browser may cache a preflight. Ten
// minutes: long enough that an editing session makes one per endpoint,
// short enough that unregistering an origin is not undone by a cache
// for the rest of the day.
const preflightMaxAge = 10 * time.Minute

// allowedRequestHeaders are the headers the overlay sends beyond the
// CORS-safelisted ones: its grant, the ETag it writes against, and the
// key that makes a retried fill one fill.
var allowedRequestHeaders = []string{"Authorization", "Content-Type", "If-Match", "Idempotency-Key"}

// exposedResponseHeaders are what the overlay may read back. ETag is the
// one that matters: without it the editor cannot send If-Match, and
// every save would either clobber a concurrent edit or be impossible.
var exposedResponseHeaders = []string{"ETag"}

// registered reports whether origin is a preview origin of some
// project. It is a field so the middleware can be tested without a
// database; New points it at the service.
func (a *API) registered(ctx context.Context, origin string) (bool, error) {
	if a.originLookup == nil {
		return false, nil
	}
	return a.originLookup(ctx, origin)
}

// corsSurface answers "may a preview origin call this?" for a request.
// It is a ServeMux holding exactly the in-context routes, so matching
// follows the same precedence rules as the real router.
type corsSurface struct {
	mux     *http.ServeMux
	methods map[string][]string // route pattern -> methods, for Allow-Methods
}

func newCORSSurface() (*corsSurface, error) {
	routes, err := apiv1.InContextRoutes()
	if err != nil {
		return nil, err
	}
	s := &corsSurface{mux: http.NewServeMux(), methods: map[string][]string{}}
	for path, methods := range routes {
		for _, m := range methods {
			pattern := m + " " + path
			s.mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
			s.methods[pattern] = methods
		}
	}
	return s, nil
}

// match reports the route method would take, if it is on the surface.
func (s *corsSurface) match(r *http.Request, method string) (pattern string, ok bool) {
	probe, err := http.NewRequestWithContext(r.Context(), method, r.URL.String(), nil)
	if err != nil {
		return "", false
	}
	probe.Host = r.Host
	h, pattern := s.mux.Handler(probe)
	if h == nil || pattern == "" {
		return "", false
	}
	// ServeMux answers a path it knows with a method it doesn't by
	// registering a 405 handler under no pattern, so an empty pattern
	// is already the "not on the surface" answer.
	if _, known := s.methods[pattern]; !known {
		return "", false
	}
	return pattern, true
}

// Preflight answers OPTIONS under /v1. It is registered as its own route
// because the generated router knows only the real methods, so a
// preflight would otherwise fall through to the not-found handler with
// no CORS headers and an opaque failure in the browser.
//
// A preflight this refuses is answered 204 with no CORS headers at all:
// the browser then blocks the real request, which is what refusing
// means. Saying more would tell an unregistered page which endpoints
// exist.
func (a *API) Preflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	w.Header().Add("Vary", "Access-Control-Request-Method")
	defer w.WriteHeader(http.StatusNoContent)

	origin := r.Header.Get("Origin")
	method := r.Header.Get("Access-Control-Request-Method")
	if origin == "" || method == "" {
		return
	}
	pattern, ok := a.cors.match(r, method)
	if !ok {
		return
	}
	allowed, err := a.registered(r.Context(), origin)
	if err != nil {
		a.logger.ErrorContext(r.Context(), "preview origin lookup failed", "error", err)
		return
	}
	if !allowed {
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Methods", strings.Join(a.cors.methods[pattern], ", "))
	h.Set("Access-Control-Allow-Headers", strings.Join(allowedRequestHeaders, ", "))
	h.Set("Access-Control-Max-Age", strconv.Itoa(int(preflightMaxAge.Seconds())))
	// No Access-Control-Allow-Credentials, ever: with an allowed origin
	// that would let a page send this API the person's Studio cookies.
}

// CORS puts the headers on a real cross-origin response. It runs inside
// the router, next to the Guard, so it sees the route the request will
// take and can refuse one that isn't on the editor's surface before the
// handler does any work.
//
// It runs before the Guard so that a refused request still carries the
// headers: without them the browser hides the problem body, and the
// editor would show "network error" where the server said "your session
// expired".
func (a *API) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || sameOrigin(r, origin) {
			// Same-origin callers (Studio) get nothing added and nothing
			// taken away. Browsers send Origin on same-origin POSTs too.
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		if _, ok := a.cors.methods[r.Pattern]; !ok {
			next.ServeHTTP(w, r)
			return
		}
		allowed, err := a.registered(r.Context(), origin)
		if err != nil {
			a.errs.write(w, r, err)
			return
		}
		if allowed {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", strings.Join(exposedResponseHeaders, ", "))
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether origin names this very server, so Studio's
// own requests are left alone. The scheme is taken from the request:
// behind a TLS-terminating proxy r.TLS is nil, so a forwarded proto is
// honoured when present.
func sameOrigin(r *http.Request, origin string) bool {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(p, ",")[0]))
	}
	return strings.EqualFold(origin, scheme+"://"+r.Host)
}
