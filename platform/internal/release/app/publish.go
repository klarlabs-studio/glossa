package app

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// PublishInput is what to publish where.
type PublishInput struct {
	Environment string
	Note        string
	// Force publishes although the environment's check policy refuses it
	// (RFC 0005 §4.1). ForceReason is mandatory with it and is recorded
	// in the environment's deployment history.
	Force       bool
	ForceReason string
}

// Publish builds a release of the project under the environment's
// policy, stores its artifacts, records it and points the environment
// at it. A repeated idemKey returns the first request's release with
// replayed set.
//
// The expensive part — reading the catalog, serializing and uploading —
// happens before the transaction that records the release. Artifacts
// are content-addressed, so uploading one that exists is skipped:
// publishing an unchanged catalog uploads nothing.
//
// Between the build and the upload stands the project's check policy
// (RFC 0005 §4.1): a release that does not meet what the environment
// requires of it is refused with domain.ErrPolicyNotMet, and goes out
// only when in.Force carries a reason the deployment records.
//
// An environment that requires approval (RFC 0006 §5.1) holds the
// publish: the release is built, gated, stored and recorded as above,
// a release request is made, and no pointer moves. The error is then a
// *domain.HeldError (domain.ErrApprovalRequired) carrying the request,
// beside the recorded release. A forced publish is held like any other:
// force overrides the gate, never the approval.
func (s *Service) Publish(ctx context.Context, project uuid.UUID, in PublishInput, idemKey string) (domain.Release, bool, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Release{}, false, err
	}
	name, err := domain.ParseEnvironmentName(in.Environment)
	if err != nil {
		return domain.Release{}, false, err
	}
	// A force is refused for want of a reason before anything is read or
	// built: the caller learns it the moment they ask, not after the
	// expensive part.
	var override domain.Override
	if in.Force {
		if override, err = domain.NewOverride(in.ForceReason); err != nil {
			return domain.Release{}, false, err
		}
	}
	id, keyed, err := idempotentID("release.publish", project.String(), by, idemKey)
	if err != nil {
		return domain.Release{}, false, err
	}
	env, first, err := s.prepare(ctx, project, name, id, keyed)
	if err != nil || first != nil {
		if first != nil {
			rel, replayed, err := s.replay(*first, in)
			if err == nil {
				err = s.heldReplay(ctx, rel)
			}
			return rel, replayed, err
		}
		return domain.Release{}, false, err
	}
	built, err := s.build(ctx, project, env)
	if err != nil {
		return domain.Release{}, false, err
	}
	// The gate before the upload: a publish the policy refuses writes no
	// artifacts.
	requested := override
	verdict, override, err := s.gate(ctx, project, env, built, override)
	if err != nil {
		return domain.Release{}, false, err
	}
	if built.Stats.NewArtifacts, err = s.upload(ctx, project, built.Artifacts); err != nil {
		return domain.Release{}, false, err
	}
	rel, held, replayed, err := s.record(ctx, id, project, env, built, in.Note, by, gated{
		verdict: verdict, override: override, requested: requested,
	})
	if err != nil || replayed {
		if replayed {
			rel, replayed, err := s.replay(rel, in)
			if err == nil {
				err = s.heldReplay(ctx, rel)
			}
			return rel, replayed, err
		}
		return domain.Release{}, false, err
	}
	if held != nil {
		return rel, false, &domain.HeldError{Request: *held}
	}
	s.syncNow(ctx, project, name)
	return rel, false, nil
}

// gated is what the publish gate said about a build: its verdict, the
// override to record if the build deploys now, and the force the
// publisher asked for, which a held request keeps for its deploy.
type gated struct {
	verdict   domain.GateVerdict
	override  domain.Override
	requested domain.Override
}

// policyGate resolves what the project's check policy asks of one
// environment (RFC 0005 §4.1): the locales that must be complete there
// and the review state its text must have reached. Publishing and
// promoting both go through it, so the two paths cannot decide the
// question differently.
//
// The policy is read through Release's Source port — Catalog's
// application service — never out of Catalog's tables, and always
// before a transaction opens: Catalog runs its own, and nesting them is
// how deadlocks are built.
func (s *Service) policyGate(ctx context.Context, project uuid.UUID, environment string) (domain.PolicyGate, error) {
	doc, err := s.source.CheckPolicy(ctx, project)
	if err != nil {
		return domain.PolicyGate{}, err
	}
	return domain.NewPolicyGate(doc, environment), nil
}

// gate holds the publish to the environment's check policy and returns
// its verdict and the override to record.
func (s *Service) gate(ctx context.Context, project uuid.UUID, env domain.Environment, built domain.Built, override domain.Override) (domain.GateVerdict, domain.Override, error) {
	g, err := s.policyGate(ctx, project, env.Name)
	if err != nil {
		return domain.GateVerdict{}, domain.Override{}, err
	}
	// env.Policy is what the release is being built under, which is what
	// a stored release records as its own: the gate sees the same pair
	// of facts here as it does on promote.
	verdict := domain.VerdictOf(g.Check(env.Policy, built.Content, built.Stats))
	override, err = g.Enforce(env.Policy, built.Content, built.Stats, override)
	return verdict, override, err
}

// prepare ensures the environments exist and returns the target one, or
// the release an earlier request with the same key already recorded.
func (s *Service) prepare(ctx context.Context, project uuid.UUID, name string, id uuid.UUID, keyed bool) (domain.Environment, *domain.Release, error) {
	var (
		env   domain.Environment
		first *domain.Release
	)
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if keyed {
			r, err := st.Release(ctx, project, id)
			if err == nil {
				first = &r
				return nil
			}
			if !isNotFound(err) {
				return err
			}
		}
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		var err error
		env, err = st.Environment(ctx, project, name, false)
		return err
	})
	return env, first, err
}

func (s *Service) replay(first domain.Release, in PublishInput) (domain.Release, bool, error) {
	if first.Environment != in.Environment || first.Note != in.Note {
		return domain.Release{}, false, ErrIdempotencyReuse
	}
	return first, true, nil
}

// build is the one build of a release, shared by Publish and
// PreviewPublish: the project's releasable source and the translations
// env's policy makes eligible, turned into artifacts — plus, for a
// branch environment, that branch's overlay and no other (RFC 0004
// §4.2). It reads; it stores nothing.
func (s *Service) build(ctx context.Context, project uuid.UUID, env domain.Environment) (domain.Built, error) {
	snap, err := s.source.Snapshot(ctx, project, env.Policy.States)
	if err != nil {
		return domain.Built{}, err
	}
	if env.Kind != domain.KindBranch {
		return domain.Build(snap, env.Policy)
	}
	overlay, err := s.source.BranchOverlay(ctx, project, env.Branch)
	if err != nil {
		return domain.Built{}, err
	}
	return domain.BuildBranch(snap.WithOverlay(overlay), env.Policy)
}

// missing returns the artifacts storage doesn't have yet.
func (s *Service) missing(ctx context.Context, project uuid.UUID, artifacts []domain.Artifact) ([]domain.Artifact, error) {
	var out []domain.Artifact
	for _, a := range artifacts {
		ok, err := s.objects.Exists(ctx, delivery.ArtifactPath(project.String(), a.Ref.SHA256))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStorage, err)
		}
		if !ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// upload stores the artifacts storage doesn't have yet and returns how
// many it wrote.
func (s *Service) upload(ctx context.Context, project uuid.UUID, artifacts []domain.Artifact) (int, error) {
	missing, err := s.missing(ctx, project, artifacts)
	if err != nil {
		return 0, err
	}
	for i, a := range missing {
		if err := s.objects.Put(ctx, delivery.ArtifactPath(project.String(), a.Ref.SHA256), a.Body, "application/json"); err != nil {
			return i, fmt.Errorf("%w: %v", ErrStorage, err)
		}
	}
	return len(missing), nil
}

// record commits the release and the pointer move in one transaction —
// or, when the environment requires approval, the release and a request
// to deploy it (held), moving nothing.
func (s *Service) record(ctx context.Context, id, project uuid.UUID, target domain.Environment, built domain.Built, note, by string, g gated) (domain.Release, *domain.ReleaseRequest, bool, error) {
	var (
		rel      domain.Release
		held     *domain.ReleaseRequest
		replayed bool
	)
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		envs, err := st.LockProjectEnvironments(ctx, project)
		if err != nil {
			return err
		}
		env, ok := findEnvironment(envs, target.Name)
		if !ok {
			return ErrNotFound
		}
		version, err := st.MaxReleaseVersion(ctx, project)
		if err != nil {
			return err
		}
		// Built under target's policy (and overlay): the one read before
		// building, even if the environment changed since.
		rel, err = domain.NewRelease(id, project, version+1, env.Current, target, built, note, by, s.now())
		if err != nil {
			return err
		}
		inserted, err := st.InsertRelease(ctx, rel)
		if err != nil {
			return err
		}
		if !inserted { // the same Idempotency-Key, committed by a concurrent request
			rel, err = st.Release(ctx, project, id)
			replayed = true
			return err
		}
		// The approval requirement read under the lock decides: one set
		// while this release was being built still holds it.
		if env.Approval != nil {
			req, err := s.hold(ctx, st, env, rel, domain.ActionPublish, by, g.verdict, g.requested)
			held = &req
			return err
		}
		if err := s.move(ctx, st, &env, rel, domain.ActionPublish, by, g.override); err != nil {
			return err
		}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventPublished, AggregateType: domain.AggregateRelease, AggregateID: rel.ID.String(), Actor: outbox.Actor(by),
			Payload: domain.Published{
				ReleaseID: rel.ID.String(), ProjectID: project.String(), Version: rel.Version, Environment: env.Name,
				ParentID: optionalID(rel.Parent), ManifestDigest: rel.Digest, Messages: rel.Stats.Messages, By: by,
			},
		})
	})
	return rel, held, replayed, err
}

func findEnvironment(envs []domain.Environment, name string) (domain.Environment, bool) {
	for _, e := range envs {
		if e.Name == name {
			return e, true
		}
	}
	return domain.Environment{}, false
}

// move points env at rel and appends the deployment to its history,
// carrying the override that let the move past the environment's check
// policy, if any. An active rollout in env refuses a publish or promote
// and is aborted by a rollback (rolloutBeforeMove).
func (s *Service) move(ctx context.Context, st Store, env *domain.Environment, rel domain.Release, action domain.Action, by string, override domain.Override) error {
	if env.Current == rel.ID {
		return nil
	}
	if err := s.rolloutBeforeMove(ctx, st, *env, action, by); err != nil {
		return err
	}
	previous, expected := env.Current, env.Version
	if !env.Point(rel.ID, s.now()) {
		return nil
	}
	if err := st.UpdateEnvironment(ctx, *env, expected); err != nil {
		return err
	}
	n, err := st.LastDeploymentNumber(ctx, env.ProjectID, env.Name)
	if err != nil {
		return err
	}
	return st.AppendDeployment(ctx, domain.Deployment{
		ProjectID: env.ProjectID, Environment: env.Name, Number: n + 1, ReleaseID: rel.ID, Previous: previous,
		Action: action, By: by, CreatedAt: env.UpdatedAt, Override: override,
	})
}

func optionalID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// GetRelease returns one release.
func (s *Service) GetRelease(ctx context.Context, project, id uuid.UUID) (domain.Release, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return domain.Release{}, err
	}
	var r domain.Release
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		r, err = st.Release(ctx, project, id)
		return err
	})
	return r, err
}

// ListReleases lists a project's releases, newest first.
func (s *Service) ListReleases(ctx context.Context, project uuid.UUID, page pagination.Page) ([]domain.Release, *string, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, nil, err
	}
	before, err := beforeInt(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Release
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.Releases(ctx, project, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.Release) string { return strconv.Itoa(r.Version) })
	return items, next, nil
}
