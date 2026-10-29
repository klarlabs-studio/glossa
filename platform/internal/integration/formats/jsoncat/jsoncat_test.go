package jsoncat_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/jsoncat"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	en = bcp47.MustParse("en")
	de = bcp47.MustParse("de")
)

func parse(t *testing.T, syntax mfcontent.Syntax, text string, l bcp47.Tag) mfcontent.Content {
	t.Helper()
	c, err := formats.ParseContent(syntax, text, l)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sampleCatalog(t *testing.T) formats.Catalog {
	return formats.Catalog{SourceLocale: en, Entries: []formats.Entry{
		{ID: "checkout.title", Source: parse(t, mfcontent.MF1, "Checkout", en),
			Targets: []formats.Target{{Locale: de, Content: parse(t, mfcontent.MF1, "Kasse", de)}}},
		{ID: "checkout.items", Source: parse(t, mfcontent.MF1, "{count, plural, one {# item} other {# items}}", en),
			Targets: []formats.Target{{Locale: de, Content: parse(t, mfcontent.MF1, "{count, plural, one {# Artikel} other {# Artikel}}", de)}}},
		{ID: "checkout.greeting", Source: parse(t, mfcontent.MF2, "Hi {$name}, it's <b>{{}}</b> & more", en),
			Targets: []formats.Target{{Locale: de, Content: parse(t, mfcontent.MF2, "Hallo {$name}", de)}}},
		{ID: "app.name", Source: parse(t, mfcontent.MF1, "Glossa", en)},
	}}
}

func write(t *testing.T, cat formats.Catalog, opts jsoncat.WriteOptions) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jsoncat.Write(&buf, cat, opts); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func read(t *testing.T, doc []byte, opts jsoncat.ReadOptions) formats.Catalog {
	t.Helper()
	cat, err := jsoncat.Read(bytes.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	return cat
}

func TestWriteGolden(t *testing.T) {
	cat := sampleCatalog(t)
	formatstest.Golden(t, "testdata/golden/en.flat.json", write(t, cat, jsoncat.WriteOptions{}), *update)
	formatstest.Golden(t, "testdata/golden/en.nested.json", write(t, cat, jsoncat.WriteOptions{Layout: jsoncat.Nested}), *update)
	formatstest.Golden(t, "testdata/golden/de.mf2.json", write(t, cat, jsoncat.WriteOptions{Locale: de, Syntax: mfcontent.MF2}), *update)
}

// sortedByID orders entries as Write does.
func sortedByID(cat formats.Catalog) formats.Catalog {
	out := cat
	out.Entries = slices.Clone(cat.Entries)
	slices.SortFunc(out.Entries, func(a, b formats.Entry) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// onlyLocale keeps the content of one locale, as a JSON file does.
func onlyLocale(cat formats.Catalog, l bcp47.Tag, state formats.State) formats.Catalog {
	out := formats.Catalog{SourceLocale: cat.SourceLocale}
	for _, e := range cat.Entries {
		e := formats.Entry{ID: e.ID, Source: e.Source, Targets: e.Targets}
		if l != cat.SourceLocale {
			t, ok := e.Target(l)
			if !ok {
				continue
			}
			t.State = state
			e.Source, e.Targets = mfcontent.Content{}, []formats.Target{t}
		} else {
			e.Targets = nil
		}
		out.Entries = append(out.Entries, e)
	}
	return sortedByID(out)
}

func TestRoundTripSample(t *testing.T) {
	cat := sampleCatalog(t)
	for _, layout := range []jsoncat.Layout{jsoncat.Flat, jsoncat.Nested} {
		for _, syntax := range []mfcontent.Syntax{mfcontent.MF1, mfcontent.MF2} {
			src := read(t, write(t, cat, jsoncat.WriteOptions{Layout: layout, Syntax: syntax}), jsoncat.ReadOptions{Locale: en, Syntax: syntax})
			formatstest.SameCatalog(t, onlyLocale(cat, en, ""), src)
			doc := write(t, cat, jsoncat.WriteOptions{Locale: de, Layout: layout, Syntax: syntax})
			tgt := read(t, doc, jsoncat.ReadOptions{Locale: de, SourceLocale: en, Syntax: syntax, State: formats.StateApproved})
			formatstest.SameCatalog(t, onlyLocale(cat, de, formats.StateApproved), tgt)
		}
	}
}

func TestMF1TextIsVerbatimAndDerivedWhenSafe(t *testing.T) {
	doc := string(write(t, sampleCatalog(t), jsoncat.WriteOptions{}))
	for _, want := range []string{
		`"checkout.items": "{count, plural, one {# item} other {# items}}"`,
		`"checkout.greeting": "Hi {name}, it''s <b>'{}'</b> & more"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %s in\n%s", want, doc)
		}
	}
	complexMF2 := formats.Catalog{SourceLocale: en, Entries: []formats.Entry{
		{ID: "a", Source: parse(t, mfcontent.MF2, ".input {$n :number} .match $n one {{a}} * {{b}}", en)},
	}}
	if err := jsoncat.Write(&bytes.Buffer{}, complexMF2, jsoncat.WriteOptions{}); !errors.Is(err, formats.ErrUnsupported) {
		t.Errorf("complex MF2 as MF1 err = %v", err)
	}
	fn := formats.Catalog{SourceLocale: en, Entries: []formats.Entry{{ID: "a", Source: parse(t, mfcontent.MF2, "{$n :number}", en)}}}
	if err := jsoncat.Write(&bytes.Buffer{}, fn, jsoncat.WriteOptions{}); !errors.Is(err, formats.ErrUnsupported) {
		t.Errorf("function MF2 as MF1 err = %v", err)
	}
}

// TestRoundTripProperty: random MF2 messages round-trip as MF2 in both
// layouts, and literal text with plain variables round-trips as MF1.
func TestRoundTripProperty(t *testing.T) {
	for seed := range uint64(300) {
		g := formatstest.New(seed)
		g.Control = true
		cat := formats.Catalog{SourceLocale: en}
		mf1 := formats.Catalog{SourceLocale: en}
		for i := range g.R.IntN(8) {
			key := g.Key(i)
			cat.Entries = append(cat.Entries, formats.Entry{ID: key, Source: g.Content(t)})
			plain, err := formats.FromModel(formats.PatternMessage(mf.Pattern{
				mf.Text(g.Text()), mf.Expression{Arg: mf.VariableRef{Name: g.Word()}}, mf.Text(g.Text()),
			}))
			if err != nil {
				t.Fatal(err)
			}
			mf1.Entries = append(mf1.Entries, formats.Entry{ID: key, Source: plain})
		}
		layout := formatstest.Pick(g, jsoncat.Flat, jsoncat.Nested)
		doc := write(t, cat, jsoncat.WriteOptions{Layout: layout, Syntax: mfcontent.MF2})
		formatstest.SameCatalog(t, sortedByID(cat), read(t, doc, jsoncat.ReadOptions{Locale: en, Syntax: mfcontent.MF2}))
		doc1 := write(t, mf1, jsoncat.WriteOptions{Layout: layout})
		formatstest.SameCatalog(t, sortedByID(mf1), read(t, doc1, jsoncat.ReadOptions{Locale: en}))
		if t.Failed() {
			t.Fatalf("seed %d:\n%s\n%s", seed, doc, doc1)
		}
	}
}

func TestReadMixedLayout(t *testing.T) {
	doc := `{"a.b": "1", "c": {"d": {"e": "2"}, "f.g": "3"}, "h": ""}`
	cat := read(t, []byte(doc), jsoncat.ReadOptions{Locale: en, Namespace: "web"})
	var keys []string
	for _, e := range cat.Entries {
		keys = append(keys, e.ID)
		if e.Namespace != "web" {
			t.Errorf("%s namespace = %q", e.ID, e.Namespace)
		}
	}
	if fmt.Sprint(keys) != "[a.b c.d.e c.f.g h]" {
		t.Errorf("keys = %v", keys)
	}
}

func TestReadErrors(t *testing.T) {
	tests := map[string]struct {
		doc       string
		want      error
		line, col int
	}{
		"not an object":  {`["a"]`, formats.ErrInvalid, 1, 1},
		"number":         {"{\n  \"a\": 1\n}", formats.ErrInvalid, 2, 8},
		"array":          {"{\"a\": {\"b\": [\"x\"]}}", formats.ErrInvalid, 1, 13},
		"duplicate":      {"{\"a\": \"x\",\n \"a\": \"y\"}", formats.ErrInvalid, 2, 2},
		"flat vs nested": {`{"a.b": "x", "a": {"b": "y"}}`, formats.ErrInvalid, 1, 25},
		"empty key":      {`{"": "x"}`, formats.ErrInvalid, 1, 2},
		"bad mf1":        {"{\n\"a\": \"{oops\"}", formats.ErrInvalid, 2, 6},
		"syntax":         {`{"a": "x",}`, formats.ErrInvalid, 1, 0},
		"trailing":       {`{"a": "x"} {}`, formats.ErrInvalid, 1, 12},
		"too deep":       {strings.Repeat(`{"a":`, formats.MaxDepth+2) + `"x"` + strings.Repeat("}", formats.MaxDepth+2), formats.ErrInvalid, 0, 0},
		"too many":       {`{"a": "1", "b": "2", "c": "3"}`, formats.ErrTooLarge, 1, 27},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := jsoncat.Read(strings.NewReader(tt.doc), jsoncat.ReadOptions{Locale: en, Limits: formats.Limits{MaxItems: 2}})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			var fe *formats.Error
			if !errors.As(err, &fe) || fe.Format != "json" {
				t.Fatalf("err = %#v", err)
			}
			if tt.line > 0 && (fe.Line != tt.line || tt.col > 0 && fe.Column != tt.col) {
				t.Errorf("at %d:%d, want %d:%d (%v)", fe.Line, fe.Column, tt.line, tt.col, err)
			}
		})
	}
	if _, err := jsoncat.Read(strings.NewReader("{}"), jsoncat.ReadOptions{}); !errors.Is(err, formats.ErrInvalid) {
		t.Errorf("missing locale err = %v", err)
	}
	if _, err := jsoncat.Read(strings.NewReader(`{"a": "`+strings.Repeat("x", 100)+`"}`), jsoncat.ReadOptions{Locale: en, Limits: formats.Limits{MaxBytes: 50}}); !errors.Is(err, formats.ErrTooLarge) {
		t.Errorf("MaxBytes err = %v", err)
	}
}

func TestWriteNestedConflicts(t *testing.T) {
	for name, keys := range map[string][]string{
		"prefix":        {"a", "a.b"},
		"empty segment": {"a..b"},
	} {
		cat := formats.Catalog{SourceLocale: en}
		for _, k := range keys {
			cat.Entries = append(cat.Entries, formats.Entry{ID: k, Source: parse(t, mfcontent.MF1, "x", en)})
		}
		if err := jsoncat.Write(&bytes.Buffer{}, cat, jsoncat.WriteOptions{Layout: jsoncat.Nested}); !errors.Is(err, formats.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
		if err := jsoncat.Write(&bytes.Buffer{}, cat, jsoncat.WriteOptions{}); err != nil {
			t.Errorf("%s: flat err = %v", name, err)
		}
	}
	if got := string(write(t, formats.Catalog{SourceLocale: en}, jsoncat.WriteOptions{})); got != "{}\n" {
		t.Errorf("empty catalog = %q", got)
	}
}
