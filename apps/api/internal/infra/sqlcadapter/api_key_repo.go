package sqlcadapter

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/apikey"
)

// APIKeyRepo is the sqlc-backed adapter for apikey.Repository.
type APIKeyRepo struct {
	q *db.Queries
}

// NewAPIKeyRepo wires the adapter.
func NewAPIKeyRepo(q *db.Queries) *APIKeyRepo {
	return &APIKeyRepo{q: q}
}

// Create persists a new key.
func (r *APIKeyRepo) Create(ctx context.Context, projectID uuid.UUID, hash []byte, scope apikey.Scope, label string) (apikey.Key, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.CreateProjectAPIKey(ctx, db.CreateProjectAPIKeyParams{
		ProjectID: toPgUUID(projectID),
		Hash:      hash,
		Scope:     string(scope),
		Label:     label,
	})
	if err != nil {
		return apikey.Key{}, err
	}
	return mapAPIKeyRow(row.ID, row.ProjectID, row.Scope, row.Label, row.CreatedAt.Time, row.LastUsedAt, row.RevokedAt), nil
}

// List returns every key for a project (including revoked rows so
// the UI can show the audit history; the caller filters as needed).
func (r *APIKeyRepo) List(ctx context.Context, projectID uuid.UUID) ([]apikey.Key, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListProjectAPIKeys(ctx, toPgUUID(projectID))
	if err != nil {
		return nil, err
	}
	out := make([]apikey.Key, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapAPIKeyRow(row.ID, row.ProjectID, row.Scope, row.Label, row.CreatedAt.Time, row.LastUsedAt, row.RevokedAt))
	}
	return out, nil
}

// ResolveByHash powers the API-key auth middleware. RLS is NOT set
// at this point in the request — pre-auth we don't know the tenant
// yet. The query uses the pool-direct Queries to bypass any RLS-tx
// middleware context.
func (r *APIKeyRepo) ResolveByHash(ctx context.Context, hash []byte) (apikey.Resolution, error) {
	row, err := r.q.GetProjectAPIKeyByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apikey.Resolution{}, apikey.ErrNotFound
		}
		return apikey.Resolution{}, err
	}
	return apikey.Resolution{
		KeyID:         fromPgUUID(row.ID),
		ProjectID:     fromPgUUID(row.ProjectID),
		TenantID:      fromPgUUID(row.TenantID),
		ProjectSlug:   row.ProjectSlug,
		ProjectName:   row.ProjectName,
		DefaultLocale: row.DefaultLocale,
		Scope:         apikey.Scope(row.Scope),
	}, nil
}

// Touch updates last_used_at to NOW() for usage analytics. Best
// effort — failures don't fail the request.
func (r *APIKeyRepo) Touch(ctx context.Context, id uuid.UUID) error {
	return r.q.TouchProjectAPIKey(ctx, toPgUUID(id))
}

// Revoke flips revoked_at to NOW() so the partial unique index on
// hash drops it from auth lookups immediately.
func (r *APIKeyRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.RevokeProjectAPIKey(ctx, toPgUUID(id))
}

func mapAPIKeyRow(
	id, projectID pgtype.UUID,
	scope, label string,
	createdAt time.Time,
	lastUsedAt, revokedAt pgtype.Timestamptz,
) apikey.Key {
	k := apikey.Key{
		ID:        fromPgUUID(id),
		ProjectID: fromPgUUID(projectID),
		Scope:     apikey.Scope(scope),
		Label:     label,
		CreatedAt: createdAt,
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = lastUsedAt.Time
	}
	if revokedAt.Valid {
		k.RevokedAt = revokedAt.Time
	}
	return k
}
