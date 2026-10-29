package httpapi_test

import (
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/httpapi"
)

func TestCreateAssignmentPath(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/v1/tenants/t1/assignments", true},
		{http.MethodGet, "/v1/tenants/t1/assignments", false},
		{http.MethodPost, "/v1/tenants/t1/assignments/a1/completion", false},
		{http.MethodPost, "/v1/tenants//assignments", false},
		{http.MethodPost, "/v1/tenants/t1/projects/p1/assignments", false},
	} {
		if got := httpapi.CreateAssignmentPath(tc.method, tc.path); got != tc.want {
			t.Errorf("CreateAssignmentPath(%s %s) = %t", tc.method, tc.path, got)
		}
	}
}
