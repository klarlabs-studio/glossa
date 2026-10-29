package apiv1_test

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedCodeIsCurrent regenerates the server from api/openapi.yaml
// with the pinned generator and fails if it differs from the committed
// apiv1.gen.go, so the contract and the code can't drift apart.
func TestGeneratedCodeIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the code generator")
	}
	cfg, err := os.ReadFile("oapi-codegen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "apiv1.gen.go")
	tmpCfg := filepath.Join(t.TempDir(), "oapi-codegen.yaml")
	patched := strings.Replace(string(cfg), "output: apiv1.gen.go", "output: "+out, 1)
	if patched == string(cfg) {
		t.Fatal("oapi-codegen.yaml no longer sets output: apiv1.gen.go; update this test")
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
	got, err := os.ReadFile("apiv1.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	gotCode, gotSpec := splitEmbeddedSpec(t, got)
	wantCode, wantSpec := splitEmbeddedSpec(t, want)
	if !bytes.Equal(gotCode, wantCode) || !bytes.Equal(gotSpec, wantSpec) {
		t.Fatal("apiv1.gen.go is stale: run `go generate ./internal/apiv1/...` and commit the result")
	}
}

// splitEmbeddedSpec returns the generated code without its embedded
// spec, and the spec decoded. The spec is flate-compressed, and
// compress/flate's output differs between Go releases, so comparing the
// encoded bytes would tie this test to one toolchain.
func splitEmbeddedSpec(t *testing.T, src []byte) (code, spec []byte) {
	t.Helper()
	const open = "var swaggerSpec = []string{\n"
	start := bytes.Index(src, []byte(open))
	if start < 0 {
		t.Fatal("apiv1.gen.go has no embedded spec; update this test")
	}
	body := src[start+len(open):]
	end := bytes.Index(body, []byte("\n}\n"))
	if end < 0 {
		t.Fatal("apiv1.gen.go: unterminated swaggerSpec")
	}
	var encoded strings.Builder
	for _, line := range strings.Split(string(body[:end]), "\n") {
		encoded.WriteString(strings.Trim(strings.TrimSpace(line), `",`))
	}
	zipped, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil {
		t.Fatalf("embedded spec: %v", err)
	}
	if spec, err = io.ReadAll(flate.NewReader(bytes.NewReader(zipped))); err != nil {
		t.Fatalf("embedded spec: %v", err)
	}
	code = append(append([]byte{}, src[:start]...), body[end:]...)
	return code, spec
}
