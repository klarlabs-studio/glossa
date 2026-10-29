package glossa

import (
	"slices"
	"testing"
)

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"en", "en", true},
		{"EN_us", "en-US", true},
		{"iw", "he", true},
		{"en-Latn-US", "en-US", true},
		{"zh-hant-tw", "zh-Hant-TW", true},
		{"de-DE-u-co-phonebk", "de-DE", true},
		{"en-a-bbb-x-foo", "en", true},
		{" de ", "de", true},
		{"x-foo", "", false},
		{"und", "", false},
		{"", "", false},
		{"*", "", false},
		{"not a tag", "", false},
	}
	for _, tc := range cases {
		got, ok := canonicalize(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("canonicalize(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCanonicalizeAll(t *testing.T) {
	got := canonicalizeAll([]string{"de-AT", "bogus tag", "de_at", "en"})
	want := []string{"de-AT", "en"}
	if !slices.Equal(got, want) {
		t.Fatalf("canonicalizeAll = %v, want %v", got, want)
	}
}

func TestTruncations(t *testing.T) {
	cases := map[string][]string{
		"zh-Hant-TW":   {"zh-Hant", "zh"},
		"en":           nil,
		"fr-CA":        {"fr"},
		"de-a-bbb-foo": {"de-a-bbb", "de"},
		"sl-rozaj-a-x": {"sl-rozaj", "sl"},
	}
	for in, want := range cases {
		if got := truncations(in); !slices.Equal(got, want) {
			t.Errorf("truncations(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	available := []string{"de", "de-AT", "en", "zh-Hant", "fr-CA", "fr"}
	cases := []struct {
		requested []string
		want      string
		ok        bool
	}{
		{[]string{"en"}, "en", true},
		{[]string{"en-GB"}, "en", true},
		{[]string{"zh-Hant-TW"}, "zh-Hant", true},
		{[]string{"ja", "fr-CA"}, "fr-CA", true},
		{[]string{"ja"}, "", false},
		{nil, "", false},
		{[]string{"DE-at"}, "", false}, // lookup expects canonical input
	}
	for _, tc := range cases {
		got, ok := lookup(tc.requested, available)
		if got != tc.want || ok != tc.ok {
			t.Errorf("lookup(%v) = %q, %v; want %q, %v", tc.requested, got, ok, tc.want, tc.ok)
		}
	}
}

func TestScriptDirection(t *testing.T) {
	cases := map[string]Direction{"ar": RTL, "he": RTL, "fa-IR": RTL, "de": LTR, "pa-Arab": RTL, "pa": LTR, "": LTR}
	for tag, want := range cases {
		if got := scriptDirection(tag); got != want {
			t.Errorf("scriptDirection(%q) = %q, want %q", tag, got, want)
		}
	}
}
