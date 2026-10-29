// Package releasetest checks release output against the delivery
// contract's JSON Schemas (runtimes/testdata/schemas) and the MF2 data
// model schema, for tests only.
package releasetest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	once    sync.Once
	schemas map[string]*jsonschema.Schema
	loadErr error
)

// repoRoot is the repository root, found from this file's location.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

func load() {
	root := repoRoot()
	files := map[string]string{
		"manifest": filepath.Join(root, "runtimes", "testdata", "schemas", "manifest.schema.json"),
		"artifact": filepath.Join(root, "runtimes", "testdata", "schemas", "artifact.schema.json"),
		"key":      filepath.Join(root, "runtimes", "testdata", "schemas", "delivery-key.schema.json"),
		"message":  filepath.Join(root, "messageformat", "testdata", "unicode", "data-model", "message.schema.json"),
	}
	schemas = map[string]*jsonschema.Schema{}
	for name, path := range files {
		f, err := os.Open(path) //nolint:gosec // fixed paths in the repository
		if err != nil {
			loadErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		_ = f.Close()
		if err != nil {
			loadErr = err
			return
		}
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		if err := c.AddResource(name+".json", doc); err != nil {
			loadErr = err
			return
		}
		if schemas[name], loadErr = c.Compile(name + ".json"); loadErr != nil {
			return
		}
	}
}

func validate(t *testing.T, name string, doc []byte) {
	t.Helper()
	once.Do(load)
	if loadErr != nil {
		t.Fatalf("load schemas: %v", loadErr)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if err := schemas[name].Validate(inst); err != nil {
		t.Errorf("%s does not match its schema: %v\n%s", name, err, doc)
	}
}

// Manifest validates a manifest against manifest.schema.json.
func Manifest(t *testing.T, doc []byte) {
	t.Helper()
	validate(t, "manifest", doc)
}

// KeyIndex validates a delivery key index object against
// delivery-key.schema.json.
func KeyIndex(t *testing.T, doc []byte) {
	t.Helper()
	validate(t, "key", doc)
}

// RepoRoot is the repository root (for tests reading runtimes/testdata).
func RepoRoot() string { return repoRoot() }

// Artifact validates an artifact against artifact.schema.json, and each
// of its messages against the MF2 data model schema.
func Artifact(t *testing.T, doc []byte) {
	t.Helper()
	validate(t, "artifact", doc)
	var a struct {
		Messages map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(doc, &a); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	for _, m := range a.Messages {
		validate(t, "message", m)
	}
}
