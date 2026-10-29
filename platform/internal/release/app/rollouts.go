package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Staged rollouts (RFC 0006 §5.2). A rollout changes what an
// environment's manifest says without moving its pointer until it
// completes: start, advance and abort each rewrite the manifest (the
// `rollout` member appears, changes, or goes), and complete moves the
// pointer to the candidate the way a promote does. Every change locks
// the environment's row first, then its rollout's, so a rollout and its
// environment never change apart and the manifest writer always sees
// both as committed together.
//
// A publish or promote into an environment with an active rollout is
// refused (domain.ErrRolloutActive): it would replace the stable side
// under installations that the rollout is comparing it with. A rollback
// is never refused (RFC 0006 §5.1, §14 decision 8): it aborts the
// rollout in the same transaction, so every installation lands on the
// rollback target at once.

// RolloutInput is what starting a rollout asks for.
type RolloutInput struct {
	// Release is the candidate: a release of the project the
	// environment's policy covers, as a promote would need.
	Release uuid.UUID
	// Percent is the share of installations in the candidate, 0–100.
	Percent int
	// MaxDuration bounds the rollout; zero is 14 days. The sweep aborts
	// it then.
	MaxDuration time.Duration
	// Force starts the rollout although the environment's check policy
	// refuses the candidate (RFC 0005 §4.1). ForceReason is mandatory
	// with it, and the deployment that completes the rollout records it.
	Force       bool
	ForceReason string
}

// StartRollout starts serving a candidate release to a share of an
// environment's installations. The environment must serve a release
// (the stable side) and have no active rollout. The candidate is held
// to what a promote into the environment is held to: the environment's
// policy must cover it, and the check-policy gate must pass or be
// forced with a reason. A repeated idemKey returns the first request's
// rollout with replayed set.
func (s *Service) StartRollout(ctx context.Context, project uuid.UUID, name string, in RolloutInput, idemKey string) (domain.Rollout, bool, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Rollout{}, false, err
	}
	if !validName(name) {
		return domain.Rollout{}, false, ErrNotFound
	}
	var override domain.Override
	if in.Force {
		if override, err = domain.NewOverride(in.ForceReason); err != nil {
			return domain.Rollout{}, false, err
		}
	}
	id, keyed, err := idempotentID("release.rollout.start", project.String()+"/"+name, by, idemKey)
	if err != nil {
		return domain.Rollout{}, false, err
	}
	// Resolved before the transaction opens: Catalog's service runs its
	// own.
	gate, err := s.policyGate(ctx, project, name)
	if err != nil {
		return domain.Rollout{}, false, err
	}
	var (
		ro       domain.Rollout
		replayed bool
	)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if keyed {
			first, err := st.Rollout(ctx, project, id)
			if err == nil {
				ro, replayed = first, true
				if first.Environment != name || first.Candidate != in.Release {
					return ErrIdempotencyReuse
				}
				return nil
			}
			if !isNotFound(err) {
				return err
			}
		}
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		env, err := st.Environment(ctx, project, name, true)
		if err != nil {
			return err
		}
		if _, err := st.ActiveRollout(ctx, project, name, true); err == nil {
			return domain.ErrRolloutActive
		} else if !isNotFound(err) {
			return err
		}
		candidate, err := st.Release(ctx, project, in.Release)
		if isNotFound(err) {
			return ErrReleaseNotInProject
		}
		if err != nil {
			return err
		}
		var stable domain.Release
		if env.Current != uuid.Nil {
			if stable, err = st.Release(ctx, project, env.Current); err != nil {
				return err
			}
		}
		ro, err = domain.StartRollout(id, env, stable, candidate, domain.RolloutStart{
			Percent: in.Percent, MaxDuration: in.MaxDuration, Salt: domain.NewSalt(),
			ApprovalRequired: approvalRequired(env),
		}, by, s.now())
		if err != nil {
			return err
		}
		// candidate.Policy: the gate asks what the release carries, as a
		// promote's does.
		if ro.Override, err = gate.Enforce(candidate.Policy, candidate.Content, candidate.Stats, override); err != nil {
			return err
		}
		inserted, err := st.InsertRollout(ctx, ro)
		if err != nil {
			return err
		}
		if !inserted { // a concurrent start won the environment's one active slot
			return domain.ErrRolloutActive
		}
		return st.Publish(ctx, rolloutEvent(domain.EventRolloutStarted, ro, nil, by))
	})
	if err != nil {
		return domain.Rollout{}, false, err
	}
	if !replayed {
		s.syncNow(ctx, project, name)
	}
	return ro, replayed, nil
}

// approvalRequired reports whether moving env's pointer needs release
// approvals (RFC 0006 §5.1). RFC 0006 §5.2 says starting a rollout into
// such an environment needs them as a publish would; until a release
// request can carry a rollout, StartRollout refuses it
// (domain.ErrRolloutNeedsApproval) rather than starting it unapproved.
func approvalRequired(env domain.Environment) bool { return env.Approval != nil }

// AdvanceRollout changes the share of installations in the candidate.
// Any percent may follow any other (RFC 0006 §15 Q5), and advancing
// needs no approval (§5.2). ifMatch, when given, is the rollout's
// version.
func (s *Service) AdvanceRollout(ctx context.Context, project uuid.UUID, name string, id uuid.UUID, percent int, ifMatch *int) (domain.Rollout, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Rollout{}, err
	}
	var changed bool
	ro, err := s.changeRollout(ctx, project, name, id, ifMatch, func(ctx context.Context, st Store, _ *domain.Environment, ro *domain.Rollout) error {
		previous := ro.Percent
		var err error
		if changed, err = ro.Advance(percent, s.now()); err != nil || !changed {
			return err
		}
		return st.Publish(ctx, rolloutEvent(domain.EventRolloutAdvanced, *ro, &previous, by))
	})
	if err == nil && changed {
		s.syncNow(ctx, project, name)
	}
	return ro, err
}

// CompleteRollout makes the candidate the environment's release: the
// pointer moves to it as a promote moves it, the deployment records the
// override the rollout started with, and the manifest drops `rollout`
// with the candidate now its top-level release. The gate is not asked
// again: it was asked when the rollout started, about the same
// immutable release.
func (s *Service) CompleteRollout(ctx context.Context, project uuid.UUID, name string, id uuid.UUID, ifMatch *int) (domain.Rollout, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Rollout{}, err
	}
	ro, err := s.changeRollout(ctx, project, name, id, ifMatch, func(ctx context.Context, st Store, env *domain.Environment, ro *domain.Rollout) error {
		candidate, err := st.Release(ctx, project, ro.Candidate)
		if err != nil {
			return err
		}
		expected := ro.Version
		if err := ro.Complete(by, s.now()); err != nil {
			return err
		}
		// Saved before the pointer moves, so the move sees no active
		// rollout in its way.
		if err := st.UpdateRollout(ctx, *ro, expected); err != nil {
			return err
		}
		previous := env.Current
		if err := s.move(ctx, st, env, candidate, domain.ActionPromote, by, ro.Override); err != nil {
			return err
		}
		if err := st.Publish(ctx, outbox.Event{
			Type: domain.EventPromoted, AggregateType: domain.AggregateRelease, AggregateID: candidate.ID.String(), Actor: outbox.Actor(by),
			Payload: domain.PointerMoved{
				ReleaseID: candidate.ID.String(), ProjectID: project.String(), Version: candidate.Version, Environment: name,
				PreviousReleaseID: optionalID(previous), By: by,
			},
		}); err != nil {
			return err
		}
		return st.Publish(ctx, rolloutEvent(domain.EventRolloutCompleted, *ro, nil, by))
	}, saved())
	if err == nil {
		s.syncNow(ctx, project, name)
	}
	return ro, err
}

// AbortRollout ends the rollout and leaves the environment's pointer
// where it is: the manifest drops `rollout`, and every installation
// returns to the stable release on its next refresh. Aborting needs no
// approval and is never delayed (RFC 0006 §5.2).
func (s *Service) AbortRollout(ctx context.Context, project uuid.UUID, name string, id uuid.UUID, ifMatch *int) (domain.Rollout, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Rollout{}, err
	}
	ro, err := s.abortRollout(ctx, project, name, id, ifMatch, domain.EndAborted, by)
	if err == nil {
		s.syncNow(ctx, project, name)
	}
	return ro, err
}

// expireRollout aborts a rollout past its max_duration, as the sweep's
// principal. A rollout that ended or was extended meanwhile is left
// alone.
func (s *Service) expireRollout(ctx context.Context, ref RolloutRef) (bool, error) {
	by, err := s.checkProject(ctx, ref.Project, authz.ReleasesPublish)
	if err != nil {
		return false, err
	}
	var expired bool
	_, err = s.changeRollout(ctx, ref.Project, ref.Environment, ref.Rollout, nil, func(ctx context.Context, st Store, _ *domain.Environment, ro *domain.Rollout) error {
		if !ro.Expired(s.now()) {
			return nil
		}
		expired = true
		if err := ro.Abort(domain.EndExpired, by, s.now()); err != nil {
			return err
		}
		return st.Publish(ctx, rolloutEvent(domain.EventRolloutAborted, *ro, nil, by))
	})
	if errors.Is(err, domain.ErrRolloutEnded) || isNotFound(err) {
		return false, nil
	}
	if err == nil && expired {
		s.syncNow(ctx, ref.Project, ref.Environment)
	}
	return expired, err
}

func (s *Service) abortRollout(ctx context.Context, project uuid.UUID, name string, id uuid.UUID, ifMatch *int, why domain.RolloutEnd, by string) (domain.Rollout, error) {
	return s.changeRollout(ctx, project, name, id, ifMatch, func(ctx context.Context, st Store, _ *domain.Environment, ro *domain.Rollout) error {
		if err := ro.Abort(why, by, s.now()); err != nil {
			return err
		}
		return st.Publish(ctx, rolloutEvent(domain.EventRolloutAborted, *ro, nil, by))
	})
}

// changeOption adjusts changeRollout.
type changeOption func(*changeConfig)

type changeConfig struct {
	// saved: the change saves the rollout itself (complete must end it
	// before the pointer moves).
	saved bool
}

func saved() changeOption { return func(c *changeConfig) { c.saved = true } }

// changeRollout locks the environment and then its active rollout,
// which must be id, lets change modify it, and saves it if its version
// moved. An ended rollout is domain.ErrRolloutEnded; a rollout of
// another environment, or none, is ErrNotFound.
func (s *Service) changeRollout(ctx context.Context, project uuid.UUID, name string, id uuid.UUID, ifMatch *int,
	change func(context.Context, Store, *domain.Environment, *domain.Rollout) error, opts ...changeOption,
) (domain.Rollout, error) {
	var cfg changeConfig
	for _, o := range opts {
		o(&cfg)
	}
	if !validName(name) {
		return domain.Rollout{}, ErrNotFound
	}
	var ro domain.Rollout
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		env, err := st.Environment(ctx, project, name, true)
		if err != nil {
			return err
		}
		if ro, err = st.ActiveRollout(ctx, project, name, true); isNotFound(err) || (err == nil && ro.ID != id) {
			return s.inactiveRollout(ctx, st, project, name, id)
		} else if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != ro.Version {
			return ErrPreconditionFailed
		}
		expected := ro.Version
		if err := change(ctx, st, &env, &ro); err != nil {
			return err
		}
		if cfg.saved || ro.Version == expected {
			return nil
		}
		return st.UpdateRollout(ctx, ro, expected)
	})
	return ro, err
}

// inactiveRollout explains why id is not the environment's active
// rollout: it ended, or it isn't one of the environment's at all.
func (s *Service) inactiveRollout(ctx context.Context, st Store, project uuid.UUID, name string, id uuid.UUID) error {
	ro, err := st.Rollout(ctx, project, id)
	if err != nil {
		return err
	}
	if ro.Environment != name {
		return ErrNotFound
	}
	return domain.ErrRolloutEnded
}

// rolloutBeforeMove is what an active rollout means to a pointer move
// into its environment: a rollback aborts it, in the move's
// transaction, so rollback stays immediate and lands every installation
// on its target; a publish or promote is refused. The caller holds the
// environment's lock.
func (s *Service) rolloutBeforeMove(ctx context.Context, st Store, env domain.Environment, action domain.Action, by string) error {
	ro, err := st.ActiveRollout(ctx, env.ProjectID, env.Name, true)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if action != domain.ActionRollback {
		return domain.ErrRolloutActive
	}
	expected := ro.Version
	if err := ro.Abort(domain.EndRolledBack, by, s.now()); err != nil {
		return err
	}
	if err := st.UpdateRollout(ctx, ro, expected); err != nil {
		return err
	}
	return st.Publish(ctx, rolloutEvent(domain.EventRolloutAborted, ro, nil, by))
}

func rolloutEvent(typ string, ro domain.Rollout, previousPercent *int, by string) outbox.Event {
	payload := ro.Changed(by)
	payload.PreviousPercent = previousPercent
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateRollout, AggregateID: ro.ID.String(), Actor: outbox.Actor(by),
		Payload: payload,
	}
}

// GetRollout returns one of the project's rollouts.
func (s *Service) GetRollout(ctx context.Context, project, id uuid.UUID) (domain.Rollout, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return domain.Rollout{}, err
	}
	var ro domain.Rollout
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		ro, err = st.Rollout(ctx, project, id)
		return err
	})
	return ro, err
}

// ListRollouts lists an environment's rollouts, newest first: the
// active one, if any, and the history of ended ones, a page at a time.
func (s *Service) ListRollouts(ctx context.Context, project uuid.UUID, name string, page pagination.Page) ([]domain.Rollout, *string, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, nil, err
	}
	if !validName(name) {
		return nil, nil, ErrNotFound
	}
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Rollout
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		if _, err := st.Environment(ctx, project, name, false); err != nil {
			return err
		}
		var err error
		rows, err = st.Rollouts(ctx, project, name, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.Rollout) string { return r.ID.String() })
	return items, next, nil
}
