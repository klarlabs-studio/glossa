package tmx_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/unicode"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tmx"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	en = bcp47.MustParse("en")
	de = bcp47.MustParse("de")
	fr = bcp47.MustParse("fr")
)

func mf2(t *testing.T, s string) mfcontent.Content {
	t.Helper()
	c, err := formats.ParseContent(mfcontent.MF2, s, en)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sampleUnits(t *testing.T) []formats.TMUnit {
	created := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)
	return []formats.TMUnit{
		{ID: "tm-1", SourceLocale: en, TargetLocale: de, CreatedAt: created, ChangedAt: created.Add(time.Hour),
			LastUsedAt: created.Add(48 * time.Hour), UsageCount: 7,
			Props:  []formats.Prop{{Type: "x-project", Value: "shop"}, {Type: "x-origin", Value: "revision:42"}},
			Notes:  []string{"approved by Anna"},
			Source: mf2(t, "Hello {#b}{$name}{/b}, you have {$count :number} items{#br/} {#i}"),
			Target: mf2(t, "Hallo {#b}{$name}{/b}, Sie haben {$count :number} Artikel{#br/} {#i}")},
		{ID: "tm-2", SourceLocale: en, TargetLocale: de,
			Source: mf2(t, ".input {$n :number} .match $n one {{{$n} file}} * {{{$n} files}}"),
			Target: mf2(t, ".input {$n :number} .match $n one {{{$n} Datei}} * {{{$n} Dateien}}")},
		{ID: "tm-3", SourceLocale: en, TargetLocale: fr,
			Source: mf2(t, `Escapes: & < > " \{braces\}`), Target: mf2(t, `Échappements : & < > " \{accolades\}`)},
	}
}

func write(t *testing.T, units []formats.TMUnit) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := tmx.Write(&buf, units, tmx.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readAll(t *testing.T, doc []byte) []formats.TMUnit {
	t.Helper()
	units, err := tmx.ReadAll(bytes.NewReader(doc), tmx.ReadOptions{})
	if err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	return units
}

func sameUnits(t *testing.T, want, got []formats.TMUnit) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%d units, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.ID != g.ID || w.SourceLocale != g.SourceLocale || w.TargetLocale != g.TargetLocale ||
			!w.CreatedAt.Equal(g.CreatedAt) || !w.ChangedAt.Equal(g.ChangedAt) || !w.LastUsedAt.Equal(g.LastUsedAt) ||
			w.UsageCount != g.UsageCount || fmt.Sprint(w.Props) != fmt.Sprint(g.Props) || fmt.Sprint(w.Notes) != fmt.Sprint(g.Notes) {
			t.Errorf("unit %d = %+v\nwant %+v", i, g, w)
		}
		formatstest.SameContent(t, fmt.Sprintf("unit %d source", i), w.Source, g.Source)
		formatstest.SameContent(t, fmt.Sprintf("unit %d target", i), w.Target, g.Target)
	}
}

func TestWriteGolden(t *testing.T) {
	formatstest.Golden(t, "testdata/golden/memory.tmx", write(t, sampleUnits(t)), *update)
}

func TestRoundTripSample(t *testing.T) {
	want := sampleUnits(t)
	sameUnits(t, want, readAll(t, write(t, want)))
}

func TestRoundTripProperty(t *testing.T) {
	iterations := 300
	if testing.Short() {
		iterations = 30
	}
	for seed := range uint64(iterations) {
		g := formatstest.New(seed)
		var units []formats.TMUnit
		for i := range 1 + g.R.IntN(5) {
			units = append(units, formats.TMUnit{
				ID: fmt.Sprint("u", i), SourceLocale: en, TargetLocale: formatstest.Pick(g, de, fr),
				Source: g.Content(t), Target: g.Content(t), UsageCount: g.R.IntN(3),
				Props: []formats.Prop{{Type: "x-" + g.Word(), Value: g.Text()}},
			})
		}
		doc := write(t, units)
		sameUnits(t, units, readAll(t, doc))
		if t.Failed() {
			t.Fatalf("seed %d:\n%s", seed, doc)
		}
	}
}

// foreignTMX is TMX as CAT tools write it: UTF-16, a DOCTYPE, legacy
// codes and highlighting, three variants, srclang on the tu.
const foreignTMX = `<?xml version="1.0" encoding="UTF-16"?>
<!DOCTYPE tmx SYSTEM "tmx14.dtd">
<tmx version="1.4">
<header creationtool="SDL" creationtoolversion="17" segtype="sentence" o-tmf="TW4Win" adminlang="EN-US" srclang="*all*" datatype="rtf"/>
<body>
<tu tuid="7" srclang="EN-US" creationdate="20240102T030405Z" usagecount="2">
<prop type="x-Client">ACME</prop>
<tuv xml:lang="DE-DE"><seg>Klicken Sie <bpt i="1" x="1">&lt;b&gt;</bpt>hier<ept i="1">&lt;/b&gt;</ept><ph x="2" type="lb">&lt;br/&gt;</ph><hi type="x">jetzt</hi></seg></tuv>
<tuv lang="EN-US"><seg>Click <bpt i="1" x="1">&lt;b&gt;</bpt>here<ept i="1">&lt;/b&gt;</ept><ph x="2" type="lb">&lt;br/&gt;</ph><hi type="x">now</hi><ut>{\i}</ut></seg></tuv>
<tuv xml:lang="fr-FR"><seg>Cliquez <it pos="begin" x="1">&lt;b<sub>alt</sub>&gt;</it>ici</seg></tuv>
</tu>
<tu><tuv xml:lang="en"><seg>alone</seg></tuv></tu>
</body>
</tmx>`

func TestReadForeignTMX(t *testing.T) {
	doc, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().String(foreignTMX)
	if err != nil {
		t.Fatal(err)
	}
	rd, err := tmx.NewReader(strings.NewReader(doc), tmx.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if h := rd.Header(); !h.SourceLocale.IsZero() || h.CreationTool != "SDL" {
		t.Errorf("header = %+v", h)
	}
	var units []formats.TMUnit
	for {
		u, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, u)
	}
	if len(units) != 2 {
		t.Fatalf("%d units, want de and fr from the first tu", len(units))
	}
	de, frU := units[0], units[1]
	if de.SourceLocale.String() != "en-US" || de.TargetLocale.String() != "de-DE" || frU.TargetLocale.String() != "fr-FR" {
		t.Errorf("locales = %s→%s, %s", de.SourceLocale, de.TargetLocale, frU.TargetLocale)
	}
	if de.ID != "7" || de.UsageCount != 2 || de.CreatedAt.Year() != 2024 || de.Props[0].Value != "ACME" {
		t.Errorf("unit = %+v", de)
	}
	want := "Click {#tmx:bpt i=1 native=|<b>| x=1 /}here{#tmx:ept i=1 native=|</b>| /}{#tmx:ph native=|<br/>| type=lb x=2 /}now{#tmx:ut native=|{\\\\i}| /}"
	if de.Source.Text != want {
		t.Errorf("source = %s\n want %s", de.Source.Text, want)
	}
	if frU.Target.Text != "Cliquez {#tmx:it native=|<balt>| pos=begin x=1 /}ici" {
		t.Errorf("fr target = %s", frU.Target.Text)
	}
	// Foreign codes survive a write/read cycle.
	sameUnits(t, units, readAll(t, write(t, units)))
	out := string(write(t, units))
	for _, code := range []string{`<bpt i="1" x="1">&lt;b&gt;</bpt>`, `<ept i="1">&lt;/b&gt;</ept>`, `<ph type="lb" x="2">&lt;br/&gt;</ph>`, `<ut>{\i}</ut>`} {
		if !strings.Contains(out, code) {
			t.Errorf("foreign code %s not written back:\n%s", code, out)
		}
	}
}

func TestReadErrors(t *testing.T) {
	head := `<tmx version="1.4"><header srclang="en"/><body>`
	tests := map[string]struct {
		doc  string
		want error
		line int
	}{
		"not tmx":        {`<xliff/>`, formats.ErrInvalid, 0},
		"bad lang":       {head + "\n<tu><tuv xml:lang=\"*\"><seg>a</seg></tuv></tu></body></tmx>", formats.ErrInvalid, 2},
		"no source tuv":  {head + `<tu><tuv xml:lang="de"><seg>a</seg></tuv><tuv xml:lang="fr"><seg>b</seg></tuv></tu></body></tmx>`, formats.ErrInvalid, 1},
		"bad date":       {head + `<tu creationdate="yesterday"><tuv xml:lang="en"><seg>a</seg></tuv></tu></body></tmx>`, formats.ErrInvalid, 1},
		"bad count":      {head + `<tu usagecount="-1"><tuv xml:lang="en"><seg>a</seg></tuv></tu></body></tmx>`, formats.ErrInvalid, 1},
		"no seg":         {head + `<tu><tuv xml:lang="en"/></tu></body></tmx>`, formats.ErrInvalid, 1},
		"unknown inline": {head + `<tu><tuv xml:lang="en"><seg><b>x</b></seg></tuv></tu></body></tmx>`, formats.ErrUnsupported, 1},
		"bad mf2":        {head + `<tu><tuv xml:lang="en"><prop type="x-glossa-syntax">mf2</prop><seg>.match {</seg></tuv></tu></body></tmx>`, formats.ErrInvalid, 1},
		"truncated":      {head + `<tu>`, formats.ErrInvalid, 0},
		"xxe":            {`<!DOCTYPE tmx [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + head + `<tu><tuv xml:lang="en"><seg>&x;</seg></tuv></tu></body></tmx>`, formats.ErrUnsupported, 0},
		"billion laughs": {`<!DOCTYPE tmx [<!ENTITY a "aaaa"><!ENTITY b "&a;&a;&a;">]><tmx>&b;</tmx>`, formats.ErrUnsupported, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := tmx.ReadAll(strings.NewReader(tt.doc), tmx.ReadOptions{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			var fe *formats.Error
			if !errors.As(err, &fe) || fe.Format != "tmx" {
				t.Fatalf("err = %#v", err)
			}
			if tt.line > 0 && fe.Line != tt.line {
				t.Errorf("line = %d, want %d (%v)", fe.Line, tt.line, err)
			}
		})
	}
}

func TestReaderStreamsLargeMemories(t *testing.T) {
	const n = 20000
	pr, pw := io.Pipe()
	unit := formats.TMUnit{SourceLocale: en, TargetLocale: de, Source: mf2(t, "Hello {$name}"), Target: mf2(t, "Hallo {$name}")}
	go func() {
		w := tmx.NewWriter(pw, tmx.WriteOptions{SourceLocale: en})
		for i := range n {
			unit.ID = fmt.Sprint(i)
			if err := w.Write(unit); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.CloseWithError(w.Close())
	}()
	rd, err := tmx.NewReader(pr, tmx.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for ; ; count++ {
		u, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if u.ID != fmt.Sprint(count) {
			t.Fatalf("unit %d has ID %s", count, u.ID)
		}
	}
	if count != n {
		t.Errorf("read %d units, want %d", count, n)
	}
}

func TestReadLimits(t *testing.T) {
	doc := write(t, sampleUnits(t))
	if _, err := tmx.ReadAll(bytes.NewReader(doc), tmx.ReadOptions{Limits: formats.Limits{MaxItems: 2}}); !errors.Is(err, formats.ErrTooLarge) {
		t.Errorf("MaxItems err = %v", err)
	}
	if _, err := tmx.ReadAll(bytes.NewReader(doc), tmx.ReadOptions{Limits: formats.Limits{MaxBytes: 64}}); !errors.Is(err, formats.ErrTooLarge) {
		t.Errorf("MaxBytes err = %v", err)
	}
}

func TestWriteRefusesIncompleteUnits(t *testing.T) {
	var buf bytes.Buffer
	err := tmx.Write(&buf, []formats.TMUnit{{ID: "x", SourceLocale: en}}, tmx.WriteOptions{})
	if !errors.Is(err, formats.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
	ctl, _ := formats.FromModel(formats.PatternMessage(mf.Pattern{mf.Text("bell \x07")}))
	err = tmx.Write(&buf, []formats.TMUnit{{SourceLocale: en, TargetLocale: de, Source: ctl, Target: ctl}}, tmx.WriteOptions{})
	if !errors.Is(err, formats.ErrInvalid) {
		t.Errorf("control character err = %v", err)
	}
}
