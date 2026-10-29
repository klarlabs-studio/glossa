package postgres

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres/localizationsql"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// The bulk forms of the projection and translation writes (#77): one
// statement per call, each row an element of parallel arrays.

// LockMessageStates implements app.Store.
func (s *store) LockMessageStates(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]app.MessageState, error) {
	out := make(map[uuid.UUID]app.MessageState, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.LockMessageStates(ctx, ids)
	if err != nil {
		return nil, storeError(err)
	}
	for _, r := range rows {
		out[r.MessageID] = app.MessageState{
			MessageID: r.MessageID, ProjectID: r.ProjectID, Key: r.Key, Namespace: r.Namespace,
			State: r.State, SourceRevision: int(r.SourceRevision), Version: int(r.Version), UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

// SaveMessageStates implements app.Store.
func (s *store) SaveMessageStates(ctx context.Context, ms []app.MessageState) error {
	if len(ms) == 0 {
		return nil
	}
	var p localizationsql.UpsertMessageStatesParams
	for _, m := range ms {
		p.MessageIds = append(p.MessageIds, m.MessageID)
		p.ProjectIds = append(p.ProjectIds, m.ProjectID)
		p.Keys = append(p.Keys, m.Key)
		p.Namespaces = append(p.Namespaces, m.Namespace)
		p.States = append(p.States, m.State)
		p.SourceRevisions = append(p.SourceRevisions, int32Of(m.SourceRevision))
		p.Versions = append(p.Versions, int32Of(m.Version))
		p.UpdatedAts = append(p.UpdatedAts, m.UpdatedAt)
	}
	_, err := s.q.UpsertMessageStates(ctx, p)
	return storeError(err)
}

// NewlyOutdatedFor implements app.Store.
func (s *store) NewlyOutdatedFor(ctx context.Context, rs []app.RevisionRange) ([]domain.Translation, error) {
	if len(rs) == 0 {
		return nil, nil
	}
	var p localizationsql.NewlyOutdatedTranslationsForParams
	for _, r := range rs {
		p.MessageIds = append(p.MessageIds, r.MessageID)
		p.OldRevisions = append(p.OldRevisions, int32Of(r.Old))
		p.NewRevisions = append(p.NewRevisions, int32Of(r.New))
	}
	rows, err := s.q.NewlyOutdatedTranslationsFor(ctx, p)
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

// LockTranslations implements app.Store.
func (s *store) LockTranslations(ctx context.Context, slots []app.TranslationSlot) (map[app.TranslationSlot]domain.Translation, error) {
	out := make(map[app.TranslationSlot]domain.Translation, len(slots))
	if len(slots) == 0 {
		return out, nil
	}
	var p localizationsql.LockTranslationsForParams
	for _, sl := range slots {
		p.MessageIds = append(p.MessageIds, sl.MessageID)
		p.Locales = append(p.Locales, sl.Locale.String())
	}
	rows, err := s.q.LockTranslationsFor(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	for _, r := range rows {
		t, err := translation(r)
		if err != nil {
			return nil, err
		}
		out[app.TranslationSlot{MessageID: t.MessageID, Locale: t.Locale}] = t
	}
	return out, nil
}

// InsertTranslations implements app.Store.
func (s *store) InsertTranslations(ctx context.Context, ts []domain.Translation) error {
	if len(ts) == 0 {
		return nil
	}
	var p localizationsql.InsertTranslationsParams
	for _, t := range ts {
		p.Ids = append(p.Ids, t.ID.UUID())
		p.ProjectIds = append(p.ProjectIds, t.ProjectID)
		p.MessageIds = append(p.MessageIds, t.MessageID)
		p.Locales = append(p.Locales, t.Locale.String())
		p.Syntaxes = append(p.Syntaxes, string(t.Content.Syntax))
		p.Texts = append(p.Texts, t.Content.Text)
		p.Models = append(p.Models, t.Content.ModelJSON())
		p.States = append(p.States, string(t.State))
		p.Origins = append(p.Origins, string(t.Origin))
		p.Authors = append(p.Authors, t.By)
		p.SourceRevisions = append(p.SourceRevisions, int32Of(t.SourceRevision))
		p.Warnings = append(p.Warnings, findingsJSON(t.Warnings))
		p.Revisions = append(p.Revisions, int32Of(t.Revision))
		p.CreatedAts = append(p.CreatedAts, t.CreatedAt)
		p.UpdatedAts = append(p.UpdatedAts, t.UpdatedAt)
	}
	return storeError(s.q.InsertTranslations(ctx, p))
}

// UpdateTranslations implements app.Store.
func (s *store) UpdateTranslations(ctx context.Context, us []app.TranslationUpdate) error {
	if len(us) == 0 {
		return nil
	}
	var p localizationsql.UpdateTranslationsParams
	for _, u := range us {
		t := u.Translation
		p.Ids = append(p.Ids, t.ID.UUID())
		p.Syntaxes = append(p.Syntaxes, string(t.Content.Syntax))
		p.Texts = append(p.Texts, t.Content.Text)
		p.Models = append(p.Models, t.Content.ModelJSON())
		p.States = append(p.States, string(t.State))
		p.Origins = append(p.Origins, string(t.Origin))
		p.Authors = append(p.Authors, t.By)
		p.SourceRevisions = append(p.SourceRevisions, int32Of(t.SourceRevision))
		p.Warnings = append(p.Warnings, findingsJSON(t.Warnings))
		p.Revisions = append(p.Revisions, int32Of(t.Revision))
		p.UpdatedAts = append(p.UpdatedAts, t.UpdatedAt)
		p.ExpectedRevisions = append(p.ExpectedRevisions, int32Of(u.Expected))
	}
	n, err := s.q.UpdateTranslations(ctx, p)
	if err != nil {
		return storeError(err)
	}
	if int(n) != len(us) {
		return app.ErrStaleVersion
	}
	return nil
}

// AppendRevisions implements app.Store.
func (s *store) AppendRevisions(ctx context.Context, rs []domain.Revision) error {
	if len(rs) == 0 {
		return nil
	}
	var p localizationsql.InsertTranslationRevisionsParams
	for _, r := range rs {
		p.TranslationIds = append(p.TranslationIds, r.TranslationID.UUID())
		p.Revisions = append(p.Revisions, int32Of(r.Number))
		p.Kinds = append(p.Kinds, string(r.Kind))
		p.Syntaxes = append(p.Syntaxes, string(r.Content.Syntax))
		p.Texts = append(p.Texts, r.Content.Text)
		p.Models = append(p.Models, r.Content.ModelJSON())
		p.States = append(p.States, string(r.State))
		p.Origins = append(p.Origins, string(r.Provenance.Origin))
		p.OriginDetails = append(p.OriginDetails, r.Provenance.Detail)
		p.Authors = append(p.Authors, r.Provenance.By)
		p.SourceRevisions = append(p.SourceRevisions, int32Of(r.SourceRevision))
		p.Findings = append(p.Findings, findingsJSON(r.Findings))
		p.SelfReviews = append(p.SelfReviews, r.SelfReview)
		p.CreatedAts = append(p.CreatedAts, r.CreatedAt)
	}
	return storeError(s.q.InsertTranslationRevisions(ctx, p))
}

// PublishAll implements app.Store.
func (s *store) PublishAll(ctx context.Context, es []outbox.Event) error {
	_, err := outbox.PublishAll(ctx, s.tx, es)
	return err
}
