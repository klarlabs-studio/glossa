package messageformat

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Behaviour of ParseMF1 beyond the shared fixture (testdata/glossa/mf1-to-mf2.json).

func TestParseMF1Errors(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		locale string
		want   ErrorCode
	}{
		{"invalid locale", "Hallo", "not a locale!", CodeInvalidLocale},
		{"positional argument is not an MF2 name", "{0} Einträge", "de", CodeMF1Unsupported},
		{"selector conflict", "{x, select, a {A} other {B}} {x, selectordinal, one {1} other {n}}", "en", CodeMF1Unsupported},
		{"nested argument in a style", "{n, number, {x}}", "en", CodeMF1Unsupported},
		{
			name:   "too many variants",
			src:    manySelects(5), // 8^5 variants
			locale: "en",
			want:   CodeMF1Unsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMF1(tt.src, tt.locale)
			if !errors.Is(err, &Error{Code: tt.want}) {
				t.Errorf("ParseMF1(%q) = %v, want %s", tt.src, err, tt.want)
			}
		})
	}
}

// manySelects returns n sibling selects with eight keys each.
func manySelects(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "{v%d, select, a {a} b {b} c {c} d {d} e {e} f {f} g {g} other {x}}", i)
	}
	return b.String()
}

func TestParseMF1ToMF2Syntax(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "number pattern inside a plural keeps its # characters",
			src:  "{n, plural, other {{n, number, #,##0.0} Punkte}}",
			want: ".input {$n :number}\n.match $n\n* {{{$n :mf1:number mf1:argStyle=|#,##0.0| @mf1:argStyle=|#,##0.0| @mf1:argType=number} Punkte}}",
		},
		{
			name: "offset local avoids a taken name",
			src:  "{n, plural, offset:1 one {{n_minus_1} #} other {#}}",
			want: ".input {$n :number}\n.local $n_minus_1_2 = {$n :offset subtract=1}\n.match $n_minus_1_2\none {{{$n_minus_1} {$n_minus_1_2}}}\n* {{{$n_minus_1_2}}}",
		},
		{
			name: "exact key with leading zero",
			src:  "{n, plural, =01 {eins} other {viele}}",
			want: ".input {$n :number}\n.match $n\n1 {{eins}}\n* {{viele}}",
		},
		{
			name: "unknown currency skeleton falls back",
			src:  "{p, number, ::currency/eur}",
			want: "{$p :mf1:number mf1:argStyle=|::currency/eur| @mf1:argStyle=|::currency/eur| @mf1:argType=number}",
		},
		{
			name: "custom time pattern falls back",
			src:  "{t, time, HH:mm}",
			want: "{$t :mf1:time mf1:argStyle=|HH:mm| @mf1:argStyle=|HH:mm| @mf1:argType=time}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseMF1(tt.src, "de")
			if err != nil {
				t.Fatalf("ParseMF1: %v", err)
			}
			got, err := Stringify(msg)
			if err != nil {
				t.Fatalf("Stringify: %v", err)
			}
			want, err := ParseMF2(tt.want)
			if err != nil {
				t.Fatalf("ParseMF2(want): %v", err)
			}
			wantSrc, _ := Stringify(want)
			if got != wantSrc {
				t.Errorf("ParseMF1(%q) =\n%s\nwant\n%s", tt.src, got, wantSrc)
			}
		})
	}
}

func TestParseMF1VariantsDoNotShareState(t *testing.T) {
	msg, err := ParseMF1("{g, select, a {x} other {y}} {total, number, ::currency/EUR}", "de")
	if err != nil {
		t.Fatal(err)
	}
	first := msg.Variants[0].Value[1].(Expression)
	first.Function.Options["currency"] = Literal{Value: "USD"}
	second := msg.Variants[1].Value[1].(Expression)
	if got := second.Function.Options["currency"]; got != (Literal{Value: "EUR"}) {
		t.Errorf("mutating one variant changed another: %v", got)
	}
}
