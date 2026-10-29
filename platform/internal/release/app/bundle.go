package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// The reads `glossa pull --release` bundles a release from
// (runtimes/SPEC.md §3.4): its manifest for an environment, byte for byte
// what the edge serves when that environment serves it, and its
// artifacts as stored.

// ReleaseManifest returns the signed manifest of a release as
// environment serves it.
func (s *Service) ReleaseManifest(ctx context.Context, project, id uuid.UUID, environment string) ([]byte, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, err
	}
	name, err := domain.ParseEnvironmentName(environment)
	if err != nil {
		return nil, err
	}
	rel, err := s.release(ctx, project, id)
	if err != nil {
		return nil, err
	}
	return s.ManifestBytes(rel, name)
}

// ReleaseArtifact returns one artifact of a release, verified against
// its digest. A digest the release doesn't name is not found.
func (s *Service) ReleaseArtifact(ctx context.Context, project, id uuid.UUID, digest string) ([]byte, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, err
	}
	rel, err := s.release(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if !names(rel, digest) {
		return nil, ErrNotFound
	}
	body, err := s.objects.Get(ctx, delivery.ArtifactPath(project.String(), digest), delivery.MaxArtifactBytes)
	if errors.Is(err, objectstore.ErrNotFound) {
		return nil, fmt.Errorf("%w: artifact %s is missing from storage", ErrStorage, digest)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	if delivery.Digest(body) != digest {
		return nil, fmt.Errorf("%w: artifact %s doesn't match its hash", ErrStorage, digest)
	}
	return body, nil
}

func names(rel domain.Release, digest string) bool {
	for _, namespaces := range rel.Content.Artifacts {
		for _, ref := range namespaces {
			if ref.SHA256 == digest {
				return true
			}
		}
	}
	return false
}

func (s *Service) release(ctx context.Context, project, id uuid.UUID) (domain.Release, error) {
	var rel domain.Release
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rel, err = st.Release(ctx, project, id)
		return err
	})
	return rel, err
}
