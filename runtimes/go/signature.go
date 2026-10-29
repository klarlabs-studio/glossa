package glossa

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// Manifest signatures (runtimes/SPEC.md §1.3).

const signatureAlg = "Ed25519"

// PublicKey is a release signing key the client trusts.
type PublicKey struct {
	// KeyID matches the manifest's signatures[].keyId.
	KeyID string
	Key   ed25519.PublicKey
}

// ParsePublicKey decodes a raw Ed25519 public key in base64url (padding
// optional), the form the platform publishes signing keys in.
func ParsePublicKey(keyID, encoded string) (PublicKey, error) {
	if keyID == "" {
		return PublicKey{}, fmt.Errorf("glossa: public key: empty key ID")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return PublicKey{}, fmt.Errorf("glossa: public key %q: %w", keyID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("glossa: public key %q: %d bytes, want %d", keyID, len(raw), ed25519.PublicKeySize)
	}
	return PublicKey{KeyID: keyID, Key: ed25519.PublicKey(raw)}, nil
}

// verifySignatures checks that raw carries a valid signature from one of
// keys over its JCS form without `signatures`. Without keys, verification
// is off (SPEC §1.3 allows it; TLS still protects the transport).
func verifySignatures(raw []byte, m *manifest, keys []PublicKey) error {
	if len(keys) == 0 {
		return nil
	}
	signed, err := canonicalJSONWithout(raw, "signatures")
	if err != nil {
		return fmt.Errorf("%w: manifest can't be canonicalized: %v", errSignature, err)
	}
	candidates := 0
	for _, s := range m.Signatures {
		key, ok := findKey(keys, s.KeyID)
		if !ok || s.Alg != signatureAlg {
			continue
		}
		candidates++
		sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s.Sig, "="))
		if err == nil && ed25519.Verify(key.Key, signed, sig) {
			return nil
		}
	}
	if candidates == 0 {
		return fmt.Errorf("%w: release %s has no signature from a configured key", errSignature, m.Release.ID)
	}
	return fmt.Errorf("%w: release %s: invalid signature", errSignature, m.Release.ID)
}

func findKey(keys []PublicKey, id string) (PublicKey, bool) {
	for _, k := range keys {
		if k.KeyID == id {
			return k, true
		}
	}
	return PublicKey{}, false
}
