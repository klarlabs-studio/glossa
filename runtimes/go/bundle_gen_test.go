package glossa

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// exampleBundle is the source of testdata/bundle, the bundled catalogs the
// godoc examples load. Regenerate with:
//
//	GLOSSA_WRITE_EXAMPLE_BUNDLE=1 go test -run TestExampleBundle .
var exampleBundle = map[string]map[string]string{
	"en": {
		"email.welcome.subject": "Welcome to Brotwerk, {$name}!",
		"email.welcome.body": ".input {$loaves :integer} .match $loaves " +
			"one {{Your first loaf is on its way.}} * {{Your {$loaves} loaves are on their way.}}",
		"cli.sync.done":   "Synced {$count :integer} items in {$seconds :number maximumFractionDigits=1}s",
		"greeting":        "Hello!",
		"invoice.overdue": "Invoice {$number} is overdue.",
		"invoice.terms": "Pay within {#b}{$days :integer} days{/b}.{#br/}" +
			"{#link href=|https://example.com/help|}{#i}Questions?{/i} Ask {#u}us{/u}.{/link}",
	},
	"de": {
		"email.welcome.subject": "Willkommen bei Brotwerk, {$name}!",
		"email.welcome.body": ".input {$loaves :integer} .match $loaves " +
			"one {{Dein erstes Brot ist unterwegs.}} * {{Deine {$loaves} Brote sind unterwegs.}}",
		"cli.sync.done": "{$count :integer} Einträge in {$seconds :number maximumFractionDigits=1} s synchronisiert",
		"greeting":      "Hallo!",
	},
	"ar": {"greeting": "مرحبا!"},
}

const exampleBundleDir = "testdata/bundle"

func TestExampleBundle(t *testing.T) {
	rel := buildRelease(t, "rel_example", 1, exampleBundle, "en", "de", "ar")
	rel.manifest = mutateJSON(t, rel.manifest, func(m map[string]any) {
		m["project"] = "prj_example"
		m["fallback"] = map[string]any{"de-AT": []any{"de"}}
	})
	files := map[string][]byte{"manifest.json": rel.manifest}
	for digest, body := range rel.artifacts {
		files[filepath.Join("a", digest+".json")] = body
	}
	if os.Getenv("GLOSSA_WRITE_EXAMPLE_BUNDLE") == "1" {
		writeBundle(t, exampleBundleDir, files)
		return
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(exampleBundleDir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; regenerate it (see exampleBundle)", name)
		}
	}
}

// writeBundle replaces the bundle in dir with files.
func writeBundle(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
