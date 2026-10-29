package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

func TestTransfersKeepThePathPrefixAndTheTokenOnTheServer(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the API token went to another host: %v", r.Header)
		}
		w.Header().Set("ETag", `W/"abc"`)
		_, _ = io.WriteString(w, "bytes")
	}))
	t.Cleanup(storage.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("no token on the server: %v", r.Header)
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/glossa/v1/tenants/t1/import-jobs/j1/file":
			body, _ := io.ReadAll(r.Body)
			if string(body) != "file" || r.ContentLength != 4 || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("upload = %q (%d, %s)", body, r.ContentLength, r.Header.Get("Content-Type"))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "state": "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/glossa/v1/tenants/t1/export-jobs/j2/file":
			w.Header().Set("ETag", `"0123"`)
			_, _ = io.WriteString(w, "exported")
		default:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusGone)
			_, _ = io.WriteString(w, `{"code":"file_expired","detail":"gone"}`)
		}
	}))
	t.Cleanup(api.Close)
	c, err := remote.New(api.URL+"/glossa", token, remote.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job, err := c.UploadImportFile(ctx, "/v1/tenants/t1/import-jobs/j1/file", strings.NewReader("file"), 4)
	if err != nil || job.Id != "j1" {
		t.Fatalf("upload = %+v, %v", job, err)
	}
	d, err := c.DownloadExportFile(ctx, "/v1/tenants/t1/export-jobs/j2/file")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if string(body) != "exported" || d.ETag != "0123" {
		t.Errorf("download = %q, etag %q", body, d.ETag)
	}
	d, err = c.DownloadExportFile(ctx, storage.URL+"/presigned")
	if err != nil || d.ETag != "abc" {
		t.Fatalf("download elsewhere = %+v, %v", d, err)
	}
	_ = d.Body.Close()
	_, err = c.DownloadExportFile(ctx, "/v1/tenants/t1/export-jobs/j3/file")
	var ae *remote.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusGone || ae.Code != "file_expired" {
		t.Errorf("expired download = %v", err)
	}
}
