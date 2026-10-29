// Package idempotency implements the API's Idempotency-Key convention
// (api/openapi.yaml, "Idempotency") without a key store: the same
// (operation, scope, caller, key) always derives the same resource ID,
// so a retry collides with the first request's row (INSERT … ON
// CONFLICT (id) DO NOTHING) and the caller returns that row instead of
// creating a second one. The row is the record; nothing expires.
package idempotency

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidKey means the key is empty, too long, or not printable
// ASCII.
var ErrInvalidKey = errors.New("idempotency: Idempotency-Key must be 1-255 printable ASCII characters")

// namespace seeds the name-based IDs. It differs from Identity's, so the
// two derivations never collide.
var namespace = uuid.MustParse("0b8f3d8e-4f52-4c1f-9b53-2f0c8a6e7d14")

// ID derives the ID a create gets from its Idempotency-Key. scope is
// what the key is unique within (a tenant, a project); caller is the
// acting principal.
func ID(operation, scope, caller, key string) uuid.UUID {
	name := strings.Join([]string{operation, scope, caller, key}, "\x00")
	return uuid.NewSHA1(namespace, []byte(name))
}

// CheckKey validates a non-empty key.
func CheckKey(key string) error {
	if key == "" || len(key) > 255 {
		return ErrInvalidKey
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return ErrInvalidKey
		}
	}
	return nil
}
