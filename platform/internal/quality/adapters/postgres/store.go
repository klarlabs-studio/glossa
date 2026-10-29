// Package postgres implements Quality's persistence port on the
// kernel's unit of work, with sqlc queries over the quality_* tables:
// check runs, their immutable findings, and the waivers that accept a
// finding (migration 0027, RFC 0005 §2).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres/qualitysql"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// insertBatch bounds the rows one INSERT … unnest statement carries. A
// run holds at most 10 000 findings (RFC 0005 §10), so at most two
// statements.
const insertBatch = 5000

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{q: qualitysql.New(tx), tx: tx})
	})
}

type store struct {
	q *qualitysql.Queries
	// tx is the same transaction the queries run in, kept so a domain
	// event lands with the rows that raised it.
	tx *db.TenantTx
}

// Publish implements app.Store: the event goes in the transaction that
// wrote the run, so a rollback leaves no announcement of a run nobody
// stored.
func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}

var _ app.Store = (*store)(nil)

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}

func notFound(err, as error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return as
	}
	return err
}

//nolint:gosec // counts, offsets, revisions and limits are bounded by the domain and the columns
func int32Of(n int) int32 { return int32(n) }

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func timestamp(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timestampPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	at := t.Time.UTC()
	return &at
}

func boolean(b *bool) pgtype.Bool {
	if b == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *b, Valid: true}
}

// ── check runs ──────────────────────────────────────────────────────

func checkRun(r qualitysql.QualityCheckRun) domain.CheckRun {
	out := domain.CheckRun{
		ID: r.ID, Project: r.ProjectID, Ref: r.Ref, Commit: r.CommitSha, Trigger: domain.Trigger(r.Trigger),
		PolicyVersion: int(r.PolicyVersion), Layers: make([]domain.Layer, len(r.Layers)),
		Counts:     domain.Counts{Errors: int(r.Errors), Warnings: int(r.Warnings), Waived: int(r.Waived)},
		Conclusion: domain.Conclusion(r.Conclusion.String), CreatedBy: r.CreatedBy, StartedAt: r.StartedAt.UTC(),
	}
	for i, l := range r.Layers {
		out.Layers[i] = domain.Layer(l)
	}
	if r.CompletedAt.Valid {
		out.CompletedAt = r.CompletedAt.Time.UTC()
	}
	return out
}

func (s *store) InsertCheckRun(ctx context.Context, r domain.CheckRun) error {
	layers := make([]string, len(r.Layers))
	for i, l := range r.Layers {
		layers[i] = string(l)
	}
	return s.q.InsertCheckRun(ctx, qualitysql.InsertCheckRunParams{
		ID: r.ID, ProjectID: r.Project, Ref: r.Ref, CommitSha: r.Commit, RunTrigger: string(r.Trigger),
		PolicyVersion: int32Of(r.PolicyVersion), Layers: layers, Errors: int32Of(r.Counts.Errors),
		Warnings: int32Of(r.Counts.Warnings), Waived: int32Of(r.Counts.Waived),
		Conclusion: text(string(r.Conclusion)), CreatedBy: r.CreatedBy, StartedAt: r.StartedAt,
		CompletedAt: timestamp(r.CompletedAt),
	})
}

func (s *store) CheckRun(ctx context.Context, project, id uuid.UUID) (domain.CheckRun, error) {
	r, err := s.q.GetCheckRun(ctx, qualitysql.GetCheckRunParams{ProjectID: project, ID: id})
	if err != nil {
		return domain.CheckRun{}, notFound(err, app.ErrCheckRunNotFound)
	}
	return checkRun(r), nil
}

func (s *store) LatestCheckRun(ctx context.Context, project uuid.UUID, f app.RunFilter) (domain.CheckRun, error) {
	r, err := s.q.LatestCheckRun(ctx, qualitysql.LatestCheckRunParams{
		ProjectID: project, Ref: f.Ref, CommitSha: f.Commit, Conclusion: f.Conclusion, RunTrigger: f.Trigger,
	})
	if err != nil {
		return domain.CheckRun{}, notFound(err, app.ErrCheckRunNotFound)
	}
	return checkRun(r), nil
}

func (s *store) HasCheckRunOf(ctx context.Context, project uuid.UUID, triggers []domain.Trigger) (bool, error) {
	names := make([]string, len(triggers))
	for i, t := range triggers {
		names[i] = string(t)
	}
	found, err := s.q.HasCheckRunOf(ctx, qualitysql.HasCheckRunOfParams{ProjectID: project, RunTriggers: names})
	if err != nil {
		return false, storeError(err)
	}
	return found, nil
}

func (s *store) ListCheckRuns(ctx context.Context, project uuid.UUID, f app.RunFilter, after *app.RunCursor, limit int) ([]domain.CheckRun, error) {
	p := qualitysql.ListCheckRunsParams{
		ProjectID: project, Ref: f.Ref, CommitSha: f.Commit, Conclusion: f.Conclusion, RunTrigger: f.Trigger,
		MaxRows: int32Of(limit),
	}
	if after != nil {
		p.AfterStartedAt = pgtype.Timestamptz{Time: after.StartedAt, Valid: true}
		p.AfterID = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	rows, err := s.q.ListCheckRuns(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.CheckRun, len(rows))
	for i, r := range rows {
		out[i] = checkRun(r)
	}
	return out, nil
}

// ── findings ────────────────────────────────────────────────────────

// findingColumns is one batch's columns, as parallel arrays for the
// INSERT … unnest. uuid.Nil stands for an absent ID, 0 for an absent
// line or column, -1 for an absent span offset or source revision, and
// ” for absent JSON.
type findingColumns struct {
	p qualitysql.InsertFindingsParams
}

func (c *findingColumns) add(f domain.Finding) error {
	evidence, fix := "", ""
	if len(f.Evidence) > 0 {
		b, err := json.Marshal(f.Evidence)
		if err != nil {
			return err
		}
		evidence = string(b)
	}
	if f.Fix != nil {
		b, err := json.Marshal(f.Fix)
		if err != nil {
			return err
		}
		fix = string(b)
	}
	span := domain.Span{Start: -1, End: -1}
	if f.Locus.Span != nil {
		span = *f.Locus.Span
	}
	revision := -1
	if f.SourceRevision != nil {
		revision = *f.SourceRevision
	}
	p := &c.p
	p.Ids = append(p.Ids, uuid.Must(uuid.NewV7()))
	p.Fingerprints = append(p.Fingerprints, f.Fingerprint)
	p.Layers = append(p.Layers, string(f.Layer))
	p.Codes = append(p.Codes, f.Code)
	p.Severities = append(p.Severities, string(f.Severity))
	p.MessageIds = append(p.MessageIds, parseID(f.Locus.Message))
	p.MessageKeys = append(p.MessageKeys, f.Locus.Key)
	p.Locales = append(p.Locales, f.Locus.Locale)
	p.Namespaces = append(p.Namespaces, f.Locus.Namespace)
	p.TranslationRevisions = append(p.TranslationRevisions, parseID(f.Locus.Revision))
	p.Files = append(p.Files, f.Locus.File)
	p.Lines = append(p.Lines, int32Of(f.Locus.Line))
	p.Cols = append(p.Cols, int32Of(f.Locus.Column))
	p.Routes = append(p.Routes, f.Locus.Route)
	p.Components = append(p.Components, f.Locus.Component)
	p.CaptureIds = append(p.CaptureIds, parseID(f.Locus.Capture))
	p.Regions = append(p.Regions, f.Locus.Region)
	p.SpanSides = append(p.SpanSides, string(span.Side))
	p.SpanStarts = append(p.SpanStarts, int32Of(span.Start))
	p.SpanEnds = append(p.SpanEnds, int32Of(span.End))
	p.Explanations = append(p.Explanations, f.Message)
	p.Subjects = append(p.Subjects, f.Subject)
	p.Details = append(p.Details, f.Detail)
	p.Evidences = append(p.Evidences, evidence)
	p.Fixes = append(p.Fixes, fix)
	p.SourceRevisions = append(p.SourceRevisions, int32Of(revision))
	return nil
}

func (c *findingColumns) len() int { return len(c.p.Ids) }

// parseID reads an optional ID from the wire; anything unparseable is
// simply absent, because a locus field is a hint and never a key.
func parseID(s string) uuid.UUID {
	if s == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func (s *store) InsertFindings(ctx context.Context, run, project uuid.UUID, fs []domain.Finding) error {
	for start := 0; start < len(fs); start += insertBatch {
		end := min(start+insertBatch, len(fs))
		c := &findingColumns{p: qualitysql.InsertFindingsParams{RunID: run, ProjectID: project}}
		for _, f := range fs[start:end] {
			if err := c.add(f); err != nil {
				return err
			}
		}
		if c.len() == 0 {
			continue
		}
		if err := s.q.InsertFindings(ctx, c.p); err != nil {
			return storeError(err)
		}
	}
	return nil
}

func (s *store) ListFindings(ctx context.Context, run domain.CheckRun, f app.FindingFilter, after string, limit int, now time.Time) ([]app.FindingRecord, error) {
	rows, err := s.q.ListRunFindings(ctx, qualitysql.ListRunFindingsParams{
		RunID: run.ID, Ref: run.Ref, Now: now, Layer: f.Layer, Severity: f.Severity, Code: f.Code,
		Locale: f.Locale, Namespace: f.Namespace, MessageKey: f.Key, Waived: boolean(f.Waived),
		After: after, MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.FindingRecord, len(rows))
	for i, r := range rows {
		rec, err := finding(r)
		if err != nil {
			return nil, err
		}
		out[i] = rec
	}
	return out, nil
}

// ListCaptureFindings pages the findings on one capture, optionally on
// one of its regions. It reads across runs rather than through the
// project's newest, because the run that saw this screenshot is the one
// that ingested it (RFC 0005 §5).
func (s *store) ListCaptureFindings(
	ctx context.Context, project, capture uuid.UUID, region, after string, limit int, now time.Time,
) ([]app.FindingRecord, error) {
	rows, err := s.q.ListCaptureFindings(ctx, qualitysql.ListCaptureFindingsParams{
		ProjectID: project, CaptureID: uuid.NullUUID{UUID: capture, Valid: true}, Region: region,
		Now: now, After: after, MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.FindingRecord, len(rows))
	for i, r := range rows {
		// The two queries select the same columns in the same order, so
		// one mapper reads both rows.
		rec, err := finding(qualitysql.ListRunFindingsRow(r))
		if err != nil {
			return nil, err
		}
		out[i] = rec
	}
	return out, nil
}

// CaptureFingerprints are the distinct fingerprints one capture's
// stored findings carry: the previous sighting the two-sighting rule
// counts against (RFC 0005 §5.2).
func (s *store) CaptureFingerprints(ctx context.Context, project, capture uuid.UUID) ([]string, error) {
	fps, err := s.q.ListCaptureFingerprints(ctx, qualitysql.ListCaptureFingerprintsParams{
		ProjectID: project, CaptureID: uuid.NullUUID{UUID: capture, Valid: true},
	})
	if err != nil {
		return nil, storeError(err)
	}
	return fps, nil
}

func finding(r qualitysql.ListRunFindingsRow) (app.FindingRecord, error) {
	f := domain.Finding{
		Schema: domain.Schema, Fingerprint: r.Fingerprint, Layer: domain.Layer(r.Layer), Code: r.Code,
		Severity: domain.Severity(r.EffectiveSeverity), Message: r.Explanation, Subject: r.Subject, Detail: r.Detail,
		Locus: domain.Locus{
			Key: r.MessageKey, Locale: r.Locale, Namespace: r.Namespace, File: r.File,
			Line: int(r.Line.Int32), Column: int(r.Col.Int32), Route: r.Route, Component: r.Component, Region: r.Region,
		},
	}
	if r.MessageID.Valid {
		f.Locus.Message = r.MessageID.UUID.String()
	}
	if r.TranslationRevision.Valid {
		f.Locus.Revision = r.TranslationRevision.UUID.String()
	}
	if r.CaptureID.Valid {
		f.Locus.Capture = r.CaptureID.UUID.String()
	}
	if r.SpanSide.Valid {
		f.Locus.Span = &domain.Span{Side: domain.Side(r.SpanSide.String), Start: int(r.SpanStart.Int32), End: int(r.SpanEnd.Int32)}
	}
	if r.SourceRevision.Valid {
		revision := int(r.SourceRevision.Int32)
		f.SourceRevision = &revision
	}
	if len(r.Evidence) > 0 {
		if err := json.Unmarshal(r.Evidence, &f.Evidence); err != nil {
			return app.FindingRecord{}, err
		}
	}
	if len(r.Fix) > 0 {
		if err := json.Unmarshal(r.Fix, &f.Fix); err != nil {
			return app.FindingRecord{}, err
		}
	}
	if r.IsWaived {
		f.Waiver = r.WaiverID.String()
	}
	return app.FindingRecord{Finding: f, Waived: r.IsWaived, SortKey: r.SortKey}, nil
}

func (s *store) CountFindings(ctx context.Context, run domain.CheckRun, now time.Time) (domain.Counts, error) {
	c, err := s.q.CountRunFindings(ctx, qualitysql.CountRunFindingsParams{RunID: run.ID, Ref: run.Ref, Now: now})
	if err != nil {
		return domain.Counts{}, storeError(err)
	}
	return domain.Counts{Errors: int(c.Errors), Warnings: int(c.Warnings), Waived: int(c.Waived)}, nil
}

func (s *store) CountFindingsByLayer(
	ctx context.Context, run domain.CheckRun, now time.Time,
) ([]domain.LocaleLayerCount, error) {
	rows, err := s.q.CountRunFindingsByLayer(ctx, qualitysql.CountRunFindingsByLayerParams{
		RunID: run.ID, Ref: run.Ref, Now: now,
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.LocaleLayerCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.LocaleLayerCount{
			Locale: r.Locale,
			LayerCount: domain.LayerCount{
				Layer:  domain.Layer(r.Layer),
				Counts: domain.Counts{Errors: int(r.Errors), Warnings: int(r.Warnings), Waived: int(r.Waived)},
			},
		})
	}
	return out, nil
}

func (s *store) RollUpFindingsByDay(ctx context.Context, project uuid.UUID, day time.Time) error {
	start := day.UTC().Truncate(24 * time.Hour)
	return s.q.RollUpFindingsByDay(ctx, qualitysql.RollUpFindingsByDayParams{
		ProjectID: project,
		Day:       pgtype.Date{Time: start, Valid: true},
		DayStart:  start,
		NextDay:   start.AddDate(0, 0, 1),
	})
}

func (s *store) FindingsByDay(
	ctx context.Context, project uuid.UUID, from, to time.Time,
) ([]domain.DailyFindings, error) {
	rows, err := s.q.ListFindingsByDay(ctx, qualitysql.ListFindingsByDayParams{
		ProjectID: project,
		FromDay:   pgtype.Date{Time: from.UTC().Truncate(24 * time.Hour), Valid: true},
		ToDay:     pgtype.Date{Time: to.UTC().Truncate(24 * time.Hour), Valid: true},
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.DailyFindings, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.DailyFindings{
			Day: r.Day.Time.UTC(), Layer: domain.Layer(r.Layer), Findings: int(r.Findings),
		})
	}
	return out, nil
}

func (s *store) LatestFinding(ctx context.Context, project uuid.UUID, fingerprint string) (app.FindingSummary, bool, error) {
	r, err := s.q.GetLatestFinding(ctx, qualitysql.GetLatestFindingParams{ProjectID: project, Fingerprint: fingerprint})
	if errors.Is(err, pgx.ErrNoRows) {
		return app.FindingSummary{}, false, nil
	}
	if err != nil {
		return app.FindingSummary{}, false, err
	}
	return app.FindingSummary{
		Layer: r.Layer, Code: r.Code, Locale: r.Locale, Key: r.MessageKey, Namespace: r.Namespace,
		Explanation: r.Explanation, SourceRevision: int(r.SourceRevision.Int32),
	}, true, nil
}

// ── waivers ─────────────────────────────────────────────────────────

func waiver(r qualitysql.QualityWaiver) domain.Waiver {
	return domain.Waiver{
		ID: r.ID, Project: r.ProjectID, Fingerprint: r.Fingerprint, Reason: r.Reason,
		Scope: domain.WaiverScope(r.Scope), Ref: r.Ref, SourceRevision: int(r.SourceRevision),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), ExpiresAt: timePtr(r.ExpiresAt),
		ExpiredAt: timePtr(r.ExpiredAt), RevokedAt: timePtr(r.RevokedAt),
	}
}

func (s *store) UpsertWaiver(ctx context.Context, w domain.Waiver) (domain.Waiver, bool, error) {
	r, err := s.q.UpsertWaiver(ctx, qualitysql.UpsertWaiverParams{
		ID: w.ID, ProjectID: w.Project, Fingerprint: w.Fingerprint, Reason: w.Reason, Scope: string(w.Scope),
		Ref: w.Ref, SourceRevision: int32Of(w.SourceRevision), CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt,
		ExpiresAt: timestampPtr(w.ExpiresAt),
	})
	if err != nil {
		return domain.Waiver{}, false, storeError(err)
	}
	return waiver(qualitysql.QualityWaiver{
		ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, Fingerprint: r.Fingerprint, Reason: r.Reason,
		Scope: r.Scope, Ref: r.Ref, SourceRevision: r.SourceRevision, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, ExpiredAt: r.ExpiredAt, RevokedAt: r.RevokedAt,
	}), r.Inserted, nil
}

func (s *store) Waiver(ctx context.Context, project, id uuid.UUID) (domain.Waiver, error) {
	r, err := s.q.GetWaiver(ctx, qualitysql.GetWaiverParams{ProjectID: project, ID: id})
	if err != nil {
		return domain.Waiver{}, notFound(err, app.ErrWaiverNotFound)
	}
	return waiver(r), nil
}

func (s *store) ListWaivers(ctx context.Context, project uuid.UUID, f app.WaiverFilter, after *app.WaiverCursor, limit int, now time.Time) ([]app.WaiverRecord, error) {
	p := qualitysql.ListWaiversParams{
		ProjectID: project, Fingerprint: f.Fingerprint, Layer: f.Layer, Code: f.Code, MessageKey: f.Key,
		Active: boolean(f.Active), Now: now, MaxRows: int32Of(limit),
	}
	if after != nil {
		p.AfterCreatedAt = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		p.AfterID = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	rows, err := s.q.ListWaivers(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.WaiverRecord, len(rows))
	for i, r := range rows {
		w := waiver(qualitysql.QualityWaiver{
			ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, Fingerprint: r.Fingerprint, Reason: r.Reason,
			Scope: r.Scope, Ref: r.Ref, SourceRevision: r.SourceRevision, CreatedBy: r.CreatedBy,
			CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, ExpiredAt: r.ExpiredAt, RevokedAt: r.RevokedAt,
		})
		out[i] = app.WaiverRecord{
			Waiver: w,
			Accepts: app.FindingSummary{
				Layer: r.FindingLayer, Code: r.FindingCode, Locale: r.FindingLocale, Key: r.FindingMessageKey,
				Namespace: r.FindingNamespace, Explanation: r.FindingExplanation,
			},
			Active: w.Live(now),
		}
	}
	return out, nil
}

func (s *store) RevokeWaiver(ctx context.Context, project, id uuid.UUID, at time.Time) error {
	_, err := s.q.RevokeWaiver(ctx, qualitysql.RevokeWaiverParams{
		ProjectID: project, ID: id, RevokedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	return storeError(err)
}

func (s *store) LiveWaivers(ctx context.Context, project uuid.UUID, now time.Time) ([]domain.Waiver, error) {
	rows, err := s.q.ListLiveWaivers(ctx, qualitysql.ListLiveWaiversParams{ProjectID: project, Now: now})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Waiver, len(rows))
	for i, r := range rows {
		out[i] = waiver(r)
	}
	return out, nil
}

// ── check-policy versions ───────────────────────────────────────────
//
// The document that grades lives in the project's settings, which
// Catalog owns; this is the append-only record of how it got there
// (migration 0031, RFC 0005 §4.3).

func policyVersion(r qualitysql.QualityPolicyVersion) (app.PolicyVersion, error) {
	out := app.PolicyVersion{
		ID: r.ID, Project: r.ProjectID, Version: int(r.Version),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
	}
	if err := json.Unmarshal(r.Document, &out.Policy); err != nil {
		return app.PolicyVersion{}, err
	}
	return out, nil
}

func (s *store) InsertPolicyVersion(ctx context.Context, v app.PolicyVersion) (bool, error) {
	doc, err := json.Marshal(v.Policy)
	if err != nil {
		return false, err
	}
	n, err := s.q.InsertPolicyVersion(ctx, qualitysql.InsertPolicyVersionParams{
		ID: v.ID, ProjectID: v.Project, Version: int32Of(v.Version), Document: doc,
		CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
	})
	if err != nil {
		return false, storeError(err)
	}
	return n > 0, nil
}

func (s *store) PolicyVersion(ctx context.Context, project uuid.UUID, version int) (app.PolicyVersion, error) {
	r, err := s.q.GetPolicyVersion(ctx, qualitysql.GetPolicyVersionParams{
		ProjectID: project, Version: int32Of(version),
	})
	if err != nil {
		return app.PolicyVersion{}, notFound(err, app.ErrPolicyVersionNotFound)
	}
	return policyVersion(r)
}

func (s *store) ListPolicyVersions(
	ctx context.Context, project uuid.UUID, after *int, limit int,
) ([]app.PolicyVersion, error) {
	p := qualitysql.ListPolicyVersionsParams{ProjectID: project, MaxRows: int32Of(limit)}
	if after != nil {
		p.AfterVersion = pgtype.Int4{Int32: int32Of(*after), Valid: true}
	}
	rows, err := s.q.ListPolicyVersions(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.PolicyVersion, len(rows))
	for i, r := range rows {
		if out[i], err = policyVersion(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}
