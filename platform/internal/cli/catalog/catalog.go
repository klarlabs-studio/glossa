// Package catalog reads and writes local JSON message catalogs: a flat
// object of message ID to text ({"checkout.pay": "Pay {amount}"}), a
// nested one ({"checkout": {"pay": "Pay {amount}"}}), or a mix of both.
// Nested keys join with dots. Catalogs are an import/export format, never
// the model: the CLI parses their text with the MessageFormat kernel.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Catalog is one locale's messages from one file.
type Catalog struct {
	Path    string
	Entries map[string]string
}

// Keys returns the message IDs in order.
func (c Catalog) Keys() []string {
	keys := make([]string, 0, len(c.Entries))
	for k := range c.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Error is a catalog that can't be read, with the JSON path of the
// problem.
type Error struct {
	Path    string
	Key     string
	Problem string
}

func (e *Error) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("%s: %s: %s", e.Path, e.Key, e.Problem)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Problem)
}

// Load reads a catalog. A missing file is fs.ErrNotExist.
func Load(path string) (Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	return Parse(path, raw)
}

// Parse reads catalog JSON; path is for messages.
func Parse(path string, raw []byte) (Catalog, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return Catalog{}, &Error{Path: path, Problem: "not valid JSON: " + err.Error()}
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return Catalog{}, &Error{Path: path, Problem: "must be a JSON object of message ID to text"}
	}
	c := Catalog{Path: path, Entries: map[string]string{}}
	if err := flatten(c, "", obj); err != nil {
		return Catalog{}, err
	}
	return c, nil
}

func flatten(c Catalog, prefix string, obj map[string]any) error {
	for k, v := range obj {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case string:
			if _, dup := c.Entries[key]; dup {
				return &Error{Path: c.Path, Key: key, Problem: "defined twice (flat and nested)"}
			}
			c.Entries[key] = val
		case map[string]any:
			if err := flatten(c, key, val); err != nil {
				return err
			}
		default:
			return &Error{Path: c.Path, Key: key, Problem: fmt.Sprintf("must be a string (the message text) or an object, not %s", jsonType(v))}
		}
	}
	return nil
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case json.Number:
		return "a number"
	case []any:
		return "an array"
	}
	return fmt.Sprintf("%T", v)
}

// Style is how Write lays out a catalog.
type Style string

// Styles.
const (
	Flat   Style = "flat"
	Nested Style = "nested"
)

// Encode renders entries deterministically (sorted keys, two-space
// indent, trailing newline), so a pull produces reviewable diffs.
func Encode(entries map[string]string, style Style) ([]byte, error) {
	var root any = entries
	if style == Nested {
		n, err := nest(entries)
		if err != nil {
			return nil, err
		}
		root = n
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func nest(entries map[string]string) (map[string]any, error) {
	root := map[string]any{}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		node := root
		parts := strings.Split(k, ".")
		for i, part := range parts {
			if i == len(parts)-1 {
				if _, taken := node[part]; taken {
					return nil, fmt.Errorf("catalog: %q is both a message and a group; use the flat style", k)
				}
				node[part] = entries[k]
				break
			}
			child, ok := node[part].(map[string]any)
			if !ok {
				if _, taken := node[part]; taken {
					return nil, fmt.Errorf("catalog: %q is both a message and a group; use the flat style", strings.Join(parts[:i+1], "."))
				}
				child = map[string]any{}
				node[part] = child
			}
			node = child
		}
	}
	return root, nil
}

// Write saves entries to path (creating directories); it reports whether
// the file changed.
func Write(path string, entries map[string]string, style Style) (bool, error) {
	data, err := Encode(entries, style)
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, 0o644)
}

// Discover finds the locales that have a catalog file for pattern (an
// absolute path pattern with {locale}), mapped to their files.
func Discover(pattern string) (map[string]string, error) {
	const ph = "{locale}"
	if !strings.Contains(pattern, ph) {
		return nil, errors.New("catalog: pattern has no {locale}")
	}
	glob := strings.ReplaceAll(pattern, ph, "*")
	re, err := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(filepath.ToSlash(pattern)), regexp.QuoteMeta(ph), "([A-Za-z0-9_-]+)") + "$")
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, m := range matches {
		sub := re.FindStringSubmatch(filepath.ToSlash(m))
		if sub == nil {
			continue
		}
		if st, err := os.Stat(m); err != nil || st.IsDir() {
			continue
		}
		out[sub[1]] = m
	}
	return out, nil
}

// IsNotExist reports whether err is a missing catalog file.
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
