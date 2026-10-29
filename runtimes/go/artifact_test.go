package glossa

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func refFor(body string) artifactRef {
	sum := sha256.Sum256([]byte(body))
	return artifactRef{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}

const deArtifact = `{"schema":"glossa.artifact/v1","locale":"de","namespace":"default","messages":{` +
	`"cart.checkout":{"type":"message","declarations":[],"pattern":["Zur Kasse"]},` +
	`"broken":{"type":"message","declarations":[],"pattern":[{"type":"nonsense"}]}}}`

func TestVerifyArtifact(t *testing.T) {
	ref := refFor(deArtifact)
	if err := verifyArtifact([]byte(deArtifact), ref); err != nil {
		t.Fatal(err)
	}
	if err := verifyArtifact([]byte(deArtifact+" "), ref); !errors.Is(err, errIntegrity) {
		t.Fatalf("err = %v, want errIntegrity", err)
	}
}

func TestParseArtifact(t *testing.T) {
	cat, bad, err := parseArtifact([]byte(deArtifact), "de", "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat["cart.checkout"]; !ok || len(cat) != 1 {
		t.Fatalf("catalog = %v", cat)
	}
	if len(bad) != 1 || bad[0].id != "broken" {
		t.Fatalf("bad messages = %+v, want the broken one", bad)
	}
}

func TestParseArtifactRejects(t *testing.T) {
	cases := map[string]struct{ body, locale, ns string }{
		"wrong locale":    {deArtifact, "en", "default"},
		"wrong namespace": {deArtifact, "de", "checkout"},
		"other major":     {`{"schema":"glossa.artifact/v2","locale":"de","namespace":"default","messages":{}}`, "de", "default"},
		"not json":        {`{`, "de", "default"},
		"no messages":     {`{"schema":"glossa.artifact/v1","locale":"de","namespace":"default"}`, "de", "default"},
	}
	for name, tc := range cases {
		if _, _, err := parseArtifact([]byte(tc.body), tc.locale, tc.ns); !errors.Is(err, errSchema) {
			t.Errorf("%s: err = %v, want errSchema", name, err)
		}
	}
}
