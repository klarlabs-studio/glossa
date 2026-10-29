package messageformat

import (
	"encoding/json"
	"testing"
)

// Fuzz tests: user input (MF1, MF2 and JSON messages) must produce errors,
// never panics. The seeds run with every `go test`; run
// `go test -fuzz=FuzzParseMF1 -fuzztime=30s` to search further.

// fuzzLocales keeps the fuzzer on message source: a random locale would
// almost always fail validation before the parser runs.
var fuzzLocales = []string{"en", "de", "fr", "pl", "ja", "ar"}

func FuzzParseMF1(f *testing.F) {
	fixture := loadJSON[mf1Fixture](f, "testdata/glossa/mf1-to-mf2.json")
	for i, tc := range fixture.Tests {
		f.Add(tc.Src, uint8(i))
	}
	f.Add("{n, plural, offset:3 =0 {a} one {{m, select, x {#} other {{k, selectordinal, few {#} other {#}}}}} other {#}}", uint8(0))
	f.Add("'{'''}'#'", uint8(1))
	f.Fuzz(func(t *testing.T, src string, loc uint8) {
		locale := fuzzLocales[int(loc)%len(fuzzLocales)]
		msg, err := ParseMF1(src, locale)
		if err != nil {
			return
		}
		if _, err := Stringify(msg); err != nil {
			t.Fatalf("ParseMF1(%q) returned a model that does not stringify: %v", src, err)
		}
		_ = Arguments(msg)
		_ = CheckCompat(msg, msg, locale)
	})
}

func FuzzParseMF2(f *testing.F) {
	for _, s := range []string{
		"", "Hallo {$name}", ".input {$n :number} .match $n one {{a}} * {{b}}",
		".local $x = {$y :offset subtract=1} .match $x 0 {{}} * {{}}", "{#b}x{/b}{#br/}",
		"{{.x}}", "{|a| :u:x @a=b}",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		msg, err := ParseMF2(src)
		if err != nil {
			return
		}
		out, err := Stringify(msg)
		if err != nil {
			t.Fatalf("ParseMF2(%q) accepted a message that does not stringify: %v", src, err)
		}
		if _, err := ParseMF2(out); err != nil {
			t.Fatalf("Stringify output %q does not parse: %v", out, err)
		}
		values := map[string]any{"n": 1, "name": "x"}
		str, _ := Format(msg, "de", values)
		parts, _ := FormatToParts(msg, "de", values)
		if joined := PartsText(parts); joined != str {
			t.Fatalf("FormatToParts(%q) joins to %q, Format = %q", src, joined, str)
		}
		_ = CheckCompat(msg, msg, "de")
	})
}

func FuzzMessageJSON(f *testing.F) {
	f.Add(`{"type":"message","declarations":[],"pattern":["a",{"type":"expression","arg":{"type":"variable","name":"x"}}]}`)
	f.Add(`{"type":"select","declarations":[],"selectors":[{"type":"variable","name":"x"}],"variants":[{"keys":[{"type":"*"}],"value":[]}]}`)
	f.Fuzz(func(t *testing.T, doc string) {
		var msg Message
		if err := json.Unmarshal([]byte(doc), &msg); err != nil {
			return
		}
		_ = Validate(msg)
		_ = Arguments(msg)
		_ = MarkupElements(msg)
		_ = CheckCompat(msg, msg, "en")
		_, _ = Format(msg, "en", nil)
	})
}
