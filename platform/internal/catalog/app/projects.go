package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// NewProject is the input of CreateProject.
type NewProject struct {
	Slug         string
	Name         string
	SourceLocale string
	// Settings nil means domain.DefaultSettings.
	Settings *domain.Settings
}

// CreateProject creates a project. A repeated idemKey returns the first
// request's project with replayed set.
//
// A principal limited to some projects can't: the new project would be
// outside their own scope (RFC 0006 §4.1).
func (s *Service) CreateProject(ctx context.Context, in NewProject, idemKey string) (p domain.Project, replayed bool, err error) {
	if err := authz.RequireUnscoped(ctx, authz.CatalogWrite); err != nil {
		return domain.Project{}, false, err
	}
	by, err := author(ctx, authz.CatalogWrite)
	if err != nil {
		return domain.Project{}, false, err
	}
	slug, err := domain.ParseSlug(in.Slug)
	if err != nil {
		return domain.Project{}, false, err
	}
	source, err := bcp47.Parse(in.SourceLocale)
	if err != nil {
		return domain.Project{}, false, err
	}
	settings := domain.DefaultSettings()
	if in.Settings != nil {
		settings = *in.Settings
	}
	tenant, _ := tenancy.FromContext(ctx)
	p, err = domain.NewProject(tenant, slug, in.Name, source, settings, s.now())
	if err != nil {
		return domain.Project{}, false, err
	}
	id, err := idempotentID("project.create", tenant.String(), by, idemKey)
	if err != nil {
		return domain.Project{}, false, err
	}
	if id != uuid.Nil {
		p.ID = domain.ProjectID(id)
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertProject(ctx, p, by)
		if err != nil {
			return err
		}
		if !inserted {
			first, err := st.Project(ctx, p.ID)
			if err != nil {
				return err
			}
			if first.Slug != p.Slug || first.SourceLocale != p.SourceLocale {
				return ErrIdempotencyReuse
			}
			p, replayed = first, true
			return nil
		}
		return st.Publish(ctx, projectEvent(domain.EventProjectCreated, p, by))
	})
	return p, replayed, err
}

func projectEvent(typ string, p domain.Project, by domain.Author) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateProject, AggregateID: p.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.ProjectEventOf(p, by),
	}
}

// GetProject returns one project. A project outside the caller's scope
// — for an assigned member, one no assignment of theirs reaches into —
// is not found (RFC 0006 §4.1, §3.3).
func (s *Service) GetProject(ctx context.Context, id domain.ProjectID) (domain.Project, error) {
	if err := authz.RequireProject(ctx, authz.CatalogRead, id.UUID()); err != nil {
		return domain.Project{}, err
	}
	var p domain.Project
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		p, err = st.Project(ctx, id)
		return err
	})
	return p, err
}

// ListProjects lists the tenant's projects a caller may see: those in
// their project scope (RFC 0006 §4.1), filtered in the query, and for
// an assigned member only those an assignment of theirs reaches into
// (§3.3).
func (s *Service) ListProjects(ctx context.Context, page pagination.Page) ([]domain.Project, *string, error) {
	p, ok := authz.From(ctx)
	if ok && p.Assigned() {
		return s.listAssignedProjects(ctx, page)
	}
	scope, err := authz.Projects(ctx, authz.CatalogRead)
	if err != nil {
		return nil, nil, err
	}
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Project
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.Projects(ctx, domain.ProjectID(after), scope.IDs(), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(p domain.Project) string { return p.ID.String() })
	return items, next, nil
}

// listAssignedProjects is ListProjects for a member whose visibility
// is `assigned`: the projects an assignment of theirs reaches into,
// found by asking authz project by project. The filter runs before the
// page is cut, so the page is full whenever more such projects exist.
func (s *Service) listAssignedProjects(ctx context.Context, page pagination.Page) ([]domain.Project, *string, error) {
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Project
	cursor := domain.ProjectID(after)
	for len(rows) < page.Limit() {
		var batch []domain.Project
		err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			var err error
			batch, err = st.Projects(ctx, cursor, nil, assignedProjectScan)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		for _, pr := range batch {
			switch err := authz.RequireProject(ctx, authz.CatalogRead, pr.ID.UUID()); {
			case err == nil:
				rows = append(rows, pr)
			case !errors.Is(err, authz.ErrNotVisible):
				return nil, nil, err
			}
			if len(rows) == page.Limit() {
				break
			}
		}
		if len(batch) < assignedProjectScan {
			break
		}
		cursor = batch[len(batch)-1].ID
	}
	items, next := pagination.Trim(rows, page, func(p domain.Project) string { return p.ID.String() })
	return items, next, nil
}

// assignedProjectScan is how many projects listAssignedProjects reads
// at a time.
const assignedProjectScan = 200

// UpdateProject changes a project if it is still at version ifMatch.
func (s *Service) UpdateProject(ctx context.Context, id domain.ProjectID, ifMatch int, c domain.ProjectChange) (domain.Project, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, id.UUID())
	if err != nil {
		return domain.Project{}, err
	}
	// The locales come from Localization, whose service runs its own
	// transaction, so they are read before this one opens rather than
	// inside it (Transactor: calls don't nest).
	known, err := s.requiredLocales(ctx, id, c.Settings)
	if err != nil {
		return domain.Project{}, err
	}
	var p domain.Project
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if p, err = st.LockProject(ctx, id); err != nil {
			return err
		}
		if p.Version != ifMatch {
			return ErrPreconditionFailed
		}
		changed, err := p.Change(c, known, s.now())
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateProject(ctx, p, ifMatch); err != nil {
			return err
		}
		return st.Publish(ctx, projectEvent(domain.EventProjectUpdated, p, by))
	})
	return p, err
}

// requiredLocales is the project's locales when the write names locales
// that must be complete, and nil otherwise.
//
// Nothing is read unless a policy actually names one, and what is read
// is read inside this write's own authorization: the port answers to
// the catalog.write already proved above, never to translations.read,
// so a CI token (catalog.read and catalog.write, nothing else) can
// write a policy that names locales.
func (s *Service) requiredLocales(ctx context.Context, id domain.ProjectID, settings *domain.Settings) ([]string, error) {
	if s.locales == nil || settings == nil || settings.CheckPolicy == nil ||
		len(settings.CheckPolicy.RequireComplete) == 0 {
		return nil, nil
	}
	return s.locales.LocaleCodes(ctx, id)
}

// DeleteProject deletes a project with its applications, messages and
// source history. Localization drops its data for the project when it
// sees catalog.project.deleted. It needs tenant.manage: it destroys
// history.
func (s *Service) DeleteProject(ctx context.Context, id domain.ProjectID, ifMatch *int) error {
	by, err := authorIn(ctx, authz.TenantManage, id.UUID())
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		p, err := st.LockProject(ctx, id)
		if err != nil {
			return err
		}
		if ifMatch != nil && p.Version != *ifMatch {
			return ErrPreconditionFailed
		}
		if err := st.DeleteProject(ctx, id); err != nil {
			return err
		}
		return st.Publish(ctx, projectEvent(domain.EventProjectDeleted, p, by))
	})
}

// NewApplication is the input of CreateApplication.
type NewApplication struct {
	Slug     string
	Name     string
	Platform string
}

// CreateApplication adds an application to a project.
func (s *Service) CreateApplication(ctx context.Context, project domain.ProjectID, in NewApplication, idemKey string) (a domain.Application, replayed bool, err error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Application{}, false, err
	}
	slug, err := domain.ParseSlug(in.Slug)
	if err != nil {
		return domain.Application{}, false, err
	}
	a, err = domain.NewApplication(project, slug, in.Name, domain.Platform(in.Platform), s.now())
	if err != nil {
		return domain.Application{}, false, err
	}
	id, err := idempotentID("application.create", project.String(), by, idemKey)
	if err != nil {
		return domain.Application{}, false, err
	}
	if id != uuid.Nil {
		a.ID = domain.ApplicationID(id)
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.LockProject(ctx, project); err != nil {
			return err
		}
		inserted, err := st.InsertApplication(ctx, a, by)
		if err != nil {
			return err
		}
		if !inserted {
			first, err := st.Application(ctx, project, a.ID)
			if err != nil {
				return err
			}
			if first.Slug != a.Slug {
				return ErrIdempotencyReuse
			}
			a, replayed = first, true
			return nil
		}
		return st.Publish(ctx, applicationEvent(domain.EventApplicationCreated, a, by))
	})
	return a, replayed, err
}

func applicationEvent(typ string, a domain.Application, by domain.Author) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateApplication, AggregateID: a.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.ApplicationEventOf(a, by),
	}
}

// GetApplication returns one application.
func (s *Service) GetApplication(ctx context.Context, project domain.ProjectID, id domain.ApplicationID) (domain.Application, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return domain.Application{}, err
	}
	var a domain.Application
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		a, err = st.Application(ctx, project, id)
		return err
	})
	return a, err
}

// ListApplications lists a project's applications.
func (s *Service) ListApplications(ctx context.Context, project domain.ProjectID, page pagination.Page) ([]domain.Application, *string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, nil, err
	}
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Application
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		rows, err = st.Applications(ctx, project, domain.ApplicationID(after), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(a domain.Application) string { return a.ID.String() })
	return items, next, nil
}

// UpdateApplication changes an application at version ifMatch.
func (s *Service) UpdateApplication(ctx context.Context, project domain.ProjectID, id domain.ApplicationID, ifMatch int, c domain.ApplicationChange) (domain.Application, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Application{}, err
	}
	var a domain.Application
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if a, err = st.LockApplication(ctx, project, id); err != nil {
			return err
		}
		if a.Version != ifMatch {
			return ErrPreconditionFailed
		}
		changed, err := a.Change(c, s.now())
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateApplication(ctx, a, ifMatch); err != nil {
			return err
		}
		return st.Publish(ctx, applicationEvent(domain.EventApplicationUpdated, a, by))
	})
	return a, err
}

// DeleteApplication removes an application.
func (s *Service) DeleteApplication(ctx context.Context, project domain.ProjectID, id domain.ApplicationID, ifMatch *int) error {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		a, err := st.LockApplication(ctx, project, id)
		if err != nil {
			return err
		}
		if ifMatch != nil && a.Version != *ifMatch {
			return ErrPreconditionFailed
		}
		if err := st.DeleteApplication(ctx, project, id); err != nil {
			return err
		}
		return st.Publish(ctx, applicationEvent(domain.EventApplicationDeleted, a, by))
	})
}
