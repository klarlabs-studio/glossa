// Package mfcontent is the shared value object for a message's text:
// what someone authored, in which syntax, and the canonical MessageFormat
// 2 data model it parses to (RFC 0002 §5), with the metadata derived from
// that model. Catalog uses it for source text, Localization for
// translations — one conceptual model for both (intent §62.11).
//
// Parsing is the MessageFormat kernel's job (package messageformat);
// nothing here reads message syntax itself.
package mfcontent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Syntax is the authoring syntax. Both parse into the canonical model;
// the authored text is kept so editors can show what the author wrote.
type Syntax string

// Authoring syntaxes.
const (
	MF1 Syntax = "mf1" // ICU MessageFormat 1, the default
	MF2 Syntax = "mf2" // Unicode MessageFormat 2
)

// MaxTextBytes bounds the authored text of one message.
const MaxTextBytes = 20000

var (
	// ErrInvalidSyntax names an unknown authoring syntax.
	ErrInvalidSyntax = errors.New("mfcontent: syntax must be mf1 or mf2")
	// ErrTooLong means the text exceeds MaxTextBytes.
	ErrTooLong = errors.New("mfcontent: text must be at most 20000 bytes")
)

// ParseSyntax validates a syntax name; "" means def.
func ParseSyntax(s string, def Syntax) (Syntax, error) {
	switch Syntax(s) {
	case "":
		return def, nil
	case MF1, MF2:
		return Syntax(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidSyntax, s)
}

// InvalidError reports text that doesn't parse or isn't a valid message.
// Code is the MessageFormat kernel's stable error code.
type InvalidError struct {
	Code    mf.ErrorCode
	Message string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("invalid message (%s): %s", e.Code, e.Message)
}

// Content is a message's text in canonical form.
type Content struct {
	Syntax Syntax
	Text   string
	Model  mf.Message
	// Arguments and Markup are derived from Model whenever it is built.
	Arguments []mf.Argument
	Markup    []mf.MarkupElement
}

// Parse parses text in syntax into the canonical model. locale decides
// which plural keys MF1 accepts.
func Parse(syntax Syntax, text string, locale bcp47.Tag) (Content, error) {
	if len(text) > MaxTextBytes {
		return Content{}, ErrTooLong
	}
	var (
		model mf.Message
		err   error
	)
	switch syntax {
	case MF1:
		model, err = mf.ParseMF1(text, locale.String())
	case MF2:
		model, err = mf.ParseMF2(text)
	default:
		return Content{}, fmt.Errorf("%w: %q", ErrInvalidSyntax, syntax)
	}
	if err != nil {
		var mfErr *mf.Error
		if errors.As(err, &mfErr) {
			return Content{}, &InvalidError{Code: mfErr.Code, Message: mfErr.Message}
		}
		return Content{}, &InvalidError{Code: mf.CodeInternalError, Message: err.Error()}
	}
	return build(syntax, text, model), nil
}

// Restore rebuilds stored content, re-deriving its metadata from the
// model.
func Restore(syntax Syntax, text string, modelJSON []byte) (Content, error) {
	var model mf.Message
	if err := json.Unmarshal(modelJSON, &model); err != nil {
		return Content{}, fmt.Errorf("mfcontent: stored model: %w", err)
	}
	return build(syntax, text, model), nil
}

func build(syntax Syntax, text string, model mf.Message) Content {
	return Content{
		Syntax: syntax, Text: text, Model: model,
		Arguments: mf.Arguments(model), Markup: mf.MarkupElements(model),
	}
}

// IsZero reports whether c holds no message.
func (c Content) IsZero() bool { return c.Model.Type == "" }

// ModelJSON is the canonical model's JSON in the MF2 data model schema's
// shape. Map keys are sorted, so equal models encode to equal bytes.
func (c Content) ModelJSON() []byte {
	b, err := json.Marshal(c.Model)
	if err != nil {
		// A parsed or restored model always encodes; this is a bug.
		panic(fmt.Sprintf("mfcontent: encode model: %v", err))
	}
	return b
}

// ArgumentsJSON and MarkupJSON encode the derived metadata.
func (c Content) ArgumentsJSON() []byte { return mustJSON(nonNil(c.Arguments)) }

// MarkupJSON encodes the derived markup elements.
func (c Content) MarkupJSON() []byte { return mustJSON(nonNil(c.Markup)) }

// SameModel reports whether c and o are the same message however they
// were written: an MF1 plural and its MF2 equivalent are one message.
func (c Content) SameModel(o Content) bool {
	return bytes.Equal(c.ModelJSON(), o.ModelJSON())
}

// TextLengths returns the length in characters of the literal text of
// each pattern (every variant of a select message), placeholders
// excluded. It is the lower bound a max_length constraint can check
// without formatting.
func (c Content) TextLengths() []int {
	var out []int
	for _, p := range c.Model.Patterns() {
		n := 0
		for _, el := range p {
			if t, ok := el.(mf.Text); ok {
				n += len([]rune(string(t)))
			}
		}
		out = append(out, n)
	}
	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mfcontent: encode: %v", err))
	}
	return b
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
