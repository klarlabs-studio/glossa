package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// purgePrincipal is the background principal of the retention run.
// Its name is stable: it appears as the actor in logs and audits.
const purgePrincipal = "context.purge"

// Purged says what a purge deleted.
type Purged struct {
	// Builds are the deleted builds (their usages, captures and regions
	// went with them).
	Builds []uuid.UUID
	// Captures counts the captures the deleted builds held.
	Captures int
	// OrphanedImages are the images no capture of the project references
	// any more, deleted from object storage with the builds.
	OrphanedImages []domain.Digest
	// ImagesDeleted counts the orphaned images object storage confirmed
	// gone. It is below len(OrphanedImages) only when a delete failed.
	ImagesDeleted int
}

// PurgeProject applies the retention policy to a project's builds
// (RFC 0004 §2.3): it keeps the latest builds per application, branch
// and source and every current one, and deletes a closed branch's
// builds after the grace period, then the images no remaining capture
// references. Needs catalog.write.
func (s *Service) PurgeProject(ctx context.Context, project uuid.UUID) (Purged, error) {
	if _, err := actorIn(ctx, authz.CatalogWrite, project); err != nil {
		return Purged{}, err
	}
	closed, err := s.catalog.ClosedBranches(ctx, project)
	if err != nil {
		return Purged{}, err
	}
	var (
		out  Purged
		left int64
	)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		all, err := st.BuildSummaries(ctx, project)
		if err != nil {
			return err
		}
		expired := s.retention.Expired(all, closed, s.now())
		if len(expired) == 0 {
			left, err = st.StoredImageBytes(ctx)
			return err
		}
		images, err := st.BuildImages(ctx, expired)
		if err != nil {
			return err
		}
		if out.Captures, err = st.CountCapturesOfBuilds(ctx, expired); err != nil {
			return err
		}
		if _, err := st.DeleteBuilds(ctx, expired); err != nil {
			return err
		}
		out.Builds = expired
		if out.OrphanedImages, err = orphaned(ctx, st, project, images); err != nil {
			return err
		}
		// The rows are gone, so this is already what the tenant holds
		// after the run, whatever the object deletes below do.
		left, err = st.StoredImageBytes(ctx)
		return err
	})
	if err != nil {
		return Purged{}, err
	}
	s.recordStorage(ctx, left)
	// The builds are gone, so a later purge wouldn't find these images:
	// a failure to delete one is reported and logged, the rest go on.
	deleted, err := s.deleteImages(ctx, project, out.OrphanedImages)
	out.ImagesDeleted = deleted
	s.logImageErrors(ctx, project, err)
	s.metrics.Purged(out)
	return out, err
}

// orphaned returns which of images no capture of the project
// references any more.
func orphaned(ctx context.Context, st Store, project uuid.UUID, images []domain.Digest) ([]domain.Digest, error) {
	if len(images) == 0 {
		return nil, nil
	}
	still, err := st.ReferencedImages(ctx, project, images)
	if err != nil {
		return nil, err
	}
	var out []domain.Digest
	for _, d := range images {
		if !slices.Contains(still, d) {
			out = append(out, d)
		}
	}
	return out, nil
}

// deleteImages deletes a project's images from object storage (none
// without WithImages), all of them whatever fails; the failures are
// joined. Deletes are idempotent: the port answers nil for an object
// that is already gone, so a re-run after a partial failure is safe,
// and so is a purge racing the subscriber that erases a deleted
// project. They go in batches of DeleteBatch, so freeing a large
// backlog doesn't flood the object store.
func (s *Service) deleteImages(ctx context.Context, project uuid.UUID, images []domain.Digest) (int, error) {
	if s.objects == nil {
		return 0, nil
	}
	t, _ := tenancy.FromContext(ctx)
	var (
		deleted int
		errs    []error
	)
	for batch := range slices.Chunk(images, s.deleteBatch) {
		for _, d := range batch {
			if err := s.objects.Delete(ctx, domain.ImageKey(t, project, d)); err != nil {
				errs = append(errs, fmt.Errorf("%w: delete image %s: %v", ErrStorageUnavailable, d, err))
				continue
			}
			deleted++
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
	}
	return deleted, errors.Join(errs...)
}

func (s *Service) logImageErrors(ctx context.Context, project uuid.UUID, err error) {
	if err != nil {
		s.logger.ErrorContext(ctx, "context: images not deleted", slog.String("project_id", project.String()),
			slog.Any("error", err))
	}
}

// ProjectPurge is one project's part of a retention run.
type ProjectPurge struct {
	ProjectRef
	Purged
}

// Purge runs retention over every project holding builds, each in its
// tenant's scope as the background principal context.purge. It runs
// outside any tenant (the daily job) and needs WithSweeper. A failing
// project doesn't stop the others; their errors are joined.
func (s *Service) Purge(ctx context.Context) ([]ProjectPurge, error) {
	if s.sweeper == nil {
		return nil, errors.New("context: Purge needs a Sweeper")
	}
	targets, err := s.sweeper.ProjectsWithBuilds(ctx)
	if err != nil {
		return nil, err
	}
	var (
		out  []ProjectPurge
		errs []error
	)
	for _, t := range targets {
		bg, err := authz.Background(tenancy.ContextWithTenant(ctx, t.Tenant), purgePrincipal,
			authz.CatalogRead, authz.CatalogWrite)
		if err != nil {
			return out, err
		}
		p, err := s.PurgeProject(bg, t.Project)
		if err != nil {
			s.logger.ErrorContext(ctx, "context: retention", slog.String("tenant_id", t.Tenant.String()),
				slog.String("project_id", t.Project.String()), slog.Any("error", err))
			errs = append(errs, fmt.Errorf("context: purge project %s: %w", t.Project, err))
			continue
		}
		if len(p.Builds) > 0 {
			out = append(out, ProjectPurge{ProjectRef: t, Purged: p})
		}
	}
	return out, errors.Join(errs...)
}
