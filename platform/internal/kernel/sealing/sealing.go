// Package sealing seals secrets at rest (tenants' AI provider keys,
// RFC 0003 §3.1) with AES-256-GCM. The key is derived from
// GLOSSA_AUTH_SECRET by the composition root (HKDF, its own purpose), so
// rotating that secret makes sealed values unreadable: they must be
// entered again.
//
// Every value is sealed for a context — the tenant and the row it is
// stored in — passed as associated data: a ciphertext copied into
// another tenant's or another row's column does not open.
package sealing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

// version prefixes every sealed value, so the format can change.
const version byte = 1

// ErrOpen means a sealed value could not be opened: tampered, sealed
// with another key or for another context.
var ErrOpen = errors.New("sealing: the sealed value can't be opened")

// Sealer seals and opens values. It is safe for concurrent use.
type Sealer struct{ aead cipher.AEAD }

// New returns a sealer for a 32-byte key.
func New(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("sealing: the key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sealing: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sealing: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext for context: version ‖ nonce ‖ ciphertext.
func (s *Sealer) Seal(plaintext []byte, context ...string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sealing: nonce: %w", err)
	}
	out := append([]byte{version}, nonce...)
	return s.aead.Seal(out, nonce, plaintext, associated(context)), nil
}

// Open decrypts a value sealed for the same context.
func (s *Sealer) Open(sealed []byte, context ...string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < 1+n+s.aead.Overhead() || sealed[0] != version {
		return nil, ErrOpen
	}
	plain, err := s.aead.Open(nil, sealed[1:1+n], sealed[1+n:], associated(context))
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}

// associated length-prefixes each part, so ("ab", "c") ≠ ("a", "bc").
func associated(parts []string) []byte {
	var out []byte
	for _, p := range parts {
		out = binary.BigEndian.AppendUint32(out, uint32(len(p))) //nolint:gosec // context parts are short identifiers
		out = append(out, p...)
	}
	return out
}
