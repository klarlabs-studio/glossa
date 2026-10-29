package xliff

import (
	"fmt"
	"io"
	"regexp"
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/inline"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/xmlx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// WriteOptions configure Write.
type WriteOptions struct {
	// TargetLocale selects the translations to write. The zero value
	// writes a source-only document.
	TargetLocale bcp47.Tag
}

// Write writes cat as an XLIFF 2.1 document. Every entry needs source
// content; XLIFF has no empty document, so cat needs at least one entry.
func Write(w io.Writer, cat formats.Catalog, opts WriteOptions) error {
	if err := checkCatalog(cat); err != nil {
		return err
	}
	xw := xmlx.NewWriter(w)
	xw.Decl()
	xw.Open("xliff", "xmlns", Namespace, "xmlns:slr", NamespaceSLR, "version", Version,
		"srcLang", cat.SourceLocale.String(), "trgLang", opts.TargetLocale.String(), "xml:space", "preserve")
	for i, group := range byNamespace(cat.Entries) {
		if err := writeFile(xw, i+1, group, opts.TargetLocale); err != nil {
			return err
		}
	}
	xw.Close()
	if err := xw.Flush(); err != nil {
		return &formats.Error{Format: format, Err: err}
	}
	return nil
}

func checkCatalog(cat formats.Catalog) error {
	if cat.SourceLocale.IsZero() {
		return &formats.Error{Format: format, Err: formats.Invalidf("the catalog has no source locale")}
	}
	if len(cat.Entries) == 0 {
		return &formats.Error{Format: format, Err: formats.Invalidf("an XLIFF document needs at least one message")}
	}
	for _, e := range cat.Entries {
		if e.ID == "" || e.Source.IsZero() {
			return &formats.Error{Format: format, Item: fmt.Sprintf("message %q", e.ID), Err: formats.Invalidf("a message needs an ID and source content")}
		}
	}
	return nil
}

// byNamespace groups entries by namespace, in order of first appearance.
func byNamespace(entries []formats.Entry) [][]formats.Entry {
	index := map[string]int{}
	var groups [][]formats.Entry
	for _, e := range entries {
		i, ok := index[e.Namespace]
		if !ok {
			i = len(groups)
			index[e.Namespace] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], e)
	}
	return groups
}

func writeFile(xw *xmlx.Writer, n int, entries []formats.Entry, target bcp47.Tag) error {
	xw.Open("file", "id", "f"+strconv.Itoa(n), "original", entries[0].Namespace)
	if anyMaxLength(entries) {
		xw.Empty("slr:profiles", "generalProfile", "xliff:codepoints")
	}
	ids := unitIDs(entries)
	for i, e := range entries {
		if err := writeUnit(xw, ids[i], e, target); err != nil {
			return &formats.Error{Format: format, Item: fmt.Sprintf("message %q", e.ID), Err: err}
		}
	}
	xw.Close()
	return nil
}

func anyMaxLength(entries []formats.Entry) bool {
	for _, e := range entries {
		if e.MaxLength > 0 {
			return true
		}
	}
	return false
}

var nmtoken = regexp.MustCompile(`^[\p{L}\p{Nd}\p{Mn}\p{Mc}\p{Lm}\p{Nl}._:\-·]+$`)

// unitIDs uses each key as its unit ID when it is an NMTOKEN, and
// generates a unique ID otherwise.
func unitIDs(entries []formats.Entry) []string {
	used := map[string]bool{}
	for _, e := range entries {
		used[e.ID] = true
	}
	ids := make([]string, len(entries))
	next := 1
	for i, e := range entries {
		if nmtoken.MatchString(e.ID) {
			ids[i] = e.ID
			continue
		}
		for used["glossa-u"+strconv.Itoa(next)] {
			next++
		}
		ids[i] = "glossa-u" + strconv.Itoa(next)
		used[ids[i]] = true
	}
	return ids
}

func writeUnit(xw *xmlx.Writer, id string, e formats.Entry, locale bcp47.Tag) error {
	tgt, hasTarget := formats.Target{}, false
	if !locale.IsZero() {
		tgt, hasTarget = e.Target(locale)
	}
	complex := formats.IsComplex(e.Source.Model) || (hasTarget && formats.IsComplex(tgt.Content.Model))
	unitType := ""
	if complex {
		unitType = TypeMF2
	}
	xw.Open("unit", "id", id, "name", e.ID, "type", unitType, "slr:sizeRestriction", maxLength(e.MaxLength))
	writeNotes(xw, e)
	var err error
	if complex {
		err = writeTextSegment(xw, e.Source, tgt, hasTarget)
	} else {
		err = writeCodeSegment(xw, e.Source, tgt, hasTarget)
	}
	xw.Close()
	return err
}

func maxLength(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func writeNotes(xw *xmlx.Writer, e formats.Entry) {
	if e.Description == "" && len(e.References) == 0 && len(e.Notes) == 0 {
		return
	}
	xw.Open("notes")
	if e.Description != "" {
		xw.Leaf("note", e.Description, "category", "description")
	}
	for _, r := range e.References {
		xw.Leaf("note", r, "category", "location")
	}
	for _, n := range e.Notes {
		xw.Leaf("note", n)
	}
	xw.Close()
}

// segmentAttrs are the state attributes of a segment with a target.
func segmentAttrs(tgt formats.Target, hasTarget bool) []string {
	if !hasTarget {
		return nil
	}
	switch tgt.State {
	case formats.StateNeedsReview:
		return []string{"state", "translated"}
	case formats.StateApproved:
		return []string{"state", "final"}
	case formats.StateRejected:
		return []string{"state", "initial", "subState", SubStateRejected}
	}
	return []string{"state", "initial"}
}

func writeTextSegment(xw *xmlx.Writer, src mfcontent.Content, tgt formats.Target, hasTarget bool) error {
	srcText, err := formats.MF2Text(src)
	if err != nil {
		return err
	}
	xw.Open("segment", segmentAttrs(tgt, hasTarget)...)
	xw.OpenInline("source")
	writeText(xw, srcText)
	xw.Close()
	if hasTarget {
		tgtText, err := formats.MF2Text(tgt.Content)
		if err != nil {
			return err
		}
		xw.OpenInline("target")
		writeText(xw, tgtText)
		xw.Close()
	}
	xw.Close()
	return nil
}

func writeCodeSegment(xw *xmlx.Writer, src mfcontent.Content, tgt formats.Target, hasTarget bool) error {
	srcSpans, err := inline.Spans(src.Model.Pattern)
	if err != nil {
		return err
	}
	var tgtSpans []inline.Span
	if hasTarget {
		if tgtSpans, err = inline.Spans(tgt.Content.Model.Pattern); err != nil {
			return err
		}
	}
	inline.AssignIDs(srcSpans, tgtSpans)
	data := writeOriginalData(xw, srcSpans, tgtSpans)
	xw.Open("segment", segmentAttrs(tgt, hasTarget)...)
	xw.OpenInline("source")
	writeSpans(xw, srcSpans, 0, len(srcSpans), data)
	xw.Close()
	if hasTarget {
		xw.OpenInline("target")
		writeSpans(xw, tgtSpans, 0, len(tgtSpans), data)
		xw.Close()
	}
	xw.Close()
	return nil
}

// writeOriginalData writes one <data> per distinct code syntax and
// returns the data IDs by syntax.
func writeOriginalData(xw *xmlx.Writer, spans ...[]inline.Span) map[string]string {
	ids := map[string]string{}
	var order []string
	for _, ss := range spans {
		for _, s := range ss {
			if s.Kind != inline.Text && ids[s.Text] == "" {
				ids[s.Text] = "d" + strconv.Itoa(len(order)+1)
				order = append(order, s.Text)
			}
		}
	}
	if len(order) == 0 {
		return ids
	}
	xw.Open("originalData")
	for _, src := range order {
		xw.OpenInline("data", "id", ids[src])
		writeText(xw, src)
		xw.Close()
	}
	xw.Close()
	return ids
}

func writeSpans(xw *xmlx.Writer, spans []inline.Span, from, to int, data map[string]string) {
	for i := from; i < to; i++ {
		s := spans[i]
		switch s.Kind {
		case inline.Text:
			writeText(xw, s.Text)
		case inline.Placeholder:
			xw.Tag("ph", "id", s.ID, "dataRef", data[s.Text], "disp", disp(s.Text))
		case inline.Open:
			end := spans[s.Pair]
			xw.Start("pc", "id", s.ID, "dataRefStart", data[s.Text], "dataRefEnd", data[end.Text],
				"dispStart", disp(s.Text), "dispEnd", disp(end.Text))
			writeSpans(xw, spans, i+1, s.Pair, data)
			xw.End("pc")
			i = s.Pair
		case inline.IsolatedOpen:
			xw.Tag("sc", "id", s.ID, "dataRef", data[s.Text], "disp", disp(s.Text), "isolated", "yes")
		case inline.IsolatedClose:
			xw.Tag("ec", "id", s.ID, "dataRef", data[s.Text], "disp", disp(s.Text), "isolated", "yes")
		}
	}
}

// disp is a code's display text, omitted when XML can't carry it.
func disp(src string) string {
	if xmlx.CheckChars(src) != nil {
		return ""
	}
	return src
}

// writeText writes text, representing characters XML can't carry as
// <cp hex/>.
func writeText(xw *xmlx.Writer, s string) {
	start := 0
	for i, r := range s {
		if xmlx.IsChar(r) {
			continue
		}
		xw.Text(s[start:i])
		xw.Tag("cp", "hex", fmt.Sprintf("%04X", r))
		start = i + len(string(r))
	}
	xw.Text(s[start:])
}
