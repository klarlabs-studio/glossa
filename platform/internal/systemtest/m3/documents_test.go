//go:build system

package m3_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// documentLocales are the five locales RFC 0004 §7.2 asks the Go runtime
// to render documents in.
var documentLocales = []string{"de", "en", "es", "fr", "ja"}

// documentRun is what part 4 proved.
type documentRun struct {
	// Documents are the fixture documents, by name.
	Documents []string
	// Goldens are the committed golden files the HTML and Runs tests
	// compared against.
	Goldens []string
	// PDFs is how many PDFs the fpdf example rendered.
	PDFs int
	// Tests are the Go test binaries that ran, with how long they took.
	Tests map[string]time.Duration
}

// renderDocuments runs the Go runtime's own document tests and the fpdf
// example (RFC 0004 §12.4). They live with the runtime, in their own
// module, and they own the goldens; the exit test runs them rather than
// keeping a second copy of the fixture, and records what they rendered.
func (s *scenario) renderDocuments() {
	t := s.t
	root := filepath.Join("..", "..", "..", "..")
	docs := filepath.Join(root, "runtimes", "go", "testdata", "documents")

	run := documentRun{Tests: map[string]time.Duration{}}
	for _, tc := range []struct {
		name string
		dir  string
		args []string
	}{
		{"documents (HTML and Runs goldens)", filepath.Join(root, "runtimes", "go"), []string{"test", "-count=1", "-run", "TestDocument", "."}},
		{"examples/pdf (Noto fonts)", filepath.Join(root, "runtimes", "go", "examples", "pdf"), []string{"test", "-count=1", "./..."}},
	} {
		start := time.Now()
		cmd := exec.Command("go", tc.args...)
		cmd.Dir = tc.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", tc.name, err, out)
		}
		run.Tests[tc.name] = time.Since(start)
	}

	// What those tests covered, read from the fixture they own.
	names, err := os.ReadDir(filepath.Join(docs, "templates"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range names {
		run.Documents = append(run.Documents, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
	}
	goldens, err := os.ReadDir(filepath.Join(docs, "golden"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range goldens {
		run.Goldens = append(run.Goldens, e.Name())
	}
	sort.Strings(run.Documents)
	sort.Strings(run.Goldens)

	// The goldens must cover every document in every locale, in HTML,
	// plus one Runs file per locale.
	want := map[string]bool{}
	for _, d := range run.Documents {
		for _, l := range documentLocales {
			want[d+"."+l+".html"] = true
		}
	}
	for _, l := range documentLocales {
		want["runs."+l+".json"] = true
	}
	have := map[string]bool{}
	for _, g := range run.Goldens {
		have[g] = true
	}
	var missing []string
	for name := range want {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the document goldens miss %v", missing)
	}
	run.PDFs = len(run.Documents) * len(documentLocales)

	// The Runs golden names its locale, so the report can say what was
	// rendered rather than only that a test passed.
	for _, l := range documentLocales {
		raw, err := os.ReadFile(filepath.Join(docs, "golden", "runs."+l+".json"))
		if err != nil {
			t.Errorf("runs.%s.json: %v", l, err)
			continue
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Errorf("runs.%s.json: %v", l, err)
		}
	}
	s.documents = run
}
