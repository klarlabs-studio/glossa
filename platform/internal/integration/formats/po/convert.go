package po

import (
	"fmt"
	"strconv"
	"strings"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// converter turns parsed entries into catalog entries.
type converter struct {
	opts        ReadOptions
	source      bcp47.Tag
	target      bcp47.Tag
	pluralForms string
	pluralVar   string
	translated  formats.State
	seen        map[string]bool
	formMaps    map[int]formMap
}

func convert(entries []*entry, opts ReadOptions) (formats.Catalog, error) {
	c := &converter{opts: opts, seen: map[string]bool{}, formMaps: map[int]formMap{}, pluralVar: opts.PluralVariable, translated: opts.TranslatedState}
	if c.pluralVar == "" {
		c.pluralVar = "count"
	}
	if c.translated == "" {
		c.translated = formats.StateApproved
	}
	if len(entries) > 0 && entries[0].id == "" && !entries[0].hasCtxt {
		if err := c.header(entries[0]); err != nil {
			return formats.Catalog{}, err
		}
		entries = entries[1:]
	}
	if err := c.locales(); err != nil {
		return formats.Catalog{}, err
	}
	cat := formats.Catalog{SourceLocale: c.source, TargetLocale: c.target}
	for _, e := range entries {
		out, err := c.entry(e)
		if err != nil {
			return formats.Catalog{}, &formats.Error{Format: format, Line: e.line, Column: 1, Item: fmt.Sprintf("msgid %q", e.id), Err: err}
		}
		cat.Entries = append(cat.Entries, out)
	}
	return cat, nil
}

// header reads the header entry's fields.
func (c *converter) header(e *entry) error {
	fields := map[string]string{}
	for _, line := range strings.Split(e.strs[0], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			fields[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	c.pluralForms = fields["plural-forms"]
	var err error
	if lang := fields["language"]; lang != "" && c.opts.Locale.IsZero() {
		if c.target, err = formats.ParseLocale(lang); err != nil {
			return &formats.Error{Format: format, Line: e.line, Item: "header", Err: formats.Invalidf("Language: %v", err)}
		}
	}
	if src := fields["x-source-language"]; src != "" && c.opts.SourceLocale.IsZero() {
		if c.source, err = formats.ParseLocale(src); err != nil {
			return &formats.Error{Format: format, Line: e.line, Item: "header", Err: formats.Invalidf("X-Source-Language: %v", err)}
		}
	}
	return nil
}

func (c *converter) locales() error {
	if !c.opts.Locale.IsZero() {
		c.target = c.opts.Locale
	}
	if !c.opts.SourceLocale.IsZero() {
		c.source = c.opts.SourceLocale
	}
	if c.source.IsZero() {
		c.source = bcp47.MustParse("en")
	}
	if c.target.IsZero() {
		return &formats.Error{Format: format, Err: formats.Invalidf("no target locale: the header has no Language and ReadOptions.Locale is empty")}
	}
	return nil
}

func (c *converter) entry(e *entry) (formats.Entry, error) {
	if e.id == "" {
		return formats.Entry{}, formats.Invalidf("empty msgid outside the header")
	}
	key := e.ctxt + "\x04" + e.id
	if c.seen[key] {
		return formats.Entry{}, formats.Invalidf("duplicate message (msgctxt, msgid)")
	}
	c.seen[key] = true
	out := formats.Entry{
		ID: e.id, Namespace: e.ctxt, Description: strings.Join(e.extracted, "\n"), References: e.refs,
		Pos: formats.Position{Line: e.line, Column: 1, Ref: e.ref()},
	}
	if len(e.comments) > 0 {
		out.Notes = []string{strings.Join(e.comments, "\n")}
	}
	var err error
	if out.Source, err = c.sourceContent(e); err != nil {
		return out, err
	}
	target, ok, err := c.targetContent(e)
	if err != nil || !ok {
		return out, err
	}
	state := c.translated
	if e.fuzzy {
		state = formats.StateNeedsReview
	}
	pos := out.Pos
	pos.Line = e.strLine
	out.Targets = []formats.Target{{Locale: c.target, Content: target, State: state, Pos: pos}}
	return out, nil
}

// ref names an entry the way gettext tools do: its msgctxt and msgid,
// as C string literals.
func (e *entry) ref() string {
	id := "msgid " + strconv.Quote(e.id)
	if !e.hasCtxt {
		return id
	}
	return "msgctxt " + strconv.Quote(e.ctxt) + " " + id
}

// sourceContent is the source message: literal text, or a plural select.
func (c *converter) sourceContent(e *entry) (mfcontent.Content, error) {
	if !e.hasPlural {
		return formats.PlainText(e.id)
	}
	return formats.FromModel(c.selectMessage([]variant{{"one", e.id}, {"*", e.plural}}))
}

type variant struct{ key, text string }

func (c *converter) selectMessage(vs []variant) mf.Message {
	decl := mf.Declaration{Type: mf.InputDeclaration, Name: c.pluralVar,
		Value: mf.Expression{Arg: mf.VariableRef{Name: c.pluralVar}, Function: &mf.FunctionRef{Name: "number"}}}
	m := mf.Message{Type: mf.SelectMessageType, Declarations: []mf.Declaration{decl}, Selectors: []mf.VariableRef{{Name: c.pluralVar}}}
	for _, v := range vs {
		key := mf.VariantKey{Value: v.key}
		if v.key == "*" {
			key = mf.VariantKey{Catchall: true}
		}
		m.Variants = append(m.Variants, mf.Variant{Keys: []mf.VariantKey{key}, Value: formats.TextPattern(v.text)})
	}
	return m
}

// targetContent is the translation, if the entry has a complete one.
func (c *converter) targetContent(e *entry) (mfcontent.Content, bool, error) {
	for _, s := range e.strs {
		if s == "" {
			return mfcontent.Content{}, false, nil
		}
	}
	if !e.hasPlural {
		content, err := formats.PlainText(e.strs[0])
		return content, err == nil, err
	}
	for i := range len(e.strs) {
		if _, ok := e.strs[i]; !ok {
			return mfcontent.Content{}, false, formats.Invalidf("msgstr[%d] is missing", i)
		}
	}
	forms, err := c.forms(len(e.strs))
	if err != nil {
		return mfcontent.Content{}, false, formats.Invalidf("%v", err)
	}
	content, err := formats.FromModel(c.selectMessage(targetVariants(forms, e.strs)))
	return content, err == nil, err
}

// forms maps n gettext forms onto CLDR categories, once per n.
func (c *converter) forms(n int) (formMap, error) {
	if m, ok := c.formMaps[n]; ok {
		return m, nil
	}
	m, err := mapForms(c.pluralForms, n, c.target)
	if err == nil {
		c.formMaps[n] = m
	}
	return m, err
}

// targetVariants lists a variant per CLDR category that takes a form,
// in CLDR order, then the catch-all.
func targetVariants(forms formMap, strs map[int]string) []variant {
	var out []variant
	for _, cat := range cldrOrder[:len(cldrOrder)-1] {
		if i, ok := forms.forms[cat]; ok {
			out = append(out, variant{cat, strs[i]})
		}
	}
	return append(out, variant{"*", strs[forms.catchAll]})
}
