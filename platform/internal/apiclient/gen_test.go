package apiclient_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedCodeIsCurrent regenerates the client from api/openapi.yaml
// with the pinned generator and fails if it differs from the committed
// apiclient.gen.go, so the contract and the CLI's client can't drift apart.
func TestGeneratedCodeIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the code generator")
	}
	cfg, err := os.ReadFile("oapi-codegen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "apiclient.gen.go")
	tmpCfg := filepath.Join(t.TempDir(), "oapi-codegen.yaml")
	patched := strings.Replace(string(cfg), "output: apiclient.gen.go", "output: "+out, 1)
	if patched == string(cfg) {
		t.Fatal("oapi-codegen.yaml no longer sets output: apiclient.gen.go; update this test")
	}
	if err := os.WriteFile(tmpCfg, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "tool", "oapi-codegen", "-config", tmpCfg, spec)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("oapi-codegen: %v\n%s", err, msg)
	}
	want, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("apiclient.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("apiclient.gen.go is stale: run `go generate ./internal/apiclient/...` and commit the result")
	}
}
