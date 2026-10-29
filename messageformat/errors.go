package messageformat

import (
	"fmt"
	"strings"
)

// ErrorCode is a stable, machine-readable error identifier. MF2 errors use
// the error names of the Unicode MessageFormat specification (and its
// conformance suite); Glossa adds codes for MF1 input and model shape.
type ErrorCode string

// Error codes. The MF2 codes match the `expErrors` vocabulary of the
// Unicode conformance suite.
const (
	CodeSyntaxError               ErrorCode = "syntax-error"
	CodeVariantKeyMismatch        ErrorCode = "variant-key-mismatch"
	CodeMissingFallbackVariant    ErrorCode = "missing-fallback-variant"
	CodeMissingSelectorAnnotation ErrorCode = "missing-selector-annotation"
	CodeDuplicateDeclaration      ErrorCode = "duplicate-declaration"
	CodeDuplicateOptionName       ErrorCode = "duplicate-option-name"
	CodeDuplicateVariant          ErrorCode = "duplicate-variant"
	CodeDuplicateAttribute        ErrorCode = "duplicate-attribute"
	CodeUnresolvedVariable        ErrorCode = "unresolved-variable"
	CodeUnknownFunction           ErrorCode = "unknown-function"
	CodeBadSelector               ErrorCode = "bad-selector"
	CodeBadOperand                ErrorCode = "bad-operand"
	CodeBadOption                 ErrorCode = "bad-option"
	CodeBadVariantKey             ErrorCode = "bad-variant-key"
	CodeBadFunctionResult         ErrorCode = "bad-function-result"
	CodeUnsupportedOperation      ErrorCode = "unsupported-operation"

	// CodeInvalidMessage reports a data model that cannot be represented
	// (for example an expression with neither operand nor function, or an
	// invalid variable name).
	CodeInvalidMessage ErrorCode = "invalid-message"
	// CodeMF1SyntaxError reports ICU MessageFormat 1 source that does not parse.
	CodeMF1SyntaxError ErrorCode = "mf1-syntax-error"
	// CodeMF1Unsupported reports valid MF1 that has no MF2 representation
	// (for example one argument used both as a plural and a select).
	CodeMF1Unsupported ErrorCode = "mf1-unsupported"
	// CodeInvalidLocale reports a locale that is not a BCP 47 tag.
	CodeInvalidLocale ErrorCode = "invalid-locale"
	// CodeInternalError reports a failure inside the third-party engine
	// (a recovered panic). It is always a bug worth reporting upstream.
	CodeInternalError ErrorCode = "internal-error"
)

// IsDataModelError reports whether c is one of the MF2 data model errors
// (a message that parses but is not a valid message).
func (c ErrorCode) IsDataModelError() bool {
	switch c {
	case CodeVariantKeyMismatch, CodeMissingFallbackVariant, CodeMissingSelectorAnnotation,
		CodeDuplicateDeclaration, CodeDuplicateOptionName, CodeDuplicateVariant, CodeDuplicateAttribute:
		return true
	default:
		return false
	}
}

// Error is a MessageFormat error with a stable code and a human message.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "messageformat: " + string(e.Code)
	}
	return fmt.Sprintf("messageformat: %s: %s", e.Code, e.Message)
}

// Is matches another *Error with the same code, so callers can write
// errors.Is(err, &messageformat.Error{Code: messageformat.CodeSyntaxError}).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// FormatError carries the recoverable errors that occurred while
// formatting. The formatted string returned alongside it is still usable:
// failed placeholders are rendered with their MF2 fallback representation.
type FormatError struct {
	Errors []*Error
}

func (e *FormatError) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

// Unwrap exposes the individual errors to errors.Is and errors.As.
func (e *FormatError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, err := range e.Errors {
		out[i] = err
	}
	return out
}

// Codes returns the codes of all errors, in order.
func (e *FormatError) Codes() []ErrorCode {
	out := make([]ErrorCode, len(e.Errors))
	for i, err := range e.Errors {
		out[i] = err.Code
	}
	return out
}
