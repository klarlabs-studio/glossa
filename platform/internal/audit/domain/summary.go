package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The summary's bounds. A summary describes an act; it is never a copy
// of what the act carried.
const (
	// MaxSummaryKeys is how many members one summary object describes
	// before it says only how many more there were.
	MaxSummaryKeys = 32
	// MaxVerbatimRunes bounds a selector recorded verbatim. A locale, a
	// state or an id is short; anything longer is recorded as its length
	// whatever the projection declared.
	MaxVerbatimRunes = 64
	// MaxVerbatimItems bounds a selector array recorded verbatim (roles,
	// locales, scopes).
	MaxVerbatimItems = 64
	// MaxSummaryBytes bounds a summary's encoding.
	MaxSummaryBytes = 8 << 10
	// maxSummaryDepth bounds nesting.
	maxSummaryDepth = 3
	// maxSummaryString bounds any string a summary holds: a verbatim
	// selector, or a shape such as "string(len=27)".
	maxSummaryString = 128
	// TruncatedKey carries the count of members left undescribed.
	TruncatedKey = "…"
)

// Summarize describes payload without its content (RFC 0006 §6.1,
// following mcp_tool_calls' rule). selectors are payload paths ("locale",
// "message.key") whose values are identifiers or selectors and are kept
// verbatim; everything else is recorded as its shape:
//
//   - a string becomes "string(len=N)", its length in runes, unless its
//     path is a selector (and it is at most MaxVerbatimRunes, with no
//     control characters);
//   - an array becomes "array(N)", unless its path is a selector and it
//     holds only short strings (roles, locales, scopes);
//   - an object becomes "object(N)", unless its path, or a path beneath
//     it, is a selector — then it is described member by member;
//   - an integer, a boolean and null are kept as they are: they cannot
//     carry text, and versions, revisions and counts are what make an
//     entry readable. Any other number becomes "number".
//
// A payload that is not an object is described under "_".
func Summarize(payload json.RawMessage, selectors []string) json.RawMessage {
	v, err := decode(payload)
	if err != nil {
		return mustJSON(map[string]any{"_": "invalid"})
	}
	if v == nil {
		return json.RawMessage(`{}`)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return mustJSON(map[string]any{"_": describe(v, "", nil, 0)})
	}
	return mustJSON(summarizeObject(obj, "", selectors, 0))
}

func decode(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func summarizeObject(obj map[string]any, prefix string, selectors []string, depth int) map[string]any {
	names := make([]string, 0, len(obj))
	for name := range obj {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make(map[string]any, min(len(names), MaxSummaryKeys)+1)
	for i, name := range names {
		if i == MaxSummaryKeys {
			out[TruncatedKey] = fmt.Sprintf("%d more", len(names)-MaxSummaryKeys)
			break
		}
		key := name
		if utf8.RuneCountInString(key) > MaxVerbatimRunes || hasControl(key) {
			key = fmt.Sprintf("key(len=%d)", utf8.RuneCountInString(name))
		}
		out[key] = describe(obj[name], join(prefix, name), selectors, depth)
	}
	return out
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// describe renders one value at path.
func describe(v any, path string, selectors []string, depth int) any {
	selected := isSelected(path, selectors)
	switch t := v.(type) {
	case nil:
		return nil
	case bool:
		return t
	case json.Number:
		if _, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return t
		}
		return "number"
	case string:
		if selected && verbatimString(t) {
			return t
		}
		return fmt.Sprintf("string(len=%d)", utf8.RuneCountInString(t))
	case []any:
		if selected && verbatimArray(t) {
			return t
		}
		return fmt.Sprintf("array(%d)", len(t))
	case map[string]any:
		if path != "" && depth+1 < maxSummaryDepth && (selected || selectsBeneath(path, selectors)) {
			return summarizeObject(t, path, selectors, depth+1)
		}
		return fmt.Sprintf("object(%d)", len(t))
	}
	return "invalid"
}

// isSelected reports whether path is a selector, by name or through its
// parent's wildcard ("fallback.*" selects every member of fallback).
func isSelected(path string, selectors []string) bool {
	if path == "" {
		return false
	}
	if slices.Contains(selectors, path) {
		return true
	}
	if i := strings.LastIndexByte(path, '.'); i > 0 {
		return slices.Contains(selectors, path[:i]+".*")
	}
	return false
}

func selectsBeneath(path string, selectors []string) bool {
	return slices.ContainsFunc(selectors, func(s string) bool { return strings.HasPrefix(s, path+".") })
}

func verbatimString(s string) bool {
	return utf8.RuneCountInString(s) <= MaxVerbatimRunes && !hasControl(s)
}

func verbatimArray(a []any) bool {
	if len(a) > MaxVerbatimItems {
		return false
	}
	for _, v := range a {
		s, ok := v.(string)
		if !ok || !verbatimString(s) {
			return false
		}
	}
	return true
}

// hasControl reports whether s carries a control character, which would
// make an entry unreadable (or forge structure in a log line).
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("audit: a summary that does not encode: %v", err)) // built from decoded JSON only
	}
	return b
}

// ValidateSummary checks a summary is shaped like one: a JSON object of
// bounded size and depth whose strings are all short and printable. It
// is the guard on direct writes, whose summaries a caller builds — it
// cannot tell an id from a word, but it refuses anything long enough to
// be a sentence.
func ValidateSummary(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > MaxSummaryBytes {
		return fmt.Errorf("summary is %d bytes, at most %d", len(raw), MaxSummaryBytes)
	}
	v, err := decode(raw)
	if err != nil {
		return fmt.Errorf("summary is not JSON: %w", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return errors.New("summary is not an object")
	}
	return checkSummaryValue(v, 0)
}

func checkSummaryValue(v any, depth int) error {
	switch t := v.(type) {
	case nil, bool, json.Number:
		return nil
	case string:
		if utf8.RuneCountInString(t) > maxSummaryString || hasControl(t) {
			return fmt.Errorf("summary holds a string of %d runes; record its shape", utf8.RuneCountInString(t))
		}
		return nil
	case []any:
		if len(t) > MaxVerbatimItems {
			return fmt.Errorf("summary holds an array of %d items", len(t))
		}
		for _, x := range t {
			if err := checkSummaryValue(x, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if depth >= maxSummaryDepth+1 {
			return errors.New("summary is nested too deeply")
		}
		if len(t) > MaxSummaryKeys+1 {
			return fmt.Errorf("summary object has %d members", len(t))
		}
		for k, x := range t {
			if err := checkSummaryValue(k, depth); err != nil {
				return err
			}
			if err := checkSummaryValue(x, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("summary holds a %T", v)
}
