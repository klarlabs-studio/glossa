package app

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// ErrInvalidIdempotencyKey means the key is empty, too long, or not
// printable ASCII.
var ErrInvalidIdempotencyKey = errors.New("identity: Idempotency-Key must be 1-255 printable ASCII characters")

// idempotencyNamespace seeds the name-based IDs of idempotent creates.
var idempotencyNamespace = uuid.MustParse("6f1d3c52-1d1e-4c7a-9a4e-5b0f2c9d8e71")

// idempotentID derives the ID a create gets from its Idempotency-Key.
//
// Idempotency without a key store: the same (operation, scope, caller,
// key) always yields the same resource ID, so a retry collides with the
// first request's row (INSERT … ON CONFLICT (id) DO NOTHING) and returns
// it instead of creating a second one. The row is the record; there is
// nothing to expire or clean up.
func idempotentID(operation string, scope string, by domain.Actor, key string) uuid.UUID {
	name := strings.Join([]string{operation, scope, by.String(), key}, "\x00")
	return uuid.NewSHA1(idempotencyNamespace, []byte(name))
}

func checkIdempotencyKey(key string) error {
	if key == "" || len(key) > 255 {
		return ErrInvalidIdempotencyKey
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return ErrInvalidIdempotencyKey
		}
	}
	return nil
}
