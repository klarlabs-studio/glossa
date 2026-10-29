package sqlcadapter

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
)

// TranslationRepo is the sqlc-backed Repository for translations.
type TranslationRepo struct {
	q *db.Queries
}

// NewTranslationRepo wires the repo.
func NewTranslationRepo(q *db.Queries) *TranslationRepo {
	return &TranslationRepo{q: q}
}

// Upsert performs the (key, locale) merge on the translations table.
func (r *TranslationRepo) Upsert(ctx context.Context, t translation.Translation) (translation.Translation, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.UpsertTranslation(ctx, db.UpsertTranslationParams{
		KeyID:     toPgUUID(t.KeyID),
		LocaleID:  toPgUUID(t.LocaleID),
		Value:     t.Value,
		Status:    string(t.Status),
		UpdatedBy: nullableUUID(t.UpdatedBy),
	})
	if err != nil {
		return translation.Translation{}, err
	}
	out := translation.Translation{
		ID:        fromPgUUID(row.ID),
		KeyID:     fromPgUUID(row.KeyID),
		LocaleID:  fromPgUUID(row.LocaleID),
		Value:     row.Value,
		Status:    translation.Status(row.Status),
		UpdatedAt: row.UpdatedAt.Time,
	}
	if row.UpdatedBy.Valid {
		out.UpdatedBy = uuid.UUID(row.UpdatedBy.Bytes)
	}
	return out, nil
}

// ListBundle returns every key for a project paired with its (maybe
// empty) translation in the requested locale.
func (r *TranslationRepo) ListBundle(ctx context.Context, projectID, localeID uuid.UUID) ([]translation.BundleEntry, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListBundle(ctx, db.ListBundleParams{
		ProjectID: toPgUUID(projectID),
		LocaleID:  toPgUUID(localeID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]translation.BundleEntry, 0, len(rows))
	for _, row := range rows {
		entry := translation.BundleEntry{Key: row.Key}
		if row.Value != nil {
			entry.Value = *row.Value
		}
		if row.Status != nil {
			entry.Status = translation.Status(*row.Status)
		}
		out = append(out, entry)
	}
	return out, nil
}

// Find loads the current row at (keyID, localeID). Returns
// translation.ErrNotFound when no row exists yet so the audit
// caller can record an empty BeforeValue without surfacing the
// driver's sentinel.
func (r *TranslationRepo) Find(ctx context.Context, keyID, localeID uuid.UUID) (translation.Translation, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetTranslation(ctx, db.GetTranslationParams{
		KeyID:    toPgUUID(keyID),
		LocaleID: toPgUUID(localeID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return translation.Translation{}, translation.ErrNotFound
		}
		return translation.Translation{}, err
	}
	out := translation.Translation{
		ID:        fromPgUUID(row.ID),
		KeyID:     fromPgUUID(row.KeyID),
		LocaleID:  fromPgUUID(row.LocaleID),
		Value:     row.Value,
		Status:    translation.Status(row.Status),
		UpdatedAt: row.UpdatedAt.Time,
	}
	if row.UpdatedBy.Valid {
		out.UpdatedBy = uuid.UUID(row.UpdatedBy.Bytes)
	}
	return out, nil
}

// nullableUUID wraps an optional UUID for sqlc's pgtype-based nullable
// shape. uuid.Nil maps to Valid=false so the column lands as NULL —
// useful for system-generated translation rows (CLI seed drops,
// machine-translation passthroughs) that have no human author.
func nullableUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return toPgUUID(id)
}
