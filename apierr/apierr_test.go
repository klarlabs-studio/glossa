package apierr_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/apierr"
)

func TestNew_DefaultsStatusTo500WhenZero(t *testing.T) {
	e := apierr.New("oops", "oops.key", "Oops", 0)
	if e.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", e.Status)
	}
}

func TestErrorSatisfiesErrorInterface(t *testing.T) {
	var e error = apierr.New("x", "x.key", "boom", 500)
	if e.Error() != "boom" {
		t.Fatalf("Error() = %q", e.Error())
	}
	var asTyped *apierr.Error
	if !errors.As(e, &asTyped) {
		t.Fatal("errors.As should unwrap to *apierr.Error")
	}
}

func TestWithParam_ReturnsClone(t *testing.T) {
	base := apierr.New("v", "v.key", "msg", 400)
	withFoo := base.WithParam("foo", "bar")
	withBaz := base.WithParam("baz", 42)

	if len(base.Params) != 0 {
		t.Fatalf("base mutated: %v", base.Params)
	}
	if withFoo.Params["foo"] != "bar" {
		t.Fatalf("withFoo missing foo: %v", withFoo.Params)
	}
	if _, has := withFoo.Params["baz"]; has {
		t.Fatalf("withFoo should not see withBaz's param: %v", withFoo.Params)
	}
	if withBaz.Params["baz"] != 42 {
		t.Fatalf("withBaz missing baz: %v", withBaz.Params)
	}
}

func TestWithParam_Chains(t *testing.T) {
	e := apierr.New("v", "v.key", "msg", 400).
		WithParam("a", 1).
		WithParam("b", 2)
	if e.Params["a"] != 1 || e.Params["b"] != 2 {
		t.Fatalf("chained params lost: %v", e.Params)
	}
}

func TestWithMessage_OverridesOnlyMessage(t *testing.T) {
	base := apierr.New("conflict", "conflict.key", "Resource already exists", 409)
	custom := base.WithMessage("Project slug already exists")
	if custom.Message != "Project slug already exists" {
		t.Fatalf("Message = %q", custom.Message)
	}
	if custom.Code != base.Code || custom.Key != base.Key || custom.Status != base.Status {
		t.Fatalf("non-message fields mutated: %+v", custom)
	}
	if base.Message != "Resource already exists" {
		t.Fatalf("base mutated: %q", base.Message)
	}
}

func TestWrap_AppendsCauseToMessageOnly(t *testing.T) {
	base := apierr.New("db_error", "db.error", "Database failure", 500)
	cause := errors.New("connection refused")
	wrapped := base.Wrap(cause)
	if wrapped.Message != "Database failure: connection refused" {
		t.Fatalf("Message = %q", wrapped.Message)
	}
	// Wire shape stays intact — no extra fields, no `cause` leakage
	b, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, has := decoded["cause"]; has {
		t.Fatalf("cause leaked into wire shape: %v", decoded)
	}
}

func TestBody_WrapsInErrorEnvelope(t *testing.T) {
	e := apierr.New("c", "c.key", "m", 418)
	body := e.Body()
	wrapped, ok := body["error"].(*apierr.Error)
	if !ok {
		t.Fatalf("body['error'] type = %T", body["error"])
	}
	if wrapped.Code != "c" {
		t.Fatalf("envelope lost the error: %+v", wrapped)
	}
}

func TestJSONWireShape(t *testing.T) {
	e := apierr.New("validation_email_required",
		"validation.email.required",
		"Email address is required", 400).
		WithParam("field", "email")

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"code":    "validation_email_required",
		"message": "Email address is required",
		"key":     "validation.email.required",
		"params":  map[string]any{"field": "email"},
		"status":  float64(400),
	}
	for k, v := range want {
		got, has := decoded[k]
		if !has {
			t.Errorf("missing field %q in wire shape: %v", k, decoded)
			continue
		}
		// map equality is shallow-fine for this fixture
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(v)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("field %q: got %s, want %s", k, gotJSON, wantJSON)
		}
	}
}

func TestParamsOmitEmpty(t *testing.T) {
	e := apierr.New("c", "c.key", "m", 400)
	b, _ := json.Marshal(e)
	if string(b) == "" {
		t.Fatal("empty marshal")
	}
	var decoded map[string]any
	_ = json.Unmarshal(b, &decoded)
	if _, has := decoded["params"]; has {
		t.Fatalf("params should be omitempty when nil/empty: %v", decoded)
	}
}
