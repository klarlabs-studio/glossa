package xmlx

import (
	"bufio"
	"io"
	"strings"
	"unicode/utf8"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// Writer writes indented, deterministic XML. Block methods (Open, Close,
// Leaf, Empty, OpenInline) place an element on its own line; inline
// methods (Start, End, Tag, Text) write mixed content exactly as given,
// for elements such as <seg> or <source> where whitespace is content.
//
// Attributes are name/value pairs; a pair with an empty value is
// omitted. Text that contains a character XML 1.0 cannot carry fails
// the writer with formats.ErrInvalid; callers that can represent such
// characters otherwise (XLIFF <cp>) split them out first.
type Writer struct {
	w     *bufio.Writer
	err   error
	stack []frame
}

type frame struct {
	name     string
	inline   bool
	hasBlock bool
}

// NewWriter writes to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: bufio.NewWriter(w)} }

// Decl writes the XML declaration.
func (w *Writer) Decl() { w.raw(`<?xml version="1.0" encoding="UTF-8"?>`) }

// Open starts a block element whose children are block elements.
func (w *Writer) Open(name string, attrs ...string) {
	w.blockStart()
	w.startTag(name, attrs, false)
	w.stack = append(w.stack, frame{name: name})
}

// OpenInline starts a block element whose content is written inline.
func (w *Writer) OpenInline(name string, attrs ...string) {
	w.blockStart()
	w.startTag(name, attrs, false)
	w.stack = append(w.stack, frame{name: name, inline: true})
}

// Close ends the innermost element opened with Open or OpenInline.
func (w *Writer) Close() {
	top := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	if top.hasBlock {
		w.newline()
	}
	w.raw("</" + top.name + ">")
}

// Leaf writes a block element with text content.
func (w *Writer) Leaf(name, text string, attrs ...string) {
	w.OpenInline(name, attrs...)
	w.Text(text)
	w.Close()
}

// Empty writes a block element without content.
func (w *Writer) Empty(name string, attrs ...string) {
	w.blockStart()
	w.startTag(name, attrs, true)
}

// Start writes an inline start tag.
func (w *Writer) Start(name string, attrs ...string) { w.startTag(name, attrs, false) }

// End writes an inline end tag.
func (w *Writer) End(name string) { w.raw("</" + name + ">") }

// Tag writes an inline empty element.
func (w *Writer) Tag(name string, attrs ...string) { w.startTag(name, attrs, true) }

// Text writes escaped character data.
func (w *Writer) Text(s string) {
	if w.err == nil {
		w.err = CheckChars(s)
	}
	w.raw(escapeText(s))
}

// Flush ends the document and reports the first error.
func (w *Writer) Flush() error {
	w.raw("\n")
	if w.err != nil {
		return w.err
	}
	return w.w.Flush()
}

func (w *Writer) blockStart() {
	if n := len(w.stack); n > 0 {
		w.stack[n-1].hasBlock = true
	}
	w.newline()
}

func (w *Writer) newline() {
	w.raw("\n" + strings.Repeat("  ", len(w.stack)))
}

func (w *Writer) startTag(name string, attrs []string, empty bool) {
	var b strings.Builder
	b.WriteString("<" + name)
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i+1] == "" {
			continue
		}
		if w.err == nil {
			w.err = CheckChars(attrs[i+1])
		}
		b.WriteString(" " + attrs[i] + `="` + escapeAttr(attrs[i+1]) + `"`)
	}
	if empty {
		b.WriteString("/")
	}
	b.WriteString(">")
	w.raw(b.String())
}

func (w *Writer) raw(s string) {
	if w.err != nil {
		return
	}
	_, w.err = w.w.WriteString(s)
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;",
		"\r", "&#xD;", "\n", "&#xA;", "\t", "&#x9;")
)

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }

// IsChar reports whether XML 1.0 can carry r as a character.
func IsChar(r rune) bool {
	switch {
	case r == 0x9, r == 0xA, r == 0xD:
		return true
	case r >= 0x20 && r <= 0xD7FF, r >= 0xE000 && r <= 0xFFFD, r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

// CheckChars fails for invalid UTF-8 or a character XML can't carry.
func CheckChars(s string) error {
	if !utf8.ValidString(s) {
		return formats.Invalidf("text is not valid UTF-8")
	}
	for _, r := range s {
		if !IsChar(r) {
			return formats.Invalidf("character U+%04X cannot be written in XML", r)
		}
	}
	return nil
}
