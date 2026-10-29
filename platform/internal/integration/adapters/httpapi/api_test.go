package httpapi_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/httpapi"
)

func TestFileRoutes(t *testing.T) {
	const tenant, job = "0192a1b2-0000-7000-8000-000000000001", "0192a1b2-0000-7000-8000-000000000002"
	for _, tc := range []struct {
		method, path     string
		upload, download bool
	}{
		{"PUT", "/v1/tenants/" + tenant + "/import-jobs/" + job + "/file", true, false},
		{"GET", "/v1/tenants/" + tenant + "/export-jobs/" + job + "/file", false, true},
		{"GET", "/v1/tenants/" + tenant + "/import-jobs/" + job + "/file", false, false},
		{"PUT", "/v1/tenants/" + tenant + "/import-jobs/" + job, false, false},
		{"PUT", "/v1/tenants/" + tenant + "/import-jobs//file", false, false},
		{"PUT", "/v1/tenants/" + tenant + "/import-jobs/" + job + "/file/x", false, false},
		{"PUT", "/v1/tenants/" + tenant + "/projects/" + job + "/file", false, false},
	} {
		if got := httpapi.UploadPath(tc.method, tc.path); got != tc.upload {
			t.Errorf("UploadPath(%s %s) = %t", tc.method, tc.path, got)
		}
		if got := httpapi.DownloadPath(tc.method, tc.path); got != tc.download {
			t.Errorf("DownloadPath(%s %s) = %t", tc.method, tc.path, got)
		}
	}
}
