// Package sqlcadapter implements the domain Repository ports against
// the sqlc-generated Queries struct. Each adapter is a thin shim —
// no business rules, just mapping between domain VOs and sqlc params
// / rows.
package sqlcadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

// ProjectRepo is the sqlc-backed Repository for projects.
type ProjectRepo struct {
	q *db.Queries
}

// NewProjectRepo wires the repo against a sqlc Queries.
func NewProjectRepo(q *db.Queries) *ProjectRepo {
	return &ProjectRepo{q: q}
}

// Save persists a new project. UPDATE is not yet supported — the
// only mutation we need pre-MVP is create-on-bootstrap.
func (r *ProjectRepo) Save(ctx context.Context, p project.Project) error {
	tenantID := toPgUUID(p.TenantID)
	q := db.QueriesFromContext(ctx, r.q)
	_, err := q.CreateProject(ctx, db.CreateProjectParams{
		ID:            toPgUUID(p.ID),
		TenantID:      tenantID,
		Slug:          p.Slug.String(),
		Name:          p.Name.String(),
		DefaultLocale: p.DefaultLocale,
	})
	return err
}

// Find loads a project by (tenantID, slug).
func (r *ProjectRepo) Find(ctx context.Context, tenantID uuid.UUID, slug project.Slug) (project.Project, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetProjectBySlug(ctx, db.GetProjectBySlugParams{
		TenantID: toPgUUID(tenantID),
		Slug:     slug.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return project.Project{}, fmt.Errorf("project %q: %w", slug, ErrNotFound)
		}
		return project.Project{}, err
	}
	return project.Project{
		ID:            fromPgUUID(row.ID),
		TenantID:      fromPgUUID(row.TenantID),
		Slug:          project.Slug(row.Slug),
		Name:          project.Name(row.Name),
		DefaultLocale: row.DefaultLocale,
	}, nil
}

// ListForTenant returns projects for the given tenant, newest first.
func (r *ProjectRepo) ListForTenant(ctx context.Context, tenantID uuid.UUID) ([]project.Project, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListProjectsForTenant(ctx, toPgUUID(tenantID))
	if err != nil {
		return nil, err
	}
	out := make([]project.Project, 0, len(rows))
	for _, row := range rows {
		out = append(out, project.Project{
			ID:            fromPgUUID(row.ID),
			TenantID:      tenantID,
			Slug:          project.Slug(row.Slug),
			Name:          project.Name(row.Name),
			DefaultLocale: row.DefaultLocale,
		})
	}
	return out, nil
}

func toPgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func fromPgUUID(v pgtype.UUID) uuid.UUID {
	return uuid.UUID(v.Bytes)
}
