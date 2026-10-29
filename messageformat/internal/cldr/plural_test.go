package cldr

import (
	"slices"
	"testing"
)

func TestPluralCategories(t *testing.T) {
	tests := []struct {
		locale  string
		ordinal bool
		want    []string
	}{
		{"en", false, []string{"one", "other"}},
		{"en", true, []string{"few", "one", "other", "two"}},
		{"de", false, []string{"one", "other"}},
		{"de-AT", false, []string{"one", "other"}},
		{"de", true, []string{"other"}},
		{"fr", false, []string{"many", "one", "other"}},
		{"ja", false, []string{"other"}},
		{"pl", false, []string{"few", "many", "one", "other"}},
		{"ar", false, []string{"few", "many", "one", "other", "two", "zero"}},
	}
	for _, tt := range tests {
		got, err := PluralCategories(tt.locale, tt.ordinal)
		if err != nil {
			t.Fatalf("PluralCategories(%q, %v): %v", tt.locale, tt.ordinal, err)
		}
		slices.Sort(got)
		if !slices.Equal(got, tt.want) {
			t.Errorf("PluralCategories(%q, ordinal=%v) = %v, want %v", tt.locale, tt.ordinal, got, tt.want)
		}
	}
}

func TestPluralCategoriesRejectsInvalidLocale(t *testing.T) {
	if _, err := PluralCategories("not a locale!", false); err == nil {
		t.Fatal("expected an error for an invalid locale")
	}
}

func TestIsPluralCategory(t *testing.T) {
	for _, k := range []string{"zero", "one", "two", "few", "many", "other"} {
		if !IsPluralCategory(k) {
			t.Errorf("IsPluralCategory(%q) = false", k)
		}
	}
	for _, k := range []string{"", "One", "lots", "*", "1"} {
		if IsPluralCategory(k) {
			t.Errorf("IsPluralCategory(%q) = true", k)
		}
	}
}
