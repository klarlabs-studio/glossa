package tenancy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

var (
	// ErrUnauthenticated means the request carries no valid credentials.
	ErrUnauthenticated = errors.New("tenancy: unauthenticated")
	// ErrForbidden means the principal may not act in any tenant here.
	ErrForbidden = errors.New("tenancy: forbidden")
)

// Resolver derives the tenant of an authenticated request. It is the
// port the Identity context implements (sessions, API tokens).
//
// Implementations MUST derive the tenant from server-side state — a
// session looked up by its cookie, a token looked up by its hash — and
// never from client-supplied headers, query parameters or bodies. A
// tenant named in the URL path (/v1/tenants/{tenant}/…) is a request,
// not a fact: the resolver must check it against the principal's
// memberships or the token's tenant before returning it.
//
// The returned context is derived from r.Context() and carries whatever
// else the resolver established (Identity puts the principal and its
// grant there); the middleware adds the tenant to it. A nil context means
// r.Context(). Return [ErrUnauthenticated] or [ErrForbidden] (wrapped is
// fine) for the two client errors; any other error is a server fault.
type Resolver interface {
	ResolveTenant(r *http.Request) (context.Context, ID, error)
}

// Middleware scopes each request to the tenant its [Resolver] returns,
// so downstream handlers (and the tenant-scoped unit of work) find it
// with [FromContext]. Requests that can't be scoped are rejected here.
func Middleware(res Resolver, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, id, err := res.ResolveTenant(r)
			if err == nil && id.IsZero() {
				err = errors.New("tenancy: resolver returned no tenant and no error")
			}
			if err != nil {
				writeResolveError(w, r, logger, err)
				return
			}
			if ctx == nil {
				ctx = r.Context()
			}
			next.ServeHTTP(w, r.WithContext(ContextWithTenant(ctx, id)))
		})
	}
}

func writeResolveError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		problem.Write(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, ErrForbidden):
		problem.Write(w, http.StatusForbidden, "no access to this tenant")
	default:
		logger.ErrorContext(r.Context(), "tenant resolution failed", slog.Any("error", err))
		problem.Write(w, http.StatusInternalServerError, "internal error")
	}
}
