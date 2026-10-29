package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// PromoteInput is how a promote may override the destination's check
// policy, in the shape a publish uses.
type PromoteInput struct {
	// Force promotes although the destination environment's check policy
	// refuses the release (RFC 0005 §4.1). ForceReason is mandatory with
	// it and is recorded in the environment's deployment history.
	Force       bool
	ForceReason string
}

// Promote points an environment at an existing release of the project
// (staging's release into production). Nothing is rebuilt: the
// release's artifacts are already in storage, and only the
// environment's manifest is written. The environment's policy must
// cover the policy the release was built under, so a preview release
// with drafts never reaches production, and a branch release is never
// promoted (branch_release_not_promotable): it holds text that exists
// only on its branch. Promoting the release an environment already
// serves changes nothing.
//
// The destination's check policy is enforced here as it is at publish,
// against the same gate and the same inheritance rules (RFC 0005 §4.1).
// Without it the gate would be walk-around-able by design — a release
// production refused could reach production by way of staging — which
// is worse than an absent gate, because the deployment history would
// look policed. It is forceable on the same terms: only with a reason,
// recorded on the deployment.
//
// A destination that requires approval (RFC 0006 §5.1) holds the
// promote once every check above has passed: a release request is made
// and no pointer moves; the error is a *domain.HeldError
// (domain.ErrApprovalRequired) and the environment is returned as it
// is. Promoting the release it already serves makes no request.
func (s *Service) Promote(ctx context.Context, project uuid.UUID, name string, release uuid.UUID, in PromoteInput) (domain.Environment, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Environment{}, err
	}
	if !validName(name) {
		return domain.Environment{}, ErrNotFound
	}
	// Refused for want of a reason before anything is read, as a publish
	// is.
	var override domain.Override
	if in.Force {
		if override, err = domain.NewOverride(in.ForceReason); err != nil {
			return domain.Environment{}, err
		}
	}
	// Resolved before the transaction opens: Catalog's service runs its
	// own.
	gate, err := s.policyGate(ctx, project, name)
	if err != nil {
		return domain.Environment{}, err
	}
	var held *domain.ReleaseRequest
	env, moved, err := s.pointer(ctx, project, name, func(ctx context.Context, st Store, env domain.Environment) (domain.Release, domain.Override, error) {
		rel, err := st.Release(ctx, project, release)
		if isNotFound(err) {
			return domain.Release{}, domain.Override{}, ErrReleaseNotInProject
		}
		if err != nil {
			return domain.Release{}, domain.Override{}, err
		}
		if err := rel.Promotable(); err != nil {
			return domain.Release{}, domain.Override{}, err
		}
		if !env.Policy.Covers(rel.Policy) {
			return domain.Release{}, domain.Override{},
				domain.Ineligible(env.Name, env.Policy, rel.Version, rel.Environment, rel.Policy)
		}
		// rel.Policy, not env.Policy: the gate asks what the release
		// actually carries, which is the policy it was built under.
		// Covers already refuses a release holding states the
		// destination excludes, but an environment whose own policy
		// ships drafts may still require approved text here.
		ov, err := gate.Enforce(rel.Policy, rel.Content, rel.Stats, override)
		if err != nil || env.Approval == nil || env.Current == rel.ID {
			return rel, ov, err
		}
		verdict := domain.VerdictOf(gate.Check(rel.Policy, rel.Content, rel.Stats))
		req, err := s.hold(ctx, st, env, rel, domain.ActionPromote, by, verdict, override)
		if err != nil {
			return domain.Release{}, domain.Override{}, err
		}
		held = &req
		return domain.Release{}, domain.Override{}, errHeld
	}, domain.ActionPromote, domain.EventPromoted, by)
	if held != nil && errors.Is(err, errHeld) {
		return env, &domain.HeldError{Request: *held}
	}
	if err == nil && moved {
		s.syncNow(ctx, project, name)
	}
	return env, err
}

// errHeld ends a pointer move that became a release request: the move
// does not happen, and the request the transaction recorded commits
// (pointer treats it as an outcome, not a failure).
var errHeld = errors.New("release: held for approval")

// Rollback points an environment back at a release it served before:
// release, or by default the newest release it served that is older than
// the one it serves now (so rolling back twice walks two steps back).
// Like promote, it only moves the pointer.
//
// The check policy is deliberately not enforced here (RFC 0005 §4.1).
// A rollback target is, by construction, a release this environment has
// already served — RollbackTarget and ServedIn allow nothing else — so
// rolling back can never make an environment newly serve text that did
// not pass the gate on its way in. Gating it would instead mean that
// tightening a policy strands an environment on the release that broke
// it, because the release it wants to return to no longer satisfies the
// policy of today. That turns the safety valve into a trap, during an
// incident, which is the failure §4.1 names: a hard block with no
// escape hatch gets routed around by disabling the check.
func (s *Service) Rollback(ctx context.Context, project uuid.UUID, name string, release *uuid.UUID) (domain.Environment, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Environment{}, err
	}
	if !validName(name) {
		return domain.Environment{}, ErrNotFound
	}
	env, moved, err := s.pointer(ctx, project, name, func(ctx context.Context, st Store, env domain.Environment) (domain.Release, domain.Override, error) {
		if env.Current == uuid.Nil {
			return domain.Release{}, domain.Override{}, domain.ErrNoRollbackTarget
		}
		if release != nil {
			rel, err := explicitRollback(ctx, st, env, *release)
			return rel, domain.Override{}, err
		}
		current, err := st.Release(ctx, project, env.Current)
		if err != nil {
			return domain.Release{}, domain.Override{}, err
		}
		target, err := st.RollbackTarget(ctx, project, name, current.Version)
		if isNotFound(err) {
			return domain.Release{}, domain.Override{}, domain.ErrNoRollbackTarget
		}
		return target, domain.Override{}, err
	}, domain.ActionRollback, domain.EventRolledBack, by)
	if err == nil && moved {
		s.syncNow(ctx, project, name)
	}
	return env, err
}

func explicitRollback(ctx context.Context, st Store, env domain.Environment, id uuid.UUID) (domain.Release, error) {
	rel, err := st.Release(ctx, env.ProjectID, id)
	if isNotFound(err) {
		return domain.Release{}, ErrReleaseNotInProject
	}
	if err != nil {
		return domain.Release{}, err
	}
	served, err := st.ServedIn(ctx, env.ProjectID, env.Name, id)
	if err != nil {
		return domain.Release{}, err
	}
	if !served {
		return domain.Release{}, domain.ErrNotInHistory
	}
	return rel, nil
}

// pointer locks the environment, lets choose pick the release to serve
// and the override its move is recorded with, and moves the pointer if
// it changes.
func (s *Service) pointer(ctx context.Context, project uuid.UUID, name string,
	choose func(context.Context, Store, domain.Environment) (domain.Release, domain.Override, error),
	action domain.Action, event, by string,
) (domain.Environment, bool, error) {
	var (
		env   domain.Environment
		moved bool
		held  bool
	)
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		var err error
		if env, err = st.Environment(ctx, project, name, true); err != nil {
			return err
		}
		rel, override, err := choose(ctx, st, env)
		if errors.Is(err, errHeld) {
			held = true
			return nil
		}
		if err != nil {
			return err
		}
		previous := env.Current
		if previous == rel.ID {
			return nil
		}
		if err := s.move(ctx, st, &env, rel, action, by, override); err != nil {
			return err
		}
		moved = true
		return st.Publish(ctx, outbox.Event{
			Type: event, AggregateType: domain.AggregateRelease, AggregateID: rel.ID.String(), Actor: outbox.Actor(by),
			Payload: domain.PointerMoved{
				ReleaseID: rel.ID.String(), ProjectID: project.String(), Version: rel.Version, Environment: name,
				PreviousReleaseID: optionalID(previous), By: by,
			},
		})
	})
	if err == nil && held {
		err = errHeld
	}
	return env, moved, err
}
