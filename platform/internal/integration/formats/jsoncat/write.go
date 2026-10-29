package jsoncat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// WriteOptions configure Write.
type WriteOptions struct {
	// Locale selects the messages: the source when it is the catalog's
	// source locale (or zero), else the translations into it.
	Locale bcp47.Tag
	// Layout defaults to Flat.
	Layout Layout
	// Syntax of the messages; "" means MF1.
	Syntax mfcontent.Syntax
}

// Write writes the messages of one locale as a JSON catalog. Entries
// without content in that locale are left out.
func Write(w io.Writer, cat formats.Catalog, opts WriteOptions) error {
	msgs, err := messages(cat, opts)
	if err != nil {
		return err
	}
	var root *node
	if opts.Layout == Nested {
		if root, err = nest(msgs); err != nil {
			return err
		}
	} else {
		root = flat(msgs)
	}
	bw := bufio.NewWriter(w)
	writeNode(bw, root, "")
	bw.WriteString("\n")
	if err := bw.Flush(); err != nil {
		return &formats.Error{Format: format, Err: err}
	}
	return nil
}

// messages returns the key → text pairs to write.
func messages(cat formats.Catalog, opts WriteOptions) (map[string]string, error) {
	locale := opts.Locale
	if locale.IsZero() {
		locale = cat.SourceLocale
	}
	out := map[string]string{}
	for _, e := range cat.Entries {
		c, ok := content(e, locale, cat.SourceLocale)
		if !ok {
			continue
		}
		item := fmt.Sprintf("key %q", e.ID)
		if _, dup := out[e.ID]; dup {
			return nil, &formats.Error{Format: format, Item: item, Err: formats.Invalidf("key appears twice")}
		}
		text, err := messageText(c, opts.Syntax, locale)
		if err != nil {
			return nil, &formats.Error{Format: format, Item: item, Err: err}
		}
		out[e.ID] = text
	}
	return out, nil
}

func content(e formats.Entry, locale, source bcp47.Tag) (mfcontent.Content, bool) {
	if locale == source {
		return e.Source, !e.Source.IsZero()
	}
	t, ok := e.Target(locale)
	return t.Content, ok
}

func messageText(c mfcontent.Content, syntax mfcontent.Syntax, locale bcp47.Tag) (string, error) {
	switch {
	case syntax == mfcontent.MF2:
		return formats.MF2Text(c)
	case c.Syntax == mfcontent.MF1:
		return c.Text, nil
	}
	return renderMF1(c, locale)
}

// renderMF1 writes a message of text and plain variable placeholders as
// MF1 and proves it with the MF1 converter.
func renderMF1(c mfcontent.Content, locale bcp47.Tag) (string, error) {
	if formats.IsComplex(c.Model) {
		return "", ErrNoMF1
	}
	var b strings.Builder
	for _, el := range c.Model.Pattern {
		switch el := el.(type) {
		case mf.Text:
			b.WriteString(escapeMF1(string(el)))
		case mf.Expression:
			v, ok := el.Arg.(mf.VariableRef)
			if !ok || el.Function != nil || len(el.Attributes) > 0 {
				return "", ErrNoMF1
			}
			b.WriteString("{" + v.Name + "}")
		default:
			return "", ErrNoMF1
		}
	}
	back, err := mf.ParseMF1(b.String(), locale.String())
	if err != nil || !sameModel(back, c.Model) {
		return "", ErrNoMF1
	}
	return b.String(), nil
}

// escapeMF1 quotes MF1 syntax: an apostrophe doubles, and each run of
// braces becomes one quoted literal ('{}').
func escapeMF1(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] == '\'':
			b.WriteString("''")
			i++
		case s[i] == '{' || s[i] == '}':
			j := i
			for j < len(s) && (s[j] == '{' || s[j] == '}') {
				j++
			}
			b.WriteString("'" + s[i:j] + "'")
			i = j
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func sameModel(a, b mf.Message) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// node is a JSON object being written: leaf strings and child objects.
type node struct {
	text     string
	isLeaf   bool
	children map[string]*node
}

func flat(msgs map[string]string) *node {
	root := &node{children: map[string]*node{}}
	for k, v := range msgs {
		root.children[k] = &node{text: v, isLeaf: true}
	}
	return root
}

// nest splits keys at "." into nested objects.
func nest(msgs map[string]string) (*node, error) {
	root := &node{children: map[string]*node{}}
	for _, key := range slices.Sorted(maps.Keys(msgs)) {
		if err := insert(root, key, msgs[key]); err != nil {
			return nil, &formats.Error{Format: format, Item: fmt.Sprintf("key %q", key), Err: err}
		}
	}
	return root, nil
}

func insert(root *node, key, text string) error {
	parts := strings.Split(key, ".")
	n := root
	for i, part := range parts {
		if part == "" {
			return formats.Invalidf("a key with an empty segment cannot be nested")
		}
		child, ok := n.children[part]
		last := i == len(parts)-1
		switch {
		case !ok && last:
			n.children[part] = &node{text: text, isLeaf: true}
		case !ok:
			child = &node{children: map[string]*node{}}
			n.children[part] = child
		case last || child.isLeaf:
			return formats.Invalidf("key is both a message and a prefix of another key; write the catalog flat")
		}
		n = child
	}
	return nil
}

func writeNode(w *bufio.Writer, n *node, indent string) {
	if n.isLeaf {
		w.WriteString(jsonString(n.text))
		return
	}
	if len(n.children) == 0 {
		w.WriteString("{}")
		return
	}
	w.WriteString("{")
	for i, k := range slices.Sorted(maps.Keys(n.children)) {
		if i > 0 {
			w.WriteString(",")
		}
		w.WriteString("\n" + indent + "  " + jsonString(k) + ": ")
		writeNode(w, n.children[k], indent+"  ")
	}
	w.WriteString("\n" + indent + "}")
}

// jsonString encodes s without HTML escaping, so markup stays readable.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
