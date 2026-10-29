package sqlcadapter

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
)

type UserRepo struct {
	q *db.Queries
}

func NewUserRepo(q *db.Queries) *UserRepo {
	return &UserRepo{q: q}
}

func (r *UserRepo) Save(ctx context.Context, u user.User) (user.User, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.CreateUser(ctx, db.CreateUserParams{
		TenantID:     toPgUUID(u.TenantID),
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
		Role:         string(u.Role),
		Locales:      u.Locales,
	})
	if err != nil {
		return user.User{}, err
	}
	return user.User{
		ID:        fromPgUUID(row.ID),
		TenantID:  fromPgUUID(row.TenantID),
		Email:     row.Email,
		Role:      user.Role(row.Role),
		Locales:   row.Locales,
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

func (r *UserRepo) FindByEmail(ctx context.Context, tenantID uuid.UUID, email string) (user.User, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetUserByEmail(ctx, db.GetUserByEmailParams{
		TenantID: toPgUUID(tenantID),
		Email:    email,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.User{}, user.ErrNotFound
		}
		return user.User{}, err
	}
	return user.User{
		ID:           fromPgUUID(row.ID),
		TenantID:     fromPgUUID(row.TenantID),
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
		Role:         user.Role(row.Role),
		Locales:      row.Locales,
		CreatedAt:    row.CreatedAt.Time,
	}, nil
}

func (r *UserRepo) FindByID(ctx context.Context, id uuid.UUID) (user.User, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetUserByID(ctx, toPgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.User{}, user.ErrNotFound
		}
		return user.User{}, err
	}
	return user.User{
		ID:           fromPgUUID(row.ID),
		TenantID:     fromPgUUID(row.TenantID),
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
		Role:         user.Role(row.Role),
		Locales:      row.Locales,
		CreatedAt:    row.CreatedAt.Time,
	}, nil
}

func (r *UserRepo) ListForTenant(ctx context.Context, tenantID uuid.UUID) ([]user.User, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListUsersForTenant(ctx, toPgUUID(tenantID))
	if err != nil {
		return nil, err
	}
	out := make([]user.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, user.User{
			ID:        fromPgUUID(row.ID),
			TenantID:  fromPgUUID(row.TenantID),
			Email:     row.Email,
			Role:      user.Role(row.Role),
			Locales:   row.Locales,
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return out, nil
}

func (r *UserRepo) UpdateLocales(ctx context.Context, id uuid.UUID, locales []string) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.UpdateUserLocales(ctx, db.UpdateUserLocalesParams{
		ID:      toPgUUID(id),
		Locales: locales,
	})
}

func (r *UserRepo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash []byte) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{
		ID:           toPgUUID(id),
		PasswordHash: hash,
	})
}

func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.DeleteUser(ctx, toPgUUID(id))
}

func (r *UserRepo) CountAdmins(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	q := db.QueriesFromContext(ctx, r.q)
	return q.CountAdminsInTenant(ctx, toPgUUID(tenantID))
}

func (r *UserRepo) ListTenantsForEmail(ctx context.Context, email string) ([]user.TenantMembership, error) {
	// Discovery runs BEFORE any auth, so RLS isn't active yet —
	// always use the pool-direct Queries to bypass the (nil)
	// app.current_tenant.
	rows, err := r.q.ListTenantsForEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	out := make([]user.TenantMembership, 0, len(rows))
	for _, row := range rows {
		out = append(out, user.TenantMembership{
			TenantID:   fromPgUUID(row.ID),
			TenantSlug: row.Slug,
			TenantName: row.Name,
		})
	}
	return out, nil
}
