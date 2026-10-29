package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// Catalog's event contract, as Localization reads it. The payload types
// are Localization's own (an anti-corruption layer): it depends on the
// published JSON, not on Catalog's Go types.
const (
	catalogProjectCreated = "catalog.project.created"
	catalogProjectDeleted = "catalog.project.deleted"
)

// catalogMessageEvents all carry a message snapshot.
var catalogMessageEvents = []string{
	"catalog.message.created",
	"catalog.message.source_revised",
	"catalog.message.updated",
	"catalog.message.renamed",
	"catalog.message.obsoleted",
	"catalog.message.reactivated",
	"catalog.message.activated",
	"catalog.message.proposed",
}

type messageSnapshot struct {
	MessageID      string `json:"message_id"`
	ProjectID      string `json:"project_id"`
	Key            string `json:"key"`
	Namespace      string `json:"namespace"`
	State          string `json:"state"`
	SourceRevision int    `json:"source_revision"`
	Version        int    `json:"version"`
}

type projectEvent struct {
	ProjectID    string `json:"project_id"`
	SourceLocale string `json:"source_locale"`
	By           string `json:"by"`
}

// Subscribe registers Localization's subscribers. Their names are stored
// with each event; never rename them.
func (s *Service) Subscribe(r *outbox.Registry) error {
	track := outbox.BatchHandlerFuncs{Event: s.handleMessageEvent, Batch: s.handleMessageEvents}
	if err := r.SubscribeBatch("localization.track_message", track, catalogMessageEvents...); err != nil {
		return err
	}
	if err := r.Subscribe(catalogProjectCreated, "localization.add_source_locale", outbox.HandlerFunc(s.handleProjectCreated)); err != nil {
		return err
	}
	return r.Subscribe(catalogProjectDeleted, "localization.drop_project", outbox.HandlerFunc(s.handleProjectDeleted))
}

// handleMessageEvent keeps the message projection current. It is
// idempotent and order-independent: the snapshot with the highest
// version wins. When the source revision advances, the translations it
// leaves behind are now outdated, and each gets a
// localization.translation.outdated event.
func (s *Service) handleMessageEvent(ctx context.Context, d outbox.Delivery) error {
	state, err := s.messageStateOf(d)
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return s.applyMessageState(ctx, st, state, bcp47.Tag{}, projectionActor)
	})
}

// handleMessageEvents is handleMessageEvent for a delivered batch
// (#89): one transaction and applyMessageStates' few statements for
// all of them. The snapshots are applied in the order delivered; as
// one at a time, the highest version wins whatever that order.
func (s *Service) handleMessageEvents(ctx context.Context, ds []outbox.Delivery) []error {
	errs := make([]error, len(ds))
	states := make([]MessageState, 0, len(ds))
	var in []int
	for i, d := range ds {
		state, err := s.messageStateOf(d)
		if err != nil {
			errs[i] = err
			continue
		}
		states = append(states, state)
		in = append(in, i)
	}
	if len(states) == 0 {
		return errs
	}
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return s.applyMessageStates(ctx, st, states, nil, projectionActor)
	})
	if err != nil {
		for _, i := range in {
			errs[i] = outbox.BatchFailure(err)
		}
	}
	return errs
}

func (s *Service) messageStateOf(d outbox.Delivery) (MessageState, error) {
	var e struct {
		Message messageSnapshot `json:"message"`
	}
	if err := d.Decode(&e); err != nil {
		return MessageState{}, err
	}
	state, err := e.Message.state(s.now)
	if err != nil {
		return MessageState{}, outbox.Permanent(err)
	}
	return state, nil
}

// ProjectMessages brings the message projection up to date with
// messages a Catalog write is changing, inside that write's transaction
// (Catalog's bulk upsert, through an in-process port): the listings'
// missing_in and outdated_in agree with Catalog at commit, and the
// translations a new source revision leaves behind are announced in the
// same commit. The outbox subscriber stays as the idempotent catch-up —
// it finds the projection at the snapshot's version and changes nothing.
// Needs catalog.write: it is the writer's own change.
func (s *Service) ProjectMessages(ctx context.Context, ms []MessageState) error {
	if err := authz.Require(ctx, authz.CatalogWrite); err != nil {
		return err
	}
	if len(ms) == 0 {
		return nil
	}
	// Catalog's write checked its project; the port checks it again, so
	// it is never the way round a project scope (RFC 0006 §4.1).
	for _, m := range ms {
		if err := authz.InProject(ctx, m.ProjectID); err != nil {
			return err
		}
	}
	by, err := authz.EventActor(ctx) // the writer whose source revision it is
	if err != nil {
		return err
	}
	return s.tx.InCurrent(ctx, func(ctx context.Context, st Store) error {
		return s.applyMessageStates(ctx, st, ms, nil, by)
	})
}

func (m messageSnapshot) state(now func() time.Time) (MessageState, error) {
	id, err := uuid.Parse(m.MessageID)
	if err != nil {
		return MessageState{}, fmt.Errorf("localization: message event with message_id %q", m.MessageID)
	}
	project, err := uuid.Parse(m.ProjectID)
	if err != nil {
		return MessageState{}, fmt.Errorf("localization: message event with project_id %q", m.ProjectID)
	}
	if m.Version < 1 || m.SourceRevision < 1 {
		return MessageState{}, fmt.Errorf("localization: message event for %s without version or revision", id)
	}
	return MessageState{
		MessageID: id, ProjectID: project, Key: m.Key, Namespace: m.Namespace, State: m.State,
		SourceRevision: m.SourceRevision, Version: m.Version, UpdatedAt: now(),
	}, nil
}

func stateOf(m SourceMessage) MessageState {
	return MessageState{
		MessageID: m.ID, ProjectID: m.ProjectID, Key: m.Key, Namespace: m.Namespace, State: m.State,
		SourceRevision: m.Revision, Version: m.Version,
	}
}

// projectionActor is who marks translations outdated when the message
// projection catches up with Catalog on its own — in the subscriber, or
// ahead of a translation write. That is Localization's bookkeeping, not
// the act of the person whose request happened to run it: a translator
// writing `de` does not outdate `fr`.
var projectionActor = authz.SystemEventActor("localization.track_message")

// applyMessageStates is applyMessageState for many messages in a few
// statements however many there are (#77): one locked read, one
// upsert, one read of the translations the new source revisions leave
// behind and one outbox insert. except names, per message, the locale
// not to announce. The messages keep their order, and a message listed
// twice is applied twice, as one at a time would.
func (s *Service) applyMessageStates(ctx context.Context, st Store, ms []MessageState, except map[uuid.UUID]bcp47.Tag, by outbox.Actor) error {
	if len(ms) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(ms))
	for i, m := range ms {
		ids[i] = m.MessageID
	}
	stored, err := st.LockMessageStates(ctx, ids)
	if err != nil {
		return err
	}
	var (
		saves  []MessageState
		saved  = map[uuid.UUID]int{} // message → index in saves
		ranges []RevisionRange
	)
	for _, m := range ms {
		if m.UpdatedAt.IsZero() {
			m.UpdatedAt = s.now()
		}
		cur, found := stored[m.MessageID]
		if found && cur.Version >= m.Version {
			continue
		}
		if i, ok := saved[m.MessageID]; ok {
			saves[i] = m
		} else {
			saved[m.MessageID] = len(saves)
			saves = append(saves, m)
		}
		stored[m.MessageID] = m
		if found && m.SourceRevision > cur.SourceRevision {
			ranges = append(ranges, RevisionRange{MessageID: m.MessageID, Old: cur.SourceRevision, New: m.SourceRevision})
		}
	}
	if err := st.SaveMessageStates(ctx, saves); err != nil {
		return err
	}
	outdated, err := st.NewlyOutdatedFor(ctx, ranges)
	if err != nil {
		return err
	}
	byMessage := make(map[uuid.UUID][]RevisionRange, len(ranges))
	for _, r := range ranges {
		byMessage[r.MessageID] = append(byMessage[r.MessageID], r)
	}
	var events []outbox.Event
	for _, t := range outdated {
		if l, ok := except[t.MessageID]; ok && t.Locale == l {
			continue
		}
		for _, r := range byMessage[t.MessageID] { // disjoint ranges: one matches
			if t.SourceRevision >= r.Old && t.SourceRevision < r.New {
				events = append(events, outdatedEvent(t, r.New, by))
				break
			}
		}
	}
	return st.PublishAll(ctx, events)
}

func outdatedEvent(t domain.Translation, current int, by outbox.Actor) outbox.Event {
	return outbox.Event{
		Type: domain.EventTranslationOutdated, AggregateType: domain.AggregateTranslation, AggregateID: t.ID.String(),
		Actor: by,
		Payload: domain.TranslationOutdated{
			TranslationID: t.ID.String(), ProjectID: t.ProjectID.String(), MessageID: t.MessageID.String(),
			Locale: t.Locale.String(), SourceRevision: t.SourceRevision, CurrentSourceRevision: current,
		},
	}
}

// applyMessageState stores a message snapshot unless a newer one is
// stored, and announces the translations a source revision made
// outdated — except in `except`, whose translation the caller is about
// to bring current. by is who those announcements name: the Catalog
// writer when it runs inside their write, projectionActor otherwise.
func (s *Service) applyMessageState(ctx context.Context, st Store, m MessageState, except bcp47.Tag, by outbox.Actor) error {
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = s.now()
	}
	cur, found, err := st.LockMessageState(ctx, m.MessageID)
	if err != nil {
		return err
	}
	if found && cur.Version >= m.Version {
		return nil
	}
	if err := st.SaveMessageState(ctx, m); err != nil {
		return err
	}
	if !found || m.SourceRevision <= cur.SourceRevision {
		return nil
	}
	outdated, err := st.NewlyOutdated(ctx, m.MessageID, cur.SourceRevision, m.SourceRevision)
	if err != nil {
		return err
	}
	for _, t := range outdated {
		if t.Locale == except {
			continue
		}
		if err := st.Publish(ctx, outdatedEvent(t, m.SourceRevision, by)); err != nil {
			return err
		}
	}
	return nil
}

// handleProjectCreated records the new project's source locale.
func (s *Service) handleProjectCreated(ctx context.Context, d outbox.Delivery) error {
	var e projectEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	id, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("localization: project event with project_id %q", e.ProjectID))
	}
	source, err := bcp47.Parse(e.SourceLocale)
	if err != nil {
		return outbox.Permanent(err)
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return s.ensureSourceLocale(ctx, st, ProjectInfo{ID: id, SourceLocale: source, DefaultSyntax: mfcontent.MF1}, e.By)
	})
}

// handleProjectDeleted drops everything Localization holds for a
// deleted project.
func (s *Service) handleProjectDeleted(ctx context.Context, d outbox.Delivery) error {
	var e projectEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	id, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("localization: project event with project_id %q", e.ProjectID))
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return st.DeleteProjectData(ctx, id)
	})
}
