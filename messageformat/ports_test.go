package messageformat

import "testing"

func TestEngineImplementsPorts(t *testing.T) {
	var (
		p Parser    = Engine{}
		f Formatter = Engine{}
	)
	msg, err := p.ParseMF1("{count, plural, one {# Brot} other {# Brote}}", "de")
	if err != nil {
		t.Fatalf("ParseMF1: %v", err)
	}
	got, err := f.Format(msg, "de", map[string]any{"count": 2}, WithBidiIsolation(false))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != "2 Brote" {
		t.Errorf("Format = %q, want %q", got, "2 Brote")
	}
	parts, err := f.FormatToParts(msg, "de", map[string]any{"count": 2}, WithBidiIsolation(false))
	if err != nil {
		t.Fatalf("FormatToParts: %v", err)
	}
	if len(parts) != 2 || parts[0].Type != PartNumber || PartsText(parts) != "2 Brote" {
		t.Errorf("FormatToParts = %+v, want a number part and text joining to %q", parts, "2 Brote")
	}
	again, err := p.ParseMF2(".input {$count :number}\n.match $count\none {{{$count} Brot}}\n* {{{$count} Brote}}")
	if err != nil {
		t.Fatalf("ParseMF2: %v", err)
	}
	if a, b := mustStringify(t, msg), mustStringify(t, again); a != b {
		t.Errorf("MF1 and MF2 parse to different models:\n%s\n%s", a, b)
	}
}

func mustStringify(t *testing.T, msg Message) string {
	t.Helper()
	s, err := Stringify(msg)
	if err != nil {
		t.Fatalf("Stringify: %v", err)
	}
	return s
}
