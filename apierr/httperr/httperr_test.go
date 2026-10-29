package httperr_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.klarlabs.de/glossa/apierr"
	"go.klarlabs.de/glossa/apierr/httperr"
)

func TestSend_WritesEnvelopeAndStatus(t *testing.T) {
	w := httptest.NewRecorder()
	e := apierr.New("validation_email_required",
		"validation.email.required",
		"Email address is required", http.StatusBadRequest).
		WithParam("field", "email")

	if err := httperr.Send(w, e); err != nil {
		t.Fatalf("Send: %v", err)
	}
	res := w.Result()
	if res.StatusCode != 400 {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wrapped, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("envelope shape lost: %v", body)
	}
	if wrapped["code"] != "validation_email_required" {
		t.Errorf("code = %v", wrapped["code"])
	}
	if wrapped["message"] != "Email address is required" {
		t.Errorf("message = %v", wrapped["message"])
	}
	params, _ := wrapped["params"].(map[string]any)
	if params["field"] != "email" {
		t.Errorf("params.field = %v", params)
	}
}

func TestSend_NilWrapsAsInternal(t *testing.T) {
	w := httptest.NewRecorder()
	_ = httperr.Send(w, nil)
	if w.Result().StatusCode != 500 {
		t.Errorf("nil should yield 500, got %d", w.Result().StatusCode)
	}
}

func TestSendErr_UnwrapsTypedError(t *testing.T) {
	w := httptest.NewRecorder()
	typed := apierr.New("conflict", "conflict.key", "Conflict", http.StatusConflict)
	_ = httperr.SendErr(w, typed)
	if w.Result().StatusCode != 409 {
		t.Errorf("status = %d, want 409", w.Result().StatusCode)
	}
}

func TestSendErr_WrapsUnknownAs500(t *testing.T) {
	w := httptest.NewRecorder()
	_ = httperr.SendErr(w, errors.New("something broke"))
	res := w.Result()
	if res.StatusCode != 500 {
		t.Errorf("status = %d, want 500", res.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	wrapped := body["error"].(map[string]any)
	if wrapped["code"] != "internal_error" {
		t.Errorf("code = %v", wrapped["code"])
	}
	// Wrap appends cause to message — log readability
	msg, _ := wrapped["message"].(string)
	if msg != "Internal server error: something broke" {
		t.Errorf("message = %q", msg)
	}
}

func TestSendErr_NilIsNoop(t *testing.T) {
	w := httptest.NewRecorder()
	if err := httperr.SendErr(w, nil); err != nil {
		t.Fatalf("nil err should be a no-op, got %v", err)
	}
	if w.Result().StatusCode != 200 {
		t.Errorf("default status changed: %d", w.Result().StatusCode)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body should be empty, got %q", w.Body.String())
	}
}
