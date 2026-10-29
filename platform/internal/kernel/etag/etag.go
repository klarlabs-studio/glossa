// Package etag renders and reads the strong entity tags of versioned
// resources (api/openapi.yaml, "Optimistic concurrency"): a resource's
// integer version, quoted.
package etag

import (
	"strconv"
	"strings"
)

// Format renders version as a strong entity tag.
func Format(version int) string { return `"` + strconv.Itoa(version) + `"` }

// Parse reads an If-Match value this API issued. ok is false for
// anything else, which callers answer with 412.
func Parse(s string) (version int, ok bool) {
	v, ok := parse(s)
	return v, ok && v >= 1
}

// ParseUnsaved is Parse for singletons that exist with defaults before
// anyone saves them: they answer `"0"` until then, and If-Match `"0"`
// means "only while still unsaved".
func ParseUnsaved(s string) (version int, ok bool) {
	v, ok := parse(s)
	return v, ok && v >= 0
}

func parse(s string) (int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "W/")
	if len(s) < 3 || s[0] != '"' || s[len(s)-1] != '"' {
		return 0, false
	}
	v, err := strconv.Atoi(s[1 : len(s)-1])
	return v, err == nil
}
