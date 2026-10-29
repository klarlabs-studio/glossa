package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func TestUploadPath(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/v1/tenants/t1/projects/p1/context-builds", true},
		{http.MethodGet, "/v1/tenants/t1/projects/p1/context-builds", false},
		{http.MethodPost, "/v1/tenants/t1/projects/p1/context-builds/x", false},
		{http.MethodPost, "/v1/tenants//projects/p1/context-builds", false},
		{http.MethodPost, "/v1/tenants/t1/projects/p1/message-upserts", false},
	}
	for _, tc := range cases {
		if got := UploadPath(tc.method, tc.path); got != tc.want {
			t.Errorf("UploadPath(%s %s) = %t", tc.method, tc.path, got)
		}
	}
}

func TestErrorsMapToTheDocumentedCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   problem.Code
	}{
		{fmt.Errorf("%w: usages[3]: kind", domain.ErrInvalidUpload), 400, "invalid_usages"},
		{domain.ErrTooManyUsages, 400, "too_many_usages"},
		{domain.ErrUploadTooLarge, 413, "payload_too_large"},
		{domain.ErrInvalidSource, 400, "invalid_source"},
		{domain.ErrInvalidBranch, 400, "invalid_branch"},
		{app.ErrApplicationNotFound, 400, "unknown_application"},
		{app.ErrRateLimited, 429, "rate_limited"},
		{app.ErrProjectNotFound, 404, "not_found"},
		{app.ErrMessageNotFound, 404, "not_found"},
	}
	for _, tc := range cases {
		var d *problem.Details
		if !errors.As(mapError(tc.err), &d) || d.Status != tc.status || d.Code != tc.code {
			t.Errorf("mapError(%v) = %v, want %d %s", tc.err, d, tc.status, tc.code)
		}
	}
	other := errors.New("boom")
	if mapError(other) != other {
		t.Error("an unknown error was mapped")
	}
}
