package messageformat

import "testing"

// A simple pattern whose first non-whitespace character is "." is not a
// valid simple message (MF2 simple-start excludes "."), so it must be
// serialized as a quoted pattern. The engine's serializer wrote it bare,
// which then failed to parse; the format converters' round-trip property
// tests found it.
func TestStringifyQuotesPatternsThatWouldParseAsComplex(t *testing.T) {
	for _, text := range []string{".", ". x", "\t. x", "  .input", "\n.match", "　.x"} {
		msg := Message{Type: PatternMessageType, Pattern: Pattern{Text(text)}}
		src, err := Stringify(msg)
		if err != nil {
			t.Fatalf("Stringify(%q): %v", text, err)
		}
		back, err := ParseMF2(src)
		if err != nil {
			t.Fatalf("Stringify(%q) = %q, which does not parse: %v", text, src, err)
		}
		again, err := Stringify(back)
		if err != nil || again != src {
			t.Fatalf("round trip of %q: %q -> %q (%v)", text, src, again, err)
		}
		if len(back.Pattern) != 1 || back.Pattern[0] != Text(text) {
			t.Fatalf("round trip of %q changed the pattern: %#v", text, back.Pattern)
		}
	}
}

func TestStringifyLeavesOrdinaryPatternsBare(t *testing.T) {
	src, err := Stringify(Message{Type: PatternMessageType, Pattern: Pattern{Text("Hello. x")}})
	if err != nil || src != "Hello. x" {
		t.Fatalf("Stringify = %q, %v; want bare %q", src, err, "Hello. x")
	}
}
