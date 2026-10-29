// Package postgres implements Localization's persistence port on the
// kernel's unit of work, with sqlc queries over the localization_*
// tables.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres/localizationsql"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{tx: tx, q: localizationsql.New(tx)})
	})
}

// InCurrent implements app.Transactor.
func (t *Transactor) InCurrent(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InCurrentTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{tx: tx, q: localizationsql.New(tx)})
	})
}

type store struct {
	tx *db.TenantTx
	q  *localizationsql.Queries
}

var _ app.Store = (*store)(nil)

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == "localization_translations_message_id_locale_key" {
		return app.ErrConcurrentWrite
	}
	return err
}

func int32Of(n int) int32 { return int32(n) } //nolint:gosec // versions, revisions and page sizes stay small

// ── locales ─────────────────────────────────────────────────────────

func (s *store) InsertLocale(ctx context.Context, l domain.Locale, by string) (bool, error) {
	n, err := s.q.InsertLocale(ctx, localizationsql.InsertLocaleParams{
		ProjectID: l.ProjectID, Code: l.Code.String(), IsSource: l.IsSource, CreatedBy: by, CreatedAt: l.CreatedAt,
	})
	return n == 1, storeError(err)
}

func locale(r localizationsql.LocalizationLocale) (domain.Locale, error) {
	code, err := bcp47.Parse(r.Code)
	if err != nil {
		return domain.Locale{}, fmt.Errorf("localization: stored locale %q: %w", r.Code, err)
	}
	return domain.Locale{ProjectID: r.ProjectID, Code: code, IsSource: r.IsSource, CreatedAt: r.CreatedAt.UTC()}, nil
}

func locales(rows []localizationsql.LocalizationLocale) ([]domain.Locale, error) {
	out := make([]domain.Locale, 0, len(rows))
	for _, r := range rows {
		l, err := locale(r)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

func (s *store) Locale(ctx context.Context, project uuid.UUID, code bcp47.Tag) (domain.Locale, error) {
	row, err := s.q.GetLocale(ctx, localizationsql.GetLocaleParams{ProjectID: project, Code: code.String()})
	if err != nil {
		return domain.Locale{}, storeError(err)
	}
	return locale(row)
}

func (s *store) Locales(ctx context.Context, project uuid.UUID, after string, limit int) ([]domain.Locale, error) {
	rows, err := s.q.ListLocales(ctx, localizationsql.ListLocalesParams{ProjectID: project, After: after, MaxRows: int32Of(limit)})
	if err != nil {
		return nil, storeError(err)
	}
	return locales(rows)
}

func (s *store) AllLocales(ctx context.Context, project uuid.UUID) ([]domain.Locale, error) {
	rows, err := s.q.AllLocales(ctx, project)
	if err != nil {
		return nil, storeError(err)
	}
	return locales(rows)
}

func (s *store) DeleteLocale(ctx context.Context, project uuid.UUID, code bcp47.Tag) error {
	n, err := s.q.DeleteLocale(ctx, localizationsql.DeleteLocaleParams{ProjectID: project, Code: code.String()})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ── fallback graphs ─────────────────────────────────────────────────

func (s *store) FallbackGraph(ctx context.Context, project uuid.UUID, lock bool) (app.Graph, error) {
	var (
		row localizationsql.LocalizationFallbackGraph
		err error
	)
	if lock {
		row, err = s.q.LockFallbackGraph(ctx, project)
	} else {
		row, err = s.q.GetFallbackGraph(ctx, project)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Graph{Edges: map[string][]string{}}, nil
	}
	if err != nil {
		return app.Graph{}, storeError(err)
	}
	g := app.Graph{Edges: map[string][]string{}, Version: int(row.Version)}
	if err := json.Unmarshal(row.Edges, &g.Edges); err != nil {
		return app.Graph{}, fmt.Errorf("localization: stored fallback graph of %s: %w", project, err)
	}
	return g, nil
}

func (s *store) SaveFallbackGraph(ctx context.Context, project uuid.UUID, edges map[string][]string, version, expected int, by string, at time.Time) error {
	b, err := json.Marshal(edges)
	if err != nil {
		return err
	}
	var n int64
	if expected == 0 {
		n, err = s.q.InsertFallbackGraph(ctx, localizationsql.InsertFallbackGraphParams{
			ProjectID: project, Edges: b, Version: int32Of(version), UpdatedBy: by, UpdatedAt: at,
		})
	} else {
		n, err = s.q.UpdateFallbackGraph(ctx, localizationsql.UpdateFallbackGraphParams{
			ProjectID: project, Edges: b, Version: int32Of(version), UpdatedBy: by, UpdatedAt: at,
			ExpectedVersion: int32Of(expected),
		})
	}
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrPreconditionFailed
	}
	return nil
}

// ── message projection ──────────────────────────────────────────────

func (s *store) LockMessageState(ctx context.Context, id uuid.UUID) (app.MessageState, bool, error) {
	row, err := s.q.LockMessageState(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MessageState{}, false, nil
	}
	if err != nil {
		return app.MessageState{}, false, storeError(err)
	}
	return app.MessageState{
		MessageID: row.MessageID, ProjectID: row.ProjectID, Key: row.Key, Namespace: row.Namespace,
		State: row.State, SourceRevision: int(row.SourceRevision), Version: int(row.Version), UpdatedAt: row.UpdatedAt,
	}, true, nil
}

func (s *store) SaveMessageState(ctx context.Context, m app.MessageState) error {
	_, err := s.q.UpsertMessageState(ctx, localizationsql.UpsertMessageStateParams{
		MessageID: m.MessageID, ProjectID: m.ProjectID, Key: m.Key, Namespace: m.Namespace, State: m.State,
		SourceRevision: int32Of(m.SourceRevision), Version: int32Of(m.Version), UpdatedAt: m.UpdatedAt,
	})
	return storeError(err)
}

func optText(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *p, Valid: true}
}

// likePattern turns a key prefix into a LIKE pattern, escaping the
// wildcards _ and % (both are legal in keys).
func likePattern(prefix string) pgtype.Text {
	if prefix == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: strings.NewReplacer(`\`, `\\`, `_`, `\_`, `%`, `\%`).Replace(prefix) + "%", Valid: true}
}

func (s *store) MessagesWithCoverage(ctx context.Context, project uuid.UUID, f app.CoverageFilter) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if f.Outdated {
		rows, err := s.q.MessagesOutdatedIn(ctx, localizationsql.MessagesOutdatedInParams{
			ProjectID: project, After: f.AfterKey, Namespace: optText(f.Namespace), State: optText(f.State),
			KeyLike: likePattern(f.KeyPrefix), Locale: f.Locale, MaxRows: int32Of(f.Limit),
		})
		if err != nil {
			return nil, storeError(err)
		}
		for _, r := range rows {
			ids = append(ids, r.MessageID)
		}
		return ids, nil
	}
	rows, err := s.q.MessagesMissingIn(ctx, localizationsql.MessagesMissingInParams{
		ProjectID: project, After: f.AfterKey, Namespace: optText(f.Namespace), State: optText(f.State),
		KeyLike: likePattern(f.KeyPrefix), Locale: f.Locale, MaxRows: int32Of(f.Limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	for _, r := range rows {
		ids = append(ids, r.MessageID)
	}
	return ids, nil
}

func (s *store) CurrentTranslationCounts(ctx context.Context, project uuid.UUID, ids []uuid.UUID) (map[string]int, error) {
	rows, err := s.q.CountCurrentTranslations(ctx, localizationsql.CountCurrentTranslationsParams{
		ProjectID: project, MessageIds: ids,
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Locale] = int(r.Translations)
	}
	return out, nil
}

// ── translations ────────────────────────────────────────────────────

func findingsJSON(fs []mf.Finding) []byte {
	if fs == nil {
		fs = []mf.Finding{}
	}
	b, _ := json.Marshal(fs) // plain structs of strings
	return b
}

func translation(r localizationsql.LocalizationTranslation) (domain.Translation, error) {
	loc, err := bcp47.Parse(r.Locale)
	if err != nil {
		return domain.Translation{}, fmt.Errorf("localization: stored locale of translation %s: %w", r.ID, err)
	}
	c, err := mfcontent.Restore(mfcontent.Syntax(r.Syntax), r.Text, r.Model)
	if err != nil {
		return domain.Translation{}, fmt.Errorf("localization: stored text of translation %s: %w", r.ID, err)
	}
	var warnings []mf.Finding
	if err := json.Unmarshal(r.Warnings, &warnings); err != nil {
		return domain.Translation{}, fmt.Errorf("localization: stored warnings of translation %s: %w", r.ID, err)
	}
	return domain.Translation{
		ID: domain.TranslationID(r.ID), ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: loc, Content: c,
		State: domain.ReviewState(r.State), Origin: domain.Origin(r.Origin), By: r.Author,
		SourceRevision: int(r.SourceRevision), Warnings: warnings, Revision: int(r.Revision),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

func row(t localizationsql.LocalizationTranslation, current int32) (app.TranslationRow, error) {
	tr, err := translation(t)
	return app.TranslationRow{Translation: tr, CurrentSourceRevision: int(current)}, err
}

func (s *store) Translation(ctx context.Context, message uuid.UUID, loc bcp47.Tag) (app.TranslationRow, error) {
	r, err := s.q.GetTranslation(ctx, localizationsql.GetTranslationParams{MessageID: message, Locale: loc.String()})
	if err != nil {
		return app.TranslationRow{}, storeError(err)
	}
	return row(localizationsql.LocalizationTranslation{
		ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
		Syntax: r.Syntax, Text: r.Text, Model: r.Model, State: r.State, Origin: r.Origin, Author: r.Author,
		SourceRevision: r.SourceRevision, Warnings: r.Warnings, Revision: r.Revision,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.CurrentSourceRevision)
}

func (s *store) TranslationByID(ctx context.Context, id domain.TranslationID) (app.TranslationRow, string, string, error) {
	r, err := s.q.GetTranslationByID(ctx, id.UUID())
	if err != nil {
		return app.TranslationRow{}, "", "", storeError(err)
	}
	tr, err := row(localizationsql.LocalizationTranslation{
		ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
		Syntax: r.Syntax, Text: r.Text, Model: r.Model, State: r.State, Origin: r.Origin, Author: r.Author,
		SourceRevision: r.SourceRevision, Warnings: r.Warnings, Revision: r.Revision,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.CurrentSourceRevision)
	return tr, r.Key, r.Namespace, err
}

func (s *store) TranslationsByID(ctx context.Context, project uuid.UUID, ids []domain.TranslationID) (map[domain.TranslationID]app.NamedTranslationRow, error) {
	uuids := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		uuids[i] = id.UUID()
	}
	rows, err := s.q.GetTranslationsByIDs(ctx, localizationsql.GetTranslationsByIDsParams{ProjectID: project, Ids: uuids})
	if err != nil {
		return nil, storeError(err)
	}
	out := make(map[domain.TranslationID]app.NamedTranslationRow, len(rows))
	for _, r := range rows {
		tr, err := row(localizationsql.LocalizationTranslation{
			ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
			Syntax: r.Syntax, Text: r.Text, Model: r.Model, State: r.State, Origin: r.Origin, Author: r.Author,
			SourceRevision: r.SourceRevision, Warnings: r.Warnings, Revision: r.Revision,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}, r.CurrentSourceRevision)
		if err != nil {
			return nil, err
		}
		out[tr.ID] = app.NamedTranslationRow{TranslationRow: tr, Key: r.Key, Namespace: r.Namespace}
	}
	return out, nil
}

func (s *store) LockTranslation(ctx context.Context, message uuid.UUID, loc bcp47.Tag) (domain.Translation, bool, error) {
	r, err := s.q.LockTranslation(ctx, localizationsql.LockTranslationParams{MessageID: message, Locale: loc.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Translation{}, false, nil
	}
	if err != nil {
		return domain.Translation{}, false, storeError(err)
	}
	t, err := translation(r)
	return t, err == nil, err
}

func (s *store) InsertTranslation(ctx context.Context, t domain.Translation) error {
	return storeError(s.q.InsertTranslation(ctx, localizationsql.InsertTranslationParams{
		ID: t.ID.UUID(), ProjectID: t.ProjectID, MessageID: t.MessageID, Locale: t.Locale.String(),
		Syntax: string(t.Content.Syntax), Text: t.Content.Text, Model: t.Content.ModelJSON(), State: string(t.State),
		Origin: string(t.Origin), Author: t.By, SourceRevision: int32Of(t.SourceRevision),
		Warnings: findingsJSON(t.Warnings), Revision: int32Of(t.Revision), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}))
}

func (s *store) UpdateTranslation(ctx context.Context, t domain.Translation, expected int) error {
	n, err := s.q.UpdateTranslation(ctx, localizationsql.UpdateTranslationParams{
		ID: t.ID.UUID(), Syntax: string(t.Content.Syntax), Text: t.Content.Text, Model: t.Content.ModelJSON(),
		State: string(t.State), Origin: string(t.Origin), Author: t.By, SourceRevision: int32Of(t.SourceRevision),
		Warnings: findingsJSON(t.Warnings), Revision: int32Of(t.Revision), UpdatedAt: t.UpdatedAt,
		ExpectedRevision: int32Of(expected),
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *store) AppendRevision(ctx context.Context, r domain.Revision) error {
	return storeError(s.q.InsertTranslationRevision(ctx, localizationsql.InsertTranslationRevisionParams{
		TranslationID: r.TranslationID.UUID(), Revision: int32Of(r.Number), Kind: string(r.Kind),
		Syntax: string(r.Content.Syntax), Text: r.Content.Text, Model: r.Content.ModelJSON(), State: string(r.State),
		Origin: string(r.Provenance.Origin), OriginDetail: r.Provenance.Detail, Author: r.Provenance.By,
		SourceRevision: int32Of(r.SourceRevision), Findings: findingsJSON(r.Findings), SelfReview: r.SelfReview, CreatedAt: r.CreatedAt,
	}))
}

func (s *store) TranslationsOfMessage(ctx context.Context, message uuid.UUID, after string, only []string, limit int) ([]app.TranslationRow, error) {
	rows, err := s.q.ListTranslationsOfMessage(ctx, localizationsql.ListTranslationsOfMessageParams{
		MessageID: message, After: after, OnlyLocales: only, MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.TranslationRow, 0, len(rows))
	for _, r := range rows {
		tr, err := row(localizationsql.LocalizationTranslation{
			ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
			Syntax: r.Syntax, Text: r.Text, Model: r.Model, State: r.State, Origin: r.Origin, Author: r.Author,
			SourceRevision: r.SourceRevision, Warnings: r.Warnings, Revision: r.Revision,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}, r.CurrentSourceRevision)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, nil
}

func (s *store) Revisions(ctx context.Context, t domain.TranslationID, before, limit int) ([]domain.Revision, error) {
	rows, err := s.q.ListTranslationRevisions(ctx, localizationsql.ListTranslationRevisionsParams{
		TranslationID: t.UUID(), Before: int32Of(before), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Revision, 0, len(rows))
	for _, r := range rows {
		c, err := mfcontent.Restore(mfcontent.Syntax(r.Syntax), r.Text, r.Model)
		if err != nil {
			return nil, fmt.Errorf("localization: stored revision %d of %s: %w", r.Revision, t, err)
		}
		var findings []mf.Finding
		if err := json.Unmarshal(r.Findings, &findings); err != nil {
			return nil, fmt.Errorf("localization: stored findings of revision %d of %s: %w", r.Revision, t, err)
		}
		out = append(out, domain.Revision{
			TranslationID: t, Number: int(r.Revision), Kind: domain.RevisionKind(r.Kind), Content: c,
			State: domain.ReviewState(r.State), SourceRevision: int(r.SourceRevision), Findings: findings, SelfReview: r.SelfReview,
			Provenance: domain.Provenance{Origin: domain.Origin(r.Origin), Detail: r.OriginDetail, By: r.Author},
			CreatedAt:  r.CreatedAt.UTC(),
		})
	}
	return out, nil
}

func (s *store) NewlyOutdated(ctx context.Context, message uuid.UUID, old, new int) ([]domain.Translation, error) {
	rows, err := s.q.NewlyOutdatedTranslations(ctx, localizationsql.NewlyOutdatedTranslationsParams{
		MessageID: message, OldRevision: int32Of(old), NewRevision: int32Of(new),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Translation, 0, len(rows))
	for _, r := range rows {
		t, err := translation(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *store) SnapshotTranslations(ctx context.Context, project uuid.UUID, states []domain.ReviewState, includeObsolete bool) ([]app.TranslationRow, error) {
	names := make([]string, len(states))
	for i, st := range states {
		names[i] = string(st)
	}
	rows, err := s.q.SnapshotTranslations(ctx, localizationsql.SnapshotTranslationsParams{
		ProjectID: project, States: names, IncludeObsolete: includeObsolete,
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.TranslationRow, 0, len(rows))
	for _, r := range rows {
		tr, err := row(localizationsql.LocalizationTranslation{
			ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
			Syntax: r.Syntax, Text: r.Text, Model: r.Model, State: r.State, Origin: r.Origin, Author: r.Author,
			SourceRevision: r.SourceRevision, Warnings: r.Warnings, Revision: r.Revision,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}, r.CurrentSourceRevision)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, nil
}

func (s *store) ProjectTranslations(ctx context.Context, project uuid.UUID, q app.ProjectTranslationQuery) ([]app.ProjectTranslationRow, error) {
	locales := make([]string, len(q.Locales))
	for i, l := range q.Locales {
		locales[i] = l.String()
	}
	var states []string // nil: any state
	for _, st := range q.States {
		states = append(states, string(st))
	}
	var origins []string // nil: any provenance
	for _, o := range q.Origins {
		origins = append(origins, string(o))
	}
	outdated := pgtype.Bool{}
	if q.Outdated != nil {
		outdated = pgtype.Bool{Bool: *q.Outdated, Valid: true}
	}
	rows, err := s.q.PageProjectTranslations(ctx, localizationsql.PageProjectTranslationsParams{
		ProjectID: project, Locales: locales,
		AfterKey: q.After.Key, AfterMessage: q.After.Message, AfterLocale: q.After.Locale,
		States: states, Origins: origins, Outdated: outdated,
		Namespace: optText(q.Namespace), MessageState: optText(q.MessageState),
		KeyLike: likePattern(q.KeyPrefix), Keys: q.Keys, Units: units(q.Units), MaxRows: int32Of(q.Limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.ProjectTranslationRow, 0, len(rows))
	for _, r := range rows {
		tr, err := row(localizationsql.LocalizationTranslation{
			ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, MessageID: r.MessageID, Locale: r.Locale,
			Syntax: r.Syntax, Text: r.Text, Model: r.Model, State: r.State, Origin: r.Origin, Author: r.Author,
			SourceRevision: r.SourceRevision, Warnings: r.Warnings, Revision: r.Revision,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}, r.CurrentSourceRevision)
		if err != nil {
			return nil, err
		}
		out = append(out, app.ProjectTranslationRow{TranslationRow: tr, Key: r.Key, Namespace: r.Namespace, MessageState: r.MessageState})
	}
	return out, nil
}

// units spells a unit filter as PageProjectTranslations matches it,
// "<message id> <locale>"; nil is no filter, empty is nothing.
func units(us []authz.Unit) []string {
	if us == nil {
		return nil
	}
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Message.String() + " " + u.Locale
	}
	return out
}

func (s *store) TranslationStats(ctx context.Context, project uuid.UUID) (app.StoredStats, error) {
	rows, err := s.q.TranslationStats(ctx, project)
	if err != nil {
		return app.StoredStats{}, storeError(err)
	}
	var out app.StoredStats
	for _, r := range rows {
		out.Messages = int(r.Messages)
		if !r.Code.Valid { // the project has no locale yet
			continue
		}
		code, err := bcp47.Parse(r.Code.String)
		if err != nil {
			return app.StoredStats{}, fmt.Errorf("localization: stored locale %q: %w", r.Code.String, err)
		}
		out.Locales = append(out.Locales, app.LocaleCounts{
			Code: code, IsSource: r.IsSource.Bool, Outdated: int(r.Outdated),
			States: domain.StateCounts{
				Draft: int(r.Draft), NeedsReview: int(r.NeedsReview), Approved: int(r.Approved), Rejected: int(r.Rejected),
			},
		})
	}
	return out, nil
}

func (s *store) LeadTimeSamples(
	ctx context.Context, project uuid.UUID, q app.LeadTimeQuery,
) ([]app.LeadTimeSample, error) {
	rows, err := s.q.LeadTimeSamples(ctx, localizationsql.LeadTimeSamplesParams{
		ProjectID: project, States: q.States, Since: q.Since.UTC(),
		Locales: append([]string{}, q.Locales...), MaxRows: int32Of(q.Limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.LeadTimeSample, 0, len(rows))
	for _, r := range rows {
		out = append(out, app.LeadTimeSample{
			Locale: r.Locale, SourceChangedAt: r.SourceChangedAt.UTC(), TranslatedAt: r.TranslatedAt.UTC(),
		})
	}
	return out, nil
}

func (s *store) DeleteProjectData(ctx context.Context, project uuid.UUID) error {
	for _, del := range []func(context.Context, uuid.UUID) error{
		s.q.DeleteProjectTranslations, s.q.DeleteProjectMessages, s.q.DeleteProjectFallbackGraph, s.q.DeleteProjectLocales,
	} {
		if err := del(ctx, project); err != nil {
			return storeError(err)
		}
	}
	return nil
}

func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}
