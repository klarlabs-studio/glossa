package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres/auditsql"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// ExportJobs implements app.ExportJobs on audit_export_jobs (migration
// 0053), in the tenant's own scope; its events go through the outbox in
// the same transaction.
type ExportJobs struct{ uow *db.UnitOfWork }

// NewExportJobs returns the export job store on uow.
func NewExportJobs(uow *db.UnitOfWork) *ExportJobs { return &ExportJobs{uow: uow} }

var _ app.ExportJobs = (*ExportJobs)(nil)

// Create implements app.ExportJobs.
func (s *ExportJobs) Create(ctx context.Context, j domain.ExportJob, e outbox.Event) (bool, error) {
	var inserted bool
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		p := auditsql.InsertAuditExportJobParams{
			ID: j.ID, TenantID: tx.Tenant().UUID(), MaxAttempts: int32(j.MaxAttempts), AvailableAt: j.CreatedAt,
			CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, ExpiresAt: j.ExpiresAt,
		}
		if j.Range.ByTime() {
			p.OccurredFrom, p.OccurredTo = timestamptz(j.Range.From), timestamptz(j.Range.To)
		} else {
			p.FirstSequence, p.LastSequence = seq(j.Range.FirstSequence, true), seq(j.Range.LastSequence, true)
		}
		n, err := auditsql.New(tx).InsertAuditExportJob(ctx, p)
		if err != nil || n == 0 {
			return err
		}
		inserted = true
		_, err = outbox.Publish(ctx, tx, e)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("audit: create export job: %w", err)
	}
	return inserted, nil
}

// Get implements app.ExportJobs.
func (s *ExportJobs) Get(ctx context.Context, id uuid.UUID) (domain.ExportJob, error) {
	var out domain.ExportJob
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		r, err := auditsql.New(tx).AuditExportJob(ctx, auditsql.AuditExportJobParams{TenantID: tx.Tenant().UUID(), ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrNotFound
		}
		out = jobOf(r)
		return err
	})
	return out, err
}

// List implements app.ExportJobs.
func (s *ExportJobs) List(ctx context.Context, before *app.ExportCursor, limit int) ([]domain.ExportJob, error) {
	var out []domain.ExportJob
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		p := auditsql.AuditExportJobsParams{TenantID: tx.Tenant().UUID(), PageSize: int32(min(max(limit, 1), 1000))}
		if before != nil {
			p.BeforeCreatedAt = timestamptz(before.CreatedAt)
			p.BeforeID = uuid.NullUUID{UUID: before.ID, Valid: true}
		}
		rows, err := auditsql.New(tx).AuditExportJobs(ctx, p)
		for _, r := range rows {
			out = append(out, jobOf(r))
		}
		return err
	})
	return out, err
}

// Finish implements app.ExportJobs.
func (s *ExportJobs) Finish(ctx context.Context, j domain.ExportJob, token uuid.UUID, e outbox.Event) error {
	return s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		q := auditsql.New(tx)
		tenant := tx.Tenant().UUID()
		if err := lockClaimed(ctx, q, tenant, j.ID, token); err != nil {
			return err
		}
		succeeded := j.State == domain.ExportSucceeded
		resolved := succeeded || !j.Range.ByTime()
		err := q.FinishAuditExportJob(ctx, auditsql.FinishAuditExportJobParams{
			TenantID: tenant, ID: j.ID, State: string(j.State),
			FirstSequence: seq(j.Range.FirstSequence, resolved), LastSequence: seq(j.Range.LastSequence, resolved),
			EntryCount: j.EntryCount, FirstPrevHash: text(j.FirstPrevHash), LastHash: text(j.LastHash), KeyID: text(j.KeyID),
			EntriesKey: text(j.Entries.Key), EntriesSha256: text(j.Entries.SHA256), EntriesBytes: seq(j.Entries.Bytes, succeeded),
			ManifestKey: text(j.Manifest.Key), ManifestSha256: text(j.Manifest.SHA256),
			ManifestBytes: seq(j.Manifest.Bytes, succeeded), FailureCode: text(j.FailureCode),
			FailureMessage: text(j.FailureMessage), StartedAt: timestampPtr(j.StartedAt),
			FinishedAt: timestampPtr(j.FinishedAt), UpdatedAt: j.UpdatedAt,
		})
		if err != nil {
			return fmt.Errorf("audit: finish export job: %w", err)
		}
		_, err = outbox.Publish(ctx, tx, e)
		return err
	})
}

// Retry implements app.ExportJobs.
func (s *ExportJobs) Retry(ctx context.Context, j domain.ExportJob, token uuid.UUID, cause string, delay time.Duration) error {
	return s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		n, err := auditsql.New(tx).RetryAuditExportJob(ctx, auditsql.RetryAuditExportJobParams{
			TenantID: tx.Tenant().UUID(), ID: j.ID, ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
			DelaySeconds: delay.Seconds(), FailureMessage: text(cause), StartedAt: timestampPtr(j.StartedAt),
		})
		if err == nil && n == 0 {
			return app.ErrLeaseLost
		}
		return err
	})
}

func lockClaimed(ctx context.Context, q *auditsql.Queries, tenant, id, token uuid.UUID) error {
	_, err := q.LockClaimedAuditExportJob(ctx, auditsql.LockClaimedAuditExportJobParams{
		TenantID: tenant, ID: id, ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrLeaseLost
	}
	return err
}

func seq(n int64, valid bool) pgtype.Int8 { return pgtype.Int8{Int64: n, Valid: valid} }

func jobOf(r auditsql.AuditExportJob) domain.ExportJob {
	j := domain.ExportJob{
		ID: r.ID, State: domain.ExportState(r.State), EntryCount: r.EntryCount,
		FirstPrevHash: r.FirstPrevHash.String, LastHash: r.LastHash.String, KeyID: r.KeyID.String,
		Entries:     domain.ExportObject{Key: r.EntriesKey.String, SHA256: r.EntriesSha256.String, Bytes: r.EntriesBytes.Int64},
		Manifest:    domain.ExportObject{Key: r.ManifestKey.String, SHA256: r.ManifestSha256.String, Bytes: r.ManifestBytes.Int64},
		FailureCode: r.FailureCode.String, FailureMessage: r.FailureMessage.String,
		Attempts: int(r.Attempts), MaxAttempts: int(r.MaxAttempts), CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), ExpiresAt: r.ExpiresAt.UTC(),
		StartedAt: timePtr(r.StartedAt), FinishedAt: timePtr(r.FinishedAt), FilesDeletedAt: timePtr(r.FilesDeletedAt),
	}
	if r.OccurredFrom.Valid {
		j.Range.From, j.Range.To = r.OccurredFrom.Time.UTC(), r.OccurredTo.Time.UTC()
	}
	j.Range.FirstSequence, j.Range.LastSequence = r.FirstSequence.Int64, r.LastSequence.Int64
	return j
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// ExportClaimer implements app.ExportClaimer in the system scope
// audit.export_jobs, which migration 0053 opens to SELECT and UPDATE on
// audit_export_jobs only.
type ExportClaimer struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewExportClaimer returns the claimer on uow.
func NewExportClaimer(uow *db.UnitOfWork) *ExportClaimer {
	return &ExportClaimer{uow: uow, scope: db.NewSystemScope("audit.export_jobs")}
}

var _ app.ExportClaimer = (*ExportClaimer)(nil)

// Claim implements app.ExportClaimer. An advisory lock serializes
// claims across replicas.
func (c *ExportClaimer) Claim(ctx context.Context, lease time.Duration) (app.ExportClaim, bool, error) {
	var (
		out app.ExportClaim
		ok  bool
	)
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		q := auditsql.New(tx)
		if err := q.LockAuditExportClaims(ctx); err != nil {
			return err
		}
		r, err := q.ClaimAuditExportJob(ctx, lease.Seconds())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, ok = app.ExportClaim{JobID: r.ID, TenantID: r.TenantID, Token: r.ClaimToken.UUID, Attempts: int(r.Attempts)}, true
		return nil
	})
	if err != nil {
		return app.ExportClaim{}, false, fmt.Errorf("audit: claim export job: %w", err)
	}
	return out, ok, nil
}

// Expired implements app.ExportClaimer.
func (c *ExportClaimer) Expired(ctx context.Context, limit int) ([]app.ExpiredExport, error) {
	var out []app.ExpiredExport
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := auditsql.New(tx).ExpiredAuditExports(ctx, int32(limit))
		for _, r := range rows {
			e := app.ExpiredExport{JobID: r.ID, TenantID: r.TenantID}
			for _, k := range []string{r.EntriesKey, r.ManifestKey} {
				if k != "" {
					e.Keys = append(e.Keys, k)
				}
			}
			out = append(out, e)
		}
		return err
	})
	return out, err
}

// MarkDeleted implements app.ExportClaimer.
func (c *ExportClaimer) MarkDeleted(ctx context.Context, ids []uuid.UUID) error {
	return c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		return auditsql.New(tx).MarkAuditExportsDeleted(ctx, ids)
	})
}
