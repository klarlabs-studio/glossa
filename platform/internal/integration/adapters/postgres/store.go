// Package postgres implements Integration's persistence on the kernel's
// unit of work, with sqlc queries over the integration_* tables: the
// tenant-scoped Store and the cross-tenant Claimer (system scope
// integration.jobs).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres/integrationsql"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{q: integrationsql.New(tx), tx: tx})
	})
}

type store struct {
	q  *integrationsql.Queries
	tx *db.TenantTx
}

var _ app.Store = (*store)(nil)

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}

func i32(n int) int32 { return int32(n) } //nolint:gosec // counts and page sizes stay small

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func uuidPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}

func ts(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// timestamptz is ts for a time the domain spells as a value, where the
// zero time means "nobody recorded one" and the column is NULL.
func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("integration store: encode %T: %v", v, err))
	}
	return raw
}

func job(r integrationsql.IntegrationJob) (domain.Job, error) {
	j := domain.Job{
		ID: r.ID, Direction: domain.Direction(r.Direction), ProjectID: uuidPtr(r.ProjectID), Kind: domain.Kind(r.Kind),
		Format: domain.Format(r.Format), Mode: domain.Mode(r.Mode.String), State: domain.State(r.State), FileName: r.FileName,
		Fingerprint: r.Fingerprint.String, ReusedJobID: uuidPtr(r.ReusedJobID), TotalItems: int(r.TotalItems),
		ProcessedItems: int(r.ProcessedItems), FailureCode: r.FailureCode.String, FailureMessage: r.FailureMessage.String,
		CancelRequested: r.CancelRequested, Attempts: int(r.Attempts), MaxAttempts: int(r.MaxAttempts),
		AvailableAt: r.AvailableAt.UTC(), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
		StartedAt: tsPtr(r.StartedAt), FinishedAt: tsPtr(r.FinishedAt), UpdatedAt: r.UpdatedAt.UTC(),
		ExpiresAt: r.ExpiresAt.UTC(), FilesDeletedAt: tsPtr(r.FilesDeletedAt),
	}
	if r.FileKey.Valid {
		j.File = &domain.File{Key: r.FileKey.String, Size: r.FileSize.Int64, SHA256: r.FileSha256.String, ContentType: r.ContentType.String}
	}
	for name, raw := range map[string]struct {
		data []byte
		into any
	}{"options": {r.Options, &j.Options}, "access": {r.Access, &j.Access}, "summary": {r.Summary, &j.Summary}} {
		if err := json.Unmarshal(raw.data, raw.into); err != nil {
			return domain.Job{}, fmt.Errorf("integration: stored %s of job %s: %w", name, r.ID, err)
		}
	}
	return j, nil
}

func jobs(rows []integrationsql.IntegrationJob) ([]domain.Job, error) {
	out := make([]domain.Job, 0, len(rows))
	for _, r := range rows {
		j, err := job(r)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func (s *store) InsertJob(ctx context.Context, j domain.Job) (bool, error) {
	n, err := s.q.InsertJob(ctx, integrationsql.InsertJobParams{
		ID: j.ID, Direction: string(j.Direction), ProjectID: nullUUID(j.ProjectID), Kind: string(j.Kind),
		Format: string(j.Format), Mode: text(string(j.Mode)), Options: mustJSON(j.Options), Access: mustJSON(j.Access),
		State: string(j.State), FileName: j.FileName, MaxAttempts: i32(j.MaxAttempts),
		CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, ExpiresAt: j.ExpiresAt,
	})
	return n == 1, storeError(err)
}

func (s *store) Job(ctx context.Context, id uuid.UUID) (domain.Job, error) {
	r, err := s.q.GetJob(ctx, id)
	if err != nil {
		return domain.Job{}, storeError(err)
	}
	return job(r)
}

func (s *store) LockJob(ctx context.Context, id uuid.UUID) (domain.Job, error) {
	r, err := s.q.LockJob(ctx, id)
	if err != nil {
		return domain.Job{}, storeError(err)
	}
	return job(r)
}

func (s *store) LockClaimedJob(ctx context.Context, id, token uuid.UUID) (domain.Job, error) {
	r, err := s.q.LockClaimedJob(ctx, integrationsql.LockClaimedJobParams{ID: id, ClaimToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, app.ErrLeaseLost
	}
	if err != nil {
		return domain.Job{}, err
	}
	return job(r)
}

func (s *store) SaveJob(ctx context.Context, j domain.Job) error {
	p := integrationsql.SaveJobParams{
		ID: j.ID, State: string(j.State), FileName: j.FileName, Fingerprint: text(j.Fingerprint),
		ReusedJobID: nullUUID(j.ReusedJobID), Summary: mustJSON(j.Summary), TotalItems: i32(j.TotalItems),
		ProcessedItems: i32(j.ProcessedItems), FailureCode: text(j.FailureCode), FailureMessage: text(j.FailureMessage),
		CancelRequested: j.CancelRequested, StartedAt: ts(j.StartedAt),
		FinishedAt: ts(j.FinishedAt), UpdatedAt: j.UpdatedAt,
	}
	if f := j.File; f != nil {
		p.FileKey, p.FileSize = text(f.Key), pgtype.Int8{Int64: f.Size, Valid: true}
		p.FileSha256, p.ContentType = text(f.SHA256), text(f.ContentType)
	}
	return storeError(s.q.SaveJob(ctx, p))
}

func (s *store) RetryJob(ctx context.Context, j domain.Job, delay time.Duration) error {
	return storeError(s.q.RetryJob(ctx, integrationsql.RetryJobParams{
		ID: j.ID, DelaySeconds: delay.Seconds(), Summary: mustJSON(j.Summary), TotalItems: i32(j.TotalItems),
		ProcessedItems: i32(j.ProcessedItems), FailureMessage: text(j.FailureMessage), StartedAt: ts(j.StartedAt),
		UpdatedAt: j.UpdatedAt,
	}))
}

func (s *store) Jobs(ctx context.Context, f app.JobFilter, before *app.JobCursor, limit int) ([]domain.Job, error) {
	p := integrationsql.ListJobsParams{Direction: string(f.Direction), ProjectID: nullUUID(f.ProjectID),
		TenantWide: f.TenantWide, Projects: f.Projects, MaxRows: i32(limit)}
	if f.State != nil {
		p.State = text(string(*f.State))
	}
	if f.Kind != nil {
		p.Kind = text(string(*f.Kind))
	}
	if before != nil {
		p.BeforeAt, p.BeforeID = pgtype.Timestamptz{Time: before.CreatedAt, Valid: true}, before.ID
	}
	rows, err := s.q.ListJobs(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	return jobs(rows)
}

func (s *store) ReusableJob(ctx context.Context, fingerprint string, exclude uuid.UUID) (domain.Job, error) {
	r, err := s.q.ReusableJob(ctx, integrationsql.ReusableJobParams{Fingerprint: text(fingerprint), Exclude: exclude})
	if err != nil {
		return domain.Job{}, storeError(err)
	}
	return job(r)
}

func (s *store) PutItems(ctx context.Context, jobID uuid.UUID, items []domain.Item) error {
	p := integrationsql.PutItemsParams{JobID: jobID}
	for _, it := range items {
		p.Seqs = append(p.Seqs, i32(it.Seq))
		p.Kinds = append(p.Kinds, string(it.Kind))
		p.Keys = append(p.Keys, it.Key)
		p.Locales = append(p.Locales, it.Locale)
		p.Statuses = append(p.Statuses, string(it.Status))
		p.Codes = append(p.Codes, it.Code)
		p.Details = append(p.Details, it.Detail)
		p.Lines = append(p.Lines, i32(it.Line))
		p.Cols = append(p.Cols, i32(it.Column))
		p.Refs = append(p.Refs, it.Ref)
	}
	return storeError(s.q.PutItems(ctx, p))
}

func (s *store) Items(ctx context.Context, jobID uuid.UUID, f app.ItemFilter, afterSeq, limit int) ([]domain.Item, error) {
	p := integrationsql.ListItemsParams{JobID: jobID, AfterSeq: i32(afterSeq), MaxRows: i32(limit)}
	if f.Status != nil {
		p.Status = text(string(*f.Status))
	}
	if f.Kind != nil {
		p.Kind = text(string(*f.Kind))
	}
	rows, err := s.q.ListItems(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Item, len(rows))
	for i, r := range rows {
		out[i] = domain.Item{
			Seq: int(r.Seq), Kind: domain.ItemKind(r.Kind), Key: r.ItemKey, Locale: r.Locale, Status: domain.ItemStatus(r.Status),
			Code: r.Code.String, Detail: r.Detail.String, Line: int(r.Line.Int32), Column: int(r.Col.Int32), Ref: r.Ref.String,
		}
	}
	return out, nil
}

func (s *store) ProjectJobs(ctx context.Context, project uuid.UUID) ([]domain.Job, error) {
	rows, err := s.q.ProjectJobs(ctx, uuid.NullUUID{UUID: project, Valid: true})
	if err != nil {
		return nil, storeError(err)
	}
	return jobs(rows)
}

func (s *store) DeleteProjectJobs(ctx context.Context, project uuid.UUID) error {
	return storeError(s.q.DeleteProjectJobs(ctx, uuid.NullUUID{UUID: project, Valid: true}))
}

func (s *store) CheckHealth(ctx context.Context, project uuid.UUID, since time.Time) (app.CheckHealth, error) {
	r, err := s.q.CheckHealth(ctx, integrationsql.CheckHealthParams{ProjectID: project, Since: ts(&since)})
	if err != nil {
		return app.CheckHealth{}, storeError(err)
	}
	out := app.CheckHealth{
		Concluded: int(r.Concluded), Succeeded: int(r.Succeeded), Failed: int(r.Failed), Neutral: int(r.Neutral),
	}
	// -1 is the query's sentinel for a percentile over no sample. It
	// stays a zero Duration here, and Concluded == 0 is what tells a
	// caller the percentiles mean nothing — never a latency of none.
	if out.Concluded > 0 && r.P50Seconds >= 0 {
		out.P50 = time.Duration(r.P50Seconds * float64(time.Second))
		out.P90 = time.Duration(r.P90Seconds * float64(time.Second))
	}
	return out, nil
}

func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}

// Claimer implements app.Claimer in the system scope integration.jobs,
// which migration 0009 opens to SELECT and UPDATE on integration_jobs
// only.
type Claimer struct {
	uow        *db.UnitOfWork
	scope      db.SystemScope
	maxRunning int
}

// MaxRunningPerTenant bounds a tenant's jobs running at once across
// replicas, so one tenant's large imports can't starve the others.
const MaxRunningPerTenant = 2

// NewClaimer returns a claimer on uow.
func NewClaimer(uow *db.UnitOfWork) *Claimer {
	return &Claimer{uow: uow, scope: db.NewSystemScope("integration.jobs"), maxRunning: MaxRunningPerTenant}
}

var _ app.Claimer = (*Claimer)(nil)

// Claim implements app.Claimer. An advisory lock serializes claims, so
// each tenant's running count is exact across replicas.
func (c *Claimer) Claim(ctx context.Context, lease time.Duration) (app.Claim, bool, error) {
	var (
		out app.Claim
		ok  bool
	)
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		q := integrationsql.New(tx)
		if err := q.LockClaims(ctx); err != nil {
			return err
		}
		r, err := q.ClaimJob(ctx, integrationsql.ClaimJobParams{LeaseSeconds: lease.Seconds(), MaxRunning: i32(c.maxRunning)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, ok = app.Claim{JobID: r.ID, TenantID: r.TenantID, Token: r.ClaimToken.UUID, Attempts: int(r.Attempts)}, true
		return nil
	})
	if err != nil {
		return app.Claim{}, false, fmt.Errorf("integration: claim job: %w", err)
	}
	return out, ok, nil
}

// ExpiredFiles implements app.Claimer.
func (c *Claimer) ExpiredFiles(ctx context.Context, limit int) ([]app.ExpiredFile, error) {
	var out []app.ExpiredFile
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := integrationsql.New(tx).ExpiredFiles(ctx, i32(limit))
		for _, r := range rows {
			out = append(out, app.ExpiredFile{JobID: r.ID, TenantID: r.TenantID, Key: r.FileKey})
		}
		return err
	})
	return out, err
}

// MarkFilesDeleted implements app.Claimer.
func (c *Claimer) MarkFilesDeleted(ctx context.Context, ids []uuid.UUID) error {
	return c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		return integrationsql.New(tx).MarkFilesDeleted(ctx, ids)
	})
}

// ExpireUploads implements app.Claimer.
func (c *Claimer) ExpireUploads(ctx context.Context, window time.Duration) (int, error) {
	var n int64
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = integrationsql.New(tx).ExpireUploads(ctx, window.Seconds())
		return err
	})
	return int(n), err
}
