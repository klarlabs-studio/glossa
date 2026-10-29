package domain_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

func TestShape(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		verbatim []string
		want     map[string]string
	}{
		{
			name: "no arguments",
			args: ``,
			want: map[string]string{},
		},
		{
			name: "null arguments",
			args: `null`,
			want: map[string]string{},
		},
		{
			name: "message text becomes a length, never its content",
			args: `{"source":"You have {count} unread messages"}`,
			want: map[string]string{"source": "string(len=32)"},
		},
		{
			name:     "a declared selector is recorded as it is",
			args:     `{"locale":"de-CH","state":"active","source":"Hallo"}`,
			verbatim: []string{"locale", "state"},
			want:     map[string]string{"locale": "de-CH", "state": "active", "source": "string(len=5)"},
		},
		{
			name:     "an undeclared argument is never verbatim, whatever its name",
			args:     `{"locale":"de","text":"Guten Tag"}`,
			verbatim: []string{"locale"},
			want:     map[string]string{"locale": "de", "text": "string(len=9)"},
		},
		{
			name:     "a selector that arrives as a paragraph is recorded as a length",
			args:     `{"locale":"` + strings.Repeat("x", domain.MaxVerbatimRunes+1) + `"}`,
			verbatim: []string{"locale"},
			want:     map[string]string{"locale": "string(len=65)"},
		},
		{
			name:     "a selector carrying a control character is recorded as a length",
			args:     `{"locale":"de\nSubject: x"}`,
			verbatim: []string{"locale"},
			want:     map[string]string{"locale": "string(len=13)"},
		},
		{
			name: "runes are counted, not bytes",
			args: `{"source":"日本語"}`,
			want: map[string]string{"source": "string(len=3)"},
		},
		{
			name: "every JSON type has a shape",
			args: `{"n":42,"b":true,"nil":null,"a":[1,2,3],"o":{"x":1,"y":2}}`,
			want: map[string]string{
				"n": "number", "b": "boolean", "nil": "null", "a": "array(3)", "o": "object(2)",
			},
		},
		{
			name: "arguments that are not an object are described under _",
			args: `["a","b"]`,
			want: map[string]string{"_": "array(2)"},
		},
		{
			name: "malformed arguments do not panic",
			args: `{"a":`,
			want: map[string]string{"_": "invalid"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.Shape(json.RawMessage(tc.args), tc.verbatim)
			if !maps.Equal(got, tc.want) {
				t.Errorf("Shape() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A shape must stay small however large the call was: the ledger is an
// account of what happened, not a copy of the request.
func TestShapeIsBounded(t *testing.T) {
	args := map[string]string{}
	for i := range domain.MaxShapeKeys * 3 {
		args[string(rune('a'+i%26))+string(rune('a'+i/26))] = "x"
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	got := domain.Shape(raw, nil)
	if len(got) != domain.MaxShapeKeys+1 {
		t.Fatalf("shaped %d keys, want %d plus the truncation note", len(got), domain.MaxShapeKeys)
	}
	if note := got[domain.TruncatedKey]; note == "" {
		t.Errorf("no truncation note; got %v", got)
	}
}

// The shape is what stands between an audit ledger and a copy of every
// translation an agent ever wrote. It must not leak the value even for
// an argument a tool forgot to declare.
func TestShapeNeverEchoesUndeclaredText(t *testing.T) {
	const secret = "Bitte bestätigen Sie Ihre Bestellung"
	got := domain.Shape(json.RawMessage(`{"translation":"`+secret+`"}`), []string{"locale", "state"})
	for k, v := range got {
		if strings.Contains(v, "bestätigen") {
			t.Fatalf("argument %q echoed the text: %q", k, v)
		}
	}
}
