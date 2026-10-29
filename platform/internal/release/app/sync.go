package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// syncNow writes an environment's manifest right after a pointer move
// committed. A failure is only logged: the move's outbox event makes the
// subscriber write it again until storage takes it.
func (s *Service) syncNow(ctx context.Context, project uuid.UUID, environment string) {
	if err := s.SyncEnvironment(ctx, project, environment); err != nil {
		s.logger.WarnContext(ctx, "release: manifest not written yet; the outbox will retry",
			slog.String("project_id", project.String()), slog.String("environment", environment), slog.Any("error", err))
	}
}

// SyncEnvironment writes the manifest of the release an environment
// serves — and of its active rollout's candidate, if any — to storage,
// where glossa-edge reads it. It holds the
// environment's row lock while writing, so two syncs (or a sync and a
// pointer move) can't reorder: the last write always describes the
// current pointer. It is idempotent: the same release, environment and
// keys give the same bytes.
func (s *Service) SyncEnvironment(ctx context.Context, project uuid.UUID, environment string) error {
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		env, err := st.Environment(ctx, project, environment, true)
		if isNotFound(err) {
			return s.remove(ctx, delivery.ManifestPath(project.String(), environment))
		}
		if err != nil {
			return err
		}
		if env.Current == uuid.Nil {
			return nil
		}
		rel, err := st.Release(ctx, project, env.Current)
		if err != nil {
			return err
		}
		m := rel.Manifest(environment)
		// An active rollout adds its candidate (RFC 0006 §5.2), nested so
		// a runtime that predates SPEC §1.4 serves the stable release.
		// Read under the environment's lock, which every rollout change
		// takes first, so the member and the pointer are one state.
		ro, err := st.ActiveRollout(ctx, project, environment, false)
		switch {
		case err == nil:
			candidate, err := st.Release(ctx, project, ro.Candidate)
			if err != nil {
				return err
			}
			m = m.WithRollout(ro, candidate)
		case !isNotFound(err):
			return err
		}
		body, err := m.Encode(s.signer)
		if err != nil {
			return err
		}
		if err := s.objects.Put(ctx, delivery.ManifestPath(project.String(), environment), body, "application/json"); err != nil {
			return fmt.Errorf("%w: %v", ErrStorage, err)
		}
		return nil
	})
}

// ManifestBytes is the signed manifest of rel as environment serves it
// without a rollout.
func (s *Service) ManifestBytes(rel domain.Release, environment string) ([]byte, error) {
	return rel.Manifest(environment).Encode(s.signer)
}

// SyncDeliveryKey writes or removes a key's index object to match its
// row: present while the key is active, gone once it is revoked or the
// key no longer exists.
func (s *Service) SyncDeliveryKey(ctx context.Context, project, id uuid.UUID) error {
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		k, err := st.DeliveryKey(ctx, project, id, true)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.syncKey(ctx, st, k)
	})
}

// keyIndexVersion is the key index object format syncKey writes: 2 has
// the key's scope (environments, branches); 1 predates scopes.
const keyIndexVersion = 2

// syncKey writes or removes k's index object in st's transaction, and
// records the format written.
func (s *Service) syncKey(ctx context.Context, st Store, k domain.DeliveryKey) error {
	path := delivery.KeyIndexPath(k.Key)
	if !k.Active() {
		return s.remove(ctx, path)
	}
	if err := s.objects.Put(ctx, path, delivery.EncodeKeyIndex(k.ProjectID.String(), k.ID.String(), k.Scope), "application/json"); err != nil {
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return st.MarkKeyIndexed(ctx, k.ID, keyIndexVersion)
}

func (s *Service) remove(ctx context.Context, path string) error {
	if err := s.objects.Delete(ctx, path); err != nil {
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return nil
}
