package sqlcadapter

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/audit"
)

type AuditRepo struct {
	q *db.Queries
}

func NewAuditRepo(q *db.Queries) *AuditRepo {
	return &AuditRepo{q: q}
}

func (r *AuditRepo) Append(ctx context.Context, e audit.Entry) error {
	q := db.QueriesFromContext(ctx, r.q)
	kind := e.ActorKind
	if kind == "" {
		kind = "user"
	}
	return q.AppendAuditEntry(ctx, db.AppendAuditEntryParams{
		TenantID:      toPgUUID(e.TenantID),
		TranslationID: nullablePgUUID(e.TranslationID),
		BeforeValue:   optionalStringText(e.BeforeValue),
		AfterValue:    optionalStringText(e.AfterValue),
		ChangedBy:     nullablePgUUID(e.ChangedBy),
		ActorKind:     kind,
		ActorLabel:    e.ActorLabel,
	})
}

func (r *AuditRepo) ListForTenant(ctx context.Context, tenantID uuid.UUID, limit, offset int32) ([]audit.Entry, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListAuditForTenant(ctx, db.ListAuditForTenantParams{
		TenantID: toPgUUID(tenantID),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]audit.Entry, 0, len(rows))
	for _, row := range rows {
		entry := audit.Entry{
			ID:         row.ID,
			TenantID:   fromPgUUID(row.TenantID),
			ChangedAt:  row.ChangedAt.Time,
			ActorKind:  row.ActorKind,
			ActorLabel: row.ActorLabel,
		}
		if row.TranslationID.Valid {
			entry.TranslationID = uuid.UUID(row.TranslationID.Bytes)
		}
		if row.BeforeValue != nil {
			entry.BeforeValue = *row.BeforeValue
		}
		if row.AfterValue != nil {
			entry.AfterValue = *row.AfterValue
		}
		if row.ChangedBy.Valid {
			entry.ChangedBy = uuid.UUID(row.ChangedBy.Bytes)
		}
		out = append(out, entry)
	}
	return out, nil
}

func nullablePgUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func optionalStringText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
