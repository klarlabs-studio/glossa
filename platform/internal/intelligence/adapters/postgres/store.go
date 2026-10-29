// Package postgres implements Intelligence's persistence on the kernel's
// unit of work, with sqlc queries over the intelligence_* tables: the
// tenant-scoped Store and the cross-tenant job Claimer (system scope
// intelligence.jobs).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/postgres/intelligencesql"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{q: intelligencesql.New(tx)})
	})
}

type store struct{ q *intelligencesql.Queries }

var _ app.Store = (*store)(nil)

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "intelligence_providers_tenant_id_name_key" {
		return app.ErrProviderNameTaken
	}
	return err
}

func i32(n int) int32 { return int32(n) } //nolint:gosec // versions, counts and page sizes stay small

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

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func ts(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
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
		panic(fmt.Sprintf("intelligence store: encode %T: %v", v, err))
	}
	return raw
}

// rowsAffected turns a version-checked write that changed nothing into
// ErrStaleVersion.
func rowsAffected(n int64, err error) error {
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

// ── providers ────────────────────────────────────────────────────────

func provider(r intelligencesql.IntelligenceProvider) app.StoredProvider {
	return app.StoredProvider{
		ProviderConfig: domain.ProviderConfig{
			ID: r.ID, Name: r.Name, Kind: domain.ProviderKind(r.Kind), BaseURL: r.BaseUrl, Models: r.Models,
			Enabled: r.Enabled, HasKey: len(r.ApiKeySealed) > 0, Version: int(r.Version),
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC(),
		},
		SealedKey: r.ApiKeySealed,
	}
}

func (s *store) InsertProvider(ctx context.Context, p domain.ProviderConfig, sealed []byte) (bool, error) {
	n, err := s.q.InsertProvider(ctx, intelligencesql.InsertProviderParams{
		ID: p.ID, Name: p.Name, Kind: string(p.Kind), BaseUrl: p.BaseURL, Models: nonNil(p.Models), Enabled: p.Enabled,
		ApiKeySealed: sealed, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt,
	})
	return n == 1, storeError(err)
}

func (s *store) Provider(ctx context.Context, id uuid.UUID) (domain.ProviderConfig, error) {
	r, err := s.q.GetProvider(ctx, id)
	if err != nil {
		return domain.ProviderConfig{}, storeError(err)
	}
	return provider(r).ProviderConfig, nil
}

func (s *store) LockProvider(ctx context.Context, id uuid.UUID) (app.StoredProvider, error) {
	r, err := s.q.LockProvider(ctx, id)
	if err != nil {
		return app.StoredProvider{}, storeError(err)
	}
	return provider(r), nil
}

func (s *store) Providers(ctx context.Context, after string, limit int) ([]domain.ProviderConfig, error) {
	rows, err := s.q.ListProviders(ctx, intelligencesql.ListProvidersParams{After: after, MaxRows: i32(limit)})
	out := make([]domain.ProviderConfig, len(rows))
	for i, r := range rows {
		out[i] = provider(r).ProviderConfig
	}
	return out, storeError(err)
}

func (s *store) AllProviders(ctx context.Context) ([]app.StoredProvider, error) {
	rows, err := s.q.AllProviders(ctx)
	out := make([]app.StoredProvider, len(rows))
	for i, r := range rows {
		out[i] = provider(r)
	}
	return out, storeError(err)
}

func (s *store) UpdateProvider(ctx context.Context, p domain.ProviderConfig, sealed []byte, expected int) error {
	return rowsAffected(s.q.UpdateProvider(ctx, intelligencesql.UpdateProviderParams{
		Name: p.Name, Kind: string(p.Kind), BaseUrl: p.BaseURL, Models: nonNil(p.Models), Enabled: p.Enabled,
		ApiKeySealed: sealed, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt, ID: p.ID, ExpectedVersion: i32(expected),
	}))
}

func (s *store) DeleteProvider(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteProvider(ctx, id)
	if err == nil && n == 0 {
		return app.ErrNotFound
	}
	return storeError(err)
}

// ── settings ─────────────────────────────────────────────────────────

func (s *store) Settings(ctx context.Context, lock bool) (domain.TenantSettings, bool, error) {
	get := s.q.GetSettings
	if lock {
		get = s.q.LockSettings
	}
	r, err := get(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DefaultTenantSettings(), false, nil
	}
	if err != nil {
		return domain.TenantSettings{}, false, err
	}
	out := domain.TenantSettings{
		ProviderConsent: r.ProviderConsent, ConsentChangedBy: r.ConsentChangedBy.String, ConsentChangedAt: tsPtr(r.ConsentChangedAt),
		MaxConcurrentJobs: int(r.MaxConcurrentJobs), MonthlyBudget: domain.MicroUSD(r.MonthlyBudgetMicroUsd),
		Prices: domain.PriceTable{}, Version: int(r.Version), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC(),
	}
	if err := json.Unmarshal(r.Prices, &out.Prices); err != nil {
		return domain.TenantSettings{}, false, fmt.Errorf("intelligence settings: prices: %w", err)
	}
	return out, true, nil
}

func (s *store) SaveSettings(ctx context.Context, v domain.TenantSettings, expected int) error {
	prices := v.Prices
	if prices == nil {
		prices = domain.PriceTable{}
	}
	if expected == 0 {
		n, err := s.q.InsertSettings(ctx, intelligencesql.InsertSettingsParams{
			ProviderConsent: v.ProviderConsent, ConsentChangedBy: text(v.ConsentChangedBy), ConsentChangedAt: ts(v.ConsentChangedAt),
			MaxConcurrentJobs: i32(v.MaxConcurrentJobs), MonthlyBudgetMicroUsd: int64(v.MonthlyBudget), Prices: mustJSON(prices),
			UpdatedBy: v.UpdatedBy, UpdatedAt: v.UpdatedAt,
		})
		return rowsAffected(n, err)
	}
	return rowsAffected(s.q.UpdateSettings(ctx, intelligencesql.UpdateSettingsParams{
		ProviderConsent: v.ProviderConsent, ConsentChangedBy: text(v.ConsentChangedBy), ConsentChangedAt: ts(v.ConsentChangedAt),
		MaxConcurrentJobs: i32(v.MaxConcurrentJobs), MonthlyBudgetMicroUsd: int64(v.MonthlyBudget), Prices: mustJSON(prices),
		UpdatedBy: v.UpdatedBy, UpdatedAt: v.UpdatedAt, ExpectedVersion: i32(expected),
	}))
}

func (s *store) ProjectSettings(ctx context.Context, project uuid.UUID) (domain.ProjectSettings, bool, error) {
	r, err := s.q.GetProjectSettings(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DefaultProjectSettings(project), false, nil
	}
	if err != nil {
		return domain.ProjectSettings{}, false, err
	}
	out := domain.ProjectSettings{
		ProjectID: r.ProjectID, NamespaceTags: domain.NamespaceTags{}, AutoTranslateLocales: r.AutoTranslateLocales,
		Review: domain.DefaultReviewSettings(), Version: int(r.Version), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC(),
	}
	if err := json.Unmarshal(r.NamespaceTags, &out.NamespaceTags); err != nil {
		return domain.ProjectSettings{}, false, fmt.Errorf("intelligence project settings: tags: %w", err)
	}
	if string(r.ReviewPolicy) != "{}" {
		if err := json.Unmarshal(r.ReviewPolicy, &out.Review); err != nil {
			return domain.ProjectSettings{}, false, fmt.Errorf("intelligence project settings: review: %w", err)
		}
	}
	return out, true, nil
}

func (s *store) SaveProjectSettings(ctx context.Context, v domain.ProjectSettings, expected int) error {
	tags := v.NamespaceTags
	if tags == nil {
		tags = domain.NamespaceTags{}
	}
	if expected == 0 {
		n, err := s.q.InsertProjectSettings(ctx, intelligencesql.InsertProjectSettingsParams{
			ProjectID: v.ProjectID, NamespaceTags: mustJSON(tags), AutoTranslateLocales: nonNil(v.AutoTranslateLocales),
			ReviewPolicy: mustJSON(v.Review), UpdatedBy: v.UpdatedBy, UpdatedAt: v.UpdatedAt,
		})
		return rowsAffected(n, err)
	}
	return rowsAffected(s.q.UpdateProjectSettings(ctx, intelligencesql.UpdateProjectSettingsParams{
		NamespaceTags: mustJSON(tags), AutoTranslateLocales: nonNil(v.AutoTranslateLocales), ReviewPolicy: mustJSON(v.Review),
		UpdatedBy: v.UpdatedBy, UpdatedAt: v.UpdatedAt, ProjectID: v.ProjectID, ExpectedVersion: i32(expected),
	}))
}

func routingRecord(r intelligencesql.IntelligenceRoutingPolicy) (domain.RoutingRecord, error) {
	out := domain.RoutingRecord{ProjectID: uuidPtr(r.ProjectID), Version: int(r.Version), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC()}
	if err := json.Unmarshal(r.Policy, &out.Policy); err != nil {
		return domain.RoutingRecord{}, fmt.Errorf("intelligence routing policy: %w", err)
	}
	return out, nil
}

func (s *store) RoutingPolicy(ctx context.Context, project *uuid.UUID) (domain.RoutingRecord, bool, error) {
	r, err := s.q.GetRoutingPolicy(ctx, nullUUID(project))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RoutingRecord{}, false, nil
	}
	if err != nil {
		return domain.RoutingRecord{}, false, err
	}
	rec, err := routingRecord(r)
	return rec, err == nil, err
}

func (s *store) RoutingPolicies(ctx context.Context) ([]domain.RoutingRecord, error) {
	rows, err := s.q.ListRoutingPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.RoutingRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := routingRecord(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *store) SaveRoutingPolicy(ctx context.Context, r domain.RoutingRecord, expected int) error {
	if expected == 0 {
		n, err := s.q.InsertRoutingPolicy(ctx, intelligencesql.InsertRoutingPolicyParams{
			ID: uuid.Must(uuid.NewV7()), ProjectID: nullUUID(r.ProjectID), Policy: mustJSON(r.Policy),
			UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt,
		})
		return rowsAffected(n, err)
	}
	return rowsAffected(s.q.UpdateRoutingPolicy(ctx, intelligencesql.UpdateRoutingPolicyParams{
		Policy: mustJSON(r.Policy), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt, ProjectID: nullUUID(r.ProjectID),
		ExpectedVersion: i32(expected),
	}))
}

func (s *store) DeleteRoutingPolicy(ctx context.Context, project uuid.UUID) error {
	n, err := s.q.DeleteRoutingPolicy(ctx, uuid.NullUUID{UUID: project, Valid: true})
	if err == nil && n == 0 {
		return app.ErrNotFound
	}
	return err
}

// ── spend ────────────────────────────────────────────────────────────

func (s *store) InsertSpend(ctx context.Context, e app.SpendEntry) error {
	u := e.Spend.Usage
	return s.q.InsertSpend(ctx, intelligencesql.InsertSpendParams{
		ID: e.ID, JobID: nullUUID(e.JobID), ProjectID: nullUUID(e.ProjectID), Task: string(e.Spend.Task),
		Provider: e.Spend.Provider, Model: e.Spend.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostMicroUsd: int64(e.Spend.Cost),
		Priced: e.Priced, OccurredAt: e.OccurredAt,
	})
}

func (s *store) SpendSince(ctx context.Context, since time.Time) (domain.MicroUSD, int, error) {
	r, err := s.q.SpendSince(ctx, since)
	return domain.MicroUSD(r.Total), int(r.Calls), err
}

func (s *store) SpendByProvider(ctx context.Context, since time.Time) ([]app.ProviderSpend, error) {
	rows, err := s.q.SpendByProviderSince(ctx, since)
	out := make([]app.ProviderSpend, len(rows))
	for i, r := range rows {
		out[i] = app.ProviderSpend{Provider: r.Provider, Model: r.Model, Cost: domain.MicroUSD(r.Cost), Calls: int(r.Calls),
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}
	}
	return out, err
}

func before(c *app.Cursor) (pgtype.Timestamptz, uuid.UUID) {
	if c == nil {
		return pgtype.Timestamptz{}, uuid.Nil
	}
	return pgtype.Timestamptz{Time: c.At, Valid: true}, c.ID
}

func (s *store) SpendEntries(ctx context.Context, since time.Time, c *app.Cursor, limit int) ([]app.SpendEntry, error) {
	at, id := before(c)
	rows, err := s.q.ListSpend(ctx, intelligencesql.ListSpendParams{Since: since, BeforeAt: at, BeforeID: id, MaxRows: i32(limit)})
	out := make([]app.SpendEntry, len(rows))
	for i, r := range rows {
		out[i] = app.SpendEntry{
			ID: r.ID, JobID: uuidPtr(r.JobID), ProjectID: uuidPtr(r.ProjectID), Priced: r.Priced, OccurredAt: r.OccurredAt.UTC(),
			Spend: domain.Spend{
				Task: domain.Task(r.Task), Provider: r.Provider, Model: r.Model, Cost: domain.MicroUSD(r.CostMicroUsd),
				Usage: domain.Usage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens, CacheWriteTokens: r.CacheWriteTokens},
			},
		}
	}
	return out, err
}

// ── fills ────────────────────────────────────────────────────────────

func (s *store) InsertFill(ctx context.Context, f app.Fill) error {
	return s.q.InsertFill(ctx, intelligencesql.InsertFillParams{
		ID: f.ID, ProjectID: f.ProjectID, Trigger: string(f.Trigger), Locales: f.Locales, Filter: mustJSON(f.Filter),
		RequestedBy: f.RequestedBy, CreatedAt: f.CreatedAt,
	})
}

func (s *store) FinishFill(ctx context.Context, f app.Fill) error {
	skipped := f.Skipped
	if skipped == nil {
		skipped = map[string]int{}
	}
	return s.q.FinishFill(ctx, intelligencesql.FinishFillParams{
		JobsCreated: i32(f.JobsCreated), JobsExisting: i32(f.JobsExisting), Skipped: mustJSON(skipped), ID: f.ID,
	})
}

func (s *store) Fill(ctx context.Context, id uuid.UUID) (app.Fill, error) {
	r, err := s.q.GetFill(ctx, id)
	if err != nil {
		return app.Fill{}, storeError(err)
	}
	f := app.Fill{
		ID: r.ID, ProjectID: r.ProjectID, Trigger: domain.Trigger(r.Trigger), Locales: r.Locales,
		JobsCreated: int(r.JobsCreated), JobsExisting: int(r.JobsExisting), RequestedBy: r.RequestedBy, CreatedAt: r.CreatedAt.UTC(),
	}
	if err := errors.Join(json.Unmarshal(r.Filter, &f.Filter), json.Unmarshal(r.Skipped, &f.Skipped)); err != nil {
		return app.Fill{}, fmt.Errorf("intelligence fill %s: %w", id, err)
	}
	return f, nil
}

func (s *store) FillJobCounts(ctx context.Context, id uuid.UUID) (map[domain.JobState]int, error) {
	rows, err := s.q.FillJobCounts(ctx, uuid.NullUUID{UUID: id, Valid: true})
	out := map[domain.JobState]int{}
	for _, r := range rows {
		out[domain.JobState(r.State)] = int(r.N)
	}
	return out, err
}

// ── jobs ─────────────────────────────────────────────────────────────

func job(r intelligencesql.IntelligenceJob) app.JobView {
	return app.JobView{
		Job: domain.Job{
			ID: r.ID, ProjectID: r.ProjectID, MessageID: r.MessageID, MessageKey: r.MessageKey, Namespace: r.Namespace,
			Locale: r.Locale, SourceRevision: int(r.SourceRevision), Fingerprint: r.KnowledgeFingerprint,
			Trigger: domain.Trigger(r.Trigger), FillID: uuidPtr(r.FillID), Forced: r.Forced, State: domain.JobState(r.State),
			Attempts: int(r.Attempts), MaxAttempts: int(r.MaxAttempts), AvailableAt: r.AvailableAt.UTC(),
			FailureCode: r.FailureCode.String, LastError: r.LastError.String, SuggestionID: uuidPtr(r.SuggestionID),
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), StartedAt: tsPtr(r.StartedAt), FinishedAt: tsPtr(r.FinishedAt),
			UpdatedAt: r.UpdatedAt.UTC(),
		},
		Audit: r.Audit,
	}
}

func (s *store) EnqueueJob(ctx context.Context, j domain.Job, requeue bool) (domain.Job, bool, error) {
	id, err := s.q.EnqueueJob(ctx, intelligencesql.EnqueueJobParams{
		ID: j.ID, ProjectID: j.ProjectID, MessageID: j.MessageID, MessageKey: j.MessageKey, Namespace: j.Namespace,
		Locale: j.Locale, SourceRevision: i32(j.SourceRevision), KnowledgeFingerprint: j.Fingerprint,
		Trigger: string(j.Trigger), FillID: nullUUID(j.FillID), MaxAttempts: i32(j.MaxAttempts), CreatedAt: j.CreatedAt,
		CreatedBy: j.CreatedBy, Requeue: requeue, Forced: j.Forced,
	})
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return domain.Job{}, false, err
	}
	var r intelligencesql.IntelligenceJob
	if created {
		r, err = s.q.GetJob(ctx, id)
	} else {
		r, err = s.q.GetJobByKey(ctx, intelligencesql.GetJobByKeyParams{
			MessageID: j.MessageID, Locale: j.Locale, SourceRevision: i32(j.SourceRevision), KnowledgeFingerprint: j.Fingerprint,
		})
	}
	return job(r).Job, created, storeError(err)
}

func (s *store) JobStates(ctx context.Context, jobs []domain.Job) (map[uuid.UUID]domain.JobState, error) {
	p := intelligencesql.JobStatesByKeyParams{}
	for _, j := range jobs {
		p.MessageIds = append(p.MessageIds, j.MessageID)
		p.Locales = append(p.Locales, j.Locale)
		p.SourceRevisions = append(p.SourceRevisions, i32(j.SourceRevision))
		p.Fingerprints = append(p.Fingerprints, j.Fingerprint)
	}
	rows, err := s.q.JobStatesByKey(ctx, p)
	out := make(map[uuid.UUID]domain.JobState, len(rows))
	for _, r := range rows {
		out[r.MessageID] = domain.JobState(r.State)
	}
	return out, err
}

func (s *store) Job(ctx context.Context, id uuid.UUID) (app.JobView, error) {
	r, err := s.q.GetJob(ctx, id)
	if err != nil {
		return app.JobView{}, storeError(err)
	}
	return job(r), nil
}

func (s *store) Jobs(ctx context.Context, f app.JobFilter, c *app.Cursor, limit int) ([]domain.Job, error) {
	at, id := before(c)
	p := intelligencesql.ListJobsParams{
		ProjectID: nullUUID(f.ProjectID), Locale: text(f.Locale), FillID: nullUUID(f.FillID), MessageID: nullUUID(f.MessageID),
		Projects: f.Projects, BeforeAt: at, BeforeID: id, MaxRows: i32(limit),
	}
	if f.State != nil {
		p.State = text(string(*f.State))
	}
	rows, err := s.q.ListJobs(ctx, p)
	out := make([]domain.Job, len(rows))
	for i, r := range rows {
		out[i] = job(r).Job
	}
	return out, err
}

func (s *store) CancelJob(ctx context.Context, id uuid.UUID, by string, at time.Time) (bool, error) {
	n, err := s.q.CancelJob(ctx, intelligencesql.CancelJobParams{Now: ts(&at), By: by, ID: id})
	return n == 1, err
}

func (s *store) CancelFillJobs(ctx context.Context, fill uuid.UUID, by string, at time.Time) (int, error) {
	n, err := s.q.CancelFillJobs(ctx, intelligencesql.CancelFillJobsParams{Now: ts(&at), By: by, FillID: uuid.NullUUID{UUID: fill, Valid: true}})
	return int(n), err
}

func (s *store) LockClaimedJob(ctx context.Context, id, token uuid.UUID) (domain.Job, error) {
	r, err := s.q.LockClaimedJob(ctx, intelligencesql.LockClaimedJobParams{ID: id, ClaimToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, domain.ErrLeaseLost
	}
	return job(r).Job, err
}

func (s *store) FinishJob(ctx context.Context, o app.JobOutcome) error {
	return s.q.FinishJob(ctx, intelligencesql.FinishJobParams{
		State: string(o.State), FailureCode: text(o.FailureCode), LastError: text(o.LastError),
		SuggestionID: nullUUID(o.SuggestionID), Audit: o.Audit, Now: ts(&o.At), ID: o.JobID,
	})
}

func (s *store) RetryJob(ctx context.Context, id uuid.UUID, at time.Time, o app.JobOutcome) error {
	// The delay runs on the database's clock, which claims compare with.
	return s.q.RetryJob(ctx, intelligencesql.RetryJobParams{
		DelaySeconds: max(at.Sub(o.At).Seconds(), 0), FailureCode: text(o.FailureCode), LastError: text(o.LastError),
		Audit: o.Audit, Now: o.At, ID: id,
	})
}

// ── suggestions ──────────────────────────────────────────────────────

func (s *store) InsertSuggestion(ctx context.Context, r domain.SuggestionRecord) error {
	model, err := json.Marshal(r.Model)
	if err != nil {
		return err
	}
	return s.q.InsertSuggestion(ctx, intelligencesql.InsertSuggestionParams{
		ID: r.ID, JobID: r.JobID, ProjectID: r.ProjectID, MessageID: r.MessageID, MessageKey: r.MessageKey,
		Namespace: r.Namespace, Locale: r.Locale, SourceRevision: i32(r.SourceRevision), MessageMf2: r.Message,
		Model: model, Findings: mustJSON(nonNil(r.Findings)), TermFindings: mustJSON(nonNil(r.TermFindings)),
		Provenance: mustJSON(r.Provenance), Origin: string(r.Provenance.Origin), Score: r.Confidence.Score,
		Explanation: mustJSON(nonNil(r.Confidence.Explanation)), Action: string(r.Action), ActionNote: text(r.ActionNote),
		RiskTags: nonNil(r.RiskTags), Calls: mustJSON(nonNil(r.Calls)), Usage: mustJSON(r.Usage),
		CostMicroUsd: int64(r.Cost), Status: string(r.Status), CreatedAt: r.CreatedAt,
	})
}

func suggestion(r intelligencesql.IntelligenceSuggestion) (domain.SuggestionRecord, error) {
	out := domain.SuggestionRecord{
		ID: r.ID, JobID: r.JobID, ProjectID: r.ProjectID, MessageID: r.MessageID, MessageKey: r.MessageKey,
		Namespace: r.Namespace, Locale: r.Locale, SourceRevision: int(r.SourceRevision), ActionNote: r.ActionNote.String,
		RiskTags: r.RiskTags, Status: domain.SuggestionStatus(r.Status), DecidedBy: r.DecidedBy.String,
		DecidedAt: tsPtr(r.DecidedAt), Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(),
	}
	if r.TranslationRevision.Valid {
		rev := int(r.TranslationRevision.Int32)
		out.TranslationRevision = &rev
	}
	s := &out.Suggestion
	s.MessageID, s.TargetLocale, s.Message = r.MessageID.String(), r.Locale, r.MessageMf2
	s.Confidence.Score, s.Action, s.Cost = r.Score, domain.Action(r.Action), domain.MicroUSD(r.CostMicroUsd)
	var model mf.Message
	err := errors.Join(
		json.Unmarshal(r.Model, &model),
		json.Unmarshal(r.Findings, &s.Findings),
		json.Unmarshal(r.TermFindings, &s.TermFindings),
		json.Unmarshal(r.Provenance, &s.Provenance),
		json.Unmarshal(r.Explanation, &s.Confidence.Explanation),
		json.Unmarshal(r.Calls, &s.Calls),
		json.Unmarshal(r.Usage, &s.Usage),
	)
	s.Model = model
	if len(r.Decision) > 0 {
		out.Decision = &domain.Decision{}
		err = errors.Join(err, json.Unmarshal(r.Decision, out.Decision))
	}
	if err != nil {
		return domain.SuggestionRecord{}, fmt.Errorf("intelligence suggestion %s: %w", r.ID, err)
	}
	return out, nil
}

func suggestions(rows []intelligencesql.IntelligenceSuggestion, err error) ([]domain.SuggestionRecord, error) {
	if err != nil {
		return nil, err
	}
	out := make([]domain.SuggestionRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := suggestion(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *store) SupersedePending(ctx context.Context, message uuid.UUID, locale string, keep uuid.UUID) error {
	return s.q.SupersedePending(ctx, intelligencesql.SupersedePendingParams{MessageID: message, Locale: locale, Keep: keep})
}

func (s *store) Suggestion(ctx context.Context, id uuid.UUID, lock bool) (domain.SuggestionRecord, error) {
	get := s.q.GetSuggestion
	if lock {
		get = s.q.LockSuggestion
	}
	r, err := get(ctx, id)
	if err != nil {
		return domain.SuggestionRecord{}, storeError(err)
	}
	return suggestion(r)
}

func (s *store) DecideSuggestion(ctx context.Context, r domain.SuggestionRecord, expected int) error {
	var decision []byte
	if r.Decision != nil {
		decision = mustJSON(r.Decision)
	}
	p := intelligencesql.DecideSuggestionParams{
		Status: string(r.Status), DecidedBy: text(r.DecidedBy), DecidedAt: ts(r.DecidedAt), Decision: decision,
		ActionNote: text(r.ActionNote), ID: r.ID, ExpectedVersion: i32(expected),
	}
	if r.TranslationRevision != nil {
		p.TranslationRevision = pgtype.Int4{Int32: i32(*r.TranslationRevision), Valid: true}
	}
	return rowsAffected(s.q.DecideSuggestion(ctx, p))
}

func (s *store) Suggestions(ctx context.Context, f app.SuggestionFilter, c *app.Cursor, limit int) ([]domain.SuggestionRecord, error) {
	at, id := before(c)
	p := intelligencesql.ListSuggestionsParams{
		ProjectID: nullUUID(f.ProjectID), Locale: text(f.Locale), MessageID: nullUUID(f.MessageID), JobID: nullUUID(f.JobID),
		Projects: f.Projects, BeforeAt: at, BeforeID: id, MaxRows: i32(limit),
	}
	if f.Status != nil {
		p.Status = text(string(*f.Status))
	}
	return suggestions(s.q.ListSuggestions(ctx, p))
}

func (s *store) ReviewQueue(ctx context.Context, project uuid.UUID, locales []string, after *app.QueueCursor, limit int) ([]domain.SuggestionRecord, error) {
	p := intelligencesql.ReviewQueueParams{ProjectID: project, Locales: nonNil(locales), MaxRows: i32(limit)}
	if after != nil {
		p.HasAfter, p.AfterScore, p.AfterRisk, p.AfterID = true, after.Score, i32(-after.Risk), after.ID
	}
	return suggestions(s.q.ReviewQueue(ctx, p))
}

func (s *store) DecisionStats(ctx context.Context, project uuid.UUID, since time.Time) ([]app.LocaleDecisions, error) {
	rows, err := s.q.DecisionStats(ctx, intelligencesql.DecisionStatsParams{ProjectID: project, Since: ts(&since)})
	out := make([]app.LocaleDecisions, len(rows))
	for i, r := range rows {
		out[i] = app.LocaleDecisions{
			Locale: r.Locale, Accepted: int(r.Accepted), Edited: int(r.Edited), Rejected: int(r.Rejected),
			MeanEditDistance: r.MeanEditDistance, MeanEditRatio: r.MeanEditRatio,
		}
	}
	return out, err
}

func (s *store) QueueAges(
	ctx context.Context, project uuid.UUID, locales []string, now time.Time,
) ([]app.LocaleQueueAge, error) {
	rows, err := s.q.QueueAges(ctx, intelligencesql.QueueAgesParams{
		ProjectID: project, Locales: append([]string{}, locales...), Now: now.UTC(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]app.LocaleQueueAge, len(rows))
	for i, r := range rows {
		out[i] = app.LocaleQueueAge{
			Locale: r.Locale, Waiting: int(r.Waiting),
			P50:    seconds(r.P50Seconds),
			P90:    seconds(r.P90Seconds),
			Oldest: seconds(r.OldestSeconds),
		}
	}
	return out, nil
}

// seconds turns a query's float of seconds into a Duration without
// losing the sub-second part a percentile usually has.
func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// ── disclosures ──────────────────────────────────────────────────────

func (s *store) InsertDisclosures(ctx context.Context, ds []app.DisclosureRecord) error {
	for _, d := range ds {
		if err := s.q.InsertDisclosure(ctx, intelligencesql.InsertDisclosureParams{
			ID: d.ID, JobID: d.JobID, ProjectID: d.ProjectID, MessageID: d.MessageID, Locale: d.Locale,
			Task: string(d.Task), Provider: d.Provider, Model: d.Model, SystemSha256: d.SystemSHA256,
			Sent: mustJSON(nonNil(d.Sent)), OccurredAt: d.OccurredAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) Disclosures(ctx context.Context, f app.DisclosureFilter, c *app.Cursor, limit int) ([]app.DisclosureRecord, error) {
	at, id := before(c)
	rows, err := s.q.ListDisclosures(ctx, intelligencesql.ListDisclosuresParams{
		JobID: nullUUID(f.JobID), MessageID: nullUUID(f.MessageID), ProjectID: nullUUID(f.ProjectID),
		Provider: text(f.Provider), Projects: f.Projects, BeforeAt: at, BeforeID: id, MaxRows: i32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]app.DisclosureRecord, len(rows))
	for i, r := range rows {
		out[i] = app.DisclosureRecord{
			ID: r.ID, JobID: r.JobID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
			OccurredAt: r.OccurredAt.UTC(),
			Disclosure: app.Disclosure{
				Task: domain.Task(r.Task), Provider: r.Provider, Model: r.Model, MessageID: r.MessageID.String(),
				SystemSHA256: r.SystemSha256,
			},
		}
		if err := json.Unmarshal(r.Sent, &out[i].Sent); err != nil {
			return nil, fmt.Errorf("intelligence disclosure %s: %w", r.ID, err)
		}
	}
	return out, nil
}

// DeleteProjectData implements app.Store.
func (s *store) DeleteProjectData(ctx context.Context, project uuid.UUID) error {
	return errors.Join(
		s.q.DeleteProjectSuggestions(ctx, project),
		s.q.DeleteProjectDisclosures(ctx, project),
		s.q.DeleteProjectJobs(ctx, project),
		s.q.DeleteProjectFills(ctx, project),
		s.q.DeleteProjectSettings(ctx, project),
		s.q.DeleteProjectRouting(ctx, uuid.NullUUID{UUID: project, Valid: true}),
	)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Claimer implements app.Claimer in the system scope intelligence.jobs,
// which migration 0008 opens to SELECT and UPDATE on intelligence_jobs
// and SELECT on intelligence_settings only.
type Claimer struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewClaimer returns a claimer on uow.
func NewClaimer(uow *db.UnitOfWork) *Claimer {
	return &Claimer{uow: uow, scope: db.NewSystemScope("intelligence.jobs")}
}

var _ app.Claimer = (*Claimer)(nil)

// Claim implements app.Claimer. An advisory lock serializes claims, so
// each tenant's running count (its concurrency cap) is exact across
// replicas; the claim itself is one short statement.
func (c *Claimer) Claim(ctx context.Context, lease time.Duration) (app.Claim, bool, error) {
	var (
		out app.Claim
		ok  bool
	)
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		q := intelligencesql.New(tx)
		if err := q.LockClaims(ctx); err != nil {
			return err
		}
		r, err := q.ClaimJob(ctx, lease.Seconds())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, ok = app.Claim{JobID: r.ID, TenantID: r.TenantID, Token: r.ClaimToken.UUID, Attempts: int(r.Attempts), MaxAttempts: int(r.MaxAttempts)}, true
		return nil
	})
	if err != nil {
		return app.Claim{}, false, fmt.Errorf("intelligence: claim job: %w", err)
	}
	return out, ok, nil
}

// QueueDepth implements app.Claimer.
func (c *Claimer) QueueDepth(ctx context.Context) (map[domain.JobState]int, error) {
	out := map[domain.JobState]int{domain.JobQueued: 0, domain.JobRunning: 0}
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := intelligencesql.New(tx).QueueDepth(ctx)
		for _, r := range rows {
			out[domain.JobState(r.State)] = int(r.N)
		}
		return err
	})
	return out, err
}
