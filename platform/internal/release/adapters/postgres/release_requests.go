package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/release/adapters/postgres/releasesql"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// ── release requests (RFC 0006 §5.1, migration 0047) ─────────────────

func releaseRequest(r releasesql.ReleaseRequest) (domain.ReleaseRequest, error) {
	out := domain.ReleaseRequest{
		ID: r.ID, ProjectID: r.ProjectID, Environment: r.Environment, ReleaseID: r.ReleaseID,
		Action: domain.Action(r.Action), Requester: r.Requester,
		Verdict:  domain.GateVerdict{Met: r.GateMet},
		Override: domain.Override{Forced: r.Forced, Reason: r.ForceReason},
		State:    domain.RequestState(r.State), Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(),
		DecidedBy: r.DecidedBy.String, Reason: r.Reason,
	}
	if err := json.Unmarshal(r.Approval, &out.Approval); err != nil {
		return domain.ReleaseRequest{}, fmt.Errorf("release: stored approval of request %s: %w", r.ID, err)
	}
	if err := json.Unmarshal(r.GateUnmet, &out.Verdict.Unmet); err != nil {
		return domain.ReleaseRequest{}, fmt.Errorf("release: stored verdict of request %s: %w", r.ID, err)
	}
	if r.DecidedAt.Valid {
		at := r.DecidedAt.Time.UTC()
		out.DecidedAt = &at
	}
	return out, nil
}

func (s *store) InsertReleaseRequest(ctx context.Context, r domain.ReleaseRequest) error {
	approval, err := json.Marshal(r.Approval)
	if err != nil {
		return err
	}
	unmet := r.Verdict.Unmet
	if unmet == nil {
		unmet = []string{}
	}
	gateUnmet, err := json.Marshal(unmet)
	if err != nil {
		return err
	}
	return storeError(s.q.InsertReleaseRequest(ctx, releasesql.InsertReleaseRequestParams{
		ID: r.ID, ProjectID: r.ProjectID, Environment: r.Environment, ReleaseID: r.ReleaseID, Action: string(r.Action),
		Requester: r.Requester, Approval: approval, GateMet: r.Verdict.Met, GateUnmet: gateUnmet,
		Forced: r.Override.Forced, ForceReason: r.Override.Reason, State: string(r.State), Version: int32Of(r.Version),
		CreatedAt: r.CreatedAt,
	}))
}

func (s *store) ReleaseRequest(ctx context.Context, project, id uuid.UUID, lock bool) (domain.ReleaseRequest, error) {
	var (
		row releasesql.ReleaseRequest
		err error
	)
	if lock {
		row, err = s.q.LockReleaseRequest(ctx, releasesql.LockReleaseRequestParams{ProjectID: project, ID: id})
	} else {
		row, err = s.q.GetReleaseRequest(ctx, releasesql.GetReleaseRequestParams{ProjectID: project, ID: id})
	}
	if err != nil {
		return domain.ReleaseRequest{}, storeError(err)
	}
	return releaseRequest(row)
}

func (s *store) PendingReleaseRequest(ctx context.Context, project uuid.UUID, environment string) (domain.ReleaseRequest, error) {
	row, err := s.q.LockPendingReleaseRequest(ctx, releasesql.LockPendingReleaseRequestParams{
		ProjectID: project, Environment: environment,
	})
	if err != nil {
		return domain.ReleaseRequest{}, storeError(err)
	}
	return releaseRequest(row)
}

func (s *store) ReleaseRequestFor(ctx context.Context, project, release uuid.UUID, environment string) (domain.ReleaseRequest, error) {
	row, err := s.q.ReleaseRequestFor(ctx, releasesql.ReleaseRequestForParams{
		ProjectID: project, ReleaseID: release, Environment: environment,
	})
	if err != nil {
		return domain.ReleaseRequest{}, storeError(err)
	}
	return releaseRequest(row)
}

func (s *store) ReleaseRequests(ctx context.Context, f app.RequestFilter) ([]domain.ReleaseRequest, error) {
	rows, err := s.q.ListReleaseRequests(ctx, releasesql.ListReleaseRequestsParams{
		ProjectID: f.Project, Environment: f.Environment, State: string(f.State), Before: nullID(f.Before),
		MaxRows: int32Of(f.Limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.ReleaseRequest, 0, len(rows))
	for _, r := range rows {
		req, err := releaseRequest(r)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

func (s *store) UpdateReleaseRequest(ctx context.Context, r domain.ReleaseRequest, expected int) error {
	var decidedAt pgtype.Timestamptz
	if r.DecidedAt != nil {
		decidedAt = pgtype.Timestamptz{Time: *r.DecidedAt, Valid: true}
	}
	n, err := s.q.UpdateReleaseRequest(ctx, releasesql.UpdateReleaseRequestParams{
		State: string(r.State), Version: int32Of(r.Version), DecidedBy: text(r.DecidedBy), DecidedAt: decidedAt,
		Reason: r.Reason, ProjectID: r.ProjectID, ID: r.ID, ExpectedVersion: int32Of(expected),
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}
