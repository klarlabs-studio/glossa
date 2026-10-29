package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// The event contracts Intelligence reads (an anti-corruption layer: the
// published JSON, not the other contexts' Go types).
const (
	catalogMessageCreated          = "catalog.message.created"
	catalogProjectDeleted          = "catalog.project.deleted"
	localizationTranslationOutdate = "localization.translation.outdated"
	localizationLocaleAdded        = "localization.locale.added"
)

// Subscriber and background principal names are stored with events and
// actors; never rename them.
const (
	subscriberAutoTranslate = "intelligence.auto_translate"
	subscriberDropProject   = "intelligence.drop_project"
	principalWorker         = "intelligence.worker"
)

type messageCreated struct {
	Message struct {
		MessageID      string `json:"message_id"`
		ProjectID      string `json:"project_id"`
		State          string `json:"state"`
		SourceRevision int    `json:"source_revision"`
	} `json:"message"`
}

type translationOutdated struct {
	ProjectID             string `json:"project_id"`
	MessageID             string `json:"message_id"`
	Locale                string `json:"locale"`
	CurrentSourceRevision int    `json:"current_source_revision"`
}

type localeAdded struct {
	ProjectID string `json:"project_id"`
	Locale    string `json:"locale"`
}

type projectDeleted struct {
	ProjectID string `json:"project_id"`
}

// Subscribe registers the triggers (RFC 0003 §3.4): new and outdated
// messages are translated into the locales with auto-translate on, and
// a locale added with auto-translate on is filled in a batch. All are
// idempotent — jobs are unique per message, locale, source revision and
// knowledge fingerprint, a locale's fill per event — and act as the
// background principal intelligence.auto_translate.
//
// The triggers are one batch subscriber (#89): see autoTranslateBatch.
func (s *Service) Subscribe(r *outbox.Registry) error {
	b := autoTranslateBatch{byType: map[string]outbox.HandlerFunc{
		catalogMessageCreated:          s.handleMessageCreated,
		localizationTranslationOutdate: s.handleTranslationOutdated,
		localizationLocaleAdded:        s.handleLocaleAdded,
	}}
	if err := r.SubscribeBatch(subscriberAutoTranslate, b, slices.Sorted(maps.Keys(b.byType))...); err != nil {
		return err
	}
	return r.Subscribe(catalogProjectDeleted, subscriberDropProject, outbox.HandlerFunc(s.handleProjectDeleted))
}

// background acts as the auto-translate principal: it reads the
// catalog, translations and knowledge, nothing more.
func background(ctx context.Context) (context.Context, error) {
	bg, err := authz.Background(ctx, subscriberAutoTranslate, authz.CatalogRead, authz.TranslationsRead, authz.KnowledgeRead)
	if err != nil {
		return nil, outbox.Permanent(err)
	}
	return bg, nil
}

func (s *Service) handleMessageCreated(ctx context.Context, d outbox.Delivery) error {
	var e messageCreated
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err1 := uuid.Parse(e.Message.ProjectID)
	message, err2 := uuid.Parse(e.Message.MessageID)
	if err := errors.Join(err1, err2); err != nil {
		return outbox.Permanent(fmt.Errorf("intelligence: %s event %s: %w", d.Type, d.EventID, err))
	}
	return s.autoTranslate(ctx, project, message, nil, domain.TriggerMessageCreated)
}

func (s *Service) handleTranslationOutdated(ctx context.Context, d outbox.Delivery) error {
	var e translationOutdated
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err1 := uuid.Parse(e.ProjectID)
	message, err2 := uuid.Parse(e.MessageID)
	if err := errors.Join(err1, err2); err != nil || e.Locale == "" {
		return outbox.Permanent(fmt.Errorf("intelligence: %s event %s: %w", d.Type, d.EventID, err))
	}
	return s.autoTranslate(ctx, project, message, []string{e.Locale}, domain.TriggerTranslationOutdated)
}

// autoTranslate queues jobs for a message in the locales (nil: every
// project locale) that have auto-translate on. A message in a
// sensitive namespace is never queued.
func (s *Service) autoTranslate(ctx context.Context, project, message uuid.UUID, only []string, trigger domain.Trigger) error {
	ps, err := s.triggerSettings(ctx, project)
	if err != nil || len(ps.AutoTranslateLocales) == 0 {
		return err
	}
	bg, err := background(ctx)
	if err != nil {
		return err
	}
	have, err := s.Localization.Locales(bg, project)
	if errors.Is(err, ErrProjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var locales []string
	for _, l := range have {
		if ps.AutoTranslates(l) && (only == nil || slices.Contains(only, l)) {
			locales = append(locales, l)
		}
	}
	if len(locales) == 0 {
		return nil
	}
	m, err := s.Catalog.MessageByID(bg, project, message)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrProjectNotFound) {
		return nil // deleted since; nothing to translate
	}
	if err != nil {
		return err
	}
	if !m.Active || slices.Contains(ps.NamespaceTags.Of(m.Namespace), domain.TagSensitive) {
		return nil
	}
	q := newQueuer(s, project)
	var jobs []domain.Job
	for _, l := range locales {
		tr, err := s.Localization.Translation(bg, project, m.Key, l)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if tr.upToDate(m.Revision) {
			continue
		}
		j, err := q.job(bg, m, l, trigger, nil, false, "system:"+subscriberAutoTranslate)
		if err != nil {
			return err
		}
		jobs = append(jobs, j)
	}
	_, _, _, err = s.enqueue(bg, jobs, false)
	return err
}

// handleLocaleAdded fills a new locale that has auto-translate on. The
// fill's ID is the event's, so a redelivery finds it.
func (s *Service) handleLocaleAdded(ctx context.Context, d outbox.Delivery) error {
	var e localeAdded
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("intelligence: %s event %s: %w", d.Type, d.EventID, err))
	}
	ps, err := s.projectSettings(ctx, project)
	if err != nil || !ps.AutoTranslates(e.Locale) {
		return err
	}
	bg, err := background(ctx)
	if err != nil {
		return err
	}
	if _, found, err := s.existingFill(bg, d.EventID); err != nil || found {
		return err
	}
	f := Fill{
		ID: d.EventID, ProjectID: project, Trigger: domain.TriggerLocaleAdded, Locales: []string{e.Locale},
		RequestedBy: "system:" + subscriberAutoTranslate, CreatedAt: s.Now(),
	}
	err = s.fill(bg, &f, false)
	if errors.Is(err, ErrProjectNotFound) || errors.Is(err, ErrLocaleNotFound) {
		return nil
	}
	return err
}

// handleProjectDeleted erases a deleted project's jobs, suggestions,
// disclosures and settings. Spend stays: it is the tenant's money.
func (s *Service) handleProjectDeleted(ctx context.Context, d outbox.Delivery) error {
	var e projectDeleted
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("intelligence: %s event %s: %w", d.Type, d.EventID, err))
	}
	return s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.DeleteProjectData(ctx, project) })
}
