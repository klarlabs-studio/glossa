package domain_test

import (
	"errors"
	"reflect"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

func TestParseLocaleCanonicalizes(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "de", want: "de"},
		{in: "en-us", want: "en-US"},
		{in: "EN_us", want: "en-US"},
		{in: "zh-hant-tw", want: "zh-Hant-TW"},
		{in: "iw", want: "he"},                    // deprecated code → preferred value
		{in: "en-Latn-US", want: "en-US"},         // suppressed script dropped
		{in: "i-klingon", want: "tlh"},            // grandfathered tag
		{in: " fr-CA ", want: "fr-CA"},            // surrounding space tolerated
		{in: "", wantErr: true},                   // not a tag
		{in: "und", wantErr: true},                // no language
		{in: "zz", wantErr: true},                 // unknown language
		{in: "x-foo", wantErr: true},              // private use only
		{in: "de-DE-u-co-phonebk", wantErr: true}, // extensions don't scope work
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := domain.ParseLocale(tc.in)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrInvalidLocale) {
					t.Fatalf("ParseLocale(%q) err = %v, want ErrInvalidLocale", tc.in, err)
				}
				return
			}
			if err != nil || got.String() != tc.want {
				t.Fatalf("ParseLocale(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestLocaleScopeIsCanonicalSortedAndDeduplicated(t *testing.T) {
	s, err := domain.ParseLocaleScope([]string{"fr", "de-de", "de-DE", "iw"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"de-DE", "fr", "he"}; !reflect.DeepEqual(s.Strings(), want) {
		t.Errorf("Strings() = %v, want %v", s.Strings(), want)
	}
	if s.All() {
		t.Error("a non-empty scope must not cover all locales")
	}
	if _, err := domain.ParseLocaleScope([]string{"de", "nope-nope-nope"}); !errors.Is(err, domain.ErrInvalidLocale) {
		t.Errorf("one bad tag must fail the scope, err = %v", err)
	}
}

func TestLocaleScopeCoversDescendantsAlongCLDRParents(t *testing.T) {
	scope, err := domain.ParseLocaleScope([]string{"de", "es-419", "pt-PT"})
	if err != nil {
		t.Fatal(err)
	}
	for tag, want := range map[string]bool{
		"de":      true,
		"de-AT":   true,  // de-AT → de
		"es-AR":   true,  // es-AR → es-419
		"es":      false, // a parent is not covered by its child
		"es-ES":   false, // es-ES → es, never es-419
		"pt-AO":   true,  // CLDR: pt-AO → pt-PT
		"pt-BR":   false, // pt-BR → pt
		"fr":      false,
		"sr-Latn": false,
	} {
		l, err := domain.ParseLocale(tag)
		if err != nil {
			t.Fatal(err)
		}
		if got := scope.Covers(l); got != want {
			t.Errorf("scope %v covers %s = %t, want %t", scope.Strings(), tag, got, want)
		}
	}

	var all domain.LocaleScope
	de, _ := domain.ParseLocale("de")
	if !all.All() || !all.Covers(de) {
		t.Error("the empty scope covers every locale")
	}
}
