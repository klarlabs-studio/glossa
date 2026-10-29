package glossa

import (
	"html/template"
	"strings"

	"go.klarlabs.de/glossa/messageformat"
)

// Structured rendering: formatted parts, safe HTML and styled text runs.
// The HTML rules are @klarlabs-studio/glossa/elements' (runtimes/testdata/markup.json):
// translation text is never parsed as HTML, only markup named on the safe
// list becomes an element, and markup options are always dropped, so a
// translation can't add a link, an event handler or a style.

// Part is one formatted part of a message: text, markup, a bidi
// isolation character, a placeholder's formatted value or a fallback.
// Joining the Values ([PartsText]) gives what [Localizer.T] renders.
type Part = messageformat.Part

// PartType names the kind of a [Part].
type PartType = messageformat.PartType

// Part types; see [messageformat.PartType].
const (
	PartText          = messageformat.PartText
	PartMarkup        = messageformat.PartMarkup
	PartBidiIsolation = messageformat.PartBidiIsolation
	PartFallback      = messageformat.PartFallback
	PartString        = messageformat.PartString
	PartNumber        = messageformat.PartNumber
	PartDateTime      = messageformat.PartDateTime
	PartUnknown       = messageformat.PartUnknown
)

// MarkupKind is the kind of a markup [Part]: open, close or standalone.
type MarkupKind = messageformat.MarkupKind

// Markup kinds.
const (
	MarkupOpen       = messageformat.MarkupOpen
	MarkupStandalone = messageformat.MarkupStandalone
	MarkupClose      = messageformat.MarkupClose
)

// PartsText joins the text of parts; markup renders as nothing.
func PartsText(parts []Part) string { return messageformat.PartsText(parts) }

// safeTags are the inline, attribute-free phrasing elements a translation
// may produce: runtimes/testdata/markup.json's safeTags, which
// @klarlabs-studio/glossa/elements' SAFE_TAGS is tested against too.
var safeTags = setOf("b strong i em u s small mark sub sup code kbd samp var abbr cite dfn q del ins bdi span br wbr")

// voidTags are the safe tags without children or a closing tag.
var voidTags = setOf("br wbr")

func setOf(words string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(words) {
		set[w] = true
	}
	return set
}

// Parts renders message id as formatted parts. Resolution, fallbacks and
// error reporting are those of [Localizer.T]: a missing message, or one
// that renders empty, is a single text part holding the inline default
// or the ID.
func (l *Localizer) Parts(id string, args Args, opts ...Option) []Part {
	o := l.options(opts)
	snap := l.snapshot()
	res := snap.rel.resolve(id, l.requested)
	return renderAs(l.c, snap, res, o,
		func(msg messageformat.Message, locale string) ([]Part, string, error) {
			parts, err := messageformat.FormatToParts(msg, locale, args, o.formatOptions()...)
			return parts, messageformat.PartsText(parts), err
		},
		func(text string) []Part { return []Part{{Type: PartText, Value: text}} })
}

// HTML renders message id as safe HTML: text is escaped, markup on the
// safe list (b, strong, i, em, u, code, br, …) becomes a bare element,
// and other markup, such as a translation's {#link href=…}, renders only
// its content. Markup options never become attributes. Available in
// templates as th.
func (l *Localizer) HTML(id string, args Args, opts ...Option) template.HTML {
	return template.HTML(partsHTML(l.Parts(id, args, opts...))) //nolint:gosec // escaped, allow-listed markup only
}

// Run is a piece of text in one style, for renderers with font styles
// instead of markup, such as PDF libraries.
type Run struct {
	Text      string
	Bold      bool
	Italic    bool
	Underline bool
}

// Style returns the run's style in the notation of fpdf's SetFont: "" for
// regular, else "B", "I" and "U" combined in that order ("BI", "BIU").
func (r Run) Style() string {
	var s strings.Builder
	for _, f := range []struct {
		on   bool
		code byte
	}{{r.Bold, 'B'}, {r.Italic, 'I'}, {r.Underline, 'U'}} {
		if f.on {
			s.WriteByte(f.code)
		}
	}
	return s.String()
}

// Runs renders message id as styled text runs, following the markup the
// way [Localizer.HTML] does: b and strong are bold, i and em italic, u
// underlined, nesting combines them, and br is a line break ("\n"). Other
// markup keeps its text in the surrounding style. Adjacent runs of the
// same style are merged.
//
// A PDF adapter maps each run onto the font style and writes its text:
//
//	for _, r := range l.Runs("invoice.note", args, glossa.BidiIsolation(false)) {
//		pdf.SetFont("NotoSans", r.Style(), 10)
//		pdf.Write(5, r.Text)
//	}
//
// Documents in left-to-right scripts usually turn bidi isolation off, so
// no isolation characters reach the font.
func (l *Localizer) Runs(id string, args Args, opts ...Option) []Run {
	var runs []Run
	walkRuns(partsTree(l.Parts(id, args, opts...)), Run{}, &runs)
	return runs
}

// node is a text run (tag == "") or a safe element with its children.
type node struct {
	tag      string
	text     string
	children []*node
}

// partsTree builds the tree of text and safe elements, exactly as
// @klarlabs-studio/glossa/elements' partsToTree: unsafe markup adds no element, unclosed
// markup closes at the end, a close closes everything opened after its
// open, and a close without an open is ignored.
func partsTree(parts []Part) []*node {
	root := &node{}
	type open struct {
		name string
		el   *node // nil for unsafe markup
	}
	var stack []open
	target := func() *node {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].el != nil {
				return stack[i].el
			}
		}
		return root
	}
	pushText := func(s string) {
		if s == "" {
			return
		}
		t := target()
		if n := len(t.children); n > 0 && t.children[n-1].tag == "" {
			t.children[n-1].text += s
			return
		}
		t.children = append(t.children, &node{text: s})
	}
	pushElement := func(el *node) {
		t := target()
		t.children = append(t.children, el)
	}
	for _, p := range parts {
		if p.Type != PartMarkup {
			pushText(p.Value)
			continue
		}
		safe := safeTags[p.Name]
		switch {
		case p.Kind == MarkupStandalone || (p.Kind == MarkupOpen && voidTags[p.Name]):
			if safe && voidTags[p.Name] {
				pushElement(&node{tag: p.Name})
			}
		case p.Kind == MarkupOpen:
			var el *node
			if safe {
				el = &node{tag: p.Name}
				pushElement(el)
			}
			stack = append(stack, open{name: p.Name, el: el})
		default:
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].name == p.Name {
					stack = stack[:i]
					break
				}
			}
		}
	}
	return root.children
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeText escapes s for an HTML text node, as @klarlabs-studio/glossa/elements does.
func escapeText(s string) string { return htmlEscaper.Replace(s) }

// partsHTML serializes parts as escaped text and bare safe elements.
func partsHTML(parts []Part) string {
	var b strings.Builder
	writeHTML(&b, partsTree(parts))
	return b.String()
}

func writeHTML(b *strings.Builder, nodes []*node) {
	for _, n := range nodes {
		switch {
		case n.tag == "":
			b.WriteString(escapeText(n.text))
		case !safeTags[n.tag]:
		case voidTags[n.tag]:
			b.WriteString("<" + n.tag + ">")
		default:
			b.WriteString("<" + n.tag + ">")
			writeHTML(b, n.children)
			b.WriteString("</" + n.tag + ">")
		}
	}
}

func walkRuns(nodes []*node, style Run, runs *[]Run) {
	for _, n := range nodes {
		switch n.tag {
		case "":
			appendRun(runs, style, n.text)
		case "br":
			appendRun(runs, style, "\n")
		default:
			inner := style
			switch n.tag {
			case "b", "strong":
				inner.Bold = true
			case "i", "em":
				inner.Italic = true
			case "u":
				inner.Underline = true
			}
			walkRuns(n.children, inner, runs)
		}
	}
}

func appendRun(runs *[]Run, style Run, text string) {
	if n := len(*runs); n > 0 {
		last := &(*runs)[n-1]
		if last.Bold == style.Bold && last.Italic == style.Italic && last.Underline == style.Underline {
			last.Text += text
			return
		}
	}
	style.Text = text
	*runs = append(*runs, style)
}
