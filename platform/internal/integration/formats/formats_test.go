package formats_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

func TestLimitReader(t *testing.T) {
	tests := []struct {
		in      string
		max     int64
		wantErr bool
	}{
		{"", 0, false},
		{"abc", 3, false},
		{"abcd", 3, true},
		{strings.Repeat("x", 10000), 9999, true},
		{strings.Repeat("x", 10000), 10000, false},
	}
	for _, tt := range tests {
		got, err := formats.ReadAll(strings.NewReader(tt.in), tt.max)
		if tt.wantErr {
			if !errors.Is(err, formats.ErrTooLarge) {
				t.Errorf("ReadAll(%d bytes, max %d) err = %v, want ErrTooLarge", len(tt.in), tt.max, err)
			}
			continue
		}
		if err != nil || string(got) != tt.in {
			t.Errorf("ReadAll(%d bytes, max %d) = %d bytes, %v", len(tt.in), tt.max, len(got), err)
		}
	}
}

func TestLimitReaderNeverReturnsMoreThanMax(t *testing.T) {
	r := formats.LimitReader(bytes.NewReader(make([]byte, 100)), 10)
	n, err := io.ReadFull(r, make([]byte, 50))
	if n > 10 || !errors.Is(err, formats.ErrTooLarge) {
		t.Fatalf("read %d bytes, err %v; want at most 10 and ErrTooLarge", n, err)
	}
}

func TestErrorMessage(t *testing.T) {
	err := &formats.Error{Format: "xliff", Line: 3, Column: 7, Item: `unit "a"`, Err: formats.Invalidf("bad %s", "thing")}
	if got, want := err.Error(), `xliff:3:7: unit "a": invalid: bad thing`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, formats.ErrInvalid) {
		t.Error("errors.Is(err, ErrInvalid) = false")
	}
	bare := &formats.Error{Format: "po", Err: formats.Unsupportedf("x")}
	if got := bare.Error(); got != "po: unsupported: x" {
		t.Errorf("Error() = %q", got)
	}
}

func TestPlainTextKeepsSyntaxCharactersLiteral(t *testing.T) {
	for _, s := range []string{"", "Hello", "{$x} {{y}} \\ | .match", ".input", "\t. after whitespace", " lead and trail ", "'quoted' #"} {
		c, err := formats.PlainText(s)
		if err != nil {
			t.Fatalf("PlainText(%q): %v", s, err)
		}
		out, err := mf.Format(c.Model, "en", nil, mf.WithBidiIsolation(false))
		if err != nil || out != s {
			t.Errorf("PlainText(%q) formats as %q, %v", s, out, err)
		}
		if c.Syntax != mfcontent.MF2 {
			t.Errorf("syntax = %q, want mf2", c.Syntax)
		}
	}
}

func TestMF2TextOfMF1Content(t *testing.T) {
	c, err := formats.ParseContent(mfcontent.MF1, "Hi {name}", bcp47.MustParse("en"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := formats.MF2Text(c)
	if err != nil || got != "Hi {$name}" {
		t.Errorf("MF2Text = %q, %v", got, err)
	}
	if _, err := formats.ParseContent(mfcontent.MF1, "{broken", bcp47.MustParse("en")); !errors.Is(err, formats.ErrInvalid) {
		t.Errorf("ParseContent(broken) err = %v, want ErrInvalid", err)
	}
}

func TestIsComplex(t *testing.T) {
	for src, want := range map[string]bool{
		"Hello {$name}":                                   false,
		".input {$n :number} {{{$n}}}":                    true,
		".input {$n :number} .match $n one {{a}} * {{b}}": true,
	} {
		m, err := mf.ParseMF2(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := formats.IsComplex(m); got != want {
			t.Errorf("IsComplex(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestParseLocaleCanonicalizes(t *testing.T) {
	tag, err := formats.ParseLocale("pt_br")
	if err != nil || tag.String() != "pt-BR" {
		t.Errorf("ParseLocale(pt_br) = %v, %v", tag, err)
	}
	if _, err := formats.ParseLocale("*all*"); err == nil {
		t.Error("ParseLocale(*all*) succeeded")
	}
}

func TestEntryTarget(t *testing.T) {
	de := bcp47.MustParse("de")
	e := formats.Entry{Targets: []formats.Target{{Locale: de, State: formats.StateApproved}}}
	if got, ok := e.Target(de); !ok || got.State != formats.StateApproved {
		t.Errorf("Target(de) = %+v, %v", got, ok)
	}
	if _, ok := e.Target(bcp47.MustParse("fr")); ok {
		t.Error("Target(fr) found")
	}
	if !formats.StateNeedsReview.Valid() || formats.State("x").Valid() {
		t.Error("State.Valid")
	}
	if !formats.TermForbidden.Valid() || formats.TermStatus("x").Valid() {
		t.Error("TermStatus.Valid")
	}
}
