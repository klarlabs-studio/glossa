package httpgin

import (
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
)

func TestFindLocale_MatchesCanonicalForms(t *testing.T) {
	de := locale.Locale{ID: uuid.New(), Code: "de-DE"}
	he := locale.Locale{ID: uuid.New(), Code: "he"}
	legacy := locale.Locale{ID: uuid.New(), Code: "in"} // stored before canonicalization
	all := []locale.Locale{de, he, legacy}

	cases := map[string]uuid.UUID{
		"de-DE": de.ID,
		"de-de": de.ID,
		"de_DE": de.ID,
		"iw":    he.ID,
		"id":    legacy.ID,
		"in":    legacy.ID,
	}
	for raw, want := range cases {
		got, ok := findLocale(all, raw)
		if !ok || got.ID != want {
			t.Errorf("findLocale(%q) = %v, %v; want %v", raw, got.ID, ok, want)
		}
	}
	for _, raw := range []string{"de", "fr", "", "x-foo"} {
		if _, ok := findLocale(all, raw); ok {
			t.Errorf("findLocale(%q) matched, want no match", raw)
		}
	}
}

func TestLocaleInScope(t *testing.T) {
	scopes := []string{"de-DE", "iw"}
	for _, raw := range []string{"de-DE", "de-de", "he"} {
		if !localeInScope(scopes, raw) {
			t.Errorf("localeInScope(%v, %q) = false, want true", scopes, raw)
		}
	}
	for _, raw := range []string{"de", "fr", ""} {
		if localeInScope(scopes, raw) {
			t.Errorf("localeInScope(%v, %q) = true, want false", scopes, raw)
		}
	}
}

func TestLocaleJSON_ExposesSubtagsAndDirection(t *testing.T) {
	got := localeJSON(locale.Locale{ID: uuid.New(), Code: "ar-EG", Label: "العربية (مصر)", Enabled: true})
	want := map[string]any{
		"code": "ar-EG", "language": "ar", "script": "", "region": "EG", "direction": "rtl",
		"label": "العربية (مصر)", "enabled": true,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("localeJSON[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestCanonicalLocales(t *testing.T) {
	got, err := canonicalLocales([]string{"de_de", "iw", "de-DE"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"de-DE", "he"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("canonicalLocales = %v, want %v (canonical, de-duplicated)", got, want)
	}
	if _, err := canonicalLocales([]string{"de", "nope nope"}); err == nil {
		t.Error("canonicalLocales accepted an invalid tag")
	}
	if got, err := canonicalLocales(nil); err != nil || len(got) != 0 {
		t.Errorf("canonicalLocales(nil) = %v, %v; want empty, nil", got, err)
	}
}
