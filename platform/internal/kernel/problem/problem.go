// Package problem writes RFC 9457 problem details, the error body of
// every glossa-server HTTP response.
//
// Every problem carries a stable, machine-readable Code (snake_case) and
// a Type URI derived from it (urn:glossa:problem:<code>). Clients branch
// on the code, never on Title or Detail, which are for humans and may
// change. A code, once published in api/openapi.yaml, is never renamed or
// reused for a different meaning.
package problem

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// ContentType is the RFC 9457 media type.
const ContentType = "application/problem+json"

// TypePrefix is prepended to a Code to form the problem's type URI. A URN
// rather than a URL: the type is an identifier and must not depend on a
// documentation host staying up (RFC 9457 §3.1.1).
const TypePrefix = "urn:glossa:problem:"

// Code is a stable machine-readable problem code, e.g. "slug_taken".
type Code string

// Generic codes, one per status glossa-server emits. Specific codes
// (declared by bounded contexts and listed in the OpenAPI contract)
// refine these.
const (
	CodeInvalidRequest       Code = "invalid_request"
	CodeUnauthenticated      Code = "unauthenticated"
	CodeForbidden            Code = "forbidden"
	CodeNotFound             Code = "not_found"
	CodeMethodNotAllowed     Code = "method_not_allowed"
	CodeConflict             Code = "conflict"
	CodePreconditionFailed   Code = "precondition_failed"
	CodePayloadTooLarge      Code = "payload_too_large"
	CodeUnprocessable        Code = "unprocessable"
	CodePreconditionRequired Code = "precondition_required"
	CodeRateLimited          Code = "rate_limited"
	CodeInternal             Code = "internal"
	CodeUnavailable          Code = "unavailable"
)

var genericCodes = map[int]Code{
	http.StatusBadRequest:            CodeInvalidRequest,
	http.StatusUnauthorized:          CodeUnauthenticated,
	http.StatusForbidden:             CodeForbidden,
	http.StatusNotFound:              CodeNotFound,
	http.StatusMethodNotAllowed:      CodeMethodNotAllowed,
	http.StatusConflict:              CodeConflict,
	http.StatusPreconditionFailed:    CodePreconditionFailed,
	http.StatusRequestEntityTooLarge: CodePayloadTooLarge,
	http.StatusUnprocessableEntity:   CodeUnprocessable,
	http.StatusPreconditionRequired:  CodePreconditionRequired,
	http.StatusTooManyRequests:       CodeRateLimited,
	http.StatusInternalServerError:   CodeInternal,
	http.StatusServiceUnavailable:    CodeUnavailable,
}

// CodeForStatus returns the generic code for an HTTP status.
func CodeForStatus(status int) Code {
	if c, ok := genericCodes[status]; ok {
		return c
	}
	return CodeInternal
}

// FieldError points at one invalid member of a request body.
type FieldError struct {
	// Pointer is a JSON Pointer (RFC 6901) into the request body.
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

// Details is the RFC 9457 body plus Glossa's extension members, code
// and errors.
type Details struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Code     Code         `json:"code"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
}

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// New returns a problem with the given status, code and detail. Detail is
// shown to the caller: never put secrets or internal errors in it. A
// malformed code is a programming error and panics.
func New(status int, code Code, detail string) *Details {
	if !codePattern.MatchString(string(code)) {
		panic("problem: code must be snake_case: " + string(code))
	}
	return &Details{
		Type:   TypePrefix + string(code),
		Title:  http.StatusText(status),
		Status: status,
		Code:   code,
		Detail: detail,
	}
}

// WithErrors attaches field errors and returns d.
func (d *Details) WithErrors(errs ...FieldError) *Details {
	d.Errors = append(d.Errors, errs...)
	return d
}

// Error makes a *Details usable as an error, so application code can
// return one and the HTTP edge can find it with errors.As.
func (d *Details) Error() string {
	if d.Detail == "" {
		return string(d.Code)
	}
	return string(d.Code) + ": " + d.Detail
}

// WriteDetails sends d.
func WriteDetails(w http.ResponseWriter, d *Details) {
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(d.Status)
	_ = json.NewEncoder(w).Encode(d)
}

// Write sends a problem with the status's generic code.
func Write(w http.ResponseWriter, status int, detail string) {
	WriteDetails(w, New(status, CodeForStatus(status), detail))
}
