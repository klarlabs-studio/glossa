package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func TestToProblem(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   problem.Code
	}{
		{fmt.Errorf("wrapped: %w", app.ErrLinkInvalid), 401, "link_invalid"},
		{app.ErrAccountLocked, 429, "account_locked"},
		{domain.ErrLastOwner, 409, "last_owner"},
		{app.ErrIdempotencyReuse, 422, "idempotency_key_reused"},
		{&authz.DeniedError{Permission: authz.CatalogWrite}, 403, "forbidden"},
		{problem.New(400, "invalid_page_size", "x"), 400, "invalid_page_size"},
		{errors.New("database on fire"), 500, "internal"},
	}
	for _, tc := range tests {
		d, _ := toProblem(tc.err)
		if d.Status != tc.status || d.Code != tc.code {
			t.Errorf("toProblem(%v) = %d %s, want %d %s", tc.err, d.Status, d.Code, tc.status, tc.code)
		}
	}
	d, known := toProblem(errors.New("pq: password authentication failed for user secret"))
	if known || strings.Contains(d.Detail, "secret") {
		t.Errorf("an unknown error leaks: %+v", d)
	}
	d, _ = toProblem(&authz.DeniedError{Permission: authz.CatalogWrite})
	if d.Detail != "missing permission catalog.write" {
		t.Errorf("denied detail = %q", d.Detail)
	}
}

func TestEveryMappedCodeIsSnakeCase(t *testing.T) {
	for _, p := range problems {
		_ = problem.New(p.status, p.code, "") // panics on a malformed code
	}
}

func TestParamErrorMakesMissingIfMatch428(t *testing.T) {
	e := errorWriter{}
	rec := httptest.NewRecorder()
	e.ParamError(rec, httptest.NewRequest("PATCH", "/", nil), &apiv1.RequiredHeaderError{ParamName: "If-Match"})
	if rec.Code != http.StatusPreconditionRequired {
		t.Errorf("status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.ParamError(rec, httptest.NewRequest("GET", "/", nil), &apiv1.InvalidParamFormatError{ParamName: "page_size", Err: errors.New("x")})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestETags(t *testing.T) {
	if etag(3) != `"3"` {
		t.Errorf("etag = %s", etag(3))
	}
	for in, want := range map[string]int{`"3"`: 3, `W/"12"`: 12, ` "1" `: 1} {
		if got, err := parseETag(in); err != nil || got != want {
			t.Errorf("parseETag(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{``, `*`, `3`, `"x"`, `"0"`} {
		if _, err := parseETag(in); !errors.Is(err, app.ErrPreconditionFailed) {
			t.Errorf("parseETag(%q) err = %v", in, err)
		}
	}
}

func TestCookies(t *testing.T) {
	c := sessionCookie("abc", 14*24*time.Hour)
	for _, want := range []string{"__Host-glossa_session=abc", "Path=/", "Max-Age=1209600", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(c, want) {
			t.Errorf("session cookie %q lacks %q", c, want)
		}
	}
	if strings.Contains(c, "Domain=") {
		t.Error("a __Host- cookie must not set Domain")
	}
	if c := clearedSessionCookie(); !strings.Contains(c, "Max-Age=0") {
		t.Errorf("cleared cookie = %q", c)
	}
	if c := ceremonyCookie("k"); !strings.Contains(c, "SameSite=Strict") || !strings.Contains(c, "Max-Age=300") {
		t.Errorf("ceremony cookie = %q", c)
	}
}

func TestCSRFTokenIsBoundToTheSessionAndKey(t *testing.T) {
	a := &API{csrfKey: []byte(strings.Repeat("k", 32))}
	b := &API{csrfKey: []byte(strings.Repeat("j", 32))}
	tok := a.csrfToken("session-1")
	if !a.validCSRF("session-1", tok) {
		t.Error("own token rejected")
	}
	if a.validCSRF("session-2", tok) || b.validCSRF("session-1", tok) || a.validCSRF("session-1", "") {
		t.Error("token accepted for another session, key, or empty")
	}
}
