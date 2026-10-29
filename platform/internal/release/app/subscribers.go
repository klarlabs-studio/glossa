package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// The other contexts' events, as Release reads them (its own payload
// types: an anti-corruption layer over the published JSON).
const (
	catalogProjectDeleted = "catalog.project.deleted"

	catalogBranchOpened   = "catalog.branch.opened"
	catalogBranchPushed   = "catalog.branch.pushed"
	catalogBranchReopened = "catalog.branch.reopened"
	catalogBranchClosed   = "catalog.branch.closed"
	catalogBranchMerged   = "catalog.branch.merged"

	localizationTranslationRevised = "localization.translation.revised"
)

// subscriberBranchEnvironments keeps branch environments in step with
// their branches; its name is stored with every event it takes.
const subscriberBranchEnvironments = "release.branch_environments"

// Subscribe registers Release's subscribers. Their names are stored with
// each event; never rename them.
//
//   - release.sync_manifest writes an environment's manifest after every
//     pointer move, until storage has it, and removes a destroyed branch
//     environment's.
//   - release.sync_delivery_key writes or removes a key's index object.
//   - release.retire_project stops serving a deleted project: its keys
//     are revoked and its environments and served manifests removed.
//     Releases stay: they are immutable history.
//   - release.branch_environments follows the branch lifecycle
//     (RFC 0004 §4.2): an opened or reopened branch gets its
//     environment, a push and a translation revised on one of its
//     messages ask for a (debounced) publish, and a closed or merged
//     branch's environment is destroyed.
func (s *Service) Subscribe(r *outbox.Registry) error {
	for _, typ := range []string{domain.EventPublished, domain.EventPromoted, domain.EventRolledBack, domain.EventEnvironmentDestroyed} {
		if err := r.Subscribe(typ, "release.sync_manifest", outbox.HandlerFunc(s.handlePointerMoved)); err != nil {
			return err
		}
	}
	for _, typ := range []string{domain.EventDeliveryKeyCreated, domain.EventDeliveryKeyScopeChanged, domain.EventDeliveryKeyRevoked} {
		if err := r.Subscribe(typ, "release.sync_delivery_key", outbox.HandlerFunc(s.handleKeyChanged)); err != nil {
			return err
		}
	}
	for typ, h := range map[string]outbox.HandlerFunc{
		catalogBranchOpened:            s.handleBranchLive,
		catalogBranchPushed:            s.handleBranchLive,
		catalogBranchReopened:          s.handleBranchLive,
		catalogBranchClosed:            s.handleBranchGone,
		catalogBranchMerged:            s.handleBranchGone,
		localizationTranslationRevised: s.handleTranslationRevised,
	} {
		if err := r.Subscribe(typ, subscriberBranchEnvironments, h); err != nil {
			return err
		}
	}
	return r.Subscribe(catalogProjectDeleted, "release.retire_project", outbox.HandlerFunc(s.handleProjectDeleted))
}

func parseID(field, s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, outbox.Permanent(fmt.Errorf("release: event %s %q: %w", field, s, err))
	}
	return id, nil
}

func (s *Service) handlePointerMoved(ctx context.Context, d outbox.Delivery) error {
	var e struct {
		ProjectID   string `json:"project_id"`
		Environment string `json:"environment"`
	}
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := parseID("project_id", e.ProjectID)
	if err != nil {
		return err
	}
	if !validName(e.Environment) {
		return outbox.Permanent(fmt.Errorf("release: event environment %q", e.Environment))
	}
	return s.SyncEnvironment(ctx, project, e.Environment)
}

func (s *Service) handleKeyChanged(ctx context.Context, d outbox.Delivery) error {
	var e domain.DeliveryKeyChanged
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := parseID("project_id", e.ProjectID)
	if err != nil {
		return err
	}
	id, err := parseID("key_id", e.KeyID)
	if err != nil {
		return err
	}
	return s.SyncDeliveryKey(ctx, project, id)
}

// handleProjectDeleted retires a deleted project's delivery: it is
// idempotent, since everything it does is a no-op the second time.
func (s *Service) handleProjectDeleted(ctx context.Context, d outbox.Delivery) error {
	var e struct {
		ProjectID string `json:"project_id"`
	}
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := parseID("project_id", e.ProjectID)
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		keys, err := st.ActiveDeliveryKeys(ctx, project)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := k.Revoke(systemActor, s.now()); err != nil && !errors.Is(err, domain.ErrKeyRevoked) {
				return err
			}
			if err := st.RevokeDeliveryKey(ctx, k); err != nil {
				return err
			}
			if err := s.syncKey(ctx, st, k); err != nil {
				return err
			}
		}
		names, err := st.DeleteProjectEnvironments(ctx, project)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := s.remove(ctx, delivery.ManifestPath(project.String(), name)); err != nil {
				return err
			}
		}
		return nil
	})
}
