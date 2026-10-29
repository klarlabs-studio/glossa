package tenancy_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

type resolverFunc func(*http.Request) (tenancy.ID, error)

type principalKey struct{}

// ResolveTenant also decorates the context, as Identity does with the
// principal, so the test can prove the decoration reaches the handler.
func (f resolverFunc) ResolveTenant(r *http.Request) (context.Context, tenancy.ID, error) {
	id, err := f(r)
	return context.WithValue(r.Context(), principalKey{}, "ada"), id, err
}

func TestMiddleware(t *testing.T) {
	want := tenancy.NewID()
	tests := []struct {
		name       string
		resolve    resolverFunc
		wantStatus int
		wantTenant bool
	}{
		{
			name:       "resolved tenant reaches the handler",
			resolve:    func(*http.Request) (tenancy.ID, error) { return want, nil },
			wantStatus: http.StatusOK,
			wantTenant: true,
		},
		{
			name:       "unauthenticated is 401",
			resolve:    func(*http.Request) (tenancy.ID, error) { return tenancy.ID{}, tenancy.ErrUnauthenticated },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "no tenant access is 403",
			resolve:    func(*http.Request) (tenancy.ID, error) { return tenancy.ID{}, tenancy.ErrForbidden },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolver failure is 500",
			resolve:    func(*http.Request) (tenancy.ID, error) { return tenancy.ID{}, errors.New("session store down") },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "zero ID without error is a resolver bug, 500",
			resolve:    func(*http.Request) (tenancy.ID, error) { return tenancy.ID{}, nil },
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, ok := tenancy.FromContext(r.Context())
				reached = ok && got == want && r.Context().Value(principalKey{}) == "ada"
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/projects", nil)
			// A client-supplied header must never influence the tenant.
			req.Header.Set("X-Tenant-ID", tenancy.NewID().String())

			tenancy.Middleware(tc.resolve, slog.New(slog.DiscardHandler))(next).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if reached != tc.wantTenant {
				t.Errorf("handler saw tenant = %t, want %t", reached, tc.wantTenant)
			}
		})
	}
}
