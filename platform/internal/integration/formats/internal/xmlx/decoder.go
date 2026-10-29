// Package xmlx is the XML plumbing the XML-based formats share: a
// hardened token decoder, a small element tree for one record at a time,
// and a deterministic writer.
//
// Security: encoding/xml never fetches external resources and expands
// only the five predefined entities, and Decoder adds to that:
//
//   - a document type declaration with an internal subset ("[...]",
//     where entity declarations live) is refused; a declaration that
//     only names an external DTD is ignored, never fetched;
//   - any other markup declaration is refused;
//   - references to undeclared entities are syntax errors (strict mode);
//   - input size and element nesting are bounded.
//
// That makes XXE (external entities) and entity expansion attacks
// ("billion laughs") errors instead of resolved content.
package xmlx

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// Decoder reads XML tokens with the hardening described in the package
// documentation. Errors are *formats.Error values carrying positions.
type Decoder struct {
	d      *xml.Decoder
	format string
	depth  int
	// startLine and startCol locate the '<' of the last start tag read.
	startLine, startCol int
}

// NewDecoder reads XML from r, at most maxBytes of it. format names the
// file format in errors. UTF-8 and UTF-16 (with a byte order mark) are
// read natively; other encodings a document declares are decoded with
// the WHATWG encoding index.
func NewDecoder(r io.Reader, format string, maxBytes int64) *Decoder {
	br := bufio.NewReader(formats.LimitReader(r, maxBytes))
	src, utf16 := sniffBOM(br)
	d := xml.NewDecoder(src)
	d.Strict = true
	d.Entity = nil
	d.CharsetReader = charsetReader(utf16)
	return &Decoder{d: d, format: format}
}

// sniffBOM strips a UTF-8 byte order mark and transcodes UTF-16 input
// (identified by its byte order mark) to UTF-8.
func sniffBOM(br *bufio.Reader) (io.Reader, bool) {
	head, _ := br.Peek(3)
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		_, _ = br.Discard(3)
		return br, false
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		dec := unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder()
		return transform.NewReader(br, dec), true
	}
	return br, false
}

func charsetReader(transcoded bool) func(string, io.Reader) (io.Reader, error) {
	return func(label string, in io.Reader) (io.Reader, error) {
		label = strings.ToLower(strings.TrimSpace(label))
		if transcoded && strings.HasPrefix(label, "utf-16") {
			return in, nil
		}
		if strings.HasPrefix(label, "utf-16") {
			return nil, formats.Unsupportedf("UTF-16 without a byte order mark")
		}
		enc, err := htmlindex.Get(label)
		if err != nil {
			return nil, formats.Unsupportedf("encoding %q", label)
		}
		return enc.NewDecoder().Reader(in), nil
	}
}

// Token returns the next token. Comments and processing instructions are
// returned as they come; callers skip them.
func (d *Decoder) Token() (xml.Token, error) {
	// Text between tags is a token of its own, so where the previous
	// token ended is where a start tag begins.
	line, col := d.d.InputPos()
	tok, err := d.d.Token()
	if err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, d.wrap(err)
	}
	switch t := tok.(type) {
	case xml.Directive:
		if err := checkDirective(t); err != nil {
			return nil, d.Err("", err)
		}
	case xml.StartElement:
		d.startLine, d.startCol = line, col
		d.depth++
		if d.depth > formats.MaxDepth {
			return nil, d.Err("", formats.Invalidf("elements nest deeper than %d levels", formats.MaxDepth))
		}
	case xml.EndElement:
		d.depth--
	case xml.CharData:
		return t.Copy(), nil
	}
	return tok, nil
}

// checkDirective allows a document type declaration that only names an
// external DTD (it is never fetched) and refuses everything else.
func checkDirective(dir xml.Directive) error {
	s := strings.TrimSpace(string(dir))
	if !strings.HasPrefix(s, "DOCTYPE") {
		return formats.Unsupportedf("markup declaration <!%.20s…> is not processed", s)
	}
	if strings.ContainsAny(s, "[<") {
		return formats.Unsupportedf("document type definitions with an internal subset are not processed (entity declarations are refused)")
	}
	return nil
}

// Root returns the document element, skipping the prolog.
func (d *Decoder) Root() (xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return xml.StartElement{}, d.Err("", formats.Invalidf("no document element"))
		}
		if err != nil {
			return xml.StartElement{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return xml.StartElement{}, d.Err("", formats.Invalidf("text before the document element"))
			}
		}
	}
}

// Pos returns the current line and column.
func (d *Decoder) Pos() (line, col int) { return d.d.InputPos() }

// StartPos returns the line and column of the '<' that opened the last
// start tag Token returned.
func (d *Decoder) StartPos() (line, col int) { return d.startLine, d.startCol }

// Err wraps err with the current position.
func (d *Decoder) Err(item string, err error) *formats.Error {
	var fe *formats.Error
	if errors.As(err, &fe) {
		return fe
	}
	line, col := d.Pos()
	return &formats.Error{Format: d.format, Line: line, Column: col, Item: item, Err: err}
}

func (d *Decoder) wrap(err error) *formats.Error {
	var syn *xml.SyntaxError
	if errors.As(err, &syn) {
		return &formats.Error{Format: d.format, Line: syn.Line, Err: fmt.Errorf("%w: XML: %s", formats.ErrInvalid, syn.Msg)}
	}
	if errors.Is(err, formats.ErrTooLarge) || errors.Is(err, formats.ErrUnsupported) {
		return d.Err("", err)
	}
	return d.Err("", fmt.Errorf("%w: XML: %v", formats.ErrInvalid, err))
}
