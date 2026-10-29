package delivery_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

const project = "0192f5a0-7a4e-7cc3-9d1e-3a4b5c6d7e8f"

func TestNewKey(t *testing.T) {
	a, err := delivery.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := delivery.NewKey()
	if a == b || !delivery.ValidKey(a) || !strings.HasPrefix(a, "glossa_pk_") || len(a) != len("glossa_pk_")+32 {
		t.Fatalf("keys %q %q", a, b)
	}
	for _, bad := range []string{"", "glossa_api_" + strings.Repeat("a", 43), "glossa_pk_short", a + "x", "glossa_pk_" + strings.Repeat("/", 32)} {
		if delivery.ValidKey(bad) {
			t.Errorf("ValidKey(%q) = true", bad)
		}
	}
}

func TestEnvironmentNames(t *testing.T) {
	for _, ok := range []string{"production", "preview-42", "dev", "a1", "0"} {
		if !delivery.ValidEnvironment(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "a", "Prod", "-x", "x_y", strings.Repeat("x", 64)} {
		if delivery.ValidEnvironment(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLayoutKeysAreValidObjectKeys(t *testing.T) {
	key, _ := delivery.NewKey()
	digest := delivery.Digest([]byte("x"))
	for _, k := range []string{
		delivery.KeyIndexPath(key),
		delivery.ManifestPath(project, "production"),
		delivery.ArtifactPath(project, digest),
	} {
		if err := objectstore.CheckKey(k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	if strings.Contains(delivery.KeyIndexPath(key), key) {
		t.Error("the key itself appears in its index path")
	}
	if got := delivery.ArtifactPath(project, digest); got != "v1/projects/"+project+"/a/"+digest+".json" {
		t.Errorf("artifact path %s", got)
	}
}

func TestKeyIndexRoundTrip(t *testing.T) {
	scope, err := delivery.NewScope([]string{"production", "staging"}, true)
	if err != nil {
		t.Fatal(err)
	}
	b := delivery.EncodeKeyIndex(project, "k1", scope)
	got, err := delivery.DecodeKeyIndex(b)
	if err != nil || got.Project != project || got.KeyID != "k1" ||
		!slices.Equal(got.Environments, []string{"production", "staging"}) || !got.Branches {
		t.Fatalf("%+v, %v", got, err)
	}
	for _, bad := range []string{`{}`, `{"schema":"glossa.delivery-key/v2","project":"` + project + `"}`,
		`{"schema":"glossa.delivery-key/v1","project":"../x"}`, `nope`,
		`{"schema":"glossa.delivery-key/v1","project":"` + project + `","environments":["Prod"]}`,
		`{"schema":"glossa.delivery-key/v1","project":"` + project + `","environments":"production"}`} {
		if _, err := delivery.DecodeKeyIndex([]byte(bad)); !errors.Is(err, delivery.ErrInvalidKeyIndex) {
			t.Errorf("DecodeKeyIndex(%s) = %v", bad, err)
		}
	}
}

// An index object written before scopes existed reads as the scope
// existing keys migrated to: the four default environments, no branches.
func TestLegacyKeyIndexReadsAsTheDefaultEnvironments(t *testing.T) {
	got, err := delivery.DecodeKeyIndex([]byte(`{"schema":"glossa.delivery-key/v1","project":"` + project + `","key_id":"k1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Environments, delivery.DefaultEnvironments) || got.Branches {
		t.Fatalf("legacy scope %+v", got.Scope)
	}
	// An explicit empty allowlist is not legacy.
	got, err = delivery.DecodeKeyIndex([]byte(`{"schema":"glossa.delivery-key/v1","project":"` + project + `","environments":[],"branches":true}`))
	if err != nil || len(got.Environments) != 0 || !got.Branches {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestKeyIndexEncodesAnEmptyAllowlist(t *testing.T) {
	scope, err := delivery.NewScope(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if b := delivery.EncodeKeyIndex(project, "k1", scope); !strings.Contains(string(b), `"environments":[]`) ||
		!strings.Contains(string(b), `"branches":true`) {
		t.Fatalf("%s", b)
	}
}

func TestNewScope(t *testing.T) {
	s, err := delivery.NewScope([]string{"staging", "production", "staging"}, false)
	if err != nil || !slices.Equal(s.Environments, []string{"production", "staging"}) || s.Branches {
		t.Fatalf("%+v, %v", s, err)
	}
	many := make([]string, delivery.MaxScopeEnvironments+1)
	for i := range many {
		many[i] = "env-" + strconv.Itoa(i)
	}
	for name, in := range map[string][]string{
		"empty without branches": nil,
		"invalid name":           {"Prod"},
		"artifact segment":       {"a"},
		"a branch environment":   {"pr-7"},
		"a hashed branch":        {"br-0a1b2c3d"},
		"more than the maximum":  many,
	} {
		if _, err := delivery.NewScope(in, false); !errors.Is(err, delivery.ErrInvalidScope) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if d := delivery.DefaultScope(); !slices.Equal(d.Environments, []string{"production"}) || d.Branches {
		t.Errorf("default scope %+v", d)
	}
}

func TestScopeAllows(t *testing.T) {
	prod, _ := delivery.NewScope([]string{"production"}, false)
	preview, _ := delivery.NewScope([]string{"preview"}, true)
	for _, c := range []struct {
		scope delivery.Scope
		env   string
		want  bool
	}{
		{prod, "production", true},
		{prod, "staging", false},
		{prod, "pr-12", false},
		{prod, "br-0a1b2c3d", false},
		{preview, "preview", true},
		{preview, "pr-12", true},
		{preview, "br-0a1b2c3d", true},
		{preview, "production", false},
		{preview, "pr-", false},
	} {
		if got := c.scope.Allows(c.env); got != c.want {
			t.Errorf("%+v.Allows(%q) = %v", c.scope, c.env, got)
		}
	}
}

func TestBranchEnvironments(t *testing.T) {
	if got := delivery.BranchEnvironmentName("feature/tip", 42); got != "pr-42" {
		t.Errorf("with a PR: %s", got)
	}
	sum := sha256.Sum256([]byte("feature/tip"))
	if got := delivery.BranchEnvironmentName("feature/tip", 0); got != "br-"+hex.EncodeToString(sum[:])[:8] {
		t.Errorf("without a PR: %s", got)
	}
	for _, ok := range []string{"pr-1", "pr-123456", "br-0a1b2c3d", delivery.BranchEnvironmentName("x", 0)} {
		if !delivery.IsBranchEnvironment(ok) || !delivery.ValidEnvironment(ok) {
			t.Errorf("%q is not a branch environment", ok)
		}
	}
	for _, not := range []string{"pr-0", "pr-01", "pr-", "pr-x", "br-0A1B2C3D", "br-0a1b2c3", "br-0a1b2c3d4", "production", "preview-42"} {
		if delivery.IsBranchEnvironment(not) {
			t.Errorf("%q is a branch environment", not)
		}
	}
}
func TestDigest(t *testing.T) {
	if got := delivery.Digest([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" || !delivery.ValidDigest(got) {
		t.Fatalf("digest %s", got)
	}
	if delivery.ValidDigest("BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD") {
		t.Error("uppercase digest accepted")
	}
}
