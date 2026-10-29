package bcp47_test

import (
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

func TestParseCanonicalizes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"de", "de"},
		{"de-DE", "de-DE"},
		{"EN-us", "en-US"},
		{"en_US", "en-US"},
		{" pt-br ", "pt-BR"},
		{"zh-Hant-TW", "zh-Hant-TW"},
		{"sr-Latn", "sr-Latn"},
		{"es-419", "es-419"},
		{"ca-ES-valencia", "ca-ES-valencia"},
		{"de-CH-1996", "de-CH-1996"},
		{"fil", "fil"},
		{"iw", "he"},
		{"in", "id"},
		{"en-Latn-US", "en-US"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := bcp47.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("Parse(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"star":               "*",
		"not well-formed":    "abcdefgh",
		"undetermined":       "und",
		"private use only":   "x-foo",
		"private use suffix": "en-x-foo",
		"unicode extension":  "de-DE-u-co-phonebk",
		"transform ext":      "en-t-de",
		"over 35 chars":      "de-DE-" + strings.Repeat("abcde-", 6) + "fghij",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := bcp47.Parse(in); !errors.Is(err, bcp47.ErrInvalid) {
				t.Errorf("Parse(%q) err = %v, want ErrInvalid", in, err)
			}
		})
	}
}

func TestDirection(t *testing.T) {
	cases := map[string]bcp47.Direction{
		"de": bcp47.LTR, "en-US": bcp47.LTR, "ja": bcp47.LTR, "zh-Hant-TW": bcp47.LTR,
		"ar": bcp47.RTL, "ar-EG": bcp47.RTL, "he": bcp47.RTL, "fa": bcp47.RTL, "ur": bcp47.RTL,
		"ps": bcp47.RTL, "dv": bcp47.RTL, "ckb": bcp47.RTL, "yi": bcp47.RTL,
		// Script beats language.
		"az-Arab": bcp47.RTL, "az": bcp47.LTR, "pa": bcp47.LTR, "pa-Arab": bcp47.RTL,
		// An unknown likely script falls back to LTR.
		"tlh": bcp47.LTR,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := bcp47.MustParse(in).Direction(); got != want {
				t.Errorf("Direction(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestTruncations(t *testing.T) {
	cases := map[string]string{
		"zh-Hant-TW": "zh-Hant,zh",
		"de-CH-1996": "de-CH,de",
		"de":         "",
	}
	for in, want := range cases {
		got := bcp47.MustParse(in).Truncations()
		if s := strings.Join(tagsToStrings(got), ","); s != want {
			t.Errorf("Truncations(%s) = %q, want %q", in, s, want)
		}
	}
}

func TestZeroValue(t *testing.T) {
	var z bcp47.Tag
	if !z.IsZero() || z.String() != "" || z.Direction() != bcp47.LTR {
		t.Errorf("zero tag = %q", z)
	}
}

func tagsToStrings(ts []bcp47.Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.String()
	}
	return out
}
