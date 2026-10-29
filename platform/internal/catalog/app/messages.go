package app

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// NewMessage is the input of CreateMessage.
type NewMessage struct {
	Key         string
	Namespace   string
	Description string
	MaxLength   *int
	// Text is the source in Syntax ("" means the project's default).
	Text   string
	Syntax string
}

// parseSource parses text as source content of project.
func parseSource(p domain.Project, syntax, text string) (mfcontent.Content, error) {
	s, err := mfcontent.ParseSyntax(syntax, p.Settings.DefaultSyntax)
	if err != nil {
		return mfcontent.Content{}, err
	}
	return mfcontent.Parse(s, text, p.SourceLocale)
}

// CreateMessage adds a message with its first source revision.
func (s *Service) CreateMessage(ctx context.Context, project domain.ProjectID, in NewMessage, idemKey string) (m domain.Message, replayed bool, err error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Message{}, false, err
	}
	key, err := domain.ParseMessageKey(in.Key)
	if err != nil {
		return domain.Message{}, false, err
	}
	ns, err := domain.ParseNamespace(in.Namespace)
	if err != nil {
		return domain.Message{}, false, err
	}
	id, err := idempotentID("message.create", project.String(), by, idemKey)
	if err != nil {
		return domain.Message{}, false, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		p, err := st.Project(ctx, project)
		if err != nil {
			return err
		}
		src, err := parseSource(p, in.Syntax, in.Text)
		if err != nil {
			return err
		}
		details := domain.Details{Namespace: ns, Description: in.Description, MaxLength: in.MaxLength}
		var first domain.SourceRevision
		if m, first, err = domain.NewMessage(project, key, details, src, by, s.now()); err != nil {
			return err
		}
		if id != uuid.Nil {
			m.ID, first.MessageID = domain.MessageID(id), domain.MessageID(id)
		}
		inserted, err := st.InsertMessage(ctx, m, first, by)
		if err != nil {
			return err
		}
		if !inserted {
			earlier, err := st.MessageByID(ctx, project, m.ID)
			if err != nil {
				return err
			}
			if earlier.Key != m.Key || !earlier.Source.SameModel(m.Source) {
				return ErrIdempotencyReuse
			}
			m, replayed = earlier, true
			return nil
		}
		return st.Publish(ctx, messageEvent(domain.EventMessageCreated, m, by))
	})
	return m, replayed, err
}

func messageEvent(typ string, m domain.Message, by domain.Author) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateMessage, AggregateID: m.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.MessageEvent{Message: domain.SnapshotOf(m), By: string(by)},
	}
}

func sourceRevisedEvent(m domain.Message, old int, by domain.Author) outbox.Event {
	return outbox.Event{
		Type: domain.EventMessageSourceRevised, AggregateType: domain.AggregateMessage, AggregateID: m.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.SourceRevised{Message: domain.SnapshotOf(m), OldRevision: old, NewRevision: m.Revision, By: string(by)},
	}
}

// GetMessage returns a message by key. For an assigned member a message
// none of their units is in is not found (RFC 0006 §3.3).
func (s *Service) GetMessage(ctx context.Context, project domain.ProjectID, key string) (domain.Message, error) {
	view, err := authz.Visible(ctx, authz.CatalogRead, project.UUID())
	if err != nil {
		return domain.Message{}, err
	}
	k, err := parseKeyOrNotFound(key)
	if err != nil {
		return domain.Message{}, err
	}
	var m domain.Message
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		m, err = st.MessageByKey(ctx, project, k)
		return err
	})
	if err == nil && !view.Message(m.ID.UUID()) {
		return domain.Message{}, ErrNotFound
	}
	return m, err
}

// visibleIDs is the message filter of an authz view: nil for the whole
// project, the covered messages for an assigned member.
func visibleIDs(view authz.View) []domain.MessageID {
	if view.All() {
		return nil
	}
	ids := view.Messages()
	out := make([]domain.MessageID, len(ids))
	for i, id := range ids {
		out[i] = domain.MessageID(id)
	}
	return out
}

// parseKeyOrNotFound treats a malformed key in a path like an unknown one.
func parseKeyOrNotFound(key string) (domain.MessageKey, error) {
	k, err := domain.ParseMessageKey(key)
	if err != nil {
		return "", ErrNotFound
	}
	return k, nil
}

// MessageQuery filters ListMessages.
type MessageQuery struct {
	Namespace string
	State     string
	KeyPrefix string
	// MissingIn and OutdatedIn (at most one) select by translation
	// coverage in a locale.
	MissingIn  string
	OutdatedIn string
}

func (q MessageQuery) filter() (MessageFilter, error) {
	var f MessageFilter
	if q.Namespace != "" {
		ns, err := domain.ParseNamespace(q.Namespace)
		if err != nil {
			return f, err
		}
		f.Namespace = &ns
	}
	if q.State != "" {
		st, err := domain.ParseMessageState(q.State)
		if err != nil {
			return f, err
		}
		f.State = &st
	}
	f.KeyPrefix = q.KeyPrefix
	return f, nil
}

// ListMessages lists a project's messages in key order; an assigned
// member's list holds only the messages their units are in, filtered
// in the query (RFC 0006 §3.3). The coverage filters (missing_in,
// outdated_in) read translations across the project and are refused
// them.
func (s *Service) ListMessages(ctx context.Context, project domain.ProjectID, q MessageQuery, page pagination.Page) ([]domain.Message, *string, error) {
	view, err := authz.Visible(ctx, authz.CatalogRead, project.UUID())
	if err != nil {
		return nil, nil, err
	}
	f, err := q.filter()
	if err != nil {
		return nil, nil, err
	}
	f.IDs = visibleIDs(view)
	var rows []domain.Message
	if q.MissingIn != "" || q.OutdatedIn != "" {
		rows, err = s.messagesByCoverage(ctx, project, q, f, page)
	} else {
		err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			if _, err := st.Project(ctx, project); err != nil {
				return err
			}
			rows, err = st.Messages(ctx, project, f, domain.MessageKey(page.After), page.Limit())
			return err
		})
	}
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(m domain.Message) string { return string(m.Key) })
	return items, next, nil
}

// ListNamespaces lists the namespaces of a project's messages by name,
// with how many messages each holds by state: one grouped read of the
// project's messages per page. Needs catalog.read.
func (s *Service) ListNamespaces(ctx context.Context, project domain.ProjectID, page pagination.Page) ([]NamespaceSummary, *string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, nil, err
	}
	var rows []NamespaceSummary
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		var err error
		rows, err = st.Namespaces(ctx, project, domain.Namespace(page.After), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(n NamespaceSummary) string { return string(n.Name) })
	return items, next, nil
}

// messagesByCoverage asks Localization which messages match, then loads
// them. Localization learns about messages from events, so a message
// created a moment ago may not be listed as missing yet.
func (s *Service) messagesByCoverage(ctx context.Context, project domain.ProjectID, q MessageQuery, f MessageFilter, page pagination.Page) ([]domain.Message, error) {
	if q.MissingIn != "" && q.OutdatedIn != "" {
		return nil, ErrCoverageFilter
	}
	// Coverage reads translations across the project, which an assigned
	// member may not (RFC 0006 §3.3): RequireIn refuses them.
	if err := authz.RequireIn(ctx, authz.TranslationsRead, project.UUID()); err != nil {
		return nil, err
	}
	if s.coverage == nil {
		return nil, ErrNoCoverage
	}
	cq := CoverageQuery{Project: project, Locale: q.MissingIn, Status: CoverageMissing, Filter: f, AfterKey: page.After, Limit: page.Limit()}
	if q.OutdatedIn != "" {
		cq.Locale, cq.Status = q.OutdatedIn, CoverageOutdated
	}
	locale, err := bcp47.Parse(cq.Locale)
	if err != nil {
		return nil, err
	}
	cq.Locale = locale.String()
	ids, err := s.coverage.MessagesWithCoverage(ctx, cq)
	if err != nil {
		return nil, err
	}
	var rows []domain.Message
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		byID, err := st.MessagesByIDs(ctx, project, ids)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if m, ok := byID[id]; ok {
				rows = append(rows, m)
			}
		}
		return nil
	})
	return rows, err
}

// ReviseSource makes text the message's source if the message is still
// at version ifMatch. Text that parses to the current model is no
// revision; the message comes back unchanged.
func (s *Service) ReviseSource(ctx context.Context, project domain.ProjectID, key string, ifMatch int, text, syntax string) (domain.Message, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Message{}, err
	}
	k, err := parseKeyOrNotFound(key)
	if err != nil {
		return domain.Message{}, err
	}
	var m domain.Message
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		p, err := st.Project(ctx, project)
		if err != nil {
			return err
		}
		if m, err = st.LockMessageByKey(ctx, project, k); err != nil {
			return err
		}
		if m.Version != ifMatch {
			return ErrPreconditionFailed
		}
		src, err := parseSource(p, syntax, text)
		if err != nil {
			return err
		}
		return s.revise(ctx, st, &m, src, by)
	})
	return m, err
}

// revise applies a source revision to a locked message and records it.
func (s *Service) revise(ctx context.Context, st Store, m *domain.Message, src mfcontent.Content, by domain.Author) error {
	expected, old := m.Version, m.Revision
	rev, changed, err := m.ReviseSource(src, by, s.now())
	if err != nil || !changed {
		return err
	}
	if err := st.UpdateMessage(ctx, *m, expected); err != nil {
		return err
	}
	if err := st.AppendSourceRevision(ctx, rev); err != nil {
		return err
	}
	return st.Publish(ctx, sourceRevisedEvent(*m, old, by))
}

// MessageChange is a partial update of a message's details; nil members
// keep their value. A MaxLength of 0 removes the limit.
type MessageChange struct {
	Namespace   *string
	Description *string
	MaxLength   *int
}

// UpdateMessage changes a message's details at version ifMatch.
func (s *Service) UpdateMessage(ctx context.Context, project domain.ProjectID, key string, ifMatch int, c MessageChange) (domain.Message, error) {
	return s.mutate(ctx, project, key, &ifMatch, func(m *domain.Message, by domain.Author) (outbox.Event, bool, error) {
		d := m.Details
		if c.Namespace != nil {
			ns, err := domain.ParseNamespace(*c.Namespace)
			if err != nil {
				return outbox.Event{}, false, err
			}
			d.Namespace = ns
		}
		if c.Description != nil {
			d.Description = *c.Description
		}
		if c.MaxLength != nil {
			d.MaxLength = c.MaxLength
			if *c.MaxLength == 0 {
				d.MaxLength = nil
			}
		}
		changed, err := m.ChangeDetails(d, s.now())
		return messageEvent(domain.EventMessageUpdated, *m, by), changed, err
	})
}

// ObsoleteMessage retires a message: it keeps its history and
// translations but is no longer released. Pushing it again reactivates it.
func (s *Service) ObsoleteMessage(ctx context.Context, project domain.ProjectID, key string, ifMatch *int) (domain.Message, error) {
	return s.mutate(ctx, project, key, ifMatch, func(m *domain.Message, by domain.Author) (outbox.Event, bool, error) {
		changed := m.Obsolete(s.now())
		return messageEvent(domain.EventMessageObsoleted, *m, by), changed, nil
	})
}

// RenameMessage gives a message a new key, keeping its ID and history.
func (s *Service) RenameMessage(ctx context.Context, project domain.ProjectID, key, newKey string, ifMatch *int) (domain.Message, error) {
	to, err := domain.ParseMessageKey(newKey)
	if err != nil {
		return domain.Message{}, err
	}
	return s.mutate(ctx, project, key, ifMatch, func(m *domain.Message, by domain.Author) (outbox.Event, bool, error) {
		old := m.Key
		changed := m.Rename(to, s.now())
		return outbox.Event{
			Type: domain.EventMessageRenamed, AggregateType: domain.AggregateMessage, AggregateID: m.ID.String(), Actor: outbox.Actor(by),
			Payload: domain.MessageRenamed{Message: domain.SnapshotOf(*m), OldKey: string(old), NewKey: string(to), By: string(by)},
		}, changed, nil
	})
}

// mutate runs a non-source change on a locked message and publishes its
// event if it changed anything.
func (s *Service) mutate(ctx context.Context, project domain.ProjectID, key string, ifMatch *int,
	change func(*domain.Message, domain.Author) (outbox.Event, bool, error),
) (domain.Message, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return domain.Message{}, err
	}
	k, err := parseKeyOrNotFound(key)
	if err != nil {
		return domain.Message{}, err
	}
	var m domain.Message
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if m, err = st.LockMessageByKey(ctx, project, k); err != nil {
			return err
		}
		if ifMatch != nil && m.Version != *ifMatch {
			return ErrPreconditionFailed
		}
		expected := m.Version
		e, changed, err := change(&m, by)
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateMessage(ctx, m, expected); err != nil {
			return err
		}
		return st.Publish(ctx, e)
	})
	return m, err
}

// SourceRevisions lists a message's source log, newest first.
func (s *Service) SourceRevisions(ctx context.Context, project domain.ProjectID, key string, page pagination.Page) ([]domain.SourceRevision, *string, error) {
	view, err := authz.Visible(ctx, authz.CatalogRead, project.UUID())
	if err != nil {
		return nil, nil, err
	}
	k, err := parseKeyOrNotFound(key)
	if err != nil {
		return nil, nil, err
	}
	before, err := beforeRevision(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.SourceRevision
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		m, err := st.MessageByKey(ctx, project, k)
		if err != nil {
			return err
		}
		if !view.Message(m.ID.UUID()) {
			return ErrNotFound
		}
		rows, err = st.SourceRevisions(ctx, m.ID, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.SourceRevision) string { return strconv.Itoa(r.Number) })
	return items, next, nil
}
