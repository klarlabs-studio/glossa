package domain

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
)

// The audit export signing key (RFC 0006 §6.2). It is the audit
// context's own key, never the release manifest key: a release key is
// trusted by every runtime in the field and rotated on the delivery
// plane's schedule, an audit key is trusted by auditors and must keep
// verifying exports for as long as the tenant exists. One key, one
// purpose — compromising or rotating one says nothing about the other.
//
// It is spelled the way the release keys are (internal/release/domain):
// a key id of [A-Za-z0-9._-]{1,64} and a base64 32-byte Ed25519 seed for
// the private key, a base64 32-byte public key for a retired one.

// ErrInvalidKey means a configured or supplied audit key can't be used.
var ErrInvalidKey = errors.New("audit: invalid signing key")

// SignatureAlgorithm is the only signature algorithm of glossa.audit/v1.
const SignatureAlgorithm = "Ed25519"

// KeysFormat versions the public key document (KeySet.Document).
const KeysFormat = "glossa.audit.keys/1"

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// SigningKey is the private key exports are signed with.
type SigningKey struct {
	ID  string
	Key ed25519.PrivateKey
}

// Public returns the key's public half.
func (k SigningKey) Public() PublicKey {
	return PublicKey{ID: k.ID, Key: k.Key.Public().(ed25519.PublicKey), Active: true}
}

// PublicKey is a key an export's signature is checked with.
type PublicKey struct {
	ID     string
	Key    ed25519.PublicKey
	Active bool
}

// Encoded is the key as the key document and the CLI print it:
// base64url without padding.
func (k PublicKey) Encoded() string { return base64.RawURLEncoding.EncodeToString(k.Key) }

// ParseSigningKey reads a key id and a base64 (standard or URL, padded
// or not) 32-byte Ed25519 seed.
func ParseSigningKey(id, encoded string) (SigningKey, error) {
	if !keyIDPattern.MatchString(id) {
		return SigningKey{}, fmt.Errorf("%w: key id %q must be 1-64 characters of [A-Za-z0-9._-]", ErrInvalidKey, id)
	}
	raw, err := decodeBase64(encoded)
	if err != nil || len(raw) != ed25519.SeedSize {
		return SigningKey{}, fmt.Errorf("%w: %s: want a base64 32-byte Ed25519 seed", ErrInvalidKey, id)
	}
	return SigningKey{ID: id, Key: ed25519.NewKeyFromSeed(raw)}, nil
}

// ParsePublicKey reads a key id and a base64 32-byte Ed25519 public key.
func ParsePublicKey(id, encoded string) (PublicKey, error) {
	if !keyIDPattern.MatchString(id) {
		return PublicKey{}, fmt.Errorf("%w: key id %q must be 1-64 characters of [A-Za-z0-9._-]", ErrInvalidKey, id)
	}
	raw, err := decodeBase64(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("%w: %s: want a base64 32-byte Ed25519 public key", ErrInvalidKey, id)
	}
	return PublicKey{ID: id, Key: ed25519.PublicKey(raw)}, nil
}

// ParsePublicKeyPair reads "keyId=base64", the spelling of
// GLOSSA_AUDIT_RETIRED_KEYS entries and of `glossa audit verify
// --public-key`.
func ParsePublicKeyPair(pair string) (PublicKey, error) {
	id, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
	if !ok {
		return PublicKey{}, fmt.Errorf("%w: %q is not keyId=base64", ErrInvalidKey, pair)
	}
	return ParsePublicKey(strings.TrimSpace(id), strings.TrimSpace(value))
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if raw, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// KeySet is a deployment's audit keys: the one active key new exports
// are signed with, and the retired public keys older exports still
// verify with. Rotation: a new active key, and the old one's public half
// moved to the retired keys — never dropped while an export signed with
// it may still need verifying.
type KeySet struct {
	active  SigningKey
	retired []PublicKey
}

// NewKeySet needs an active key; key ids must be unique and a retired
// key must not be the active one.
func NewKeySet(active SigningKey, retired []PublicKey) (*KeySet, error) {
	if active.ID == "" || len(active.Key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: an active key is required", ErrInvalidKey)
	}
	seen := map[string]bool{active.ID: true}
	pub := active.Key.Public().(ed25519.PublicKey)
	for _, k := range retired {
		if seen[k.ID] {
			return nil, fmt.Errorf("%w: key id %q is used twice", ErrInvalidKey, k.ID)
		}
		if pub.Equal(k.Key) {
			return nil, fmt.Errorf("%w: retired key %q is the active key", ErrInvalidKey, k.ID)
		}
		seen[k.ID] = true
	}
	out := &KeySet{active: active}
	for _, k := range retired {
		k.Active = false
		out.retired = append(out.retired, k)
	}
	return out, nil
}

// Active is the key new exports are signed with.
func (s *KeySet) Active() SigningKey { return s.active }

// PublicKeys lists every key an export of this deployment may be signed
// with: the active one first.
func (s *KeySet) PublicKeys() []PublicKey {
	return append([]PublicKey{s.active.Public()}, s.retired...)
}

// keyDocument is the glossa.audit.keys/1 document: what wave 5 serves
// at /.well-known/glossa-audit-keys.json and what `glossa audit verify
// --public-key <file>` reads.
type keyDocument struct {
	Format string        `json:"format"`
	Keys   []keyDocEntry `json:"keys"`
}

type keyDocEntry struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"`
	Active    bool   `json:"active"`
}

// KeyDocument renders keys as a glossa.audit.keys/1 document (RFC 8785
// canonical JSON).
func KeyDocument(keys []PublicKey) ([]byte, error) {
	doc := keyDocument{Format: KeysFormat, Keys: []keyDocEntry{}}
	for _, k := range keys {
		doc.Keys = append(doc.Keys, keyDocEntry{KeyID: k.ID, Algorithm: SignatureAlgorithm, PublicKey: k.Encoded(), Active: k.Active})
	}
	return jcs.Marshal(doc)
}

// ParseKeyDocument reads a glossa.audit.keys/1 document.
func ParseKeyDocument(data []byte) ([]PublicKey, error) {
	var doc keyDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: not a key document: %w", ErrInvalidKey, err)
	}
	if doc.Format != KeysFormat {
		return nil, fmt.Errorf("%w: format is %q, not %q", ErrInvalidKey, doc.Format, KeysFormat)
	}
	out := make([]PublicKey, 0, len(doc.Keys))
	for _, e := range doc.Keys {
		if e.Algorithm != SignatureAlgorithm {
			return nil, fmt.Errorf("%w: %s: algorithm %q", ErrInvalidKey, e.KeyID, e.Algorithm)
		}
		k, err := ParsePublicKey(e.KeyID, e.PublicKey)
		if err != nil {
			return nil, err
		}
		k.Active = e.Active
		out = append(out, k)
	}
	return out, nil
}
