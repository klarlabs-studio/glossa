package domain

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
)

// ReleaseRef identifies the release a manifest describes.
type ReleaseRef struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	CreatedAt string `json:"createdAt"`
}

// Signature is one manifest signature (runtimes/SPEC.md §1.3).
type Signature struct {
	KeyID string `json:"keyId"`
	Alg   string `json:"alg"`
	Sig   string `json:"sig"`
}

// Manifest is what an environment serves (runtimes/SPEC.md §1.1).
type Manifest struct {
	Schema      string     `json:"schema"`
	Project     string     `json:"project"`
	Environment string     `json:"environment"`
	Release     ReleaseRef `json:"release"`
	Content
	// Rollout is the active staged rollout, if any (SPEC §1.4). The
	// signature covers it.
	Rollout    *ManifestRollout `json:"rollout,omitempty"`
	Signatures []Signature      `json:"signatures,omitempty"`
}

// Manifest returns the unsigned manifest of r as environment serves it.
func (r Release) Manifest(environment string) Manifest {
	return Manifest{
		Schema: ManifestSchema, Project: r.ProjectID.String(), Environment: environment,
		Release: releaseRef(r),
		Content: r.Content,
	}
}

// releaseRef is how a manifest names r.
func releaseRef(r Release) ReleaseRef {
	return ReleaseRef{ID: r.ID.String(), Version: r.Version, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339)}
}

// Encode signs m with every key of s and returns the bytes to serve: the
// RFC 8785 canonical JSON of the signed manifest. Each signature is
// Ed25519 over the canonical manifest without `signatures`. Ed25519 and
// JCS are deterministic, so the same release, environment and keys
// always give the same bytes (and ETag).
func (m Manifest) Encode(s *Signer) ([]byte, error) {
	m.Signatures = nil
	unsigned, err := jcs.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("release: encode manifest: %w", err)
	}
	m.Signatures = s.Sign(unsigned)
	b, err := jcs.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("release: encode manifest: %w", err)
	}
	return b, nil
}

// ── signing keys ────────────────────────────────────────────────────

// SignatureAlg is the only signature algorithm of manifest v1.
const SignatureAlg = "Ed25519"

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ErrInvalidSigningKey means a configured key can't be used.
var ErrInvalidSigningKey = errors.New("release: invalid signing key")

// SigningKey is a private key manifests are signed with.
type SigningKey struct {
	ID  string
	Key ed25519.PrivateKey
}

// PublicKey is a key runtimes verify manifests with (runtimes/SPEC.md
// §1.3): raw Ed25519, published base64url without padding.
type PublicKey struct {
	ID  string
	Key ed25519.PublicKey
	// Active keys sign new manifests; retired ones are still published
	// for runtimes holding manifests they signed.
	Active bool
}

// Encoded returns the key as runtimes are configured with it.
func (k PublicKey) Encoded() string { return base64.RawURLEncoding.EncodeToString(k.Key) }

// ParseSigningKey reads a key ID and a base64 (standard or URL, padded or
// not) Ed25519 seed (32 bytes) or private key (64 bytes).
func ParseSigningKey(id, encoded string) (SigningKey, error) {
	if !keyIDPattern.MatchString(id) {
		return SigningKey{}, fmt.Errorf("%w: key ID %q must be 1-64 characters of [A-Za-z0-9._-]", ErrInvalidSigningKey, id)
	}
	raw, err := decodeBase64(encoded)
	if err != nil {
		return SigningKey{}, fmt.Errorf("%w: %s: not base64", ErrInvalidSigningKey, id)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return SigningKey{ID: id, Key: ed25519.NewKeyFromSeed(raw)}, nil
	case ed25519.PrivateKeySize:
		k := ed25519.PrivateKey(raw)
		if !ed25519.NewKeyFromSeed(k.Seed()).Equal(k) {
			return SigningKey{}, fmt.Errorf("%w: %s: inconsistent private key", ErrInvalidSigningKey, id)
		}
		return SigningKey{ID: id, Key: k}, nil
	}
	return SigningKey{}, fmt.Errorf("%w: %s: %d bytes, want a 32-byte seed or a 64-byte private key", ErrInvalidSigningKey, id, len(raw))
}

// ParsePublicKey reads a key ID and a base64 raw Ed25519 public key.
func ParsePublicKey(id, encoded string) (PublicKey, error) {
	if !keyIDPattern.MatchString(id) {
		return PublicKey{}, fmt.Errorf("%w: key ID %q", ErrInvalidSigningKey, id)
	}
	raw, err := decodeBase64(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("%w: %s: want a base64 32-byte Ed25519 public key", ErrInvalidSigningKey, id)
	}
	return PublicKey{ID: id, Key: ed25519.PublicKey(raw)}, nil
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// Signer signs manifests with every active key. Rotation: add the new
// key (manifests then carry both signatures), move runtimes to the new
// key, then retire the old one — keep publishing it as retired while
// runtimes may hold manifests only it signed.
type Signer struct {
	active  []SigningKey
	retired []PublicKey
}

// NewSigner needs at least one active key; key IDs must be unique.
func NewSigner(active []SigningKey, retired []PublicKey) (*Signer, error) {
	if len(active) == 0 {
		return nil, fmt.Errorf("%w: at least one signing key is required", ErrInvalidSigningKey)
	}
	seen := map[string]bool{}
	for _, id := range append(keyIDs(active), publicIDs(retired)...) {
		if seen[id] {
			return nil, fmt.Errorf("%w: key ID %q is used twice", ErrInvalidSigningKey, id)
		}
		seen[id] = true
	}
	return &Signer{active: active, retired: retired}, nil
}

func keyIDs(ks []SigningKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.ID
	}
	return out
}

func publicIDs(ks []PublicKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.ID
	}
	return out
}

// Sign returns one signature per active key over msg.
func (s *Signer) Sign(msg []byte) []Signature {
	out := make([]Signature, len(s.active))
	for i, k := range s.active {
		out[i] = Signature{KeyID: k.ID, Alg: SignatureAlg, Sig: base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.Key, msg))}
	}
	return out
}

// PublicKeys lists the keys runtimes should trust: active ones first.
func (s *Signer) PublicKeys() []PublicKey {
	out := make([]PublicKey, 0, len(s.active)+len(s.retired))
	for _, k := range s.active {
		out = append(out, PublicKey{ID: k.ID, Key: k.Key.Public().(ed25519.PublicKey), Active: true})
	}
	for _, k := range s.retired {
		k.Active = false
		out = append(out, k)
	}
	return out
}
