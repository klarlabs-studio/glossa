package glossa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"go.klarlabs.de/glossa/messageformat"
)

// Release artifacts (runtimes/SPEC.md §1.2–§1.3).

// catalog holds the messages of one locale: message ID → MF2 data model.
type catalog map[string]messageformat.Message

type rawArtifact struct {
	Schema    string                     `json:"schema"`
	Locale    string                     `json:"locale"`
	Namespace string                     `json:"namespace"`
	Messages  map[string]json.RawMessage `json:"messages"`
}

// badMessage is a message an artifact carries but the runtime can't read.
type badMessage struct {
	id  string
	err error
}

// verifyArtifact checks body against the manifest's SHA-256.
func verifyArtifact(body []byte, ref artifactRef) error {
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != ref.SHA256 {
		return fmt.Errorf("%w: artifact %s: bytes hash to %s", errIntegrity, ref.SHA256, got)
	}
	return nil
}

// parseArtifact decodes a verified artifact for locale and namespace.
// Messages that aren't valid MF2 data model are left out and returned as
// badMessages, so one bad message degrades to fallback instead of
// blocking the whole release.
func parseArtifact(body []byte, locale, namespace string) (catalog, []badMessage, error) {
	var a rawArtifact
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, nil, fmt.Errorf("%w: artifact %s/%s: %v", errSchema, locale, namespace, err)
	}
	if err := a.check(locale, namespace); err != nil {
		return nil, nil, fmt.Errorf("%w: artifact %s/%s: %v", errSchema, locale, namespace, err)
	}
	cat := make(catalog, len(a.Messages))
	var bad []badMessage
	for id, raw := range a.Messages {
		var msg messageformat.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			bad = append(bad, badMessage{id: id, err: err})
			continue
		}
		cat[id] = msg
	}
	return cat, bad, nil
}

func (a *rawArtifact) check(locale, namespace string) error {
	if major := schemaMajor(a.Schema, artifactSchemaPrefix); major != supportedMajor {
		return fmt.Errorf("unsupported schema %q", a.Schema)
	}
	if a.Locale != locale || a.Namespace != namespace {
		return fmt.Errorf("artifact is %s/%s", a.Locale, a.Namespace)
	}
	if a.Messages == nil {
		return fmt.Errorf("messages is missing")
	}
	return nil
}
