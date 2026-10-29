package xmlx

import (
	"encoding/xml"
	"io"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// XMLNamespace is the namespace of the xml: prefix (xml:lang, xml:space).
const XMLNamespace = "http://www.w3.org/XML/1998/namespace"

// Node is an element or a text node of a small in-memory tree. Readers
// build one tree per record (a unit, a tu, a concept) so large files
// stream.
type Node struct {
	Name xml.Name
	Attr []xml.Attr
	Kids []*Node
	// Text is the content of a text node (Name.Local == "").
	Text string
	// Line and Col locate the element's start tag.
	Line, Col int
}

// IsText reports whether n is a text node.
func (n *Node) IsText() bool { return n.Name.Local == "" }

// Get returns the value of the un-namespaced attribute local.
func (n *Node) Get(local string) string {
	v, _ := n.Lookup("", local)
	return v
}

// Lookup returns the value of the attribute {space}local.
func (n *Node) Lookup(space, local string) (string, bool) {
	for _, a := range n.Attr {
		if a.Name.Local == local && a.Name.Space == space {
			return a.Value, true
		}
	}
	return "", false
}

// Lang returns xml:lang, falling back to the legacy un-namespaced lang.
func (n *Node) Lang() string {
	if v, ok := n.Lookup(XMLNamespace, "lang"); ok {
		return v
	}
	return n.Get("lang")
}

// Elements returns n's child elements.
func (n *Node) Elements() []*Node {
	var out []*Node
	for _, k := range n.Kids {
		if !k.IsText() {
			out = append(out, k)
		}
	}
	return out
}

// InnerText concatenates the text of n and all its descendants.
func (n *Node) InnerText() string {
	if n.IsText() {
		return n.Text
	}
	var b strings.Builder
	for _, k := range n.Kids {
		b.WriteString(k.InnerText())
	}
	return b.String()
}

// Tree reads the element that start opened, through its end tag. It
// must be called right after Token returned start.
func (d *Decoder) Tree(start xml.StartElement) (*Node, error) {
	line, col := d.StartPos()
	root := &Node{Name: start.Name, Attr: start.Attr, Line: line, Col: col}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil, d.Err("", formats.Invalidf("unexpected end of document inside <%s>", start.Name.Local))
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			kid, err := d.Tree(t)
			if err != nil {
				return nil, err
			}
			root.Kids = append(root.Kids, kid)
		case xml.CharData:
			root.Kids = appendText(root.Kids, string(t))
		case xml.EndElement:
			return root, nil
		}
	}
}

func appendText(kids []*Node, s string) []*Node {
	if n := len(kids); n > 0 && kids[n-1].IsText() {
		kids[n-1].Text += s
		return kids
	}
	return append(kids, &Node{Text: s})
}

// Skip consumes the rest of the element whose start tag was just read.
func (d *Decoder) Skip() error {
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err == io.EOF {
			return d.Err("", formats.Invalidf("unexpected end of document"))
		}
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}
