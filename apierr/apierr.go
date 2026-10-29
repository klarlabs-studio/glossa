// Package apierr defines a framework-agnostic JSON envelope that
// carries both a stable machine-readable error code, a default
// English message for logs and curl users, and a glossa translation
// key + params for i18n rendering on the client.
//
// Shape on the wire:
//
//	{
//	  "error": {
//	    "code":    "validation_email_required",
//	    "message": "Email address is required",
//	    "key":     "validation.email.required",
//	    "params":  {"field": "email"},
//	    "status":  400
//	  }
//	}
//
// Field roles:
//   - code:    stable identifier for log filters / alerting — never
//     renamed once shipped
//   - message: the canonical English literal; what gets logged + what
//     non-glossa-aware clients render
//   - key:     glossa translation key the web frontend resolves via
//     `<glossa-text>` or `glossa.resolveError(...)`
//   - params:  interpolation values for the key (`{field}` etc.) — keeps
//     the server free of locale-specific string concat
//   - status:  HTTP status echoed in the body for clients that don't
//     read headers (mobile SDKs, browser fetch wrappers)
//
// Importers in other Go services (ascend, brotwerk, pet-medical) should
// declare a single registry of errors at startup:
//
//	var ValidationEmailRequired = apierr.New(
//	    "validation_email_required",
//	    "validation.email.required",
//	    "Email address is required",
//	    http.StatusBadRequest,
//	)
//
//	// At call site:
//	return ValidationEmailRequired.WithParam("field", "email")
//
// The base package has zero external dependencies. Framework adapters
// (gin, echo, net/http) live in subpackages so importers only pull in
// what they use.
package apierr

import (
	"fmt"
	"maps"
)

// Error is the wire-shaped envelope. Implements the standard error
// interface via the Message field so it composes with errors.Is / As
// and zerolog/slog formatters.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Key     string         `json:"key"`
	Params  map[string]any `json:"params,omitempty"`
	Status  int            `json:"status"`
}

// New constructs an Error template. The returned value is typically
// stored as a package-level var and cloned via WithParam at call time
// so two concurrent requests don't share the same Params map.
//
// status defaults to 500 when caller passes 0 or negative — better
// than silently shipping a 0 status to the client.
func New(code, key, message string, status int) *Error {
	if status <= 0 {
		status = 500
	}
	return &Error{
		Code:    code,
		Message: message,
		Key:     key,
		Status:  status,
	}
}

// Error satisfies the error interface. Returns the canonical English
// message so log lines + stack traces stay readable.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// WithParam returns a copy of the receiver with one extra interpolation
// param added. The receiver isn't mutated, so a registry-level
// `var ValidationEmailRequired = apierr.New(...)` stays safe to use
// concurrently — every call site gets its own clone.
//
// Repeated WithParam calls compose: errs.X.WithParam("a", 1).WithParam("b", 2).
func (e *Error) WithParam(k string, v any) *Error {
	out := e.clone()
	out.Params[k] = v
	return out
}

// WithParams is the bulk variant — useful when handlers already have
// a map ready to attach (e.g., validation libraries that return a
// field/violation table).
func (e *Error) WithParams(p map[string]any) *Error {
	out := e.clone()
	for k, v := range p {
		out.Params[k] = v
	}
	return out
}

// WithMessage overrides the default English message for one site —
// rare, but useful when a generic error needs a specific phrasing
// for a particular handler (e.g., "Project slug already exists" vs
// the default "Resource already exists"). The Key + Code stay the
// same; only Message changes.
func (e *Error) WithMessage(m string) *Error {
	out := e.clone()
	out.Message = m
	return out
}

// WithStatus overrides the default HTTP status. Use sparingly — most
// errors should ship with the status the registry declared.
func (e *Error) WithStatus(s int) *Error {
	out := e.clone()
	out.Status = s
	return out
}

// Wrap is the "I caught an underlying err and want to bubble it as
// this typed apierr" path. The wrapped error's text is appended to
// Message so logs carry the root cause; the wire shape only exposes
// the apierr fields. Wrapping a nil err is a no-op clone.
func (e *Error) Wrap(err error) *Error {
	out := e.clone()
	if err != nil {
		out.Message = fmt.Sprintf("%s: %v", out.Message, err)
	}
	return out
}

// Body wraps the receiver in the canonical { "error": ... } envelope
// so framework adapters can serialise it directly:
//
//	c.AbortWithStatusJSON(e.Status, e.Body())
func (e *Error) Body() map[string]any {
	return map[string]any{"error": e}
}

func (e *Error) clone() *Error {
	c := *e
	c.Params = make(map[string]any, len(e.Params)+1)
	maps.Copy(c.Params, e.Params)
	return &c
}
