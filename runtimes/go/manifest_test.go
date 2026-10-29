package glossa

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

const testSHA = "6c139d50da960438817db7efcadb788679ff0d14c6a16705140c9658881fe3df"

// manifestJSON builds a valid v1 manifest and lets a test mutate it.
func manifestJSON(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"schema":       "glossa.manifest/v1",
		"project":      "prj_1",
		"environment":  "production",
		"release":      map[string]any{"id": "rel_1", "version": 1, "createdAt": "2026-09-01T08:00:00Z"},
		"sourceLocale": "de",
		"locales": []any{
			map[string]any{"code": "de", "direction": "ltr"},
			map[string]any{"code": "ar", "direction": "rtl"},
		},
		"fallback": map[string]any{"*": []any{"de"}},
		"artifacts": map[string]any{
			"de": map[string]any{"default": map[string]any{"sha256": testSHA, "size": 10}},
			"ar": map[string]any{"default": map[string]any{"sha256": testSHA, "size": 10}},
		},
		"unknownField": true,
	}
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseManifest(t *testing.T) {
	m, err := parseManifest(manifestJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if m.Release.ID != "rel_1" || m.SourceLocale != "de" || m.direction("ar") != RTL || m.direction("de") != LTR {
		t.Fatalf("unexpected manifest %+v", m)
	}
	if got := m.localeCodes(); !slices.Equal(got, []string{"de", "ar"}) {
		t.Fatalf("localeCodes = %v", got)
	}
}

func TestParseManifestAcceptsMinorVersions(t *testing.T) {
	_, err := parseManifest(manifestJSON(t, func(m map[string]any) { m["schema"] = "glossa.manifest/v1.3" }))
	if err != nil {
		t.Fatalf("a v1 minor version must be accepted: %v", err)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"other major":          func(m map[string]any) { m["schema"] = "glossa.manifest/v2" },
		"other schema":         func(m map[string]any) { m["schema"] = "glossa.artifact/v1" },
		"missing schema":       func(m map[string]any) { delete(m, "schema") },
		"no release id":        func(m map[string]any) { m["release"] = map[string]any{"version": 1} },
		"no locales":           func(m map[string]any) { m["locales"] = []any{} },
		"source not a locale":  func(m map[string]any) { m["sourceLocale"] = "fr" },
		"non-canonical locale": func(m map[string]any) { m["locales"].([]any)[1] = map[string]any{"code": "AR", "direction": "rtl"} },
		"bad direction":        func(m map[string]any) { m["locales"].([]any)[1] = map[string]any{"code": "ar", "direction": "up"} },
		"missing artifacts":    func(m map[string]any) { delete(m["artifacts"].(map[string]any), "ar") },
		"missing default":      func(m map[string]any) { m["artifacts"].(map[string]any)["ar"] = map[string]any{} },
		"bad sha": func(m map[string]any) {
			m["artifacts"].(map[string]any)["ar"] = map[string]any{"default": map[string]any{"sha256": "XYZ", "size": 1}}
		},
		"negative size": func(m map[string]any) {
			m["artifacts"].(map[string]any)["ar"] = map[string]any{"default": map[string]any{"sha256": testSHA, "size": -1}}
		},
		"bad namespace": func(m map[string]any) {
			m["artifacts"].(map[string]any)["ar"].(map[string]any)["../x"] = map[string]any{"sha256": testSHA, "size": 1}
		},
		"duplicate locale code": func(m map[string]any) {
			m["locales"] = append(m["locales"].([]any), map[string]any{"code": "de", "direction": "ltr"})
		},
	}
	for name, mutate := range cases {
		_, err := parseManifest(manifestJSON(t, mutate))
		if !errors.Is(err, errSchema) {
			t.Errorf("%s: err = %v, want errSchema", name, err)
		}
	}
	if _, err := parseManifest([]byte("{not json")); !errors.Is(err, errSchema) {
		t.Errorf("malformed JSON: err = %v, want errSchema", err)
	}
}

func TestFallbackChain(t *testing.T) {
	m := &manifest{
		SourceLocale: "de",
		Locales: []localeEntry{
			{Code: "de"}, {Code: "de-AT"}, {Code: "de-CH"}, {Code: "en"}, {Code: "en-CA"},
			{Code: "fr"}, {Code: "fr-CA"}, {Code: "pt-BR"}, {Code: "pt-PT"}, {Code: "zh-Hant"},
		},
		Fallback: map[string][]string{
			"de-AT": {"de-CH"},
			"de-CH": {"de"},
			"fr-CA": {"fr", "en-CA"},
			"en-CA": {"en"},
			"pt-BR": {"pt-PT"},
			"pt-PT": {"pt-BR"},
			"*":     {"en"},
			"en":    {"nl"}, // fallback targets outside the locales stay in the chain
		},
	}
	cases := map[string][]string{
		"de-AT":   {"de-AT", "de-CH", "de", "en", "nl"},
		"fr-CA":   {"fr-CA", "fr", "en-CA", "en", "nl", "de"},
		"pt-BR":   {"pt-BR", "pt-PT", "en", "nl", "de"},
		"zh-Hant": {"zh-Hant", "en", "nl", "de"},
		"de":      {"de", "en", "nl"},
	}
	for active, want := range cases {
		if got := m.chain(active); !slices.Equal(got, want) {
			t.Errorf("chain(%q) = %v, want %v", active, got, want)
		}
	}
}

func TestFallbackChainTruncationOnlyWithoutEntry(t *testing.T) {
	m := &manifest{
		SourceLocale: "en",
		Locales:      []localeEntry{{Code: "en"}, {Code: "fr"}, {Code: "fr-CA"}, {Code: "es"}},
		Fallback:     map[string][]string{"fr-CA": {"es"}},
	}
	if got, want := m.chain("fr-CA"), []string{"fr-CA", "es", "en"}; !slices.Equal(got, want) {
		t.Fatalf("chain = %v, want %v (no truncation when an explicit entry exists)", got, want)
	}
}

func TestManifestSchemaMajor(t *testing.T) {
	for in, want := range map[string]int{
		"glossa.manifest/v1": 1, "glossa.manifest/v1.2": 1, "glossa.manifest/v2": 2,
		"glossa.manifest/vX": -1, "other/v1": -1, "": -1,
	} {
		if got := schemaMajor(in, "glossa.manifest/v"); got != want {
			t.Errorf("schemaMajor(%q) = %d, want %d", in, got, want)
		}
	}
	if !strings.Contains(errSchema.Error(), "schema") {
		t.Fatal("errSchema should mention schema")
	}
}
