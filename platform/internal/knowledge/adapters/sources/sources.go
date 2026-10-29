// Package sources adapts Catalog's and Localization's application
// services to Knowledge's ports: the only way Knowledge reads projects
// and translations (RFC 0002 §4, contexts reach each other through
// application ports).
package sources

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// Projects implements app.Projects on Catalog's service.
type Projects struct{ svc *catalogapp.Service }

// NewProjects returns the port.
func NewProjects(svc *catalogapp.Service) *Projects { return &Projects{svc: svc} }

var _ app.Projects = (*Projects)(nil)

// Project implements app.Projects.
func (p *Projects) Project(ctx context.Context, id uuid.UUID) (app.ProjectInfo, error) {
	pr, err := p.svc.GetProject(ctx, catalogdomain.ProjectID(id))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.ProjectInfo{}, app.ErrProjectNotFound
	}
	if err != nil {
		return app.ProjectInfo{}, err
	}
	return app.ProjectInfo{ID: id, SourceLocale: pr.SourceLocale}, nil
}

// Translations implements app.Translations on Localization's service
// (and Catalog's, for the source of a page of translations).
type Translations struct {
	svc     *localizationapp.Service
	catalog *catalogapp.Service
}

// NewTranslations returns the port.
func NewTranslations(svc *localizationapp.Service, catalog *catalogapp.Service) *Translations {
	return &Translations{svc: svc, catalog: catalog}
}

var _ app.Translations = (*Translations)(nil)

// Current implements app.Translations.
func (t *Translations) Current(ctx context.Context, project, translation uuid.UUID) (app.CurrentTranslation, error) {
	tr, err := t.svc.TranslationWithSource(ctx, project, localizationdomain.TranslationID(translation))
	if errors.Is(err, localizationapp.ErrNotFound) {
		return app.CurrentTranslation{}, app.ErrNotFound
	}
	if err != nil {
		return app.CurrentTranslation{}, err
	}
	return current(tr), nil
}

func current(tr localizationapp.TranslationWithSource) app.CurrentTranslation {
	return app.CurrentTranslation{
		ApprovedText: domain.ApprovedText{
			TranslationID: tr.ID.UUID(), ProjectID: tr.ProjectID, MessageID: tr.MessageID, Revision: tr.Revision,
			MessageKey: tr.Key, Namespace: tr.Namespace, SourceLocale: tr.SourceLocale, TargetLocale: tr.Locale,
			Source: tr.Source.Model, Target: tr.Content.Model, By: tr.By,
		},
		Approved: tr.State == localizationdomain.StateApproved,
	}
}

// Currents implements app.Translations: Localization's bulk read, which
// takes the translations' sources from Catalog in one read too.
func (t *Translations) Currents(ctx context.Context, project uuid.UUID, translations []uuid.UUID) (map[uuid.UUID]app.CurrentTranslation, error) {
	ids := make([]localizationdomain.TranslationID, len(translations))
	for i, id := range translations {
		ids[i] = localizationdomain.TranslationID(id)
	}
	found, err := t.svc.TranslationsWithSource(ctx, project, ids)
	if errors.Is(err, localizationapp.ErrNotFound) {
		return nil, nil // the project is gone, and its translations with it
	}
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]app.CurrentTranslation, len(found))
	for id, tr := range found {
		out[id.UUID()] = current(tr)
	}
	return out, nil
}

// ProjectTranslations implements app.Translations: Localization's bulk
// listing (one query) and the page's sources from Catalog (one query).
func (t *Translations) ProjectTranslations(ctx context.Context, project uuid.UUID, q app.TranslationPageQuery) ([]app.ProjectTranslation, *string, error) {
	active := string(catalogdomain.MessageActive)
	f := localizationapp.TranslationFilter{
		States: q.States, Namespace: q.Namespace, KeyPrefix: q.KeyPrefix, Keys: q.Keys, MessageState: &active,
	}
	for _, l := range q.Locales {
		f.Locales = append(f.Locales, l.String())
	}
	rows, next, err := t.svc.ListProjectTranslations(ctx, project, f, q.Page)
	if errors.Is(err, localizationapp.ErrNotFound) {
		return nil, nil, app.ErrProjectNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var ids []catalogdomain.MessageID
	for _, r := range rows {
		if id := catalogdomain.MessageID(r.MessageID); !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sources := map[catalogdomain.MessageID]catalogdomain.Message{}
	if len(ids) > 0 {
		if sources, err = t.catalog.MessagesByIDs(ctx, catalogdomain.ProjectID(project), ids); err != nil {
			return nil, nil, err
		}
	}
	out := make([]app.ProjectTranslation, len(rows))
	for i, r := range rows {
		m, ok := sources[catalogdomain.MessageID(r.MessageID)]
		out[i] = app.ProjectTranslation{
			MessageID: r.MessageID, Key: r.Key, Namespace: r.Namespace, Locale: r.Locale, State: string(r.State),
			Source: m.Source.Model, HasSource: ok, SourceRevision: m.Revision, Target: r.Content.Model,
		}
	}
	return out, next, nil
}

// Messages implements app.Messages on Catalog's service, which already
// resolves a key only to the members who may see its message.
type Messages struct{ svc *catalogapp.Service }

// NewMessages returns the port.
func NewMessages(svc *catalogapp.Service) *Messages { return &Messages{svc: svc} }

var _ app.Messages = (*Messages)(nil)

// Message implements app.Messages.
func (m *Messages) Message(ctx context.Context, project uuid.UUID, key string) (app.SourceMessage, error) {
	msg, err := m.svc.GetMessage(ctx, catalogdomain.ProjectID(project), key)
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.SourceMessage{}, app.ErrNotFound
	}
	if err != nil {
		return app.SourceMessage{}, err
	}
	pr, err := m.svc.GetProject(ctx, catalogdomain.ProjectID(project))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.SourceMessage{}, app.ErrNotFound
	}
	if err != nil {
		return app.SourceMessage{}, err
	}
	return app.SourceMessage{
		ID: msg.ID.UUID(), Key: string(msg.Key), Namespace: string(msg.Namespace), SourceLocale: pr.SourceLocale,
		Source: msg.Source.Model, SourceSyntax: msg.Source.Syntax,
	}, nil
}
