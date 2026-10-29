package sqlcadapter

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/tenant"
)

type TenantRepo struct {
	q *db.Queries
}

func NewTenantRepo(q *db.Queries) *TenantRepo {
	return &TenantRepo{q: q}
}

func (r *TenantRepo) Save(ctx context.Context, t tenant.Tenant) error {
	q := db.QueriesFromContext(ctx, r.q)
	_, err := q.CreateTenant(ctx, db.CreateTenantParams{
		ID:   toPgUUID(t.ID),
		Slug: t.Slug.String(),
		Name: t.Name.String(),
	})
	return err
}

func (r *TenantRepo) FindBySlug(ctx context.Context, s tenant.Slug) (tenant.Tenant, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetTenantBySlug(ctx, s.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.Tenant{}, ErrNotFound
		}
		return tenant.Tenant{}, err
	}
	return tenant.Tenant{
		ID:   fromPgUUID(row.ID),
		Slug: tenant.Slug(row.Slug),
		Name: tenant.Name(row.Name),
	}, nil
}

func (r *TenantRepo) FindByID(ctx context.Context, id uuid.UUID) (tenant.Tenant, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetTenantByID(ctx, toPgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.Tenant{}, ErrNotFound
		}
		return tenant.Tenant{}, err
	}
	return tenant.Tenant{
		ID:   fromPgUUID(row.ID),
		Slug: tenant.Slug(row.Slug),
		Name: tenant.Name(row.Name),
	}, nil
}
