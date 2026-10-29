package locale

import (
	"errors"
	"strings"
	"testing"
)

func TestNewCode_AcceptsAndCanonicalizesBCP47(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"de", "de"},
		{"de-DE", "de-DE"},
		{"EN-us", "en-US"},
		{"en_US", "en-US"},
		{"zh-Hant-TW", "zh-Hant-TW"},
		{"sr-Latn", "sr-Latn"},
		{"es-419", "es-419"},
		{"ca-ES-valencia", "ca-ES-valencia"},
		{"de-CH-1996", "de-CH-1996"},
		{"fil", "fil"},
		// Deprecated subtags map to their preferred values.
		{"iw", "he"},
		{"in", "id"},
		// A script the language implies anyway is suppressed.
		{"en-Latn-US", "en-US"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NewCode(tc.in)
			if err != nil {
				t.Fatalf("NewCode(%q) error: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("NewCode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewCode_Rejects(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
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
			if _, err := NewCode(in); !errors.Is(err, ErrInvalidCode) {
				t.Errorf("NewCode(%q) err = %v, want ErrInvalidCode", in, err)
			}
		})
	}
}

func TestCode_Subtags(t *testing.T) {
	cases := []struct {
		in                       string
		language, script, region string
	}{
		{"de", "de", "", ""},
		{"de-DE", "de", "", "DE"},
		{"zh-Hant-TW", "zh", "Hant", "TW"},
		{"sr-Latn", "sr", "Latn", ""},
		{"es-419", "es", "", "419"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			c, err := NewCode(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Language(); got != tc.language {
				t.Errorf("Language() = %q, want %q", got, tc.language)
			}
			if got := c.Script(); got != tc.script {
				t.Errorf("Script() = %q, want %q", got, tc.script)
			}
			if got := c.Region(); got != tc.region {
				t.Errorf("Region() = %q, want %q", got, tc.region)
			}
		})
	}
}

func TestCode_Direction(t *testing.T) {
	cases := map[string]Direction{
		"de":         LTR,
		"en-US":      LTR,
		"ja":         LTR,
		"zh-Hant-TW": LTR,
		"ar":         RTL,
		"ar-EG":      RTL,
		"he":         RTL,
		"fa":         RTL,
		"ur":         RTL,
		"ps":         RTL,
		"dv":         RTL,
		"ckb":        RTL,
		"yi":         RTL,
		// Script beats language: Azerbaijani in Arabic script is RTL,
		// Punjabi in Gurmukhi is LTR while Punjabi in Arabic is RTL.
		"az-Arab": RTL,
		"az":      LTR,
		"pa":      LTR,
		"pa-Arab": RTL,
		// Unknown likely script falls back to LTR rather than failing.
		"tlh": LTR,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			c, err := NewCode(in)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Direction(); got != want {
				t.Errorf("Direction(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestCode_Matches(t *testing.T) {
	c, err := NewCode("he")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"he", "HE", "iw"} {
		if !c.Matches(raw) {
			t.Errorf("Code(he).Matches(%q) = false, want true", raw)
		}
	}
	for _, raw := range []string{"he-IL", "", "not a tag"} {
		if c.Matches(raw) {
			t.Errorf("Code(he).Matches(%q) = true, want false", raw)
		}
	}
	// Rows stored before canonicalization existed still match.
	legacy := Code("iw")
	if !legacy.Matches("he") {
		t.Error(`Code("iw").Matches("he") = false, want true`)
	}
}
