package formats

import (
	"errors"
	"fmt"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// ParseContent parses text in syntax with the MessageFormat kernel.
// locale decides which plural keys MF1 accepts. Errors wrap ErrInvalid.
func ParseContent(syntax mfcontent.Syntax, text string, locale bcp47.Tag) (mfcontent.Content, error) {
	c, err := mfcontent.Parse(syntax, text, locale)
	if err != nil {
		return mfcontent.Content{}, fmt.Errorf("%w: %s message: %v", ErrInvalid, syntax, err)
	}
	return c, nil
}

// FromModel wraps a canonical model as content authored in MF2 syntax.
func FromModel(m mf.Message) (mfcontent.Content, error) {
	src, err := mf.Stringify(m)
	if err != nil {
		return mfcontent.Content{}, fmt.Errorf("%w: message: %v", ErrInvalid, err)
	}
	return ParseContent(mfcontent.MF2, src, bcp47.Tag{})
}

// PlainText is content for literal text: a pattern without placeholders.
// Characters that are syntax in MF1 or MF2 stay literal.
func PlainText(s string) (mfcontent.Content, error) {
	return FromModel(PatternMessage(TextPattern(s)))
}

// TextPattern is the pattern of literal text s.
func TextPattern(s string) mf.Pattern {
	if s == "" {
		return mf.Pattern{}
	}
	return mf.Pattern{mf.Text(s)}
}

// PatternMessage is a pattern message without declarations.
func PatternMessage(p mf.Pattern) mf.Message {
	return mf.Message{Type: mf.PatternMessageType, Pattern: p}
}

// MF2Text returns c in MF2 syntax: the authored text when it was written
// in MF2, else the canonical model stringified.
func MF2Text(c mfcontent.Content) (string, error) {
	if c.Syntax == mfcontent.MF2 {
		return c.Text, nil
	}
	src, err := mf.Stringify(c.Model)
	if err != nil {
		return "", fmt.Errorf("%w: message: %v", ErrInvalid, err)
	}
	return src, nil
}

// IsComplex reports whether m needs MF2 syntax to be written down: it
// selects on variants or declares variables. A simple message is a
// single pattern of text and placeholders.
func IsComplex(m mf.Message) bool {
	return m.IsSelect() || len(m.Declarations) > 0
}

// ErrNotSimple means a message is complex (see IsComplex) where a simple
// pattern is needed.
var ErrNotSimple = errors.New("message is not a simple pattern")
