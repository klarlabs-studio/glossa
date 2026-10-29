package sqlcadapter

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translationkey"
)

// KeyRepo is the sqlc-backed Repository for translation keys.
type KeyRepo struct {
	q *db.Queries
}

// NewKeyRepo wires the repo.
func NewKeyRepo(q *db.Queries) *KeyRepo {
	return &KeyRepo{q: q}
}

// Upsert idempotently inserts (or updates the description of) a key.
func (r *KeyRepo) Upsert(ctx context.Context, k translationkey.Key) (translationkey.Key, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.UpsertKey(ctx, db.UpsertKeyParams{
		ProjectID:   toPgUUID(k.ProjectID),
		Key:         k.Name.String(),
		Description: optionalString(k.Description),
	})
	if err != nil {
		return translationkey.Key{}, err
	}
	desc := ""
	if row.Description != nil {
		desc = *row.Description
	}
	return translationkey.Key{
		ID:          fromPgUUID(row.ID),
		ProjectID:   fromPgUUID(row.ProjectID),
		Name:        translationkey.Name(row.Key),
		Description: desc,
	}, nil
}

// ListForProject returns every key defined under a project.
func (r *KeyRepo) ListForProject(ctx context.Context, projectID uuid.UUID) ([]translationkey.Key, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListKeysForProject(ctx, toPgUUID(projectID))
	if err != nil {
		return nil, err
	}
	out := make([]translationkey.Key, 0, len(rows))
	for _, row := range rows {
		desc := ""
		if row.Description != nil {
			desc = *row.Description
		}
		out = append(out, translationkey.Key{
			ID:          fromPgUUID(row.ID),
			ProjectID:   projectID,
			Name:        translationkey.Name(row.Key),
			Description: desc,
		})
	}
	return out, nil
}

// Find loads a single key by (projectID, name).
func (r *KeyRepo) Find(ctx context.Context, projectID uuid.UUID, name translationkey.Name) (translationkey.Key, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetKey(ctx, db.GetKeyParams{
		ProjectID: toPgUUID(projectID),
		Key:       name.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return translationkey.Key{}, ErrNotFound
		}
		return translationkey.Key{}, err
	}
	desc := ""
	if row.Description != nil {
		desc = *row.Description
	}
	return translationkey.Key{
		ID:          fromPgUUID(row.ID),
		ProjectID:   fromPgUUID(row.ProjectID),
		Name:        translationkey.Name(row.Key),
		Description: desc,
	}, nil
}

// optionalString returns a pointer for sqlc's pointer-for-nullable
// mode. Empty strings map to nil so existing descriptions survive
// re-upserts (the SQL uses COALESCE on EXCLUDED.description).
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
