package messageformat

import (
	"errors"
	"fmt"
	"maps"
	"time"
	"unicode/utf8"

	mf2 "github.com/kaptinlin/messageformat-go"
	"github.com/kaptinlin/messageformat-go/pkg/datamodel"
	mferrors "github.com/kaptinlin/messageformat-go/pkg/errors"
	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"golang.org/x/text/language"
)

// This file is the adapter over github.com/kaptinlin/messageformat-go.
// No engine type crosses the package API: messages are converted to and
// from the canonical model, and engine errors are mapped to *Error codes.

// ParseMF2 parses MessageFormat 2 syntax into the canonical model and
// validates it. Syntax errors return an *Error with CodeSyntaxError; valid
// syntax that is not a valid message returns the data model error code.
func ParseMF2(src string) (msg Message, err error) {
	defer containEngineFailure(&err)
	return parseMF2(src)
}

// Validate checks that msg is a valid MF2 message: representable in the
// syntax and free of data model errors.
func Validate(msg Message) error {
	_, err := Stringify(msg)
	return err
}

// Stringify serializes msg as MessageFormat 2 syntax. Options and
// attributes are written in lexicographic order, so the output is stable.
func Stringify(msg Message) (src string, err error) {
	defer containEngineFailure(&err)
	return stringify(msg)
}

// Format formats msg for locale (a BCP 47 tag) with the given values.
//
// The required MF2 functions and the draft functions (:currency, :date,
// :datetime, :time, :percent, :unit) are enabled. Formatting follows the
// MF2 error model: the returned string is always usable, with fallback
// representations for placeholders that failed, and the error (a
// *FormatError) lists what failed. A message that is not valid, or an
// invalid locale, returns an *Error and an empty string.
func Format(msg Message, locale string, values map[string]any, opts ...FormatOption) (out string, err error) {
	defer containEngineFailure(&err)
	return format(msg, locale, values, opts)
}

// containEngineFailure turns a panic inside the third-party engine into a
// CodeInternalError, so malformed user input can never crash the caller.
// The fuzz tests found such panics in the engine's MF2 parser.
func containEngineFailure(err *error) {
	if r := recover(); r != nil {
		*err = &Error{Code: CodeInternalError, Message: fmt.Sprintf("messageformat engine failure: %v", r)}
	}
}

func parseMF2(src string) (Message, error) {
	if !utf8.ValidString(src) {
		return Message{}, &Error{Code: CodeSyntaxError, Message: "source is not valid UTF-8"}
	}
	em, err := datamodel.ParseMessage(src)
	if err != nil {
		return Message{}, engineError(err)
	}
	if _, err := datamodel.ValidateMessage(em, nil); err != nil {
		return Message{}, engineError(err)
	}
	msg, err := fromEngine(em)
	if err != nil {
		return Message{}, &Error{Code: CodeInvalidMessage, Message: err.Error()}
	}
	return msg, nil
}

func stringify(msg Message) (string, error) {
	em, err := toEngineValidated(msg)
	if err != nil {
		return "", err
	}
	src, err := datamodel.StringifyMessage(em)
	if err != nil {
		return "", engineError(err)
	}
	return quoteIfAmbiguous(msg, src), nil
}

// quoteIfAmbiguous guards a gap in the engine's serializer: a pattern
// message whose first non-whitespace character is "." (after MF2
// whitespace such as U+3000) must be written as a quoted pattern, but the
// engine writes it bare and the result no longer parses. When the bare
// form doesn't parse back, the quoted form {{…}} is used; its escaping
// rules for text are the same as a simple pattern's.
func quoteIfAmbiguous(msg Message, src string) string {
	if msg.Type != PatternMessageType || len(msg.Declarations) > 0 {
		return src
	}
	if _, err := parseMF2(src); err == nil {
		return src
	}
	return "{{" + src + "}}"
}

// FormatOption configures Format.
type FormatOption func(*formatConfig)

type formatConfig struct {
	bidiIsolation bool
	timeZone      *time.Location
	functions     map[string]functions.MessageFunction
}

// WithBidiIsolation turns bidi isolation of placeholders on (the MF2
// "default" strategy, the default) or off (the "none" strategy). Turn it
// off for output that must not contain isolation characters, such as
// plain-text email subjects or CLI output in left-to-right locales.
func WithBidiIsolation(on bool) FormatOption {
	return func(c *formatConfig) { c.bidiIsolation = on }
}

// withEngineFunctions registers extra engine functions. It is unexported
// on purpose: it exists for the conformance suite's :test:* functions.
func withEngineFunctions(fns map[string]functions.MessageFunction) FormatOption {
	return func(c *formatConfig) { maps.Copy(c.functions, fns) }
}

// FormatToParts formats msg like [Format], but returns the formatted
// parts: text, markup, bidi isolation, placeholder values and fallbacks.
// The parts' text joins ([PartsText]) to exactly what Format returns, and
// errors follow the same model: a *FormatError next to usable parts, or an
// *Error and no parts for an invalid message or locale.
func FormatToParts(msg Message, locale string, values map[string]any, opts ...FormatOption) (parts []Part, err error) {
	defer containEngineFailure(&err)
	formatter, err := compile(annotateExactValues(msg, values), locale, opts)
	if err != nil {
		return nil, err
	}
	engineParts, err := formatter.FormatToParts(values)
	parts = fromEngineParts(engineParts)
	if err != nil {
		return parts, formatErrors(err)
	}
	return parts, nil
}

func format(msg Message, locale string, values map[string]any, opts []FormatOption) (string, error) {
	formatter, err := compile(annotateExactValues(msg, values), locale, opts)
	if err != nil {
		return "", err
	}
	out, err := formatter.Format(values)
	if err != nil {
		return out, formatErrors(err)
	}
	return out, nil
}

// compile validates msg and locale and builds the engine formatter.
func compile(msg Message, locale string, opts []FormatOption) (*mf2.MessageFormat, error) {
	if _, err := language.Parse(locale); err != nil {
		return nil, &Error{Code: CodeInvalidLocale, Message: fmt.Sprintf("%q: %v", locale, err)}
	}
	cfg := formatConfig{bidiIsolation: true, functions: standardFunctions()}
	for _, opt := range opts {
		opt(&cfg)
	}
	withExactNumbers(cfg.functions)
	withTimeZone(cfg.functions, cfg.timeZone)
	em, err := toEngineValidated(msg)
	if err != nil {
		return nil, err
	}
	bidi := mf2.BidiNone
	if cfg.bidiIsolation {
		bidi = mf2.BidiDefault
	}
	formatter, err := mf2.Compile([]string{locale}, em, mf2.WithBidiIsolation(bidi), mf2.WithFunctions(cfg.functions))
	if err != nil {
		return nil, engineError(err)
	}
	return formatter, nil
}

// standardFunctions returns the MF2 required and draft functions.
func standardFunctions() map[string]functions.MessageFunction {
	fns := mf2.DefaultFunctionMap()
	maps.Copy(fns, mf2.DraftFunctionMap())
	return fns
}

// toEngineValidated converts msg and runs the engine's data model checks.
func toEngineValidated(msg Message) (datamodel.Message, error) {
	em, err := toEngine(msg)
	if err != nil {
		return nil, &Error{Code: CodeInvalidMessage, Message: err.Error()}
	}
	if _, err := datamodel.ValidateMessage(em, nil); err != nil {
		return nil, engineError(err)
	}
	return em, nil
}

// --- error mapping -----------------------------------------------------------

// engineKinds maps engine error kinds that differ from the spec names.
var engineKinds = map[string]ErrorCode{
	mferrors.ErrorTypeKeyMismatch:        CodeVariantKeyMismatch,
	mferrors.ErrorTypeMissingFallback:    CodeMissingFallbackVariant,
	mferrors.ErrorTypeInvalidMessage:     CodeInvalidMessage,
	mferrors.ErrorTypeNotFormattable:     CodeBadOperand,
	mferrors.ErrorTypeNoMatch:            CodeBadSelector,
	mferrors.ErrorTypeEmptyToken:         CodeSyntaxError,
	mferrors.ErrorTypeBadEscape:          CodeSyntaxError,
	mferrors.ErrorTypeBadInputExpression: CodeSyntaxError,
	mferrors.ErrorTypeExtraContent:       CodeSyntaxError,
	mferrors.ErrorTypeParseError:         CodeSyntaxError,
	mferrors.ErrorTypeMissingSyntax:      CodeSyntaxError,
}

// engineError maps one engine error to an *Error.
func engineError(err error) *Error {
	var typed interface{ ErrorType() string }
	if !errors.As(err, &typed) {
		return &Error{Code: CodeInvalidMessage, Message: err.Error()}
	}
	kind := typed.ErrorType()
	code, ok := engineKinds[kind]
	if !ok {
		code = ErrorCode(kind)
	}
	var syntaxErr *mferrors.MessageSyntaxError
	var dataModelErr *mferrors.MessageDataModelError
	if errors.As(err, &syntaxErr) && !errors.As(err, &dataModelErr) && !code.IsDataModelError() {
		code = CodeSyntaxError
	}
	return &Error{Code: code, Message: err.Error()}
}

// formatErrors maps the engine's joined format diagnostics.
func formatErrors(err error) *FormatError {
	var errs []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	} else {
		errs = []error{err}
	}
	out := &FormatError{Errors: make([]*Error, 0, len(errs))}
	for _, e := range errs {
		out.Errors = append(out.Errors, engineError(e))
	}
	return out
}
