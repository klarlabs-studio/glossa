// Package httperr is the stdlib net/http adapter for apierr. Works
// with anything that accepts an http.ResponseWriter: bare net/http,
// gorilla/mux, chi, httprouter, the new ServeMux in 1.22+, etc.
//
// Two helpers:
//
//	httperr.Send(w, errs.ValidationEmailRequired.WithParam("field", "email"))
//	httperr.SendErr(w, err) // unwraps to apierr.Error or wraps as 500
//
// Both write the canonical { "error": ... } JSON envelope and set
// Content-Type + status. The Write step is best-effort: if it fails
// (e.g., the client hung up mid-response) the error is dropped — the
// caller already has nothing to do at that point.
package httperr

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/apierr"
)

// Send writes the envelope and sets the status. After this call the
// response is committed; do not write further. Returns the error from
// the underlying Encode for callers that want to log a Write failure
// — almost always safe to ignore.
func Send(w http.ResponseWriter, e *apierr.Error) error {
	if e == nil {
		// Defensive — a nil typed error here is a caller bug. Emit a
		// generic 500 rather than serialise "null".
		e = apierr.New("internal_error", "errors.internal", "Internal server error", http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	return json.NewEncoder(w).Encode(e.Body())
}

// SendErr is the catch-all: unwraps to *apierr.Error via errors.As if
// possible, otherwise wraps the unknown err as a generic 500 with the
// underlying message preserved for logs. Use at the top of handler
// chains where you may catch errors from deeper layers that don't
// know about apierr.
//
// Passing a nil err is a no-op — Send is not called, and the caller
// is expected to write its own non-error response.
func SendErr(w http.ResponseWriter, err error) error {
	if err == nil {
		return nil
	}
	var typed *apierr.Error
	if errors.As(err, &typed) {
		return Send(w, typed)
	}
	wrapped := apierr.New(
		"internal_error",
		"errors.internal",
		"Internal server error",
		http.StatusInternalServerError,
	).Wrap(err)
	return Send(w, wrapped)
}
