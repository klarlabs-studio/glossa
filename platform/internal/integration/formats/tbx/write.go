package tbx

import (
	"io"
	"regexp"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/xmlx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Write writes tb as a TBX-Basic (TBX v3, DCA) document. Every concept
// needs at least one term; every term a locale, text and valid status
// (an empty status is written as admitted).
func Write(w io.Writer, tb formats.Termbase) error {
	if err := check(tb); err != nil {
		return err
	}
	lang := tb.Language.String()
	if lang == "" {
		lang = "en"
	}
	xw := xmlx.NewWriter(w)
	xw.Decl()
	xw.Open("tbx", "xmlns", Namespace, "type", "TBX-Basic", "style", "dca", "xml:lang", lang)
	xw.Open("tbxHeader")
	xw.Open("fileDesc")
	xw.Open("sourceDesc")
	xw.Leaf("p", "Exported from Glossa")
	xw.Close()
	xw.Close()
	xw.Close()
	xw.Open("text")
	if len(tb.Concepts) > 0 {
		xw.Open("body")
		for i, c := range tb.Concepts {
			writeConcept(xw, i, c)
		}
		xw.Close()
	}
	xw.Close()
	xw.Close()
	if err := xw.Flush(); err != nil {
		return &formats.Error{Format: format, Err: err}
	}
	return nil
}

func check(tb formats.Termbase) error {
	for i, c := range tb.Concepts {
		item := "concept " + strconv.Quote(c.ID)
		if len(c.Terms) == 0 {
			return &formats.Error{Format: format, Item: item, Err: formats.Invalidf("a concept needs at least one term")}
		}
		for j, t := range c.Terms {
			if t.Locale.IsZero() || t.Text == "" || (t.Status != "" && !t.Status.Valid()) {
				return &formats.Error{Format: format, Item: item, Err: formats.Invalidf("term %d of concept %d needs a locale, text and a valid status", j+1, i+1)}
			}
		}
	}
	return nil
}

var ncname = regexp.MustCompile(`^[\p{L}_][\p{L}\p{Nd}\p{Mn}\p{Mc}._\-]*$`)

// conceptID makes id an NCName, as TBX requires of IDs, reversibly
// (see readID).
func conceptID(id string, i int) string {
	switch {
	case id == "":
		return "c" + strconv.Itoa(i+1)
	case needsPrefix(id):
		return idPrefix + id
	}
	return id
}

// needsPrefix reports whether id is written with idPrefix: when it is
// not an NCName, or when it already looks like a prefixed ID.
func needsPrefix(id string) bool {
	if !ncname.MatchString(id) {
		return true
	}
	rest, ok := strings.CutPrefix(id, idPrefix)
	return ok && needsPrefix(rest)
}

func writeConcept(xw *xmlx.Writer, i int, c formats.Concept) {
	xw.Open("conceptEntry", "id", conceptID(c.ID, i))
	if c.Domain != "" {
		xw.Leaf("descrip", c.Domain, "type", "subjectField")
	}
	locales := termLocales(c.Terms)
	for _, d := range c.Definitions {
		if !locales[d.Locale] {
			xw.Leaf("descrip", d.Text, "type", "definition", "xml:lang", d.Locale.String())
		}
	}
	for _, n := range c.Notes {
		xw.Leaf("note", n)
	}
	for _, loc := range localeOrder(c.Terms) {
		writeLangSec(xw, loc, c)
	}
	xw.Close()
}

func termLocales(terms []formats.Term) map[bcp47.Tag]bool {
	out := map[bcp47.Tag]bool{}
	for _, t := range terms {
		out[t.Locale] = true
	}
	return out
}

// localeOrder lists the terms' locales in order of first appearance.
func localeOrder(terms []formats.Term) []bcp47.Tag {
	seen := map[bcp47.Tag]bool{}
	var out []bcp47.Tag
	for _, t := range terms {
		if !seen[t.Locale] {
			seen[t.Locale] = true
			out = append(out, t.Locale)
		}
	}
	return out
}

func writeLangSec(xw *xmlx.Writer, loc bcp47.Tag, c formats.Concept) {
	xw.Open("langSec", "xml:lang", loc.String())
	for _, d := range c.Definitions {
		if d.Locale == loc {
			xw.Leaf("descrip", d.Text, "type", "definition")
		}
	}
	for _, t := range c.Terms {
		if t.Locale == loc {
			writeTerm(xw, t)
		}
	}
	xw.Close()
}

func writeTerm(xw *xmlx.Writer, t formats.Term) {
	xw.Open("termSec")
	xw.Leaf("term", t.Text)
	if t.PartOfSpeech != "" {
		pos := t.PartOfSpeech
		if !partsOfSpeech[pos] {
			pos = "other"
		}
		xw.Leaf("termNote", pos, "type", "partOfSpeech")
	}
	status := t.Status
	if status == "" {
		status = formats.TermAdmitted
	}
	xw.Leaf("termNote", statusValues[status], "type", "administrativeStatus")
	if t.Context != "" {
		xw.Leaf("descrip", t.Context, "type", "context")
	}
	for _, n := range t.Notes {
		xw.Leaf("note", n)
	}
	xw.Close()
}
