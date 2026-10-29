package mfcontent_test

import (
	"errors"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// Every MF1 message survives MF1 → model → MF1 → model: the rendering
// parses back to the same model (RenderMF1 proves it itself).
func TestRenderMF1RoundTrips(t *testing.T) {
	for _, src := range []string{
		"Hello",
		"Hello {name}",
		"Pay {amount, number}",
		"{n, number, integer} of {total, number, percent}",
		"{price, number, ::currency/EUR}",
		"On {d, date} at {t, time}",
		"{d, date, short} {d2, date, long} {d3, date, full} {t2, time, short} {t3, time, long}",
		"It''s '{'literal'}' text",
		"{count, plural, one {# item} other {# items}}",
		"{count, plural, =0 {none} one {# Brot} other {# Brote '#'}}",
		"{place, selectordinal, one {#st} two {#nd} few {#rd} other {#th}}",
		"{gender, select, female {{count, plural, one {she has # cat} other {she has # cats}}} other {{count, plural, one {they have # cat} other {they have # cats}}}}",
		"{d, duration}",
		"{amount, number, currency}",
	} {
		t.Run(src, func(t *testing.T) {
			c, err := mfcontent.Parse(mfcontent.MF1, src, en)
			if err != nil {
				t.Fatal(err)
			}
			text, err := mfcontent.RenderMF1(c.Model, en)
			if err != nil {
				t.Fatalf("RenderMF1: %v", err)
			}
			back, err := mfcontent.Parse(mfcontent.MF1, text, en)
			if err != nil {
				t.Fatalf("%q doesn't parse: %v", text, err)
			}
			if !back.SameModel(c) {
				t.Errorf("%q is another message than %q", text, src)
			}
		})
	}
}

func TestRenderMF1FromMF2(t *testing.T) {
	for _, tc := range []struct{ mf2, mf1 string }{
		{"Pay {$amount :number}", "Pay {amount, number}"},
		{"Hello {$name}!", "Hello {name}!"},
		{".input {$n :number}\n.match $n\none {{{$n} Brot}}\n* {{{$n} Brote}}", "{n, plural, one {{n} Brot} other {{n} Brote}}"},
		{"Braces \\{ and 'quotes'", "Braces '{' and ''quotes''"},
	} {
		c, err := mfcontent.Parse(mfcontent.MF2, tc.mf2, en)
		if err != nil {
			t.Fatal(err)
		}
		got, err := mfcontent.Render(c.Model, mfcontent.MF1, en)
		if err != nil || got != tc.mf1 {
			t.Errorf("Render(%q, mf1) = %q, %v; want %q", tc.mf2, got, err, tc.mf1)
		}
	}
}

func TestRenderMF1RefusesWhatOnlyMF2Expresses(t *testing.T) {
	for _, src := range []string{
		"Hello {#b}{$name}{/b}",
		"Literal {|x|}",
		"Big {$n :number minimumFractionDigits=2}",
		".input {$n :number}\n.match $n\none {{one}}\nother {{other}}\n* {{any}}",
		".local $x = {$y :number}\n{{{$x}}}",
		".input {$n :number}\n.match $n\none {{one}}\n* {{any}}", // not valid MF1 plural keys for "ja"
	} {
		c, err := mfcontent.Parse(mfcontent.MF2, src, en)
		if err != nil {
			t.Fatal(err)
		}
		locale := en
		if src == ".input {$n :number}\n.match $n\none {{one}}\n* {{any}}" {
			locale = bcp47.MustParse("ja")
		}
		if _, err := mfcontent.RenderMF1(c.Model, locale); !errors.Is(err, mfcontent.ErrNoMF1) {
			t.Errorf("RenderMF1(%q) err = %v, want ErrNoMF1", src, err)
		}
	}
	if _, err := mfcontent.Render(mf.Message{}, "mf3", en); !errors.Is(err, mfcontent.ErrInvalidSyntax) {
		t.Errorf("Render in mf3: %v", err)
	}
}
