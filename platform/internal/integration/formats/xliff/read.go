package xliff

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/inline"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/xmlx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// ReadOptions configure Read.
type ReadOptions struct {
	Limits formats.Limits
	// PlainSyntax is how to read the text of units that carry neither
	// Glossa's type="glossa:mf2" marker nor inline codes: "" reads it
	// literally, mfcontent.MF1 parses it as ICU MessageFormat (XLIFF
	// from tools that put ICU messages into plain segments).
	PlainSyntax mfcontent.Syntax
	// TargetLocale, when set, is the locale the targets are read as,
	// whatever trgLang says: it names the locale of a file without
	// trgLang, or imports a file as another locale than it names (a
	// CAT tool's "de" into a project's "de-AT"). Zero takes trgLang.
	TargetLocale bcp47.Tag
}

// Read reads an XLIFF 2.x document. Units stream: memory holds one unit
// at a time plus the resulting catalog.
func Read(r io.Reader, opts ReadOptions) (formats.Catalog, error) {
	limits := opts.Limits.WithDefaults(formats.DefaultLimits)
	rd := &reader{d: xmlx.NewDecoder(r, format, limits.MaxBytes), opts: opts, limits: limits}
	root, err := rd.d.Root()
	if err != nil {
		return formats.Catalog{}, err
	}
	if err := rd.header(root); err != nil {
		return formats.Catalog{}, err
	}
	if err := rd.walk(root.Name, scope{}); err != nil {
		return formats.Catalog{}, err
	}
	return rd.cat, nil
}

type reader struct {
	d      *xmlx.Decoder
	opts   ReadOptions
	limits formats.Limits
	cat    formats.Catalog
	target bcp47.Tag
}

// scope is the <file> a unit is in: its namespace (original) and id.
type scope struct {
	namespace string
	fileID    string
}

// ref is a unit's XLIFF 2 fragment identifier (#/f=file/u=unit).
func (s scope) ref(unitID string) string {
	var b strings.Builder
	b.WriteString("#")
	if s.fileID != "" {
		b.WriteString("/f=" + fragmentEscape(s.fileID))
	}
	if unitID != "" {
		b.WriteString("/u=" + fragmentEscape(unitID))
	}
	return b.String()
}

// fragmentEscape escapes what XLIFF 2 fragment identifiers reserve in
// an id ('\' and '/' are escaped with '\').
func fragmentEscape(id string) string {
	return strings.NewReplacer(`\`, `\\`, "/", `\/`).Replace(id)
}

func (rd *reader) header(root xml.StartElement) error {
	n := &xmlx.Node{Name: root.Name, Attr: root.Attr}
	version := n.Get("version")
	switch {
	case root.Name.Local != "xliff":
		return rd.d.Err("", formats.Invalidf("document element is <%s>, not <xliff>", root.Name.Local))
	case strings.HasPrefix(version, "1."):
		return rd.d.Err("", formats.Unsupportedf("XLIFF %s; export XLIFF 2.x", version))
	case root.Name.Space != Namespace || !strings.HasPrefix(version, "2."):
		return rd.d.Err("", formats.Unsupportedf("XLIFF version %q in namespace %q", version, root.Name.Space))
	}
	src, err := formats.ParseLocale(n.Get("srcLang"))
	if err != nil {
		return rd.d.Err("srcLang", formats.Invalidf("%v", err))
	}
	rd.cat.SourceLocale = src
	if trg := n.Get("trgLang"); trg != "" && rd.opts.TargetLocale.IsZero() {
		if rd.target, err = formats.ParseLocale(trg); err != nil {
			return rd.d.Err("trgLang", formats.Invalidf("%v", err))
		}
	}
	if !rd.opts.TargetLocale.IsZero() {
		rd.target = rd.opts.TargetLocale
	}
	rd.cat.TargetLocale = rd.target
	return nil
}

// walk reads the children of a <xliff>, <file> or <group> element.
func (rd *reader) walk(parent xml.Name, s scope) error {
	for {
		tok, err := rd.d.Token()
		if err == io.EOF {
			return rd.d.Err("", formats.Invalidf("unexpected end of document inside <%s>", parent.Local))
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if err := rd.element(t, s); err != nil {
				return err
			}
		}
	}
}

func (rd *reader) element(t xml.StartElement, s scope) error {
	if t.Name.Space != Namespace {
		return rd.d.Skip()
	}
	switch t.Name.Local {
	case "file":
		n := &xmlx.Node{Attr: t.Attr}
		return rd.walk(t.Name, scope{namespace: n.Get("original"), fileID: n.Get("id")})
	case "group":
		return rd.walk(t.Name, s)
	case "unit":
		return rd.unit(t, s)
	}
	return rd.d.Skip()
}

func (rd *reader) unit(start xml.StartElement, s scope) error {
	if len(rd.cat.Entries) >= rd.limits.MaxItems {
		return rd.d.Err("", fmt.Errorf("%w: more than %d units", formats.ErrTooLarge, rd.limits.MaxItems))
	}
	n, err := rd.d.Tree(start)
	if err != nil {
		return err
	}
	e, err := rd.entry(n, s)
	if err != nil {
		return err
	}
	rd.cat.Entries = append(rd.cat.Entries, e)
	return nil
}

func (rd *reader) entry(n *xmlx.Node, s scope) (formats.Entry, error) {
	key := n.Get("name")
	if key == "" {
		key = n.Get("id")
	}
	item := fmt.Sprintf("unit %q", key)
	if key == "" {
		return formats.Entry{}, unitErr(n, item, formats.Invalidf("unit without id"))
	}
	u := &unitReader{rd: rd, n: n, item: item, data: map[string]*xmlx.Node{}, mf2: n.Get("type") == TypeMF2}
	e := formats.Entry{ID: key, Namespace: s.namespace,
		Pos: formats.Position{Line: n.Line, Column: n.Col, Ref: s.ref(n.Get("id"))}}
	if err := u.read(&e); err != nil {
		return formats.Entry{}, err
	}
	return e, nil
}

func unitErr(n *xmlx.Node, item string, err error) *formats.Error {
	return &formats.Error{Format: format, Line: n.Line, Column: n.Col, Item: item, Err: err}
}

// unitReader converts one <unit> tree.
type unitReader struct {
	rd   *reader
	n    *xmlx.Node
	item string
	data map[string]*xmlx.Node
	mf2  bool
	segs []*xmlx.Node
}

func (u *unitReader) err(n *xmlx.Node, err error) error { return unitErr(n, u.item, err) }

func (u *unitReader) read(e *formats.Entry) error {
	if err := u.maxLength(e); err != nil {
		return err
	}
	for _, k := range u.n.Elements() {
		if k.Name.Space != Namespace {
			continue
		}
		switch k.Name.Local {
		case "notes":
			readNotes(k, e)
		case "originalData":
			u.originalData(k)
		case "segment", "ignorable":
			u.segs = append(u.segs, k)
		}
	}
	if len(u.segs) == 0 {
		return u.err(u.n, formats.Invalidf("unit without segments"))
	}
	return u.content(e)
}

func (u *unitReader) maxLength(e *formats.Entry) error {
	v, ok := u.n.Lookup(NamespaceSLR, "sizeRestriction")
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return u.err(u.n, formats.Invalidf("slr:sizeRestriction %q is not a length", v))
	}
	e.MaxLength = n
	return nil
}

func readNotes(notes *xmlx.Node, e *formats.Entry) {
	for _, note := range notes.Elements() {
		text := note.InnerText()
		switch note.Get("category") {
		case "description":
			if e.Description != "" {
				text = e.Description + "\n\n" + text
			}
			e.Description = text
		case "location":
			e.References = append(e.References, text)
		default:
			e.Notes = append(e.Notes, text)
		}
	}
}

func (u *unitReader) originalData(od *xmlx.Node) {
	for _, d := range od.Elements() {
		if d.Name.Local == "data" {
			u.data[d.Get("id")] = d
		}
	}
}

func (u *unitReader) content(e *formats.Entry) error {
	srcParts := u.parts("source")
	if srcParts == nil {
		return u.err(u.n, formats.Invalidf("a segment without <source>"))
	}
	src, err := u.message(srcParts, u.rd.cat.SourceLocale)
	if err != nil {
		return err
	}
	e.Source = src
	tgtParts := u.parts("target")
	if tgtParts == nil || u.untranslated(tgtParts) {
		return nil
	}
	if u.rd.target.IsZero() {
		return u.err(u.n, formats.Invalidf("a target without trgLang on <xliff>"))
	}
	tgt, err := u.message(tgtParts, u.rd.target)
	if err != nil {
		return err
	}
	state, err := u.state()
	if err != nil {
		return err
	}
	pos := e.Pos
	pos.Line, pos.Column = tgtParts[0].Line, tgtParts[0].Col
	e.Targets = []formats.Target{{Locale: u.rd.target, Content: tgt, State: state, Pos: pos}}
	return nil
}

// parts returns the <source> or <target> of every segment in order. An
// ignorable without a target contributes its source. It returns nil when
// a segment has no such element: a partly translated unit has no
// translation.
func (u *unitReader) parts(which string) []*xmlx.Node {
	var out []*xmlx.Node
	for _, seg := range u.segs {
		part := child(seg, which)
		if part == nil && seg.Name.Local == "ignorable" {
			part = child(seg, "source")
		}
		if part == nil {
			return nil
		}
		out = append(out, part)
	}
	return out
}

func child(n *xmlx.Node, local string) *xmlx.Node {
	for _, k := range n.Elements() {
		if k.Name.Space == Namespace && k.Name.Local == local {
			return k
		}
	}
	return nil
}

// untranslated reports empty targets without a state: CAT tools write
// that for "not translated yet". An empty target with an explicit state
// (Glossa writes one on every segment with a target) is an empty
// translation.
func (u *unitReader) untranslated(parts []*xmlx.Node) bool {
	for _, p := range parts {
		if len(p.Kids) > 0 {
			return false
		}
	}
	for _, seg := range u.segs {
		if _, ok := seg.Lookup("", "state"); ok {
			return false
		}
	}
	return true
}

var stateRank = map[string]int{"initial": 0, "translated": 1, "reviewed": 2, "final": 3}

// state is the least advanced state of the unit's segments.
func (u *unitReader) state() (formats.State, error) {
	least, rejected := 3, false
	for _, seg := range u.segs {
		if seg.Name.Local != "segment" {
			continue
		}
		s := seg.Get("state")
		if s == "" {
			s = "initial"
		}
		rank, ok := stateRank[s]
		if !ok {
			return "", u.err(seg, formats.Invalidf("segment state %q", s))
		}
		least = min(least, rank)
		rejected = rejected || seg.Get("subState") == SubStateRejected
	}
	return stateOf(least, rejected), nil
}

func stateOf(rank int, rejected bool) formats.State {
	switch {
	case rank == 0 && rejected:
		return formats.StateRejected
	case rank == 0:
		return formats.StateDraft
	case rank == 1:
		return formats.StateNeedsReview
	}
	return formats.StateApproved
}

// message converts the joined parts of a source or target.
func (u *unitReader) message(parts []*xmlx.Node, locale bcp47.Tag) (mfcontent.Content, error) {
	if u.mf2 || u.rd.opts.PlainSyntax == mfcontent.MF1 && !u.hasCodes() {
		return u.syntaxMessage(parts, locale)
	}
	var b inline.Builder
	for _, p := range parts {
		if err := u.inline(&b, p); err != nil {
			return mfcontent.Content{}, err
		}
	}
	c, err := formats.FromModel(formats.PatternMessage(b.Pattern()))
	if err != nil {
		return mfcontent.Content{}, u.err(parts[0], err)
	}
	return c, nil
}

// syntaxMessage parses text that is message syntax: MF2 in a
// glossa:mf2 unit, else the PlainSyntax.
func (u *unitReader) syntaxMessage(parts []*xmlx.Node, locale bcp47.Tag) (mfcontent.Content, error) {
	var b strings.Builder
	for _, p := range parts {
		if err := u.plainText(&b, p); err != nil {
			return mfcontent.Content{}, err
		}
	}
	syntax := mfcontent.MF2
	if !u.mf2 {
		syntax = u.rd.opts.PlainSyntax
	}
	c, err := formats.ParseContent(syntax, b.String(), locale)
	if err != nil {
		return mfcontent.Content{}, u.err(parts[0], err)
	}
	return c, nil
}

func (u *unitReader) hasCodes() bool {
	return len(u.data) > 0
}

// plainText collects text and <cp/>, looking through annotations.
func (u *unitReader) plainText(b *strings.Builder, n *xmlx.Node) error {
	for _, k := range n.Kids {
		switch {
		case k.IsText():
			b.WriteString(k.Text)
		case k.Name.Local == "cp":
			r, err := codePoint(k)
			if err != nil {
				return u.err(k, err)
			}
			b.WriteRune(r)
		case k.Name.Local == "mrk":
			if err := u.plainText(b, k); err != nil {
				return err
			}
		case k.Name.Local == "sm", k.Name.Local == "em":
		default:
			return u.err(k, formats.Invalidf("<%s> in a unit whose text is message syntax", k.Name.Local))
		}
	}
	return nil
}

// inline converts inline content into pattern elements.
func (u *unitReader) inline(b *inline.Builder, n *xmlx.Node) error {
	for _, k := range n.Kids {
		if err := u.inlineNode(b, k); err != nil {
			return err
		}
	}
	return nil
}

func (u *unitReader) inlineNode(b *inline.Builder, k *xmlx.Node) error {
	if k.IsText() {
		b.Text(k.Text)
		return nil
	}
	switch k.Name.Local {
	case "cp":
		r, err := codePoint(k)
		if err != nil {
			return u.err(k, err)
		}
		b.Text(string(r))
	case "ph", "sc", "ec":
		return u.code(b, k, "dataRef")
	case "pc":
		return u.pairedCode(b, k)
	case "mrk":
		return u.inline(b, k)
	case "sm", "em":
	default:
		return u.err(k, formats.Unsupportedf("inline element <%s>", k.Name.Local))
	}
	return nil
}

func (u *unitReader) pairedCode(b *inline.Builder, k *xmlx.Node) error {
	if err := u.code(b, k, "dataRefStart"); err != nil {
		return err
	}
	if err := u.inline(b, k); err != nil {
		return err
	}
	return u.code(b, k, "dataRefEnd")
}

// code appends the pattern element whose MF2 syntax the code's
// original data holds.
func (u *unitReader) code(b *inline.Builder, k *xmlx.Node, attr string) error {
	ref := k.Get(attr)
	d, ok := u.data[ref]
	if ref == "" || !ok {
		return u.err(k, formats.Unsupportedf("<%s id=%q> has no original data (%s); only codes with original data can be imported", k.Name.Local, k.Get("id"), attr))
	}
	var text strings.Builder
	if err := u.plainText(&text, d); err != nil {
		return err
	}
	el, err := inline.ParseElement(text.String())
	if err != nil {
		return u.err(k, err)
	}
	b.Element(el)
	return nil
}

func codePoint(n *xmlx.Node) (rune, error) {
	v, err := strconv.ParseUint(n.Get("hex"), 16, 32)
	if err != nil || v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
		return 0, formats.Invalidf("<cp hex=%q> is not a code point", n.Get("hex"))
	}
	return rune(v), nil
}
