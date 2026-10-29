package sqlcadapter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
)

// pgtsTime unwraps the value pgx scans into an interface{} cell for
// MIN(occurred_at) aggregates. Depending on pgx config / version the
// concrete type is either pgtype.Timestamptz or plain time.Time, so
// both shapes are handled. Invalid / zero values surface as the
// time.Time zero — callers can detect via .IsZero().
func pgtsTime(v any) time.Time {
	switch ts := v.(type) {
	case pgtype.Timestamptz:
		if ts.Valid {
			return ts.Time
		}
	case time.Time:
		return ts
	case *time.Time:
		if ts != nil {
			return *ts
		}
	}
	return time.Time{}
}

// AnalyticsRepo persists + reads analytics_events.
type AnalyticsRepo struct {
	q *db.Queries
}

// NewAnalyticsRepo wires the adapter.
func NewAnalyticsRepo(q *db.Queries) *AnalyticsRepo {
	return &AnalyticsRepo{q: q}
}

// Record appends one event. Best-effort: failures bubble up to the
// caller, which always treats Record errors as non-fatal — they get
// logged but never abort the originating request.
func (r *AnalyticsRepo) Record(ctx context.Context, e analytics.Event) error {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	q := db.QueriesFromContext(ctx, r.q)
	var projectID pgtype.UUID
	if e.ProjectID != nil {
		projectID = pgtype.UUID{Bytes: *e.ProjectID, Valid: true}
	}
	return q.RecordAnalyticsEvent(ctx, db.RecordAnalyticsEventParams{
		TenantID:  toPgUUID(e.TenantID),
		ProjectID: projectID,
		Kind:      string(e.Kind),
		Metadata:  raw,
	})
}

// ProjectFunnel returns the first-occurrence timestamp + total count
// per event kind for a single project.
func (r *AnalyticsRepo) ProjectFunnel(ctx context.Context, tenantID, projectID uuid.UUID) ([]analytics.FunnelRow, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ProjectFunnel(ctx, db.ProjectFunnelParams{
		TenantID:  toPgUUID(tenantID),
		ProjectID: toPgUUID(projectID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]analytics.FunnelRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, analytics.FunnelRow{
			Kind:    analytics.Kind(row.Kind),
			FirstAt: pgtsTime(row.FirstAt),
			Total:   row.Total,
		})
	}
	return out, nil
}

// TenantProjectsFirstEvents powers the tenant-wide cohort dashboard.
func (r *AnalyticsRepo) TenantProjectsFirstEvents(ctx context.Context, tenantID uuid.UUID) ([]analytics.ProjectFirstEvent, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.TenantProjectsFirstEvents(ctx, toPgUUID(tenantID))
	if err != nil {
		return nil, err
	}
	out := make([]analytics.ProjectFirstEvent, 0, len(rows))
	for _, row := range rows {
		if !row.ProjectID.Valid {
			continue
		}
		out = append(out, analytics.ProjectFirstEvent{
			ProjectID: fromPgUUID(row.ProjectID),
			Kind:      analytics.Kind(row.Kind),
			FirstAt:   pgtsTime(row.FirstAt),
		})
	}
	return out, nil
}
