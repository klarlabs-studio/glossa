package catalog_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/catalog"
)

func TestParseFlattensNestedAndFlatKeys(t *testing.T) {
	c, err := catalog.Parse("en.json", []byte(`{
		"checkout": {"pay": "Pay {amount}", "summary": {"title": "Summary"}},
		"cart.items": "{count, plural, one {# item} other {# items}}"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"checkout.pay": "Pay {amount}", "checkout.summary.title": "Summary",
		"cart.items": "{count, plural, one {# item} other {# items}}",
	}
	if len(c.Entries) != len(want) {
		t.Fatalf("entries = %v", c.Entries)
	}
	for k, v := range want {
		if c.Entries[k] != v {
			t.Errorf("%s = %q, want %q", k, c.Entries[k], v)
		}
	}
	if keys := c.Keys(); strings.Join(keys, ",") != "cart.items,checkout.pay,checkout.summary.title" {
		t.Errorf("keys = %v", keys)
	}
}

func TestParseRejectsWhatIsNotACatalog(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"not json", `{`, "not valid JSON"},
		{"array", `["a"]`, "must be a JSON object"},
		{"number", `{"a": {"b": 1}}`, "a.b: must be a string"},
		{"duplicate", `{"a.b": "x", "a": {"b": "y"}}`, "defined twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := catalog.Parse("x.json", []byte(tc.body))
			var ce *catalog.Error
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEncodeIsDeterministicInBothStyles(t *testing.T) {
	entries := map[string]string{"b.x": "<b>", "a": "A", "b.y": "Y"}
	flat, err := catalog.Encode(entries, catalog.Flat)
	if err != nil {
		t.Fatal(err)
	}
	if string(flat) != "{\n  \"a\": \"A\",\n  \"b.x\": \"<b>\",\n  \"b.y\": \"Y\"\n}\n" {
		t.Errorf("flat = %s", flat)
	}
	nested, err := catalog.Encode(entries, catalog.Nested)
	if err != nil {
		t.Fatal(err)
	}
	back, err := catalog.Parse("n.json", nested)
	if err != nil || len(back.Entries) != 3 || back.Entries["b.x"] != "<b>" {
		t.Errorf("nested round trip = %v %v\n%s", back.Entries, err, nested)
	}
	if _, err := catalog.Encode(map[string]string{"a": "x", "a.b": "y"}, catalog.Nested); err == nil {
		t.Error("nesting a message under a message should fail")
	}
}

func TestWriteReportsChangesAndDiscoverFindsLocales(t *testing.T) {
	dir := t.TempDir()
	pattern := filepath.Join(dir, "locales", "{locale}.json")
	for _, l := range []string{"en", "de", "pt-BR"} {
		changed, err := catalog.Write(strings.ReplaceAll(pattern, "{locale}", l), map[string]string{"a": l}, catalog.Flat)
		if err != nil || !changed {
			t.Fatalf("write %s: %v %v", l, changed, err)
		}
	}
	changed, err := catalog.Write(strings.ReplaceAll(pattern, "{locale}", "en"), map[string]string{"a": "en"}, catalog.Flat)
	if err != nil || changed {
		t.Errorf("rewrite same content changed = %v, %v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locales", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := catalog.Discover(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 || found["pt-BR"] == "" {
		t.Errorf("found = %v", found)
	}
}
