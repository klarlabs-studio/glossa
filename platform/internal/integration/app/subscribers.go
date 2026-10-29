package app

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// The event contracts Integration reads (its own payload types, never
// Catalog's Go types) and its subscriber names, which are stored with
// events: never rename them.
const (
	catalogProjectDeleted = "catalog.project.deleted"
	subscriberDropProject = principalDropProject
)

type projectEvent struct {
	ProjectID string `json:"project_id"`
}

// Subscribe registers Integration's subscribers: a deleted project's
// import and export jobs, results and files are erased with it.
func (s *Service) Subscribe(r *outbox.Registry) error {
	return r.Subscribe(catalogProjectDeleted, subscriberDropProject, outbox.HandlerFunc(s.handleProjectDeleted))
}

func (s *Service) handleProjectDeleted(ctx context.Context, d outbox.Delivery) error {
	var e projectEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(err)
	}
	var keys []string
	if err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		jobs, err := st.ProjectJobs(ctx, project)
		for _, j := range jobs {
			if j.File != nil && j.FilesDeletedAt == nil {
				keys = append(keys, j.File.Key)
			}
		}
		return err
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := s.objects.Delete(ctx, k); err != nil {
			return err // retried; deleting a missing object is no error
		}
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.DeleteProjectJobs(ctx, project) })
}
