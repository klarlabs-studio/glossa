package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres/qualitysql"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Linguistic-QA jobs (migration 0038, RFC 0005 §3.8). The rows are
// Quality's because what a job produces is findings; the model call is
// Intelligence's and is reached through an application port.

// scopeDocument is the job's scope as the column holds it. It is stored
// as a document rather than as columns because it is read whole and
// never selected on: a job is looked up by id, or listed for a project.
type scopeDocument struct {
	Locales   []string `json:"locales"`
	Namespace string   `json:"namespace,omitempty"`
	KeyPrefix string   `json:"key_prefix,omitempty"`
	Keys      []string `json:"keys,omitempty"`
}

func (s *store) InsertLinguisticJob(ctx context.Context, j domain.LinguisticJob) error {
	scope, err := json.Marshal(scopeDocument{
		Locales: j.Scope.Locales, Namespace: j.Scope.Namespace, KeyPrefix: j.Scope.KeyPrefix, Keys: j.Scope.Keys,
	})
	if err != nil {
		return err
	}
	return storeError(s.q.InsertLinguisticJob(ctx, qualitysql.InsertLinguisticJobParams{
		ID: j.ID, ProjectID: j.Project, Ref: j.Ref, Scope: scope, State: string(j.State),
		Batch: j.Batch, CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt,
	}))
}

func (s *store) LinguisticJob(ctx context.Context, project, id uuid.UUID) (domain.LinguisticJob, error) {
	r, err := s.q.GetLinguisticJob(ctx, qualitysql.GetLinguisticJobParams{ProjectID: project, ID: id})
	if err != nil {
		return domain.LinguisticJob{}, notFound(err, app.ErrLinguisticJobNotFound)
	}
	return linguisticJob(r)
}

func (s *store) ListLinguisticJobs(
	ctx context.Context, project uuid.UUID, state string, after *app.LinguisticCursor, limit int,
) ([]domain.LinguisticJob, error) {
	p := qualitysql.ListLinguisticJobsParams{ProjectID: project, State: text(state), MaxRows: int32Of(limit)}
	if after != nil {
		p.AfterAt = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		p.AfterID = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	rows, err := s.q.ListLinguisticJobs(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.LinguisticJob, len(rows))
	for i, r := range rows {
		if out[i], err = linguisticJob(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// UpdateLinguisticJob writes the job's progress. The statement moves
// only a job that has not finished, so `moved` being false says another
// reader settled it first and this one should read back what stands.
func (s *store) UpdateLinguisticJob(ctx context.Context, j domain.LinguisticJob) (bool, error) {
	p := qualitysql.UpdateLinguisticJobParams{
		State: string(j.State), Batch: j.Batch, Findings: int32Of(j.Findings),
		SkippedSensitive: int32Of(j.SkippedSensitive), Reviewed: int32Of(j.Reviewed),
		FailureCode: j.FailureCode, LastError: j.LastError, UpdatedAt: j.UpdatedAt,
		StartedAt: timestampPtr(j.StartedAt), FinishedAt: timestampPtr(j.FinishedAt),
		ProjectID: j.Project, ID: j.ID,
	}
	if j.CheckRun != nil {
		p.CheckRunID = uuid.NullUUID{UUID: *j.CheckRun, Valid: true}
	}
	n, err := s.q.UpdateLinguisticJob(ctx, p)
	if err != nil {
		return false, storeError(err)
	}
	return n > 0, nil
}

func linguisticJob(r qualitysql.QualityLinguisticJob) (domain.LinguisticJob, error) {
	var scope scopeDocument
	if err := json.Unmarshal(r.Scope, &scope); err != nil {
		return domain.LinguisticJob{}, err
	}
	state, err := domain.ParseLinguisticJobState(r.State)
	if err != nil {
		return domain.LinguisticJob{}, err
	}
	j := domain.LinguisticJob{
		ID: r.ID, Project: r.ProjectID, Ref: r.Ref,
		Scope: domain.LinguisticScope{
			Locales: scope.Locales, Namespace: scope.Namespace, KeyPrefix: scope.KeyPrefix, Keys: scope.Keys,
		},
		State: state, Batch: r.Batch, Findings: int(r.Findings),
		SkippedSensitive: int(r.SkippedSensitive), Reviewed: int(r.Reviewed),
		FailureCode: r.FailureCode, LastError: r.LastError, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
	if r.CheckRunID.Valid {
		run := r.CheckRunID.UUID
		j.CheckRun = &run
	}
	if r.StartedAt.Valid {
		at := r.StartedAt.Time.UTC()
		j.StartedAt = &at
	}
	if r.FinishedAt.Valid {
		at := r.FinishedAt.Time.UTC()
		j.FinishedAt = &at
	}
	return j, nil
}
