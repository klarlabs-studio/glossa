package domain

import (
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

// DeliveryKey is a project's publishable key for glossa-edge
// (runtimes/SPEC.md §2): public by design — it ships in browser bundles
// — read-only, scoped to one project, and revocable. It grants nothing
// on the control plane. Because it is public, it is stored and listed in
// the clear; revoking it is the only protection it needs.
type DeliveryKey struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Key       string
	Name      string
	// Scope is what the key reads at the edge (RFC 0004 §4.3).
	Scope     delivery.Scope
	CreatedBy string
	CreatedAt time.Time
	// RevokedAt is nil while the key is active.
	RevokedAt *time.Time
	RevokedBy string
}

// NewDeliveryKey creates an active key named name that reads scope
// (delivery.DefaultScope for a key that ships in production bundles).
func NewDeliveryKey(project uuid.UUID, name string, scope delivery.Scope, by string, now time.Time) (DeliveryKey, error) {
	if n := utf8.RuneCountInString(name); n < 1 || n > 200 {
		return DeliveryKey{}, ErrInvalidKeyName
	}
	scope, err := delivery.NewScope(scope.Environments, scope.Branches)
	if err != nil {
		return DeliveryKey{}, err
	}
	key, err := delivery.NewKey()
	if err != nil {
		return DeliveryKey{}, err
	}
	return DeliveryKey{
		ID: uuid.Must(uuid.NewV7()), ProjectID: project, Key: key, Name: name, Scope: scope, CreatedBy: by,
		CreatedAt: now.UTC().Truncate(time.Microsecond),
	}, nil
}

// ChangeScope replaces what the key reads; false if it is unchanged. A
// revoked key reads nothing and takes no scope.
func (k *DeliveryKey) ChangeScope(environments []string, branches bool) (bool, error) {
	if !k.Active() {
		return false, ErrKeyRevoked
	}
	scope, err := delivery.NewScope(environments, branches)
	if err != nil {
		return false, err
	}
	if k.Scope.Equal(scope) {
		return false, nil
	}
	k.Scope = scope
	return true, nil
}

// Active reports whether the key still resolves at the edge.
func (k DeliveryKey) Active() bool { return k.RevokedAt == nil }

// Revoke deactivates the key.
func (k *DeliveryKey) Revoke(by string, now time.Time) error {
	if !k.Active() {
		return ErrKeyRevoked
	}
	at := now.UTC().Truncate(time.Microsecond)
	k.RevokedAt, k.RevokedBy = &at, by
	return nil
}
