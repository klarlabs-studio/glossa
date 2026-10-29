package messageformat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// Glossa's own conformance fixtures (testdata/glossa), shared with every
// other MessageFormat implementation in the repository.

// glossaFormatSkips lists samples whose expected output the Go engine does
// not reproduce, keyed by "<description> [<sample index>]", with the reason.
// Conversion (the model) is still checked for these cases.
var glossaFormatSkips = map[string]string{
	// go-intl v0.2.17 (messageformat-go's CLDR layer) drops the no-break
	// space of the German percent pattern "#,##0 %": it renders "20%".
	"number, percent [0]": "go-intl: German percent lacks the CLDR no-break space (20% vs 20 %)",
	"number, percent [1]": "go-intl: German percent lacks the CLDR no-break space (13% vs 13 %)",
	// go-intl names the UTC zone "GMT" in short style; CLDR/ICU say "UTC".
	"time, long [0]": "go-intl: short zone name of UTC is GMT, ICU says UTC",
	"time, full [0]": "go-intl: short zone name of UTC is GMT, ICU says UTC",
}

type mf1Fixture struct {
	Tests []mf1Case `json:"tests"`
}

type mf1Case struct {
	Description string          `json:"description"`
	Locale      string          `json:"locale"`
	Src         string          `json:"src"`
	MF2         *string         `json:"mf2"`
	Exp         json.RawMessage `json:"exp"`
	Divergence  string          `json:"divergence"`
	Samples     []mf1Sample     `json:"samples"`
	ExpErrors   []struct {
		Type ErrorCode `json:"type"`
	} `json:"expErrors"`
}

type mf1Sample struct {
	Params []unicodeParam `json:"params"`
	Exp    string         `json:"exp"`
	MF2Exp *string        `json:"mf2Exp"`
	// hasMF2Exp distinguishes "mf2Exp": null from an absent member.
	hasMF2Exp bool
}

func (s *mf1Sample) UnmarshalJSON(data []byte) error {
	type plain mf1Sample
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	_, p.hasMF2Exp = probe["mf2Exp"]
	*s = mf1Sample(p)
	return nil
}

func loadJSON[T any](t testing.TB, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return v
}

func TestGlossaMF1ToMF2(t *testing.T) {
	fixture := loadJSON[mf1Fixture](t, "testdata/glossa/mf1-to-mf2.json")
	if n := len(fixture.Tests); n < 40 {
		t.Fatalf("fixture has only %d cases", n)
	}
	used := map[string]bool{}
	for _, tc := range fixture.Tests {
		t.Run(tc.Description, func(t *testing.T) {
			if len(tc.ExpErrors) > 0 {
				assertMF1Error(t, tc)
				return
			}
			want := assertMF1Conversion(t, tc)
			for i, sample := range tc.Samples {
				key := fmt.Sprintf("%s [%d]", tc.Description, i)
				if reason, ok := glossaFormatSkips[key]; ok {
					used[key] = true
					t.Logf("skip format %s: %s", key, reason)
					continue
				}
				assertMF1SampleFormats(t, tc, want, sample)
			}
		})
	}
	for key := range glossaFormatSkips {
		if !used[key] {
			t.Errorf("stale glossaFormatSkips entry: %q", key)
		}
	}
}

func assertMF1Error(t *testing.T, tc mf1Case) {
	t.Helper()
	_, err := ParseMF1(tc.Src, tc.Locale)
	var mfErr *Error
	if !errors.As(err, &mfErr) {
		t.Fatalf("ParseMF1(%q) = %v, want an *Error", tc.Src, err)
	}
	codes := make([]ErrorCode, len(tc.ExpErrors))
	for i, e := range tc.ExpErrors {
		codes[i] = e.Type
	}
	if !slices.Contains(codes, mfErr.Code) {
		t.Errorf("ParseMF1(%q) code %s, want %v (%v)", tc.Src, mfErr.Code, codes, err)
	}
}

// assertMF1Conversion checks the conversion and the fixture's internal
// consistency (exp matches the schema and the mf2 source), and returns exp.
func assertMF1Conversion(t *testing.T, tc mf1Case) Message {
	t.Helper()
	assertMatchesSchema(t, tc.Exp)
	var want Message
	if err := json.Unmarshal(tc.Exp, &want); err != nil {
		t.Fatalf("decode exp: %v", err)
	}
	if tc.MF2 != nil {
		fromSyntax, err := ParseMF2(*tc.MF2)
		if err != nil {
			t.Fatalf("ParseMF2(mf2): %v", err)
		}
		assertSameModel(t, "mf2 vs exp", fromSyntax, tc.Exp)
	}
	got, err := ParseMF1(tc.Src, tc.Locale)
	if err != nil {
		t.Fatalf("ParseMF1(%q): %v", tc.Src, err)
	}
	assertSameModel(t, "ParseMF1", got, tc.Exp)
	assertMessageMatchesSchema(t, got)
	return want
}

func assertSameModel(t *testing.T, label string, got Message, want json.RawMessage) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	if !jsonEqual(t, gotJSON, want) {
		src, _ := Stringify(got)
		t.Errorf("%s: model mismatch\n got: %s\nwant: %s\n got as MF2:\n%s", label, gotJSON, want, src)
	}
}

func assertMF1SampleFormats(t *testing.T, tc mf1Case, msg Message, sample mf1Sample) {
	t.Helper()
	want := sample.Exp
	if sample.hasMF2Exp {
		if sample.MF2Exp == nil {
			return // documented: not formattable by standard MF2 functions
		}
		want = *sample.MF2Exp
	}
	if usesMF1Functions(msg) {
		return // mf1: fallback functions are not part of the standard set
	}
	values, err := unicodeTest{Params: &sample.Params}.values()
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	got, err := Format(msg, tc.Locale, values, WithBidiIsolation(false))
	if err != nil {
		t.Errorf("Format(%v): %v", sample.Params, err)
	}
	if got != want {
		t.Errorf("Format(%v) = %q, want %q", sample.Params, got, want)
	}
}

// usesMF1Functions reports whether msg calls any mf1: fallback function.
func usesMF1Functions(msg Message) bool {
	for _, arg := range Arguments(msg) {
		if strings.HasPrefix(arg.Function, "mf1:") {
			return true
		}
	}
	return false
}

type argumentsFixture struct {
	Tests []struct {
		Description string          `json:"description"`
		Src         *string         `json:"src"`
		MF1         *string         `json:"mf1"`
		Locale      string          `json:"locale"`
		Arguments   json.RawMessage `json:"arguments"`
		Markup      json.RawMessage `json:"markup"`
	} `json:"tests"`
}

func TestGlossaArguments(t *testing.T) {
	fixture := loadJSON[argumentsFixture](t, "testdata/glossa/arguments.json")
	for _, tc := range fixture.Tests {
		t.Run(tc.Description, func(t *testing.T) {
			msg := fixtureMessage(t, tc.Src, tc.MF1, tc.Locale)
			assertJSON(t, "Arguments", Arguments(msg), tc.Arguments)
			assertJSON(t, "MarkupElements", MarkupElements(msg), tc.Markup)
		})
	}
}

type compatFixture struct {
	Tests []struct {
		Description      string          `json:"description"`
		Locale           string          `json:"locale"`
		Source           *string         `json:"source"`
		SourceMF1        *string         `json:"sourceMF1"`
		SourceLocale     string          `json:"sourceLocale"`
		Translation      *string         `json:"translation"`
		TranslationMF1   *string         `json:"translationMF1"`
		TranslationModel json.RawMessage `json:"translationModel"`
		Findings         json.RawMessage `json:"findings"`
	} `json:"tests"`
}

func TestGlossaCompat(t *testing.T) {
	fixture := loadJSON[compatFixture](t, "testdata/glossa/compat.json")
	for _, tc := range fixture.Tests {
		t.Run(tc.Description, func(t *testing.T) {
			source := fixtureMessage(t, tc.Source, tc.SourceMF1, tc.SourceLocale)
			var translation Message
			if tc.TranslationModel != nil {
				if err := json.Unmarshal(tc.TranslationModel, &translation); err != nil {
					t.Fatalf("decode translationModel: %v", err)
				}
			} else {
				translation = fixtureMessage(t, tc.Translation, tc.TranslationMF1, tc.Locale)
			}
			findings := CheckCompat(source, translation, tc.Locale)
			for _, f := range findings {
				if f.Message == "" {
					t.Errorf("finding %s has no human message", f.Code)
				}
			}
			assertJSON(t, "CheckCompat", withoutMessages(findings), tc.Findings)
		})
	}
}

// withoutMessages drops the human text, whose wording is not specified.
func withoutMessages(findings []Finding) []map[string]string {
	out := make([]map[string]string, len(findings))
	for i, f := range findings {
		m := map[string]string{"code": string(f.Code), "severity": string(f.Severity)}
		if f.Subject != "" {
			m["subject"] = f.Subject
		}
		if f.Detail != "" {
			m["detail"] = f.Detail
		}
		out[i] = m
	}
	return out
}

// fixtureMessage parses a fixture message given as MF2 or as MF1 source.
func fixtureMessage(t *testing.T, mf2Src, mf1Src *string, locale string) Message {
	t.Helper()
	var (
		msg Message
		err error
	)
	switch {
	case mf2Src != nil:
		msg, err = ParseMF2(*mf2Src)
	case mf1Src != nil:
		msg, err = ParseMF1(*mf1Src, locale)
	default:
		t.Fatal("fixture message needs src or mf1")
	}
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return msg
}

func assertJSON(t *testing.T, label string, got any, want json.RawMessage) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	if !jsonEqual(t, gotJSON, want) {
		t.Errorf("%s mismatch\n got: %s\nwant: %s", label, gotJSON, want)
	}
}
