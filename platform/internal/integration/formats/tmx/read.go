package tmx

import (
	"encoding/xml"
	"fmt"
	"io"
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

// ReadOptions configure Read and NewReader.
type ReadOptions struct {
	Limits formats.Limits
}

// Header is the TMX header.
type Header struct {
	// SourceLocale is srclang; zero for "*all*".
	SourceLocale        bcp47.Tag
	CreationTool        string
	CreationToolVersion string
}

// Reader streams the units of a TMX document.
type Reader struct {
	d       *xmlx.Decoder
	limits  formats.Limits
	header  Header
	pending []formats.TMUnit
	tus     int
	done    bool
}

// NewReader reads the document up to the start of its body.
func NewReader(r io.Reader, opts ReadOptions) (*Reader, error) {
	limits := opts.Limits.WithDefaults(DefaultLimits)
	rd := &Reader{d: xmlx.NewDecoder(r, format, limits.MaxBytes), limits: limits}
	root, err := rd.d.Root()
	if err != nil {
		return nil, err
	}
	if root.Name.Local != "tmx" {
		return nil, rd.d.Err("", formats.Invalidf("document element is <%s>, not <tmx>", root.Name.Local))
	}
	if err := rd.readHeader(); err != nil {
		return nil, err
	}
	return rd, nil
}

// Header returns the document's header.
func (rd *Reader) Header() Header { return rd.header }

// readHeader reads <header> and advances into <body>.
func (rd *Reader) readHeader() error {
	for {
		t, err := rd.nextStart("tmx")
		if err != nil {
			return err
		}
		switch t.Name.Local {
		case "header":
			if err := rd.parseHeader(t); err != nil {
				return err
			}
		case "body":
			return nil
		default:
			if err := rd.d.Skip(); err != nil {
				return err
			}
		}
	}
}

func (rd *Reader) parseHeader(t xml.StartElement) error {
	n, err := rd.d.Tree(t)
	if err != nil {
		return err
	}
	rd.header.CreationTool, rd.header.CreationToolVersion = n.Get("creationtool"), n.Get("creationtoolversion")
	src := n.Get("srclang")
	if src == "" || src == "*all*" {
		return nil
	}
	if rd.header.SourceLocale, err = formats.ParseLocale(src); err != nil {
		return nodeErr(n, "header", formats.Invalidf("srclang: %v", err))
	}
	return nil
}

// nextStart returns the next start tag inside the current element, or
// io.EOF when the element ends.
func (rd *Reader) nextStart(inside string) (xml.StartElement, error) {
	for {
		tok, err := rd.d.Token()
		if err == io.EOF {
			return xml.StartElement{}, rd.d.Err("", formats.Invalidf("unexpected end of document inside <%s>", inside))
		}
		if err != nil {
			return xml.StartElement{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.EndElement:
			return xml.StartElement{}, io.EOF
		}
	}
}

// Next returns the next unit, or io.EOF after the last.
func (rd *Reader) Next() (formats.TMUnit, error) {
	for len(rd.pending) == 0 {
		if rd.done {
			return formats.TMUnit{}, io.EOF
		}
		if err := rd.readTU(); err != nil {
			return formats.TMUnit{}, err
		}
	}
	u := rd.pending[0]
	rd.pending = rd.pending[1:]
	return u, nil
}

func (rd *Reader) readTU() error {
	t, err := rd.nextStart("body")
	if err == io.EOF {
		rd.done = true
		return nil
	}
	if err != nil {
		return err
	}
	if t.Name.Local != "tu" {
		return rd.d.Skip()
	}
	if rd.tus++; rd.tus > rd.limits.MaxItems {
		return rd.d.Err("", fmt.Errorf("%w: more than %d translation units", formats.ErrTooLarge, rd.limits.MaxItems))
	}
	n, err := rd.d.Tree(t)
	if err != nil {
		return err
	}
	rd.pending, err = rd.units(n)
	return err
}

// ReadAll reads every unit of a TMX document.
func ReadAll(r io.Reader, opts ReadOptions) ([]formats.TMUnit, error) {
	rd, err := NewReader(r, opts)
	if err != nil {
		return nil, err
	}
	var units []formats.TMUnit
	for {
		u, err := rd.Next()
		if err == io.EOF {
			return units, nil
		}
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
}

func nodeErr(n *xmlx.Node, item string, err error) *formats.Error {
	return &formats.Error{Format: format, Line: n.Line, Column: n.Col, Item: item, Err: err}
}

// variant is one <tuv>.
type variant struct {
	locale  bcp47.Tag
	content mfcontent.Content
	line    int
	col     int
}

// units converts one <tu>.
func (rd *Reader) units(tu *xmlx.Node) ([]formats.TMUnit, error) {
	item := "tu"
	if id := tu.Get("tuid"); id != "" {
		item = "tu " + strconv.Quote(id)
	}
	base, err := unitBase(tu)
	if err != nil {
		return nil, nodeErr(tu, item, err)
	}
	vars, err := variants(tu, item)
	if err != nil {
		return nil, err
	}
	src, err := rd.sourceIndex(tu, vars)
	if err != nil {
		return nil, nodeErr(tu, item, err)
	}
	var out []formats.TMUnit
	for i, v := range vars {
		if i == src {
			continue
		}
		u := base
		u.SourceLocale, u.Source = vars[src].locale, vars[src].content
		u.TargetLocale, u.Target = v.locale, v.content
		u.Pos = formats.Position{Line: v.line, Column: v.col, Ref: fmt.Sprintf("tu[%d]", rd.tus)}
		out = append(out, u)
	}
	return out, nil
}

// unitBase reads a tu's attributes, props and notes.
func unitBase(tu *xmlx.Node) (formats.TMUnit, error) {
	u := formats.TMUnit{ID: tu.Get("tuid")}
	var err error
	for _, d := range []struct {
		attr string
		dst  *time.Time
	}{{"creationdate", &u.CreatedAt}, {"changedate", &u.ChangedAt}, {"lastusagedate", &u.LastUsedAt}} {
		if *d.dst, err = parseDate(tu.Get(d.attr)); err != nil {
			return u, fmt.Errorf("%w: %s: %v", formats.ErrInvalid, d.attr, err)
		}
	}
	if v := tu.Get("usagecount"); v != "" {
		if u.UsageCount, err = strconv.Atoi(v); err != nil || u.UsageCount < 0 {
			return u, formats.Invalidf("usagecount %q", v)
		}
	}
	for _, k := range tu.Elements() {
		switch k.Name.Local {
		case "prop":
			u.Props = append(u.Props, formats.Prop{Type: k.Get("type"), Value: k.InnerText()})
		case "note":
			u.Notes = append(u.Notes, k.InnerText())
		}
	}
	return u, nil
}

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(dateLayout, s)
}

func variants(tu *xmlx.Node, item string) ([]variant, error) {
	var out []variant
	for _, k := range tu.Elements() {
		if k.Name.Local != "tuv" {
			continue
		}
		v, err := readVariant(k)
		if err != nil {
			return nil, nodeErr(k, item, err)
		}
		out = append(out, v)
	}
	return out, nil
}

func readVariant(tuv *xmlx.Node) (variant, error) {
	locale, err := formats.ParseLocale(tuv.Lang())
	if err != nil {
		return variant{}, fmt.Errorf("%w: tuv: %v", formats.ErrInvalid, err)
	}
	var seg *xmlx.Node
	isMF2 := false
	for _, k := range tuv.Elements() {
		switch {
		case k.Name.Local == "seg":
			seg = k
		case k.Name.Local == "prop" && k.Get("type") == PropSyntax:
			isMF2 = strings.TrimSpace(k.InnerText()) == "mf2"
		}
	}
	if seg == nil {
		return variant{}, formats.Invalidf("tuv %s without <seg>", locale)
	}
	c, err := segContent(seg, isMF2)
	return variant{locale: locale, content: c, line: tuv.Line, col: tuv.Col}, err
}

// sourceIndex picks the source variant: the one in the tu's srclang,
// else the header's, else (for "*all*") the first.
func (rd *Reader) sourceIndex(tu *xmlx.Node, vars []variant) (int, error) {
	if len(vars) == 0 {
		return 0, formats.Invalidf("tu without <tuv>")
	}
	want := rd.header.SourceLocale
	if s := tu.Get("srclang"); s == "*all*" {
		want = bcp47.Tag{}
	} else if s != "" {
		tag, err := formats.ParseLocale(s)
		if err != nil {
			return 0, fmt.Errorf("%w: srclang: %v", formats.ErrInvalid, err)
		}
		want = tag
	}
	if want.IsZero() {
		return 0, nil
	}
	for i, v := range vars {
		if v.locale == want {
			return i, nil
		}
	}
	return 0, formats.Invalidf("no tuv in the source language %s", want)
}

// segContent converts a <seg>.
func segContent(seg *xmlx.Node, isMF2 bool) (mfcontent.Content, error) {
	if isMF2 {
		if codes := seg.Elements(); len(codes) > 0 {
			return mfcontent.Content{}, formats.Invalidf("<%s> in an MF2 syntax segment", codes[0].Name.Local)
		}
		return formats.ParseContent(mfcontent.MF2, seg.InnerText(), bcp47.Tag{})
	}
	var b inline.Builder
	if err := segInline(&b, seg); err != nil {
		return mfcontent.Content{}, err
	}
	return formats.FromModel(formats.PatternMessage(b.Pattern()))
}

func segInline(b *inline.Builder, n *xmlx.Node) error {
	for _, k := range n.Kids {
		switch {
		case k.IsText():
			b.Text(k.Text)
		case k.Name.Local == "hi":
			if err := segInline(b, k); err != nil {
				return err
			}
		case foreignCodes[k.Name.Local] != nil:
			b.Element(codeElement(k))
		default:
			return formats.Unsupportedf("<%s> in <seg>", k.Name.Local)
		}
	}
	return nil
}

// codeElement reads a native code: MF2 syntax written by Glossa becomes
// its element again; anything else becomes tmx:* markup.
func codeElement(k *xmlx.Node) mf.PatternElement {
	native := k.InnerText()
	if el, err := inline.ParseElement(native); err == nil {
		return el
	}
	opts := mf.Options{"native": mf.Literal{Value: native}}
	for _, a := range foreignCodes[k.Name.Local] {
		if v := k.Get(a); v != "" {
			opts[a] = mf.Literal{Value: v}
		}
	}
	return mf.Markup{Kind: mf.MarkupStandalone, Name: foreignPrefix + k.Name.Local, Options: opts}
}
