package problem_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) problem.Details {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	var got problem.Details
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestWriteUsesTheGenericCodeForTheStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	problem.Write(rec, http.StatusRequestEntityTooLarge, "body exceeds 1024 bytes")

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d", rec.Code)
	}
	got := decode(t, rec)
	want := problem.Details{
		Type:   "urn:glossa:problem:payload_too_large",
		Title:  "Request Entity Too Large",
		Status: 413,
		Code:   "payload_too_large",
		Detail: "body exceeds 1024 bytes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %+v, want %+v", got, want)
	}
}

func TestGenericCodes(t *testing.T) {
	for status, want := range map[int]problem.Code{
		400: problem.CodeInvalidRequest,
		401: problem.CodeUnauthenticated,
		403: problem.CodeForbidden,
		404: problem.CodeNotFound,
		409: problem.CodeConflict,
		412: problem.CodePreconditionFailed,
		422: problem.CodeUnprocessable,
		428: problem.CodePreconditionRequired,
		429: problem.CodeRateLimited,
		500: problem.CodeInternal,
		418: problem.CodeInternal, // unknown statuses fall back
	} {
		if got := problem.CodeForStatus(status); got != want {
			t.Errorf("CodeForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestDetailsIsAnError(t *testing.T) {
	p := problem.New(http.StatusConflict, "slug_taken", "the slug acme is taken").
		WithErrors(problem.FieldError{Pointer: "/slug", Detail: "taken"})
	err := fmt.Errorf("create tenant: %w", p)

	var got *problem.Details
	if !errors.As(err, &got) {
		t.Fatal("a wrapped *Details is not found by errors.As")
	}
	if got.Type != "urn:glossa:problem:slug_taken" || got.Title != "Conflict" || got.Code != "slug_taken" {
		t.Errorf("details = %+v", got)
	}
	if got.Error() != "slug_taken: the slug acme is taken" {
		t.Errorf("Error() = %q", got.Error())
	}

	rec := httptest.NewRecorder()
	problem.WriteDetails(rec, got)
	body := decode(t, rec)
	if rec.Code != http.StatusConflict || len(body.Errors) != 1 || body.Errors[0].Pointer != "/slug" {
		t.Errorf("written = %d %+v", rec.Code, body)
	}
}

func TestNewRejectsMalformedCodes(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted a code that is not snake_case")
		}
	}()
	problem.New(http.StatusBadRequest, "Not-A-Code", "")
}
