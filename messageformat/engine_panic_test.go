package messageformat

import (
	"errors"
	"testing"
)

// knownEnginePanics are inputs that crash kaptinlin/messageformat-go v0.8.6
// (found by FuzzParseMF2; candidates for upstream issues). Glossa contains
// them as CodeInternalError. When an upgrade fixes one, this test fails:
// delete the entry.
var knownEnginePanics = map[string]string{
	"{|0| :A @A ": "internal/cst.parseExpression: index out of range on an unterminated attribute",
}

func TestEnginePanicsAreContained(t *testing.T) {
	for src, bug := range knownEnginePanics {
		t.Run(src, func(t *testing.T) {
			_, err := ParseMF2(src)
			if !errors.Is(err, &Error{Code: CodeInternalError}) {
				t.Fatalf("ParseMF2(%q) = %v; the engine no longer panics (%s) — remove the entry", src, err, bug)
			}
		})
	}
}

func TestParseMF2RejectsInvalidUTF8(t *testing.T) {
	// The engine's parser panics on this input (slice bounds out of range);
	// Glossa rejects invalid UTF-8 before parsing.
	_, err := ParseMF2(".match\xfc0")
	if !errors.Is(err, &Error{Code: CodeSyntaxError}) {
		t.Fatalf("want syntax-error, got %v", err)
	}
}
