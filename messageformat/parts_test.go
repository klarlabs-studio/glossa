package messageformat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

// FormatToParts against the formatted-parts expectations of Glossa's
// runtime-format.json (the reference formatter's parts) and, in
// conformance_unicode_test.go, the Unicode suite's expParts.

const runtimeFormatFixture = "testdata/glossa/runtime-format.json"

type runtimeFormatCase struct {
	Description   string          `json:"description"`
	Locale        string          `json:"locale"`
	Message       json.RawMessage `json:"message"`
	Params        []unicodeParam  `json:"params"`
	BidiIsolation string          `json:"bidiIsolation"`
	Exp           string          `json:"exp"`
	ExpParts      []any           `json:"expParts"`
}

func TestFormatToPartsRuntimeFormatFixture(t *testing.T) {
	b, err := os.ReadFile(runtimeFormatFixture)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Tests []runtimeFormatCase `json:"tests"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	withParts := 0
	for i, tc := range fixture.Tests {
		name := fmt.Sprintf("%03d %s", i, tc.Description)
		var msg Message
		if err := json.Unmarshal(tc.Message, &msg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		values := map[string]any{}
		for _, p := range tc.Params {
			values[p.Name] = p.Value
			if p.Type == "datetime" {
				if values[p.Name], err = parseDateTimeParam(p.Value.(string)); err != nil {
					t.Fatal(err)
				}
			}
		}
		opt := WithBidiIsolation(tc.BidiIsolation != "none")
		parts, partsErr := FormatToParts(msg, tc.Locale, values, opt)
		str, strErr := Format(msg, tc.Locale, values, opt)
		// The parts always join to exactly what Format returns, with the
		// same errors, CLDR gaps included.
		if got := PartsText(parts); got != str {
			t.Errorf("%s: parts join to %q, Format = %q", name, got, str)
		}
		if fmt.Sprint(partsErr) != fmt.Sprint(strErr) {
			t.Errorf("%s: FormatToParts error %v, Format error %v", name, partsErr, strErr)
		}
		if tc.ExpParts == nil {
			continue
		}
		withParts++
		assertParts(t, name, parts, tc.ExpParts)
	}
	if withParts == 0 {
		t.Fatal("runtime-format.json has no expParts cases")
	}
}

// assertParts checks parts against the suite's expParts: every expected
// field is present with the same value; implementations may add fields
// (Unicode suite, "Expected parts").
func assertParts(t *testing.T, name string, parts []Part, exp []any) {
	t.Helper()
	raw, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("%s: marshal parts: %v", name, err)
	}
	var got []any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !containsJSON(got, exp) {
		want, _ := json.Marshal(exp)
		t.Errorf("%s: parts\n got %s\nwant %s (fields may be added, not changed)", name, raw, want)
	}
}

// containsJSON reports whether got has every field of want: objects may
// carry extra keys, arrays must have the same length.
func containsJSON(got, want any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range w {
			if gv, ok := g[k]; !ok || !containsJSON(gv, wv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !containsJSON(g[i], w[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(got, want)
	}
}

func TestFormatToPartsShapes(t *testing.T) {
	msg, err := ParseMF2("{#b}{$n :integer}{/b} am {$d :date length=long} für {$who} {#br/}{$missing}")
	if err != nil {
		t.Fatal(err)
	}
	d := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	parts, err := FormatToParts(msg, "de", map[string]any{"n": 1200, "d": d, "who": "Ada"}, WithBidiIsolation(false))
	var fe *FormatError
	if !errors.As(err, &fe) || len(fe.Errors) != 1 || fe.Errors[0].Code != CodeUnresolvedVariable {
		t.Fatalf("want one unresolved-variable, got %v", err)
	}
	want := []Part{
		{Type: PartMarkup, Kind: MarkupOpen, Name: "b"},
		{Type: PartNumber, Value: "1.200", Source: "$n", Locale: "de", Dir: "ltr", Parts: []SubPart{
			{Type: "integer", Value: "1"}, {Type: "group", Value: "."}, {Type: "integer", Value: "200"},
		}},
		{Type: PartMarkup, Kind: MarkupClose, Name: "b"},
		{Type: PartText, Value: " am "},
	}
	if !reflect.DeepEqual(parts[:4], want) {
		t.Errorf("parts[:4]\n got %+v\nwant %+v", parts[:4], want)
	}
	if p := parts[4]; p.Type != PartDateTime || p.Value != "19. September 2026" || len(p.Parts) == 0 {
		t.Errorf("date part: %+v", p)
	}
	tail := parts[5:]
	wantTail := []Part{
		{Type: PartText, Value: " für "},
		{Type: PartString, Value: "Ada", Source: "$who", Locale: "de"},
		{Type: PartText, Value: " "},
		{Type: PartMarkup, Kind: MarkupStandalone, Name: "br"},
		{Type: PartFallback, Value: "{$missing}", Source: "$missing", Locale: "de"},
	}
	if !reflect.DeepEqual(tail, wantTail) {
		t.Errorf("tail\n got %+v\nwant %+v", tail, wantTail)
	}
}

func TestFormatToPartsMarkupOptions(t *testing.T) {
	msg, err := ParseMF2("{#link href=|/terms| title=$t u:id=l1}terms{/link}")
	if err != nil {
		t.Fatal(err)
	}
	parts, err := FormatToParts(msg, "en", map[string]any{"t": "Terms"})
	if err != nil {
		t.Fatal(err)
	}
	open := parts[0]
	if open.Type != PartMarkup || open.Kind != MarkupOpen || open.Name != "link" || open.ID != "l1" {
		t.Fatalf("open: %+v", open)
	}
	if want := map[string]any{"href": "/terms", "title": "Terms"}; !reflect.DeepEqual(open.Options, want) {
		t.Errorf("options = %v, want %v", open.Options, want)
	}
}

func TestFormatToPartsBidiIsolation(t *testing.T) {
	msg, err := ParseMF2("Hi {$name}!")
	if err != nil {
		t.Fatal(err)
	}
	on, _ := FormatToParts(msg, "en", map[string]any{"name": "Ada"})
	if got := PartsText(on); got != "Hi ⁨Ada⁩!" {
		t.Errorf("isolated: %q", got)
	}
	if on[1].Type != PartBidiIsolation || on[3].Type != PartBidiIsolation {
		t.Errorf("isolation parts missing: %+v", on)
	}
	off, _ := FormatToParts(msg, "en", map[string]any{"name": "Ada"}, WithBidiIsolation(false))
	if len(off) != 3 || PartsText(off) != "Hi Ada!" {
		t.Errorf("not isolated: %+v", off)
	}
}

func TestFormatToPartsUnknownValue(t *testing.T) {
	msg, err := ParseMF2("{$v} {$nil}")
	if err != nil {
		t.Fatal(err)
	}
	type point struct{ X, Y int }
	values := map[string]any{"v": point{1, 2}, "nil": nil}
	parts, _ := FormatToParts(msg, "en", values, WithBidiIsolation(false))
	str, _ := Format(msg, "en", values, WithBidiIsolation(false))
	if got := PartsText(parts); got != str {
		t.Errorf("parts join to %q, Format = %q", got, str)
	}
}

func TestFormatToPartsRejectsInvalidInput(t *testing.T) {
	msg, _ := ParseMF2("x")
	parts, err := FormatToParts(msg, "not a locale!", nil)
	if !errors.Is(err, &Error{Code: CodeInvalidLocale}) || parts != nil {
		t.Errorf("invalid locale: %v %v", parts, err)
	}
	bad := Message{Type: "nope"}
	parts, err = FormatToParts(bad, "en", nil)
	if !errors.Is(err, &Error{Code: CodeInvalidMessage}) || parts != nil {
		t.Errorf("invalid message: %v %v", parts, err)
	}
}

func TestPartsTextSkipsMarkup(t *testing.T) {
	parts := []Part{
		{Type: PartText, Value: "a"},
		{Type: PartMarkup, Kind: MarkupOpen, Name: "b"},
		{Type: PartFallback, Value: "{$x}", Source: "$x"},
	}
	if got := PartsText(parts); got != "a{$x}" {
		t.Errorf("PartsText = %q", got)
	}
	if PartsText(nil) != "" {
		t.Error("PartsText(nil) is not empty")
	}
}
