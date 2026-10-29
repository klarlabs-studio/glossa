package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/postgres/releasesql"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// ── staged rollouts (RFC 0006 §5.2) ─────────────────────────────────

func rollout(r releasesql.ReleaseRollout) domain.Rollout {
	out := domain.Rollout{
		ID: r.ID, ProjectID: r.ProjectID, Environment: r.Environment,
		Candidate: r.CandidateReleaseID, Stable: r.StableReleaseID, Percent: int(r.Percent), Salt: r.Salt,
		Status: domain.RolloutStatus(r.Status), MaxDuration: time.Duration(r.MaxDurationSeconds) * time.Second,
		ExpiresAt: r.ExpiresAt.UTC(), Override: domain.Override{Forced: r.Forced, Reason: r.ForceReason},
		StartedBy: r.StartedBy, StartedAt: r.StartedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: int(r.Version),
		EndedBy: r.EndedBy.String, End: domain.RolloutEnd(r.EndReason.String),
	}
	if r.EndedAt.Valid {
		out.EndedAt = r.EndedAt.Time.UTC()
	}
	return out
}

func (s *store) InsertRollout(ctx context.Context, r domain.Rollout) (bool, error) {
	n, err := s.q.InsertRollout(ctx, releasesql.InsertRolloutParams{
		ID: r.ID, ProjectID: r.ProjectID, Environment: r.Environment,
		CandidateReleaseID: r.Candidate, StableReleaseID: r.Stable,
		Percent: int16(r.Percent), Salt: r.Salt, Status: string(r.Status), //nolint:gosec // 0-100, checked by the domain
		MaxDurationSeconds: int64(r.MaxDuration / time.Second), ExpiresAt: r.ExpiresAt,
		Forced: r.Override.Forced, ForceReason: r.Override.Reason,
		StartedBy: r.StartedBy, StartedAt: r.StartedAt, UpdatedAt: r.UpdatedAt, Version: int32Of(r.Version),
	})
	return n == 1, storeError(err)
}

func (s *store) Rollout(ctx context.Context, project, id uuid.UUID) (domain.Rollout, error) {
	row, err := s.q.GetRollout(ctx, releasesql.GetRolloutParams{ProjectID: project, ID: id})
	if err != nil {
		return domain.Rollout{}, storeError(err)
	}
	return rollout(row), nil
}

func (s *store) ActiveRollout(ctx context.Context, project uuid.UUID, environment string, lock bool) (domain.Rollout, error) {
	var (
		row releasesql.ReleaseRollout
		err error
	)
	if lock {
		row, err = s.q.LockActiveRollout(ctx, releasesql.LockActiveRolloutParams{ProjectID: project, Environment: environment})
	} else {
		row, err = s.q.GetActiveRollout(ctx, releasesql.GetActiveRolloutParams{ProjectID: project, Environment: environment})
	}
	if err != nil {
		return domain.Rollout{}, storeError(err)
	}
	return rollout(row), nil
}

func (s *store) Rollouts(ctx context.Context, project uuid.UUID, environment string, after uuid.UUID, limit int) ([]domain.Rollout, error) {
	rows, err := s.q.ListRollouts(ctx, releasesql.ListRolloutsParams{
		ProjectID: project, Environment: environment, After: nullID(after), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Rollout, 0, len(rows))
	for _, r := range rows {
		out = append(out, rollout(r))
	}
	return out, nil
}

func (s *store) UpdateRollout(ctx context.Context, r domain.Rollout, expected int) error {
	p := releasesql.UpdateRolloutParams{
		ProjectID: r.ProjectID, ID: r.ID, Percent: int16(r.Percent), Status: string(r.Status), //nolint:gosec // 0-100, checked by the domain
		UpdatedAt: r.UpdatedAt, Version: int32Of(r.Version), ExpectedVersion: int32Of(expected),
	}
	if !r.Active() {
		p.EndedBy, p.EndReason = pgtype.Text{String: r.EndedBy, Valid: true}, pgtype.Text{String: string(r.End), Valid: true}
		p.EndedAt = pgtype.Timestamptz{Time: r.EndedAt, Valid: true}
	}
	n, err := s.q.UpdateRollout(ctx, p)
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

// ExpiredRollouts implements app.Scanner.
func (s *Scanner) ExpiredRollouts(ctx context.Context, now time.Time, limit int) ([]app.RolloutRef, error) {
	var out []app.RolloutRef
	err := s.uow.InSystemTx(ctx, s.rollouts, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := releasesql.New(tx).ListExpiredRollouts(ctx, releasesql.ListExpiredRolloutsParams{Now: now, MaxRows: int32Of(limit)})
		for _, r := range rows {
			out = append(out, app.RolloutRef{
				EnvironmentRef: app.EnvironmentRef{Tenant: tenancy.ID(r.TenantID), Project: r.ProjectID, Environment: r.Environment},
				Rollout:        r.ID,
			})
		}
		return err
	})
	return out, err
}

// ActiveRollouts implements app.Scanner.
func (s *Scanner) ActiveRollouts(ctx context.Context) (int, error) {
	var n int32
	err := s.uow.InSystemTx(ctx, s.rollouts, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = releasesql.New(tx).CountActiveRollouts(ctx)
		return err
	})
	return int(n), err
}
