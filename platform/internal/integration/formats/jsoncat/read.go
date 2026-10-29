package jsoncat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// ReadOptions configure Read.
type ReadOptions struct {
	// Locale is the file's locale (required).
	Locale bcp47.Tag
	// SourceLocale is the catalog's source locale. When it is zero or
	// equal to Locale the file holds source messages; otherwise it holds
	// translations into Locale.
	SourceLocale bcp47.Tag
	// Namespace is given to every entry.
	Namespace string
	// Syntax of the messages; "" means MF1.
	Syntax mfcontent.Syntax
	// State of translations read; "" means needs_review (a JSON file
	// says nothing about review).
	State  formats.State
	Limits formats.Limits
}

// Read reads a JSON catalog.
func Read(r io.Reader, opts ReadOptions) (formats.Catalog, error) {
	if opts.Locale.IsZero() {
		return formats.Catalog{}, &formats.Error{Format: format, Err: formats.Invalidf("ReadOptions.Locale is required")}
	}
	limits := opts.Limits.WithDefaults(formats.DefaultLimits)
	data, err := formats.ReadAll(r, limits.MaxBytes)
	if err != nil {
		return formats.Catalog{}, &formats.Error{Format: format, Err: err}
	}
	p := newParser(data, opts, limits)
	if err := p.document(); err != nil {
		return formats.Catalog{}, err
	}
	return p.cat, nil
}

type parser struct {
	data   []byte
	dec    *json.Decoder
	opts   ReadOptions
	limits formats.Limits
	cat    formats.Catalog
	keys   map[string]bool
	target bool
	// Entry positions count newlines incrementally: posOff
	// is the offset counted up to, on line posLine starting at
	// posLineStart.
	posOff, posLineStart int64
	posLine              int
}

func newParser(data []byte, opts ReadOptions, limits formats.Limits) *parser {
	if opts.Syntax == "" {
		opts.Syntax = mfcontent.MF1
	}
	if opts.State == "" {
		opts.State = formats.StateNeedsReview
	}
	source := opts.SourceLocale
	if source.IsZero() {
		source = opts.Locale
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	p := &parser{
		data: data, dec: dec, opts: opts, limits: limits, keys: map[string]bool{},
		cat: formats.Catalog{SourceLocale: source}, target: source != opts.Locale, posLine: 1,
	}
	if p.target {
		p.cat.TargetLocale = opts.Locale
	}
	return p
}

func (p *parser) document() error {
	tok, err := p.dec.Token()
	if err != nil {
		return p.syntaxErr(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return p.errAt(0, "", formats.Invalidf("a catalog is a JSON object"))
	}
	if err := p.object("", "", 1); err != nil {
		return err
	}
	end := p.dec.InputOffset()
	if _, err := p.dec.Token(); err != io.EOF {
		return p.errAt(end, "", formats.Invalidf("data after the catalog object"))
	}
	return nil
}

// object reads the members of an object whose '{' was consumed. prefix
// is its key so far, pointer its JSON pointer (RFC 6901).
func (p *parser) object(prefix, pointer string, depth int) error {
	if depth > formats.MaxDepth {
		return p.errAt(p.dec.InputOffset(), prefix, formats.Invalidf("objects nest deeper than %d levels", formats.MaxDepth))
	}
	members := map[string]bool{}
	for p.dec.More() {
		at := p.dec.InputOffset()
		tok, err := p.dec.Token()
		if err != nil {
			return p.syntaxErr(err)
		}
		name := tok.(string)
		key := join(prefix, name)
		if name == "" || members[name] {
			return p.errAt(at, key, formats.Invalidf("empty or duplicate key"))
		}
		members[name] = true
		line, col := p.pos(at)
		pos := formats.Position{Line: line, Column: col, Ref: pointer + "/" + pointerEscape(name)}
		if err := p.member(key, pos, depth); err != nil {
			return err
		}
	}
	_, err := p.dec.Token()
	return p.syntaxErr(err)
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// pointerEscape escapes a member name as a JSON pointer token.
func pointerEscape(name string) string {
	return pointerEscaper.Replace(name)
}

// member reads the value of the member at pos (its name's position).
func (p *parser) member(key string, pos formats.Position, depth int) error {
	at := p.dec.InputOffset()
	tok, err := p.dec.Token()
	if err != nil {
		return p.syntaxErr(err)
	}
	switch v := tok.(type) {
	case string:
		return p.message(key, v, at, pos)
	case json.Delim:
		if v == '{' {
			return p.object(key, pos.Ref, depth+1)
		}
	}
	return p.errAt(at, key, formats.Invalidf("a value must be a message string or an object"))
}

func (p *parser) message(key, text string, at int64, pos formats.Position) error {
	if p.keys[key] {
		return p.errAt(at, key, formats.Invalidf("key defined twice (flat and nested)"))
	}
	if len(p.cat.Entries) >= p.limits.MaxItems {
		return p.errAt(at, key, fmt.Errorf("%w: more than %d messages", formats.ErrTooLarge, p.limits.MaxItems))
	}
	p.keys[key] = true
	c, err := formats.ParseContent(p.opts.Syntax, text, p.opts.Locale)
	if err != nil {
		return p.errAt(at, key, err)
	}
	e := formats.Entry{ID: key, Namespace: p.opts.Namespace, Pos: pos}
	if p.target {
		e.Targets = []formats.Target{{Locale: p.opts.Locale, Content: c, State: p.opts.State, Pos: pos}}
	} else {
		e.Source = c
	}
	p.cat.Entries = append(p.cat.Entries, e)
	return nil
}

func (p *parser) syntaxErr(err error) error {
	if err == nil {
		return nil
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return p.errAt(se.Offset, "", formats.Invalidf("JSON: %v", err))
	}
	return p.errAt(p.dec.InputOffset(), "", formats.Invalidf("JSON: %v", err))
}

// valueStart moves a byte offset past the whitespace and separators
// before the token it points at.
func (p *parser) valueStart(off int64) int64 {
	off = min(max(off, 0), int64(len(p.data)))
	for off < int64(len(p.data)) && bytes.IndexByte([]byte(" \t\r\n:,"), p.data[off]) >= 0 {
		off++
	}
	return off
}

// pos returns the line and column of the token at a byte offset.
// Entries come in file order, so newlines are counted incrementally.
func (p *parser) pos(off int64) (line, col int) {
	off = p.valueStart(off)
	if off < p.posOff {
		p.posOff, p.posLine, p.posLineStart = 0, 1, 0
	}
	seen := p.data[p.posOff:off]
	p.posLine += bytes.Count(seen, []byte("\n"))
	if i := bytes.LastIndexByte(seen, '\n'); i >= 0 {
		p.posLineStart = p.posOff + int64(i) + 1
	}
	p.posOff = off
	return p.posLine, int(off-p.posLineStart) + 1
}

// errAt reports err at a byte offset, skipping the whitespace and
// separators before the value it points at.
func (p *parser) errAt(off int64, key string, err error) error {
	off = p.valueStart(off)
	line, col := position(p.data[:off])
	item := ""
	if key != "" {
		item = fmt.Sprintf("key %q", key)
	}
	return &formats.Error{Format: format, Line: line, Column: col, Item: item, Err: err}
}

func position(before []byte) (line, col int) {
	line = 1 + bytes.Count(before, []byte("\n"))
	col = 1 + len(before) - (bytes.LastIndexByte(before, '\n') + 1)
	return line, col
}
