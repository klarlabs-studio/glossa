package app

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// NewDeliveryKey is a key to create.
type NewDeliveryKey struct {
	Name string
	// Scope is what the key reads at the edge; nil means
	// delivery.DefaultScope (production only). A preview key, for preview
	// deployments only, has Branches set (RFC 0004 §4.3).
	Scope *delivery.Scope
}

// CreateDeliveryKey creates a publishable delivery key for the project
// and writes its index object, so glossa-edge resolves it within its
// scope. A repeated idemKey returns the first request's key with
// replayed set (the key is public, so replaying it reveals nothing).
func (s *Service) CreateDeliveryKey(ctx context.Context, project uuid.UUID, in NewDeliveryKey, idemKey string) (domain.DeliveryKey, bool, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.DeliveryKey{}, false, err
	}
	scope := delivery.DefaultScope()
	if in.Scope != nil {
		scope = *in.Scope
	}
	k, err := domain.NewDeliveryKey(project, in.Name, scope, by, s.now())
	if err != nil {
		return domain.DeliveryKey{}, false, err
	}
	id, keyed, err := idempotentID("delivery_key.create", project.String(), by, idemKey)
	if err != nil {
		return domain.DeliveryKey{}, false, err
	}
	if keyed {
		k.ID = id
	}
	var replayed bool
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertDeliveryKey(ctx, k)
		if err != nil {
			return err
		}
		if !inserted {
			first, err := st.DeliveryKey(ctx, project, k.ID, false)
			if err != nil {
				return err
			}
			if first.Name != k.Name || !first.Scope.Equal(k.Scope) {
				return ErrIdempotencyReuse
			}
			k, replayed = first, true
			return nil
		}
		return st.Publish(ctx, keyEvent(domain.EventDeliveryKeyCreated, k, by))
	})
	if err != nil {
		return domain.DeliveryKey{}, false, err
	}
	s.syncKeyNow(ctx, project, k.ID)
	return k, replayed, nil
}

// ListDeliveryKeys lists a project's keys, revoked ones included.
func (s *Service) ListDeliveryKeys(ctx context.Context, project uuid.UUID, page pagination.Page) ([]domain.DeliveryKey, *string, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, nil, err
	}
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.DeliveryKey
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.DeliveryKeys(ctx, project, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(k domain.DeliveryKey) string { return k.ID.String() })
	return items, next, nil
}

// ChangeDeliveryKeyScope replaces what a key reads at the edge and
// writes its index object again, so the edge follows within its key
// cache TTL. The key itself never changes: widening a production key to
// a branch environment is a decision, not a new key.
func (s *Service) ChangeDeliveryKeyScope(ctx context.Context, project, id uuid.UUID, scope delivery.Scope) (domain.DeliveryKey, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.DeliveryKey{}, err
	}
	var k domain.DeliveryKey
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		k, err = st.DeliveryKey(ctx, project, id, true)
		if err != nil {
			return err
		}
		changed, err := k.ChangeScope(scope.Environments, scope.Branches)
		if err != nil || !changed {
			return err
		}
		if err := st.SetDeliveryKeyScope(ctx, k); err != nil {
			return err
		}
		return st.Publish(ctx, keyEvent(domain.EventDeliveryKeyScopeChanged, k, by))
	})
	if err != nil {
		return domain.DeliveryKey{}, err
	}
	s.syncKeyNow(ctx, project, id)
	return k, nil
}

// RevokeDeliveryKey deactivates a key and removes its index object: the
// edge answers 404 for it once its key cache expires (seconds). The key
// stays listed as revoked.
func (s *Service) RevokeDeliveryKey(ctx context.Context, project, id uuid.UUID) error {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		k, err := st.DeliveryKey(ctx, project, id, true)
		if err != nil {
			return err
		}
		if err := k.Revoke(by, s.now()); err != nil {
			return err
		}
		if err := st.RevokeDeliveryKey(ctx, k); err != nil {
			return err
		}
		return st.Publish(ctx, keyEvent(domain.EventDeliveryKeyRevoked, k, by))
	})
	if err != nil {
		return err
	}
	s.syncKeyNow(ctx, project, id)
	return nil
}

func (s *Service) syncKeyNow(ctx context.Context, project, id uuid.UUID) {
	if err := s.SyncDeliveryKey(ctx, project, id); err != nil {
		s.logger.WarnContext(ctx, "release: delivery key index not written yet; the outbox will retry",
			slog.String("project_id", project.String()), slog.String("key_id", id.String()), slog.Any("error", err))
	}
}

func keyEvent(typ string, k domain.DeliveryKey, by string) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateDeliveryKey, AggregateID: k.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.DeliveryKeyChanged{
			KeyID: k.ID.String(), ProjectID: k.ProjectID.String(), Name: k.Name,
			Environments: k.Scope.Environments, Branches: k.Scope.Branches, By: by,
		},
	}
}
