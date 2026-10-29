package xliff_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	en = bcp47.MustParse("en")
	de = bcp47.MustParse("de")
)

func content(t *testing.T, syntax mfcontent.Syntax, text string, locale bcp47.Tag) mfcontent.Content {
	t.Helper()
	c, err := formats.ParseContent(syntax, text, locale)
	if err != nil {
		t.Fatalf("%s %q: %v", syntax, text, err)
	}
	return c
}

// sampleCatalog covers every mapping rule once.
func sampleCatalog(t *testing.T) formats.Catalog {
	mf1 := func(s string, l bcp47.Tag) mfcontent.Content { return content(t, mfcontent.MF1, s, l) }
	mf2 := func(s string) mfcontent.Content { return content(t, mfcontent.MF2, s, en) }
	control, err := formats.PlainText("Tab\there, bell \x07 and {braces}")
	if err != nil {
		t.Fatal(err)
	}
	return formats.Catalog{SourceLocale: en, Entries: []formats.Entry{
		{ID: "checkout.title", Namespace: "checkout", Description: "Page title", MaxLength: 20,
			Source:  mf1("Checkout", en),
			Targets: []formats.Target{{Locale: de, Content: mf1("Kasse", de), State: formats.StateApproved}}},
		{ID: "checkout.greeting", Namespace: "checkout",
			Source: mf2("Hello {#b}{$name}{/b}, {$count :number} items{#br/} {#i}open"),
			Targets: []formats.Target{{Locale: de, State: formats.StateNeedsReview,
				Content: mf2("{$count :number} Artikel{#br/} für {#b}{$name}{/b} {#i}offen")}}},
		{ID: "checkout.items", Namespace: "checkout", References: []string{"src/cart.ts:12"},
			Source: mf1("{count, plural, one {# item} other {# items}}", en),
			Targets: []formats.Target{{Locale: de, State: formats.StateDraft,
				Content: mf2(".input {$count :number} .match $count one {{{$count} Artikel}} * {{{$count} Artikel}}")}}},
		{ID: "app.name", Notes: []string{"Brand name, do not translate."}, Source: mf1("Glossa", en)},
		{ID: "legacy key/with space", Namespace: "checkout", Source: control,
			Targets: []formats.Target{{Locale: de, Content: control, State: formats.StateRejected}}},
	}}
}

func write(t *testing.T, cat formats.Catalog, target bcp47.Tag) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := xliff.Write(&buf, cat, xliff.WriteOptions{TargetLocale: target}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func read(t *testing.T, doc []byte, opts xliff.ReadOptions) formats.Catalog {
	t.Helper()
	cat, err := xliff.Read(bytes.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	return cat
}

func TestWriteGolden(t *testing.T) {
	doc := write(t, sampleCatalog(t), de)
	formatstest.Golden(t, "testdata/golden/catalog.de.xlf", doc, *update)
	validateXSD(t, doc)
}

func TestRoundTripSample(t *testing.T) {
	want := sampleCatalog(t)
	got := read(t, write(t, want, de), xliff.ReadOptions{})
	// The default namespace comes first: files are written in order of
	// first appearance, and app.name is the only default-namespace entry.
	want.Entries = []formats.Entry{want.Entries[0], want.Entries[1], want.Entries[2], want.Entries[4], want.Entries[3]}
	formatstest.SameCatalog(t, want, got)
}

func TestRoundTripSourceOnly(t *testing.T) {
	cat := sampleCatalog(t)
	doc := write(t, cat, bcp47.Tag{})
	if bytes.Contains(doc, []byte("<target")) || bytes.Contains(doc, []byte("trgLang")) {
		t.Errorf("source-only document has targets:\n%s", doc)
	}
	for _, e := range read(t, doc, xliff.ReadOptions{}).Entries {
		if len(e.Targets) != 0 {
			t.Errorf("%q has targets", e.ID)
		}
	}
	validateXSD(t, doc)
}

// TestRoundTripProperty writes and reads random catalogs: every message,
// including control characters and complex messages, comes back as the
// same canonical model with the same metadata and state.
func TestRoundTripProperty(t *testing.T) {
	iterations := 300
	if testing.Short() {
		iterations = 30
	}
	for seed := range uint64(iterations) {
		g := formatstest.New(seed)
		g.Control = true
		cat := randomCatalog(t, g)
		doc := write(t, cat, de)
		got := read(t, doc, xliff.ReadOptions{})
		formatstest.SameCatalog(t, byNamespace(cat), got)
		if seed%50 == 0 {
			validateXSD(t, doc)
		}
		if t.Failed() {
			t.Fatalf("seed %d:\n%s", seed, doc)
		}
	}
}

func randomCatalog(t *testing.T, g *formatstest.Gen) formats.Catalog {
	cat := formats.Catalog{SourceLocale: en}
	for i := range 1 + g.R.IntN(6) {
		e := formats.Entry{
			ID: g.Key(i), Namespace: formatstest.Pick(g, "", "checkout", "auth"),
			Description: g.Text(), MaxLength: g.R.IntN(3) * 10, Source: g.Content(t),
		}
		if g.R.IntN(3) == 0 {
			e.ID = "key with space " + e.ID
		}
		if g.R.IntN(2) == 0 {
			e.Notes = []string{g.Text() + "n"}
			e.References = []string{"src/" + g.Word() + ".ts:1"}
		}
		if g.R.IntN(4) != 0 {
			e.Targets = []formats.Target{{Locale: de, Content: g.Content(t), State: formatstest.Pick(g, formatstest.States...)}}
		}
		cat.Entries = append(cat.Entries, e)
	}
	stripControl(&cat)
	return cat
}

// stripControl removes characters XML can't carry from notes and
// descriptions: only message text has <cp/>.
func stripControl(cat *formats.Catalog) {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xFFFE {
				return -1
			}
			return r
		}, s)
	}
	for i := range cat.Entries {
		e := &cat.Entries[i]
		e.Description = clean(e.Description)
		for j := range e.Notes {
			e.Notes[j] = clean(e.Notes[j])
		}
	}
}

// byNamespace reorders entries the way Write groups them.
func byNamespace(cat formats.Catalog) formats.Catalog {
	var order []string
	groups := map[string][]formats.Entry{}
	for _, e := range cat.Entries {
		if _, ok := groups[e.Namespace]; !ok {
			order = append(order, e.Namespace)
		}
		groups[e.Namespace] = append(groups[e.Namespace], e)
	}
	out := formats.Catalog{SourceLocale: cat.SourceLocale}
	for _, ns := range order {
		out.Entries = append(out.Entries, groups[ns]...)
	}
	return out
}

// validateXSD validates doc against the vendored XLIFF 2.1 core schema
// with xmllint, when it is installed (it is on CI's Ubuntu runners and
// on macOS).
func validateXSD(t *testing.T, doc []byte) {
	t.Helper()
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Log("xmllint not installed; structural checks only")
		return
	}
	path := filepath.Join(t.TempDir(), "doc.xlf")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(xmllint, "--noout", "--nonet", "--schema", "testdata/schema/xliff_core_2.0.xsd", path).CombinedOutput()
	if err != nil {
		t.Errorf("XSD validation failed: %v\n%s\n%s", err, out, doc)
	}
}

func TestReadForeignDocument(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" xmlns:mda="urn:oasis:names:tc:xliff:metadata:2.0" version="2.0" srcLang="en-us" trgLang="DE_de">
 <file id="f1" original="shop">
  <group id="g1">
   <unit id="u1">
    <mda:metadata><mda:metaGroup><mda:meta type="x">y</mda:meta></mda:metaGroup></mda:metadata>
    <notes><note category="description">First.</note><note category="description">Second.</note></notes>
    <segment state="reviewed"><source>Hello </source><target>Hallo </target></segment>
    <ignorable><source> </source></ignorable>
    <segment state="final"><source><mrk id="m1" type="term">world</mrk>!</source><target><mrk id="m1" type="term">Welt</mrk>!</target></segment>
   </unit>
   <unit id="u2"><segment><source>Untranslated</source><target/></segment></unit>
   <unit id="u3"><segment state="translated"><source>A</source><target>B</target></segment><segment><source>C</source></segment></unit>
   <unit id="u4"><segment state="translated"><source>one {x}</source><target>eins <cp hex="0001"/></target></segment></unit>
  </group>
 </file>
</xliff>`
	cat := read(t, []byte(doc), xliff.ReadOptions{})
	if cat.SourceLocale.String() != "en-US" || len(cat.Entries) != 4 {
		t.Fatalf("catalog = %s, %d entries", cat.SourceLocale, len(cat.Entries))
	}
	u1 := cat.Entries[0]
	if u1.Namespace != "shop" || u1.Description != "First.\n\nSecond." || u1.Source.Text != "Hello  world!" {
		t.Errorf("u1 = %q %q %q", u1.Namespace, u1.Description, u1.Source.Text)
	}
	if tgt, ok := u1.Target(bcp47.MustParse("de-DE")); !ok || tgt.Content.Text != "Hallo  Welt!" || tgt.State != formats.StateApproved {
		t.Errorf("u1 target = %+v, %v", tgt, ok)
	}
	if len(cat.Entries[1].Targets) != 0 {
		t.Error("an empty initial target was read as a translation")
	}
	if len(cat.Entries[2].Targets) != 0 {
		t.Error("a partly translated unit was read as translated")
	}
	if cat.Entries[3].Source.Text != `one \{x\}` || cat.Entries[3].Targets[0].Content.Model.Pattern[0] != mfText("eins \x01") {
		t.Errorf("u4 = %q / %#v", cat.Entries[3].Source.Text, cat.Entries[3].Targets[0].Content.Model.Pattern)
	}
}

func TestReadPlainSyntaxMF1(t *testing.T) {
	doc := `<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de">
<file id="f"><unit id="n"><segment state="final"><source>{n, plural, one {# file} other {# files}}</source>
<target>{n, plural, one {# Datei} other {# Dateien}}</target></segment></unit></file></xliff>`
	cat := read(t, []byte(doc), xliff.ReadOptions{PlainSyntax: mfcontent.MF1})
	e := cat.Entries[0]
	if !e.Source.Model.IsSelect() || e.Source.Syntax != mfcontent.MF1 || !e.Targets[0].Content.Model.IsSelect() {
		t.Errorf("MF1 plain units not parsed: %+v", e)
	}
	lit := read(t, []byte(doc), xliff.ReadOptions{})
	if lit.Entries[0].Source.Model.IsSelect() {
		t.Error("plain units parsed as MF1 without PlainSyntax")
	}
}

func TestReadErrors(t *testing.T) {
	head := `<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de"><file id="f">`
	tail := `</file></xliff>`
	tests := map[string]struct {
		doc  string
		want error
		line int
	}{
		"xliff 1.2":         {`<xliff xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2"><file/></xliff>`, formats.ErrUnsupported, 0},
		"not xliff":         {`<html/>`, formats.ErrInvalid, 0},
		"bad srcLang":       {`<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="*"/>`, formats.ErrInvalid, 0},
		"ph without data":   {head + "\n<unit id=\"a\"><segment><source>x <ph id=\"1\"/></source></segment></unit>" + tail, formats.ErrUnsupported, 2},
		"bad data":          {head + `<unit id="a"><originalData><data id="d1">{oops</data></originalData><segment><source><ph id="1" dataRef="d1"/></source></segment></unit>` + tail, formats.ErrInvalid, 1},
		"bad mf2":           {head + `<unit id="a" type="glossa:mf2"><segment><source>.match {</source></segment></unit>` + tail, formats.ErrInvalid, 1},
		"code in mf2":       {head + `<unit id="a" type="glossa:mf2"><originalData><data id="d1">{$x}</data></originalData><segment><source><ph id="1" dataRef="d1"/></source></segment></unit>` + tail, formats.ErrInvalid, 1},
		"bad state":         {head + `<unit id="a"><segment state="done"><source>a</source><target>b</target></segment></unit>` + tail, formats.ErrInvalid, 1},
		"no segments":       {head + `<unit id="a"/>` + tail, formats.ErrInvalid, 1},
		"bad cp":            {head + `<unit id="a"><segment><source><cp hex="D800"/></source></segment></unit>` + tail, formats.ErrInvalid, 1},
		"truncated":         {head + `<unit id="a">`, formats.ErrInvalid, 0},
		"xxe":               {`<!DOCTYPE xliff [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + head + `<unit id="a"><segment><source>&x;</source></segment></unit>` + tail, formats.ErrUnsupported, 0},
		"target no trgLang": {`<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en"><file id="f"><unit id="a"><segment state="final"><source>a</source><target>b</target></segment></unit></file></xliff>`, formats.ErrInvalid, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := xliff.Read(strings.NewReader(tt.doc), xliff.ReadOptions{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			var fe *formats.Error
			if !errors.As(err, &fe) || fe.Format != "xliff" {
				t.Fatalf("err = %#v, want *formats.Error", err)
			}
			if tt.line > 0 && fe.Line != tt.line {
				t.Errorf("line = %d, want %d (%v)", fe.Line, tt.line, err)
			}
		})
	}
}

func TestReadLimits(t *testing.T) {
	doc := write(t, sampleCatalog(t), de)
	if _, err := xliff.Read(bytes.NewReader(doc), xliff.ReadOptions{Limits: formats.Limits{MaxItems: 2}}); !errors.Is(err, formats.ErrTooLarge) {
		t.Errorf("MaxItems err = %v", err)
	}
	if _, err := xliff.Read(bytes.NewReader(doc), xliff.ReadOptions{Limits: formats.Limits{MaxBytes: 100}}); !errors.Is(err, formats.ErrTooLarge) {
		t.Errorf("MaxBytes err = %v", err)
	}
}

func TestWriteRefusesUnusableCatalogs(t *testing.T) {
	for name, cat := range map[string]formats.Catalog{
		"no source locale": {Entries: sampleCatalog(t).Entries},
		"no entries":       {SourceLocale: en},
		"no source":        {SourceLocale: en, Entries: []formats.Entry{{ID: "a"}}},
	} {
		if err := xliff.Write(&bytes.Buffer{}, cat, xliff.WriteOptions{}); !errors.Is(err, formats.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func mfText(s string) mf.PatternElement { return mf.Text(s) }
