// Package inline maps a simple MessageFormat 2 pattern onto the inline
// code model XLIFF and TMX share: text runs, placeholders, and paired or
// isolated opening and closing codes. Every code carries the MF2 syntax
// of its pattern element as its native data, so a pattern survives a
// round trip through a CAT tool that only moves codes around.
package inline

import (
	"fmt"
	"strconv"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// Kind is the kind of a span.
type Kind int

// Span kinds.
const (
	// Text is literal text.
	Text Kind = iota
	// Placeholder is an expression or standalone markup ({$x}, {#br/}).
	Placeholder
	// Open is opening markup whose closing markup follows in the same
	// pattern, properly nested ({#b} … {/b}).
	Open
	// Close is the closing markup of an Open.
	Close
	// IsolatedOpen is opening markup without its close.
	IsolatedOpen
	// IsolatedClose is closing markup without its open.
	IsolatedClose
)

// Span is one run of a pattern.
type Span struct {
	Kind Kind
	// Text is the literal text, or the MF2 syntax of the element.
	Text string
	// Pair is the index of the matching Close for an Open, and of the
	// matching Open for a Close.
	Pair int
	// ID is the code's identifier (see AssignIDs); "" for text.
	ID string
	// Element is the pattern element of a code.
	Element mf.PatternElement
}

// Spans splits a simple pattern into spans.
func Spans(p mf.Pattern) ([]Span, error) {
	spans := make([]Span, 0, len(p))
	for _, el := range p {
		s, err := span(el)
		if err != nil {
			return nil, err
		}
		spans = append(spans, s)
	}
	pairMarkup(p, spans)
	return spans, nil
}

func span(el mf.PatternElement) (Span, error) {
	if t, ok := el.(mf.Text); ok {
		return Span{Kind: Text, Text: string(t)}, nil
	}
	src, err := mf.Stringify(formats.PatternMessage(mf.Pattern{el}))
	if err != nil {
		return Span{}, fmt.Errorf("%w: placeholder: %v", formats.ErrInvalid, err)
	}
	kind := Placeholder
	if m, ok := el.(mf.Markup); ok {
		switch m.Kind {
		case mf.MarkupOpen:
			kind = IsolatedOpen
		case mf.MarkupClose:
			kind = IsolatedClose
		}
	}
	return Span{Kind: kind, Text: src, Element: el}, nil
}

// pairMarkup turns properly nested open/close markup of the same name
// into Open/Close pairs; the rest stays isolated.
func pairMarkup(p mf.Pattern, spans []Span) {
	var stack []int
	for i, el := range p {
		m, ok := el.(mf.Markup)
		if !ok {
			continue
		}
		switch {
		case m.Kind == mf.MarkupOpen:
			stack = append(stack, i)
		case m.Kind == mf.MarkupClose && len(stack) > 0 && p[stack[len(stack)-1]].(mf.Markup).Name == m.Name:
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			spans[open].Kind, spans[open].Pair = Open, i
			spans[i].Kind, spans[i].Pair = Close, open
		case m.Kind == mf.MarkupClose:
			stack = stack[:0]
		}
	}
}

// AssignIDs numbers the codes of src "1", "2", … (a pair shares one ID)
// and gives each code of tgt the ID of an unused source code with the
// same MF2 syntax, or a fresh ID after the source's.
func AssignIDs(src, tgt []Span) {
	next := 1
	free := map[string][]string{}
	for i := range src {
		if id, ok := assignFresh(src, i, &next); ok {
			free[key(src[i])] = append(free[key(src[i])], id)
		}
	}
	for i := range tgt {
		if tgt[i].Kind == Text || tgt[i].Kind == Close {
			continue
		}
		k := key(tgt[i])
		if ids := free[k]; len(ids) > 0 {
			tgt[i].ID, free[k] = ids[0], ids[1:]
		} else {
			tgt[i].ID = strconv.Itoa(next)
			next++
		}
	}
	closePairs(tgt)
}

func assignFresh(spans []Span, i int, next *int) (string, bool) {
	switch spans[i].Kind {
	case Text:
		return "", false
	case Close:
		spans[i].ID = spans[spans[i].Pair].ID
		return "", false
	}
	spans[i].ID = strconv.Itoa(*next)
	*next++
	return spans[i].ID, true
}

func closePairs(spans []Span) {
	for i := range spans {
		if spans[i].Kind == Close {
			spans[i].ID = spans[spans[i].Pair].ID
		}
	}
}

func key(s Span) string {
	if s.Kind == Open {
		return strconv.Itoa(int(IsolatedOpen)) + s.Text
	}
	return strconv.Itoa(int(s.Kind)) + s.Text
}

// ParseElement parses the MF2 syntax of one placeholder or markup
// element, the native data of a code.
func ParseElement(src string) (mf.PatternElement, error) {
	m, err := mf.ParseMF2(src)
	if err != nil {
		return nil, fmt.Errorf("%w: code data %q is not MF2: %v", formats.ErrInvalid, src, err)
	}
	if formats.IsComplex(m) || len(m.Pattern) != 1 {
		return nil, fmt.Errorf("%w: code data %q is not a single MF2 placeholder", formats.ErrInvalid, src)
	}
	if _, isText := m.Pattern[0].(mf.Text); isText {
		return nil, fmt.Errorf("%w: code data %q is text, not a placeholder", formats.ErrInvalid, src)
	}
	return m.Pattern[0], nil
}

// Builder accumulates pattern elements, merging adjacent text.
type Builder struct {
	p mf.Pattern
}

// Text appends literal text.
func (b *Builder) Text(s string) {
	if s == "" {
		return
	}
	if n := len(b.p); n > 0 {
		if t, ok := b.p[n-1].(mf.Text); ok {
			b.p[n-1] = t + mf.Text(s)
			return
		}
	}
	b.p = append(b.p, mf.Text(s))
}

// Element appends a placeholder or markup element.
func (b *Builder) Element(el mf.PatternElement) { b.p = append(b.p, el) }

// Pattern returns the accumulated pattern.
func (b *Builder) Pattern() mf.Pattern {
	if b.p == nil {
		return mf.Pattern{}
	}
	return b.p
}
