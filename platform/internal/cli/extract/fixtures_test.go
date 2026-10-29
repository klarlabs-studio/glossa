package extract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.klarlabs.de/glossa/platform/internal/cli/codegen"
	"go.klarlabs.de/glossa/platform/internal/cli/extract"
)

// fixtureCase is a runtimes/testdata/usages case's case.json.
type fixtureCase struct {
	Description     string              `json:"description"`
	Implementations []string            `json:"implementations"`
	Keys            []string            `json:"keys"`
	Routes          map[string][]string `json:"routes"`
}

// The runner header of the shared fixture contract
// (runtimes/testdata/usages/README.md, "Running a case").
var fixtureHeader = extract.Header{
	Application: "fixture",
	Commit:      "0123456789abcdef0123456789abcdef01234567",
	Branch:      "main",
	Tool:        extract.Tool{Name: "glossa", Version: "0.0.0-test"},
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

// TestUsageFixtures runs every shared usage fixture case that lists
// `extract` and compares the document with expected.json exactly, tool
// aside. The same suite runs against @klarlabs-studio/glossa/unplugin.
func TestUsageFixtures(t *testing.T) {
	dir := filepath.Join(repoRoot(), "runtimes", "testdata", "usages")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	schema := usagesSchema(t)
	ran := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		caseDir := filepath.Join(dir, e.Name())
		var c fixtureCase
		readJSON(t, filepath.Join(caseDir, "case.json"), &c)
		if !slices.Contains(c.Implementations, "extract") {
			continue
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			res, err := extract.Scan(filepath.Join(caseDir, "project"), extract.Options{
				Include:   []string{"**"},
				Templates: extract.DefaultTemplates,
				Accessors: extract.Accessors{TS: codegen.TSNames(c.Keys), Go: codegen.GoNames(c.Keys)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Skipped) > 0 {
				t.Errorf("skipped files: %+v", res.Skipped)
			}
			got, err := json.Marshal(extract.NewDocument(fixtureHeader, res.Usages))
			if err != nil {
				t.Fatal(err)
			}
			validate(t, schema, got)
			var gotDoc, wantDoc map[string]any
			if err := json.Unmarshal(got, &gotDoc); err != nil {
				t.Fatal(err)
			}
			readJSON(t, filepath.Join(caseDir, "expected.json"), &wantDoc)
			delete(gotDoc, "tool")
			delete(wantDoc, "tool")
			g, _ := json.MarshalIndent(gotDoc, "", "  ")
			w, _ := json.MarshalIndent(wantDoc, "", "  ")
			if !bytes.Equal(g, w) {
				t.Errorf("document differs from expected.json\n got: %s\nwant: %s", g, w)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no fixture case lists extract")
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // fixture paths in the repository
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func usagesSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot(), "runtimes", "testdata", "schemas", "usages.v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("usages.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("usages.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, raw []byte) {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(v); err != nil {
		t.Errorf("document doesn't validate against usages.v1: %v", err)
	}
}
