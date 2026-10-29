package tmx

import (
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/inline"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/xmlx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// WriteOptions configure a Writer.
type WriteOptions struct {
	// SourceLocale is the header's srclang; zero writes "*all*".
	SourceLocale bcp47.Tag
	// CreationTool and CreationToolVersion default to "Glossa" and "1".
	CreationTool        string
	CreationToolVersion string
}

// Writer writes a TMX document one unit at a time.
type Writer struct {
	xw  *xmlx.Writer
	err error
}

// NewWriter writes the TMX header to w.
func NewWriter(w io.Writer, opts WriteOptions) *Writer {
	tool, version := opts.CreationTool, opts.CreationToolVersion
	if tool == "" {
		tool, version = "Glossa", "1"
	}
	srclang := opts.SourceLocale.String()
	if srclang == "" {
		srclang = "*all*"
	}
	xw := xmlx.NewWriter(w)
	xw.Decl()
	xw.Open("tmx", "version", "1.4")
	xw.Empty("header", "creationtool", tool, "creationtoolversion", version, "segtype", "sentence",
		"o-tmf", "Glossa", "adminlang", "en", "srclang", srclang, "datatype", "plaintext")
	xw.Open("body")
	return &Writer{xw: xw}
}

// Write writes one unit.
func (w *Writer) Write(u formats.TMUnit) error {
	if w.err != nil {
		return w.err
	}
	if u.SourceLocale.IsZero() || u.TargetLocale.IsZero() || u.Source.IsZero() || u.Target.IsZero() {
		w.err = &formats.Error{Format: format, Item: "unit " + strconv.Quote(u.ID), Err: formats.Invalidf("a unit needs both locales and both texts")}
		return w.err
	}
	w.xw.Open("tu", "tuid", u.ID, "srclang", u.SourceLocale.String(), "creationdate", date(u.CreatedAt),
		"changedate", date(u.ChangedAt), "lastusagedate", date(u.LastUsedAt), "usagecount", count(u.UsageCount))
	for _, p := range u.Props {
		w.xw.Leaf("prop", p.Value, "type", p.Type)
	}
	for _, n := range u.Notes {
		w.xw.Leaf("note", n)
	}
	src, tgt, err := segments(u.Source, u.Target)
	if err == nil {
		writeTUV(w.xw, u.SourceLocale, u.Source, src)
		writeTUV(w.xw, u.TargetLocale, u.Target, tgt)
	}
	w.xw.Close()
	if err != nil {
		w.err = &formats.Error{Format: format, Item: "unit " + strconv.Quote(u.ID), Err: err}
	}
	return w.err
}

// Close ends the document and flushes it.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	w.xw.Close()
	w.xw.Close()
	if err := w.xw.Flush(); err != nil {
		w.err = &formats.Error{Format: format, Err: err}
	}
	return w.err
}

// Write writes units as a TMX document. The header's srclang is the
// units' common source locale, or "*all*".
func Write(w io.Writer, units []formats.TMUnit, opts WriteOptions) error {
	if opts.SourceLocale.IsZero() {
		opts.SourceLocale = commonSource(units)
	}
	tw := NewWriter(w, opts)
	for _, u := range units {
		if err := tw.Write(u); err != nil {
			return err
		}
	}
	return tw.Close()
}

func commonSource(units []formats.TMUnit) bcp47.Tag {
	if len(units) == 0 {
		return bcp47.Tag{}
	}
	for _, u := range units[1:] {
		if u.SourceLocale != units[0].SourceLocale {
			return bcp47.Tag{}
		}
	}
	return units[0].SourceLocale
}

func date(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(dateLayout)
}

func count(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// segments splits simple messages into numbered spans; complex messages
// get nil spans and are written as MF2 text.
func segments(src, tgt mfcontent.Content) ([]inline.Span, []inline.Span, error) {
	var s, t []inline.Span
	var err error
	if !formats.IsComplex(src.Model) {
		if s, err = inline.Spans(src.Model.Pattern); err != nil {
			return nil, nil, err
		}
	}
	if !formats.IsComplex(tgt.Model) {
		if t, err = inline.Spans(tgt.Model.Pattern); err != nil {
			return nil, nil, err
		}
	}
	inline.AssignIDs(s, t)
	return s, t, nil
}

func writeTUV(xw *xmlx.Writer, locale bcp47.Tag, c mfcontent.Content, spans []inline.Span) {
	xw.Open("tuv", "xml:lang", locale.String())
	if formats.IsComplex(c.Model) {
		xw.Leaf("prop", "mf2", "type", PropSyntax)
		text, _ := formats.MF2Text(c)
		xw.Leaf("seg", text)
		xw.Close()
		return
	}
	xw.OpenInline("seg")
	writeSpans(xw, spans)
	xw.Close()
	xw.Close()
}

func writeSpans(xw *xmlx.Writer, spans []inline.Span) {
	for _, s := range spans {
		if s.Kind == inline.Text {
			xw.Text(s.Text)
			continue
		}
		if name, attrs, native, ok := foreign(s.Element); ok {
			code(xw, name, native, attrs...)
			continue
		}
		switch s.Kind {
		case inline.Placeholder:
			code(xw, "ph", s.Text, "x", s.ID)
		case inline.Open:
			code(xw, "bpt", s.Text, "i", s.ID, "x", s.ID)
		case inline.Close:
			code(xw, "ept", s.Text, "i", s.ID)
		case inline.IsolatedOpen:
			code(xw, "it", s.Text, "pos", "begin", "x", s.ID)
		case inline.IsolatedClose:
			code(xw, "it", s.Text, "pos", "end", "x", s.ID)
		}
	}
}

func code(xw *xmlx.Writer, name, native string, attrs ...string) {
	xw.Start(name, attrs...)
	xw.Text(native)
	xw.End(name)
}

// foreign recognizes markup that stands for a foreign native code and
// returns the element name, its attributes and the native code.
func foreign(el mf.PatternElement) (string, []string, string, bool) {
	m, ok := el.(mf.Markup)
	if !ok || m.Kind != mf.MarkupStandalone || !strings.HasPrefix(m.Name, foreignPrefix) {
		return "", nil, "", false
	}
	name := strings.TrimPrefix(m.Name, foreignPrefix)
	allowed, ok := foreignCodes[name]
	if !ok {
		return "", nil, "", false
	}
	var native string
	var attrs []string
	for _, k := range slices.Sorted(maps.Keys(m.Options)) {
		lit, ok := m.Options[k].(mf.Literal)
		switch {
		case !ok:
			return "", nil, "", false
		case k == "native":
			native = lit.Value
		case slices.Contains(allowed, k):
			attrs = append(attrs, k, lit.Value)
		default:
			return "", nil, "", false
		}
	}
	return name, attrs, native, true
}

// foreignCodes lists the TMX inline codes and their attributes.
var foreignCodes = map[string][]string{
	"ph": {"x", "assoc", "type"}, "bpt": {"i", "x", "type"}, "ept": {"i"},
	"it": {"pos", "x", "type"}, "ut": {"x"},
}
