package tbx_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tbx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	en = bcp47.MustParse("en")
	de = bcp47.MustParse("de")
	fr = bcp47.MustParse("fr")
)

func sampleTermbase() formats.Termbase {
	return formats.Termbase{Language: en, Concepts: []formats.Concept{
		{ID: "0b6c2f1e-9d3a-4f0e-8a1b-2c3d4e5f6a7b", Domain: "Commerce",
			Definitions: []formats.Definition{
				{Locale: en, Text: "The final step of buying the items in a cart."},
				{Locale: fr, Text: "Dernière étape de l'achat."},
				{Text: "Language-neutral note on scope."},
			},
			Notes: []string{"Used across web and mobile."},
			Terms: []formats.Term{
				{Locale: en, Text: "checkout", Status: formats.TermPreferred, PartOfSpeech: "noun", Context: "Proceed to checkout.", Notes: []string{"One word."}},
				{Locale: en, Text: "check-out", Status: formats.TermForbidden, PartOfSpeech: "noun"},
				{Locale: de, Text: "Kasse", Status: formats.TermPreferred, PartOfSpeech: "noun"},
				{Locale: de, Text: "Checkout", Status: formats.TermDeprecated},
				{Locale: de, Text: "Bezahlvorgang", Status: formats.TermAdmitted},
			}},
		{ID: "brand", Terms: []formats.Term{{Locale: en, Text: "Glossa & Co <TM>", Status: formats.TermPreferred, PartOfSpeech: "other"}}},
	}}
}

func write(t *testing.T, tb formats.Termbase) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := tbx.Write(&buf, tb); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func read(t *testing.T, doc []byte) formats.Termbase {
	t.Helper()
	tb, err := tbx.Read(bytes.NewReader(doc), tbx.ReadOptions{})
	if err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	return tb
}

// sameTermbase compares termbases; definitions compare as a set, since
// Write places each by whether its locale has terms.
func sameTermbase(t *testing.T, want, got formats.Termbase) {
	t.Helper()
	if want.Language != got.Language || len(want.Concepts) != len(got.Concepts) {
		t.Fatalf("termbase %s with %d concepts, want %s with %d", got.Language, len(got.Concepts), want.Language, len(want.Concepts))
	}
	for i := range want.Concepts {
		w, g := want.Concepts[i], got.Concepts[i]
		if w.ID != g.ID || w.Domain != g.Domain || fmt.Sprint(w.Notes) != fmt.Sprint(g.Notes) ||
			fmt.Sprint(defs(w)) != fmt.Sprint(defs(g)) || fmt.Sprint(w.Terms) != fmt.Sprint(g.Terms) {
			t.Errorf("concept %d =\n%+v\nwant\n%+v", i, g, w)
		}
	}
}

func defs(c formats.Concept) []string {
	var out []string
	for _, d := range c.Definitions {
		out = append(out, d.Locale.String()+"|"+d.Text)
	}
	slices.Sort(out)
	return out
}

func TestWriteGolden(t *testing.T) {
	formatstest.Golden(t, "testdata/golden/termbase.tbx", write(t, sampleTermbase()), *update)
}

func TestRoundTripSample(t *testing.T) {
	want := sampleTermbase()
	sameTermbase(t, want, read(t, write(t, want)))
}

func TestRoundTripProperty(t *testing.T) {
	statuses := []formats.TermStatus{formats.TermPreferred, formats.TermAdmitted, formats.TermDeprecated, formats.TermForbidden}
	for seed := range uint64(300) {
		g := formatstest.New(seed)
		tb := formats.Termbase{Language: formatstest.Pick(g, en, de)}
		for i := range g.R.IntN(4) {
			c := formats.Concept{ID: formatstest.Pick(g, fmt.Sprint(i), fmt.Sprint("c", i), fmt.Sprint("c-", i), fmt.Sprint("c-x", i)), Domain: g.Text()}
			if g.R.IntN(2) == 0 {
				c.Definitions = []formats.Definition{{Locale: formatstest.Pick(g, bcp47.Tag{}, en, fr), Text: g.Text()}}
				c.Notes = []string{g.Text()}
			}
			for range 1 + g.R.IntN(3) {
				c.Terms = append(c.Terms, formats.Term{
					Locale: formatstest.Pick(g, en, de), Text: "t" + strings.TrimSpace(g.Text()) + "t",
					Status: formatstest.Pick(g, statuses...), PartOfSpeech: formatstest.Pick(g, "", "noun", "verb"),
					Context: g.Text(),
				})
			}
			tb.Concepts = append(tb.Concepts, c)
		}
		doc := write(t, tb)
		sameTermbase(t, sortTerms(tb), read(t, doc))
		if t.Failed() {
			t.Fatalf("seed %d:\n%s", seed, doc)
		}
	}
}

// sortTerms orders each concept's terms by locale of first appearance,
// as Write groups them into langSecs.
func sortTerms(tb formats.Termbase) formats.Termbase {
	for i, c := range tb.Concepts {
		var order []bcp47.Tag
		for _, t := range c.Terms {
			if !slices.Contains(order, t.Locale) {
				order = append(order, t.Locale)
			}
		}
		var terms []formats.Term
		for _, loc := range order {
			for _, t := range c.Terms {
				if t.Locale == loc {
					terms = append(terms, t)
				}
			}
		}
		tb.Concepts[i].Terms = terms
	}
	return tb
}

func TestReadDialects(t *testing.T) {
	dca := `<?xml version="1.0" encoding="utf-8"?>
<?xml-model href="TBXcoreStructV03.rng" type="application/xml"?>
<tbx type="TBX-Basic" style="dca" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
 <tbxHeader><fileDesc><sourceDesc><p>x</p></sourceDesc></fileDesc></tbxHeader>
 <text><body>
  <conceptEntry id="c1">
   <transacGrp><transac type="transactionType">origination</transac><date>2010-04-17</date></transacGrp>
   <descrip type="subjectField">Astronomy</descrip>
   <note>G-Source</note>
   <langSec xml:lang="en">
    <descripGrp><descrip type="definition">A group of stars.</descrip><admin type="source">Oxford</admin></descripGrp>
    <termSec><term>open cluster</term><termNote type="partOfSpeech">noun</termNote>
     <termNote type="administrativeStatus">preferredTerm-admn-sts</termNote>
     <descripGrp><descrip type="context">Over 1100 open clusters are known.</descrip></descripGrp>
     <note>N-Source</note></termSec>
    <termSec><term> galactic cluster </term><termNote type="administrativeStatus">supersededTerm-admn-sts</termNote></termSec>
   </langSec>
  </conceptEntry>
 </body></text>
</tbx>`
	martif := `<?xml version="1.0"?>
<!DOCTYPE martif SYSTEM "TBXcoreStructV02.dtd">
<martif type="TBX-Basic" xml:lang="en-US">
 <martifHeader><fileDesc><sourceDesc><p>x</p></sourceDesc></fileDesc></martifHeader>
 <text><body>
  <termEntry id="c1">
   <descrip type="subjectField">Astronomy</descrip>
   <note>G-Source</note>
   <langSet xml:lang="en">
    <descripGrp><descrip type="definition">A group of stars.</descrip></descripGrp>
    <tig><term>open cluster</term><termNote type="partOfSpeech">noun</termNote>
     <termNote type="administrativeStatus">preferredTerm-admn-sts</termNote>
     <descrip type="context">Over 1100 open clusters are known.</descrip><note>N-Source</note></tig>
    <ntig><termGrp><term>galactic cluster</term><termNote type="normativeAuthorization">supersededTerm</termNote></termGrp></ntig>
   </langSet>
  </termEntry>
 </body></text>
</martif>`
	dct := `<tbx type="TBX-Basic" style="dct" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2"
  xmlns:min="http://www.tbxinfo.net/ns/min" xmlns:basic="http://www.tbxinfo.net/ns/basic">
 <tbxHeader><fileDesc><sourceDesc><p>x</p></sourceDesc></fileDesc></tbxHeader>
 <text><body><conceptEntry id="c1"><min:subjectField>Astronomy</min:subjectField><note>G-Source</note>
  <langSec xml:lang="en"><basic:definition>A group of stars.</basic:definition>
   <termSec><term>open cluster</term><min:partOfSpeech>noun</min:partOfSpeech>
    <min:administrativeStatus>preferredTerm-admn-sts</min:administrativeStatus>
    <basic:context>Over 1100 open clusters are known.</basic:context><note>N-Source</note></termSec>
   <termSec><term>galactic cluster</term><min:administrativeStatus>supersededTerm-admn-sts</min:administrativeStatus></termSec>
  </langSec></conceptEntry></body></text></tbx>`
	want := formats.Concept{ID: "c1", Domain: "Astronomy", Notes: []string{"G-Source"},
		Definitions: []formats.Definition{{Locale: en, Text: "A group of stars."}},
		Terms: []formats.Term{
			{Locale: en, Text: "open cluster", Status: formats.TermPreferred, PartOfSpeech: "noun", Context: "Over 1100 open clusters are known.", Notes: []string{"N-Source"}},
			{Locale: en, Text: "galactic cluster", Status: formats.TermForbidden},
		}}
	for name, doc := range map[string]string{"dca": dca, "martif": martif, "dct": dct} {
		t.Run(name, func(t *testing.T) {
			tb := read(t, []byte(doc))
			lang := en
			if name == "martif" {
				lang = bcp47.MustParse("en-US")
			}
			sameTermbase(t, formats.Termbase{Language: lang, Concepts: []formats.Concept{want}}, tb)
		})
	}
}

func TestReadErrors(t *testing.T) {
	head := `<tbx xmlns="urn:iso:std:iso:30042:ed-2" type="TBX-Basic" style="dca" xml:lang="en"><text><body>`
	tail := `</body></text></tbx>`
	tests := map[string]struct {
		doc  string
		want error
		line int
	}{
		"not tbx":      {`<tmx/>`, formats.ErrInvalid, 0},
		"bad status":   {head + "\n<conceptEntry id=\"a\"><langSec xml:lang=\"en\"><termSec><term>x</term><termNote type=\"administrativeStatus\">maybe</termNote></termSec></langSec></conceptEntry>" + tail, formats.ErrInvalid, 2},
		"bad lang":     {head + `<conceptEntry id="a"><langSec xml:lang="??"><termSec><term>x</term></termSec></langSec></conceptEntry>` + tail, formats.ErrInvalid, 1},
		"empty term":   {head + `<conceptEntry id="a"><langSec xml:lang="en"><termSec><term> </term></termSec></langSec></conceptEntry>` + tail, formats.ErrInvalid, 1},
		"truncated":    {head + `<conceptEntry id="a">`, formats.ErrInvalid, 0},
		"xxe":          {`<!DOCTYPE tbx [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + head + tail, formats.ErrUnsupported, 0},
		"too many":     {head + strings.Repeat(`<conceptEntry id="a"><langSec xml:lang="en"><termSec><term>x</term></termSec></langSec></conceptEntry>`, 3) + tail, formats.ErrTooLarge, 0},
		"bad def lang": {head + `<conceptEntry id="a"><descrip type="definition" xml:lang="!">d</descrip><langSec xml:lang="en"><termSec><term>x</term></termSec></langSec></conceptEntry>` + tail, formats.ErrInvalid, 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := tbx.Read(strings.NewReader(tt.doc), tbx.ReadOptions{Limits: formats.Limits{MaxItems: 2}})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			var fe *formats.Error
			if !errors.As(err, &fe) || fe.Format != "tbx" {
				t.Fatalf("err = %#v", err)
			}
			if tt.line > 0 && fe.Line != tt.line {
				t.Errorf("line = %d, want %d (%v)", fe.Line, tt.line, err)
			}
		})
	}
}

func TestWriteRefusesInvalidTermbases(t *testing.T) {
	for name, c := range map[string]formats.Concept{
		"no terms":   {ID: "a"},
		"no locale":  {Terms: []formats.Term{{Text: "x"}}},
		"no text":    {Terms: []formats.Term{{Locale: en}}},
		"bad status": {Terms: []formats.Term{{Locale: en, Text: "x", Status: "maybe"}}},
	} {
		if err := tbx.Write(&bytes.Buffer{}, formats.Termbase{Concepts: []formats.Concept{c}}); !errors.Is(err, formats.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWriteNormalizesPartOfSpeechAndStatus(t *testing.T) {
	doc := write(t, formats.Termbase{Concepts: []formats.Concept{{Terms: []formats.Term{{Locale: en, Text: "Glossa", PartOfSpeech: "properNoun"}}}}})
	for _, want := range []string{`<termNote type="partOfSpeech">other</termNote>`, `admittedTerm-admn-sts`, `<conceptEntry id="c1">`, `xml:lang="en"`} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Errorf("missing %s in\n%s", want, doc)
		}
	}
}
