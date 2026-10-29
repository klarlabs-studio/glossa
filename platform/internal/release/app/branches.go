package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Branch environments (RFC 0004 §4.2): one per open branch, built from
// the main catalog plus that branch's overlay. Their lifecycle follows
// the branch — open, pushed any number of times, merged or closed, then
// destroyed — through the hooks below, which the Branches API and the
// GitHub webhooks call.

// OpenBranchEnvironment is the create-on-open hook: it creates branch's
// environment, pr-<pr> for a pull request (pr > 0) and br-<8 hex of
// sha256(branch)> otherwise, with the fixed preview policy. A branch
// has one environment: opening it again returns it (created false),
// whatever it was named. A project has at most 50
// (domain.ErrTooManyBranches).
func (s *Service) OpenBranchEnvironment(ctx context.Context, project uuid.UUID, branch string, pr int) (domain.Environment, bool, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Environment{}, false, err
	}
	e, err := domain.NewBranchEnvironment(project, branch, pr, s.now())
	if err != nil {
		return domain.Environment{}, false, err
	}
	created := false
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		if existing, err := st.BranchEnvironment(ctx, project, branch, false); err == nil {
			e = existing
			return nil
		} else if !isNotFound(err) {
			return err
		}
		// Opening serializes with publishes and other opens of the
		// project, so the limit holds under concurrency.
		if _, err := st.LockProjectEnvironments(ctx, project); err != nil {
			return err
		}
		n, err := st.CountBranchEnvironments(ctx, project)
		if err != nil {
			return err
		}
		if n >= domain.MaxBranchEnvironments {
			return domain.ErrTooManyBranches
		}
		inserted, err := st.InsertEnvironment(ctx, e, by)
		if err != nil {
			return err
		}
		if !inserted {
			return ErrEnvironmentExists // pr-<n> taken by another branch
		}
		created = true
		return st.Publish(ctx, environmentEvent(domain.EventEnvironmentCreated, e, by))
	})
	if err != nil {
		return domain.Environment{}, false, err
	}
	return e, created, nil
}

// RequestBranchPublish is the publish-on-push hook, also called when a
// translation of one of the branch's messages changes: it asks for the
// branch environment to be published again, debounced
// (domain.PublishDebounce after the last request, at most
// domain.MaxPublishDelay after the first). It is idempotent: a burst of
// requests is one pending publish, which the Publisher runs once due.
func (s *Service) RequestBranchPublish(ctx context.Context, project uuid.UUID, branch string) (domain.PublishRequest, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.PublishRequest{}, err
	}
	var r domain.PublishRequest
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		env, err := st.BranchEnvironment(ctx, project, branch, true)
		if err != nil {
			return err
		}
		r, err = st.PublishRequest(ctx, project, env.Name)
		switch {
		case isNotFound(err):
			r = domain.NewPublishRequest(project, env.Name, by, s.now())
		case err != nil:
			return err
		default:
			r.Renew(by, s.now())
		}
		if err := st.SavePublishRequest(ctx, r); err != nil {
			return err
		}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventPublishRequested, AggregateType: domain.AggregateEnvironment,
			AggregateID: project.String() + "/" + env.Name, Actor: outbox.Actor(by),
			Payload: domain.PublishRequested{
				ProjectID: project.String(), Environment: env.Name, Branch: branch, RequestID: r.ID.String(),
				NotBefore: r.NotBefore, By: by,
			},
		})
	})
	return r, err
}

// PublishDue publishes an environment whose publish request is due and
// clears the request; published is false when there is no request or it
// isn't due yet. The request's ID is the publish's idempotency key, so
// running it twice (two publishers, a retry after a crash) publishes
// once. A request made while publishing stays pending.
func (s *Service) PublishDue(ctx context.Context, project uuid.UUID, environment string) (bool, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesPublish); err != nil {
		return false, err
	}
	var (
		r   domain.PublishRequest
		due bool
	)
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		r, err = st.PublishRequest(ctx, project, environment)
		if isNotFound(err) {
			return nil
		}
		due = err == nil && r.Due(s.now())
		return err
	})
	if err != nil || !due {
		return false, err
	}
	_, _, pubErr := s.Publish(ctx, project, PublishInput{Environment: environment, Note: autoPublishNote}, r.ID.String())
	// A storage outage is retried; a catalog the build can't release
	// waits for the branch's next push (retrying can't fix it), and a
	// destroyed environment took its request with it.
	if pubErr != nil && !errors.Is(pubErr, domain.ErrNotReleasable) && !errors.Is(pubErr, ErrNotFound) {
		return false, pubErr
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return st.DeletePublishRequest(ctx, r)
	})
	if err != nil {
		return false, err
	}
	if errors.Is(pubErr, domain.ErrNotReleasable) {
		return false, pubErr
	}
	return pubErr == nil, nil
}

// autoPublishNote marks the releases PublishDue makes.
const autoPublishNote = "Published automatically after changes on the branch."

// DestroyBranchEnvironment is the destroy hook, for a merged or closed
// branch: it deletes the branch's environment and its pending publish,
// and removes its manifest, so glossa-edge answers 404 for it. Its
// releases and deployments stay as history. Destroying a branch without
// an environment does nothing.
func (s *Service) DestroyBranchEnvironment(ctx context.Context, project uuid.UUID, branch string) error {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return err
	}
	var env domain.Environment
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if env, err = st.BranchEnvironment(ctx, project, branch, true); err != nil {
			return err
		}
		if _, err := st.DeleteEnvironment(ctx, project, env.Name); err != nil {
			return err
		}
		return st.Publish(ctx, environmentEvent(domain.EventEnvironmentDestroyed, env, by))
	})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	s.syncNow(ctx, project, env.Name)
	return nil
}
