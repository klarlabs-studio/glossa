package app

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// PublishPreview is what publishing to an environment would ship now: a
// publish's build, stored nowhere.
type PublishPreview struct {
	// Environment is the target and the policy the build used. A default
	// environment the project hasn't used yet is shown as it will be
	// created (its default policy, serving nothing).
	Environment domain.Environment
	// Base is the release the environment serves now; the zero Release
	// when it serves nothing (everything would be added).
	Base domain.Release
	// Built is the release's content and counts; Stats.NewArtifacts is
	// how many artifacts storage doesn't have yet. Empty when Problems
	// isn't.
	Built domain.Built
	// Digest is the manifest digest the release would have.
	Digest string
	// Changes compare what each locale would ship with Base's.
	Changes []domain.LocaleDiff
	// Problems are why the catalog can't be released (publishing would
	// fail with not_releasable).
	Problems []domain.Problem
}

// Releasable reports whether publishing would succeed as far as the
// catalog is concerned.
func (p PublishPreview) Releasable() bool { return len(p.Problems) == 0 }

// PreviewPublish runs a publish's build for an environment without
// recording, uploading or announcing anything: no rows, no objects, no
// events — it doesn't even create the default environments. It reads
// storage only to compare with the release the environment serves and to
// count the artifacts a publish would upload. Reading releases is enough
// to ask.
func (s *Service) PreviewPublish(ctx context.Context, project uuid.UUID, environment string) (PublishPreview, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return PublishPreview{}, err
	}
	if !validName(environment) {
		return PublishPreview{}, ErrNotFound
	}
	env, base, err := s.previewTarget(ctx, project, environment)
	if err != nil {
		return PublishPreview{}, err
	}
	pv := PublishPreview{Environment: env, Base: base}
	built, err := s.build(ctx, project, env)
	var nr *domain.NotReleasableError
	if errors.As(err, &nr) {
		pv.Problems = nr.Problems
		return pv, nil
	}
	if err != nil {
		return PublishPreview{}, err
	}
	if pv.Digest, err = built.Content.Digest(); err != nil {
		return PublishPreview{}, err
	}
	missing, err := s.missing(ctx, project, built.Artifacts)
	if err != nil {
		return PublishPreview{}, err
	}
	built.Stats.NewArtifacts = len(missing)
	pv.Built = built
	if pv.Changes, err = s.changes(ctx, project, base, built); err != nil {
		return PublishPreview{}, err
	}
	return pv, nil
}

// previewTarget reads the environment and the release it serves, without
// creating anything: an unused default environment is described as
// ensureDefaults would create it.
func (s *Service) previewTarget(ctx context.Context, project uuid.UUID, name string) (domain.Environment, domain.Release, error) {
	var (
		env  domain.Environment
		base domain.Release
	)
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		env, err = st.Environment(ctx, project, name, false)
		if isNotFound(err) && slices.Contains(domain.DefaultEnvironments, name) {
			env, err = domain.NewEnvironment(project, name, domain.DefaultPolicy(name), s.now())
		}
		if err != nil || env.Current == uuid.Nil {
			return err
		}
		base, err = st.Release(ctx, project, env.Current)
		return err
	})
	return env, base, err
}

// changes diffs a build against base, locale by locale. The build's
// artifacts are compared from memory; base's are read from storage,
// except those the build shares with it (same hash).
func (s *Service) changes(ctx context.Context, project uuid.UUID, base domain.Release, built domain.Built) ([]domain.LocaleDiff, error) {
	cache := map[string]domain.LocaleMessages{}
	for _, a := range built.Artifacts {
		msgs, err := domain.ArtifactMessages(a.Body)
		if err != nil {
			return nil, err
		}
		cache[a.Ref.SHA256] = msgs
	}
	head := domain.Release{Content: built.Content}
	headMsgs, err := s.messages(ctx, project, head, cache)
	if err != nil {
		return nil, err
	}
	baseMsgs, err := s.messages(ctx, project, base, cache)
	if err != nil {
		return nil, err
	}
	return domain.Diff(localeCodes(base), localeCodes(head), baseMsgs, headMsgs), nil
}
