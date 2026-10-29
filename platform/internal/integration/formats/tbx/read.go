package tbx

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/xmlx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// ReadOptions configure Read.
type ReadOptions struct {
	Limits formats.Limits
}

// Read reads a TBX document. Concept entries stream: memory holds one
// entry at a time plus the resulting termbase.
func Read(r io.Reader, opts ReadOptions) (formats.Termbase, error) {
	limits := opts.Limits.WithDefaults(formats.DefaultLimits)
	rd := &reader{d: xmlx.NewDecoder(r, format, limits.MaxBytes), limits: limits}
	root, err := rd.d.Root()
	if err != nil {
		return formats.Termbase{}, err
	}
	if err := rd.header(root); err != nil {
		return formats.Termbase{}, err
	}
	if err := rd.walk(root.Name.Local); err != nil {
		return formats.Termbase{}, err
	}
	return rd.tb, nil
}

type reader struct {
	d      *xmlx.Decoder
	limits formats.Limits
	tb     formats.Termbase
}

func (rd *reader) header(root xml.StartElement) error {
	n := &xmlx.Node{Name: root.Name, Attr: root.Attr}
	if root.Name.Local != "tbx" && root.Name.Local != "martif" {
		return rd.d.Err("", formats.Invalidf("document element is <%s>, not <tbx> or <martif>", root.Name.Local))
	}
	if lang := n.Lang(); lang != "" {
		tag, err := formats.ParseLocale(lang)
		if err != nil {
			return rd.d.Err("", formats.Invalidf("%v", err))
		}
		rd.tb.Language = tag
	}
	return nil
}

// walk descends through text/body to the concept entries.
func (rd *reader) walk(inside string) error {
	for {
		tok, err := rd.d.Token()
		if err == io.EOF {
			return rd.d.Err("", formats.Invalidf("unexpected end of document inside <%s>", inside))
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if err := rd.element(t); err != nil {
				return err
			}
		}
	}
}

func (rd *reader) element(t xml.StartElement) error {
	switch t.Name.Local {
	case "text", "body":
		return rd.walk(t.Name.Local)
	case "conceptEntry", "termEntry":
		return rd.entry(t)
	}
	return rd.d.Skip()
}

func (rd *reader) entry(t xml.StartElement) error {
	if len(rd.tb.Concepts) >= rd.limits.MaxItems {
		return rd.d.Err("", fmt.Errorf("%w: more than %d concepts", formats.ErrTooLarge, rd.limits.MaxItems))
	}
	n, err := rd.d.Tree(t)
	if err != nil {
		return err
	}
	c, err := concept(n)
	if err != nil {
		return err
	}
	c.Pos = formats.Position{Line: n.Line, Column: n.Col, Ref: fmt.Sprintf("%s[%d]", t.Name.Local, len(rd.tb.Concepts)+1)}
	rd.tb.Concepts = append(rd.tb.Concepts, c)
	return nil
}

func nodeErr(n *xmlx.Node, item string, err error) *formats.Error {
	return &formats.Error{Format: format, Line: n.Line, Column: n.Col, Item: item, Err: err}
}

// readID reverses conceptID.
func readID(id string) string {
	if rest, ok := strings.CutPrefix(id, idPrefix); ok && needsPrefix(rest) {
		return rest
	}
	return id
}

func concept(n *xmlx.Node) (formats.Concept, error) {
	c := formats.Concept{ID: readID(n.Get("id"))}
	item := "concept " + strconv.Quote(c.ID)
	var langSecs []*xmlx.Node
	for _, k := range n.Elements() {
		switch local := k.Name.Local; {
		case local == "langSec" || local == "langSet":
			langSecs = append(langSecs, k)
		case local == "note":
			c.Notes = append(c.Notes, k.InnerText())
		default:
			if err := conceptDescrip(&c, k); err != nil {
				return c, nodeErr(k, item, err)
			}
		}
	}
	for _, ls := range langSecs {
		if err := langSec(&c, ls); err != nil {
			return c, nodeErr(ls, item, err)
		}
	}
	return c, nil
}

// descrip returns the data category and text of a descrip, a descrip in
// a descripGrp, or a DCT-style element.
func descrip(k *xmlx.Node) (string, *xmlx.Node, bool) {
	switch {
	case k.Name.Local == "descrip":
		return k.Get("type"), k, true
	case k.Name.Local == "descripGrp":
		for _, d := range k.Elements() {
			if d.Name.Local == "descrip" {
				return d.Get("type"), d, true
			}
		}
	case k.Name.Space != Namespace && k.Name.Space != "":
		return k.Name.Local, k, true
	}
	return "", nil, false
}

func conceptDescrip(c *formats.Concept, k *xmlx.Node) error {
	kind, d, ok := descrip(k)
	if !ok {
		return nil
	}
	switch kind {
	case "subjectField":
		c.Domain = d.InnerText()
	case "definition":
		var loc bcp47.Tag
		if lang := d.Lang(); lang != "" {
			tag, err := formats.ParseLocale(lang)
			if err != nil {
				return formats.Invalidf("definition: %v", err)
			}
			loc = tag
		}
		c.Definitions = append(c.Definitions, formats.Definition{Locale: loc, Text: d.InnerText()})
	}
	return nil
}

func langSec(c *formats.Concept, ls *xmlx.Node) error {
	loc, err := formats.ParseLocale(ls.Lang())
	if err != nil {
		return formats.Invalidf("langSec: %v", err)
	}
	for _, k := range ls.Elements() {
		switch local := k.Name.Local; {
		case local == "termSec" || local == "tig" || local == "ntig":
			t, err := term(k, loc)
			if err != nil {
				return err
			}
			c.Terms = append(c.Terms, t)
		case local == "note":
			c.Notes = append(c.Notes, k.InnerText())
		default:
			if kind, d, ok := descrip(k); ok && kind == "definition" {
				c.Definitions = append(c.Definitions, formats.Definition{Locale: loc, Text: d.InnerText()})
			}
		}
	}
	return nil
}

// term reads a termSec (v3), tig or ntig (TBX 2008).
func term(n *xmlx.Node, loc bcp47.Tag) (formats.Term, error) {
	t := formats.Term{Locale: loc, Status: formats.TermAdmitted}
	for _, k := range n.Elements() {
		if err := termPart(&t, k); err != nil {
			return t, err
		}
	}
	if t.Text == "" {
		return t, formats.Invalidf("a term without text")
	}
	return t, nil
}

func termPart(t *formats.Term, k *xmlx.Node) error {
	switch k.Name.Local {
	case "term":
		t.Text = strings.TrimSpace(k.InnerText())
	case "termGrp", "termNoteGrp":
		for _, g := range k.Elements() {
			if err := termPart(t, g); err != nil {
				return err
			}
		}
	case "termNote":
		return termNote(t, k.Get("type"), k.InnerText())
	case "note":
		t.Notes = append(t.Notes, k.InnerText())
	default:
		kind, d, ok := descrip(k)
		switch {
		case ok && kind == "context":
			t.Context = d.InnerText()
		case ok && k.Name.Local != "descrip" && k.Name.Local != "descripGrp":
			return termNote(t, kind, d.InnerText())
		}
	}
	return nil
}

func termNote(t *formats.Term, kind, value string) error {
	switch kind {
	case "partOfSpeech":
		t.PartOfSpeech = strings.TrimSpace(value)
	case "administrativeStatus", "normativeAuthorization":
		status, ok := parseStatus(value)
		if !ok {
			return formats.Invalidf("%s %q", kind, value)
		}
		t.Status = status
	}
	return nil
}
