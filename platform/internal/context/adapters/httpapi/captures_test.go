package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func TestCaptureUploadPath(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/v1/tenants/t1/projects/p1/captures", true},
		{http.MethodGet, "/v1/tenants/t1/projects/p1/captures", false},
		{http.MethodPost, "/v1/tenants/t1/projects/p1/captures/c1/image", false},
		{http.MethodPost, "/v1/tenants//projects/p1/captures", false},
		{http.MethodPost, "/v1/tenants/t1/projects/p1/context-builds", false},
	}
	for _, tc := range cases {
		if got := CaptureUploadPath(tc.method, tc.path); got != tc.want {
			t.Errorf("CaptureUploadPath(%s %s) = %t", tc.method, tc.path, got)
		}
	}
}

func TestCaptureErrorsMapToTheDocumentedCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   problem.Code
	}{
		{fmt.Errorf("%w: captures[0]: route", domain.ErrInvalidCaptures), 400, "invalid_captures"},
		{fmt.Errorf("%w: %w", domain.ErrInvalidCaptures, domain.ErrInvalidBranch), 400, "invalid_captures"},
		{domain.ErrTooManyCaptures, 400, "too_many_captures"},
		{domain.ErrTooManyRegions, 400, "too_many_regions"},
		{domain.ErrManifestTooLarge, 413, "payload_too_large"},
		{fmt.Errorf("%w: not a PNG", domain.ErrInvalidImage), 400, "invalid_image"},
		{domain.ErrImageTooLarge, 413, "image_too_large"},
		{fmt.Errorf("%w: 12 bytes over", app.ErrStorageQuotaExceeded), 413, "storage_quota_exceeded"},
		{&http.MaxBytesError{Limit: 10}, 413, "payload_too_large"},
		{fmt.Errorf("x: %w", app.ErrStorageUnavailable), 503, "storage_unavailable"},
		{app.ErrCaptureNotFound, 404, "not_found"},
	}
	for _, tc := range cases {
		var d *problem.Details
		if !errors.As(mapError(tc.err), &d) || d.Status != tc.status || d.Code != tc.code {
			t.Errorf("mapError(%v) = %v, want %d %s", tc.err, d, tc.status, tc.code)
		}
	}
}

func TestETagMatching(t *testing.T) {
	etag := `"abc"`
	for header, want := range map[string]bool{
		`"abc"`: true, `W/"abc"`: true, `"x", "abc"`: true, `*`: true, `"abd"`: false, ``: false,
	} {
		if got := etagMatches(header, etag); got != want {
			t.Errorf("etagMatches(%q) = %t", header, got)
		}
	}
}

// multipartBody writes parts (name, body) as one multipart body.
func multipartBody(t *testing.T, parts ...[2]string) *multipart.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		fw, err := w.CreateFormFile(p[0], p[0])
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(fw, p[1])
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	r, err := req.MultipartReader()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReadManifestAndImageParts(t *testing.T) {
	r := multipartBody(t, [2]string{"manifest", `{"schema":"glossa.captures/v1"}`}, [2]string{"abc", "png bytes"})
	m, err := readManifest(r)
	if err != nil || string(m) != `{"schema":"glossa.captures/v1"}` {
		t.Fatalf("manifest = %q, %v", m, err)
	}
	parts := &imageParts{r: r}
	name, body, err := parts.Next()
	if err != nil || name != "abc" {
		t.Fatalf("part = %q, %v", name, err)
	}
	if b, _ := io.ReadAll(body); string(b) != "png bytes" {
		t.Errorf("part body = %q", b)
	}
	if _, _, err := parts.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("after the last part: err = %v", err)
	}

	if _, err := readManifest(multipartBody(t, [2]string{"abc", "png"})); !errors.Is(err, domain.ErrInvalidCaptures) {
		t.Errorf("an image first: err = %v", err)
	}
	if _, err := readManifest(multipartBody(t)); !errors.Is(err, domain.ErrInvalidCaptures) {
		t.Errorf("no parts: err = %v", err)
	}
	big := multipartBody(t, [2]string{"manifest", strings.Repeat(" ", domain.MaxManifestBytes+1)})
	if _, err := readManifest(big); !errors.Is(err, domain.ErrManifestTooLarge) {
		t.Errorf("a manifest over the limit: err = %v", err)
	}
	// A body that breaks off inside a part is the uploader's problem.
	broken := multipart.NewReader(strings.NewReader("--b\r\nContent-Disposition: form-data; name=\"manifest\"\r\n\r\n{\"sch"), "b")
	if _, err := readManifest(broken); !errors.Is(err, domain.ErrInvalidCaptures) {
		t.Errorf("a broken body: err = %v", err)
	}
}

func TestImagePath(t *testing.T) {
	id := uuid.MustParse("0192a1b2-0000-7000-8000-0000000000cc")
	if got := ImagePath("t1", "p1", id); got != "/v1/tenants/t1/projects/p1/captures/"+id.String()+"/image" {
		t.Errorf("ImagePath = %s", got)
	}
}
