package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Shaping bounds. A tool's arguments are a JSON object the client sent,
// so the shape has to stay small whatever arrives.
const (
	// MaxShapeKeys is how many arguments a shape describes before it
	// says only how many more there were.
	MaxShapeKeys = 32
	// MaxVerbatimRunes bounds a selector recorded verbatim. A locale, a
	// state or a namespace is short; anything longer is recorded as its
	// length, whatever the tool declared.
	MaxVerbatimRunes = 64
	// TruncatedKey carries the count of arguments left undescribed.
	TruncatedKey = "…"
)

// Shape describes a tool call's arguments without recording what they
// contain. It is what the audit ledger stores (RFC 0005 §7.2, §11:
// "never message text, never translation text").
//
// Each value becomes its JSON type — "number", "boolean", "null",
// "array(3)", "object(2)" — and a string becomes "string(len=27)", its
// length in runes. The exception is verbatim: the argument names a tool
// declares as selectors (a locale, a message state, a namespace) are
// recorded as they are, because a row nobody can read is not an audit.
// A selector that arrives longer than MaxVerbatimRunes, or carrying
// control characters, is recorded as its length anyway: a tool's
// declaration says what an argument is *for*, and a caller decides what
// it actually sends.
//
// Arguments that are not a JSON object at all — the client sent an
// array, or a scalar — are described under a single "_" key, since
// there are no names to hang the shape on.
func Shape(raw json.RawMessage, verbatim []string) map[string]string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return map[string]string{}
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return map[string]string{"_": describe(raw, false)}
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make(map[string]string, min(len(names), MaxShapeKeys)+1)
	for i, name := range names {
		if i == MaxShapeKeys {
			out[TruncatedKey] = fmt.Sprintf("%d more", len(names)-MaxShapeKeys)
			break
		}
		out[name] = describe(args[name], slices.Contains(verbatim, name))
	}
	return out
}

// describe renders one JSON value's shape.
func describe(raw json.RawMessage, verbatim bool) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "invalid"
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "invalid"
		}
		if verbatim && utf8.RuneCountInString(s) <= MaxVerbatimRunes && !hasControl(s) {
			return s
		}
		return fmt.Sprintf("string(len=%d)", utf8.RuneCountInString(s))
	case '[':
		var a []json.RawMessage
		if err := json.Unmarshal(raw, &a); err != nil {
			return "invalid"
		}
		return fmt.Sprintf("array(%d)", len(a))
	case '{':
		var o map[string]json.RawMessage
		if err := json.Unmarshal(raw, &o); err != nil {
			return "invalid"
		}
		return fmt.Sprintf("object(%d)", len(o))
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return "invalid"
	}
	return "number"
}

// hasControl reports whether s carries a control character, which would
// make a ledger row unreadable (or worse, forge structure in a log).
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
