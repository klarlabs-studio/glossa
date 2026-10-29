package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// The event contracts Context reads. The payload types are Context's
// own (an anti-corruption layer): it depends on the published JSON, not
// on Catalog's Go types.
const (
	catalogProjectDeleted     = "catalog.project.deleted"
	catalogApplicationDeleted = "catalog.application.deleted"
)

// Subscriber names are stored with each event; never rename them.
const (
	subscriberDropProject     = "context.drop_project"
	subscriberDropApplication = "context.drop_application"
	// subscriberMeasureCoverage is also the background principal that
	// reads the catalog for it.
	subscriberMeasureCoverage = "context.measure_coverage"
)

type catalogEvent struct {
	ProjectID     string `json:"project_id"`
	ApplicationID string `json:"application_id"`
}

// Subscribe registers Context's subscribers: a deleted project or
// application takes its builds, usages, captures and regions with it,
// and a default-branch build updates the project's context coverage.
func (s *Service) Subscribe(r *outbox.Registry) error {
	if err := r.Subscribe(catalogProjectDeleted, subscriberDropProject, outbox.HandlerFunc(s.handleProjectDeleted)); err != nil {
		return err
	}
	if err := r.Subscribe(domain.EventBuildIngested, subscriberMeasureCoverage, outbox.HandlerFunc(s.handleBuildIngested)); err != nil {
		return err
	}
	return r.Subscribe(catalogApplicationDeleted, subscriberDropApplication, outbox.HandlerFunc(s.handleApplicationDeleted))
}

// handleBuildIngested measures the project's context coverage (RFC
// 0004 §11) after a default-branch build: the share of its active
// messages with a current usage, and with a visible region on a
// current capture. Measuring twice is harmless.
func (s *Service) handleBuildIngested(ctx context.Context, d outbox.Delivery) error {
	var e domain.BuildIngested
	if err := d.Decode(&e); err != nil {
		return err
	}
	if !e.OnDefaultBranch {
		return nil
	}
	project, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("context: build event with project_id %q", e.ProjectID))
	}
	bg, err := authz.Background(ctx, subscriberMeasureCoverage, authz.CatalogRead)
	if err != nil {
		return err
	}
	_, err = s.UnusedMessages(bg, project, "")
	if err == nil {
		err = s.measureCaptureCoverage(bg, project)
	}
	if errors.Is(err, ErrProjectNotFound) {
		return nil // deleted since: nothing to measure
	}
	return err
}

// handleProjectDeleted erases a deleted project's context, its images
// first: until its captures are gone a redelivery finds them again.
// Deleting what is already gone changes nothing, so redelivery is
// harmless.
func (s *Service) handleProjectDeleted(ctx context.Context, d outbox.Delivery) error {
	var e catalogEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("context: project event with project_id %q", e.ProjectID))
	}
	var images []domain.Digest
	if err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		images, err = st.ProjectImages(ctx, project)
		return err
	}); err != nil {
		return err
	}
	if _, err := s.deleteImages(ctx, project, images); err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return st.DeleteProjectData(ctx, project)
	})
}

// handleApplicationDeleted erases a deleted application's builds and
// the images no other capture of the project references.
func (s *Service) handleApplicationDeleted(ctx context.Context, d outbox.Delivery) error {
	var e catalogEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err1 := uuid.Parse(e.ProjectID)
	application, err2 := uuid.Parse(e.ApplicationID)
	if err := errors.Join(err1, err2); err != nil {
		return outbox.Permanent(fmt.Errorf("context: application event %s: %w", d.EventID, err))
	}
	var orphans []domain.Digest
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		all, err := st.BuildSummaries(ctx, project)
		if err != nil {
			return err
		}
		var builds []uuid.UUID
		for _, b := range all {
			if b.ApplicationID == application {
				builds = append(builds, b.ID)
			}
		}
		images, err := st.BuildImages(ctx, builds)
		if err != nil {
			return err
		}
		if err := st.DeleteApplicationData(ctx, project, application); err != nil {
			return err
		}
		orphans, err = orphaned(ctx, st, project, images)
		return err
	})
	if err != nil {
		return err
	}
	// The captures are gone: a redelivery would find no images, so a
	// failure to delete one is logged, not retried.
	_, delErr := s.deleteImages(ctx, project, orphans)
	s.logImageErrors(ctx, project, delErr)
	return nil
}
