package app

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// MaxBatch bounds a bulk request.
const MaxBatch = 500

// UpsertItem is one message of a bulk upsert (CLI push and extract).
// Detail members left nil keep the stored value (or the default for a
// new message).
type UpsertItem struct {
	Key         string
	Namespace   *string
	Description *string
	MaxLength   *int
	Text        string
	Syntax      string
	// BaseRevision, when set, is the source revision the client last
	// saw: if the stored message has moved on, the item fails with
	// source_revision_conflict instead of overwriting someone's edit.
	BaseRevision *int
}

// UpsertStatus is what a bulk upsert did with one item.
type UpsertStatus string

// Upsert statuses.
const (
	UpsertCreated   UpsertStatus = "created"
	UpsertRevised   UpsertStatus = "revised"   // new source revision (details may have changed too)
	UpsertUpdated   UpsertStatus = "updated"   // details or reactivation, same source
	UpsertUnchanged UpsertStatus = "unchanged" // nothing to do: pushing twice is a no-op
	UpsertFailed    UpsertStatus = "failed"
)

// ItemError is a per-item failure with a stable problem code.
type ItemError struct {
	Code   string
	Detail string
}

// UpsertResult reports one item, in request order.
type UpsertResult struct {
	Key     string
	Status  UpsertStatus
	Message *domain.Message
	Error   *ItemError
}

// UpsertMessages creates or revises messages by key in one transaction.
// It is idempotent: pushing the same catalog twice changes nothing the
// second time. Items fail individually (invalid key or source, duplicate
// key in the batch, stale base revision) without failing the batch.
//
// The items are decided in memory against one locked read of the
// existing messages, and the writes go out in one statement per table
// and one outbox insert for all events (#77): a full batch costs a
// handful of round trips, not several per message.
func (s *Service) UpsertMessages(ctx context.Context, project domain.ProjectID, items []UpsertItem) ([]UpsertResult, error) {
	by, err := authorIn(ctx, authz.CatalogWrite, project.UUID())
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > MaxBatch {
		return nil, ErrTooManyItems
	}
	var results []UpsertResult
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		p, err := st.Project(ctx, project)
		if err != nil {
			return err
		}
		keys, prepared := s.prepareUpserts(p, items)
		existing, err := st.LockMessagesByKeys(ctx, project, keys)
		if err != nil {
			return err
		}
		results = make([]UpsertResult, len(items))
		var (
			changed []domain.Message
			w       upsertWrites
		)
		for i, it := range prepared {
			if it.err != nil {
				results[i] = UpsertResult{Key: items[i].Key, Status: UpsertFailed, Error: it.err}
				continue
			}
			res, err := s.upsertOne(p, it, existing, by, &w)
			if err != nil {
				return err
			}
			if res.Message != nil {
				existing[res.Message.Key] = *res.Message
			}
			if res.Status == UpsertCreated || res.Status == UpsertRevised || res.Status == UpsertUpdated {
				changed = append(changed, *res.Message)
			}
			results[i] = res
		}
		if err := w.flush(ctx, st); err != nil {
			return err
		}
		if err := settleProposals(ctx, st, changed); err != nil {
			return err
		}
		if s.projection == nil || len(changed) == 0 {
			return nil
		}
		return s.projection.MessagesChanged(ctx, changed)
	})
	return results, err
}

// upsertWrites collects a bulk upsert's writes, in item order, so they
// go out as one statement per table (see Store's bulk writes).
type upsertWrites struct {
	inserts   []MessageInsert
	updates   []MessageUpdate
	revisions []domain.SourceRevision
	events    []outbox.Event
}

// flush writes everything collected. New messages go first: their
// source revisions reference them.
func (w *upsertWrites) flush(ctx context.Context, st Store) error {
	if err := st.InsertMessages(ctx, w.inserts); err != nil {
		return err
	}
	if err := st.UpdateMessages(ctx, w.updates); err != nil {
		return err
	}
	if err := st.AppendSourceRevisions(ctx, w.revisions); err != nil {
		return err
	}
	return st.PublishAll(ctx, w.events)
}

type preparedItem struct {
	UpsertItem
	key     domain.MessageKey
	content mfcontent.Content
	err     *ItemError
}

// prepareUpserts validates items without touching storage.
func (s *Service) prepareUpserts(p domain.Project, items []UpsertItem) ([]domain.MessageKey, []preparedItem) {
	seen := map[domain.MessageKey]bool{}
	var keys []domain.MessageKey
	out := make([]preparedItem, len(items))
	for i, it := range items {
		out[i] = preparedItem{UpsertItem: it}
		key, err := domain.ParseMessageKey(it.Key)
		if err != nil {
			out[i].err = &ItemError{Code: "invalid_message_key", Detail: err.Error()}
			continue
		}
		if seen[key] {
			out[i].err = &ItemError{Code: "duplicate_key", Detail: "the key appears earlier in this batch"}
			continue
		}
		seen[key] = true
		c, err := parseSource(p, it.Syntax, it.Text)
		if err != nil {
			out[i].err = sourceItemError(err)
			continue
		}
		out[i].key, out[i].content = key, c
		keys = append(keys, key)
	}
	return keys, out
}

func sourceItemError(err error) *ItemError {
	var invalid *mfcontent.InvalidError
	switch {
	case errors.As(err, &invalid):
		return &ItemError{Code: "invalid_message", Detail: string(invalid.Code) + ": " + invalid.Message}
	case errors.Is(err, mfcontent.ErrInvalidSyntax):
		return &ItemError{Code: "invalid_syntax", Detail: err.Error()}
	case errors.Is(err, mfcontent.ErrTooLong):
		return &ItemError{Code: "message_too_long", Detail: err.Error()}
	}
	return &ItemError{Code: "invalid_message", Detail: err.Error()}
}

func (it preparedItem) details(base domain.Details) (domain.Details, *ItemError) {
	d := base
	if it.Namespace != nil {
		ns, err := domain.ParseNamespace(*it.Namespace)
		if err != nil {
			return d, &ItemError{Code: "invalid_namespace", Detail: err.Error()}
		}
		d.Namespace = ns
	}
	if it.Description != nil {
		d.Description = *it.Description
	}
	if it.MaxLength != nil {
		d.MaxLength = it.MaxLength
	}
	if err := d.Validate(); err != nil {
		return d, &ItemError{Code: "invalid_details", Detail: err.Error()}
	}
	return d, nil
}

// upsertOne decides one item against the locked existing messages and
// adds its writes to w.
func (s *Service) upsertOne(p domain.Project, it preparedItem,
	existing map[domain.MessageKey]domain.Message, by domain.Author, w *upsertWrites,
) (UpsertResult, error) {
	res := UpsertResult{Key: it.Key}
	m, found := existing[it.key]
	if !found {
		if it.BaseRevision != nil && *it.BaseRevision != 0 {
			res.Status, res.Error = UpsertFailed, &ItemError{Code: "source_revision_conflict", Detail: "the message doesn't exist"}
			return res, nil
		}
		return s.createFromUpsert(p, it, by, w)
	}
	if it.BaseRevision != nil && *it.BaseRevision != m.Revision {
		res.Status, res.Error = UpsertFailed, &ItemError{Code: "source_revision_conflict",
			Detail: "the message is at a newer source revision than base_revision"}
		return res, nil
	}
	d, ierr := it.details(m.Details)
	if ierr != nil {
		res.Status, res.Error = UpsertFailed, ierr
		return res, nil
	}
	expected, oldRev, now := m.Version, m.Revision, s.now()
	// The default branch's push is the merge (RFC 0004 §4.1): a proposed
	// message it pushes goes live.
	activated := m.Activate(now)
	reactivated := m.Reactivate(now)
	detailsChanged, _ := m.ChangeDetails(d, now)
	rev, revised, err := m.ReviseSource(it.content, by, now)
	if err != nil {
		return res, err
	}
	res.Message, res.Status = &m, UpsertUnchanged
	if !activated && !reactivated && !detailsChanged && !revised {
		return res, nil
	}
	w.updates = append(w.updates, MessageUpdate{Message: m, Expected: expected})
	if activated || reactivated {
		typ := domain.EventMessageReactivated
		if activated {
			typ = domain.EventMessageActivated
		}
		w.events = append(w.events, messageEvent(typ, m, by))
	}
	if detailsChanged {
		w.events = append(w.events, messageEvent(domain.EventMessageUpdated, m, by))
	}
	res.Status = UpsertUpdated
	if revised {
		res.Status = UpsertRevised
		w.revisions = append(w.revisions, rev)
		w.events = append(w.events, sourceRevisedEvent(m, oldRev, by))
	}
	return res, nil
}

func (s *Service) createFromUpsert(p domain.Project, it preparedItem, by domain.Author, w *upsertWrites) (UpsertResult, error) {
	res := UpsertResult{Key: it.Key}
	d, ierr := it.details(domain.Details{Namespace: domain.DefaultNamespace})
	if ierr != nil {
		res.Status, res.Error = UpsertFailed, ierr
		return res, nil
	}
	m, first, err := domain.NewMessage(p.ID, it.key, d, it.content, by, s.now())
	if err != nil {
		return res, err
	}
	w.inserts = append(w.inserts, MessageInsert{Message: m, First: first, By: by})
	w.events = append(w.events, messageEvent(domain.EventMessageCreated, m, by))
	res.Status, res.Message = UpsertCreated, &m
	return res, nil
}

// settleProposals retires the branch proposals the default branch's push
// just merged: new keys whose message is now active, and source changes
// its new source revision now says. Proposals it didn't merge stay.
func settleProposals(ctx context.Context, st Store, changed []domain.Message) error {
	if len(changed) == 0 {
		return nil
	}
	byID := make(map[domain.MessageID]domain.Message, len(changed))
	ids := make([]domain.MessageID, len(changed))
	for i, m := range changed {
		byID[m.ID], ids[i] = m, m.ID
	}
	all, err := st.ProposalsForMessages(ctx, ids)
	if err != nil {
		return err
	}
	var merged []domain.Proposal
	for _, pr := range all {
		m := byID[pr.MessageID]
		if m.State == domain.MessageActive &&
			(pr.Kind == domain.ProposalNewKey || pr.Kind == domain.ProposalSourceChange && pr.Matches(m)) {
			merged = append(merged, pr)
		}
	}
	return st.DeleteProposals(ctx, merged) // one statement however many (#89)
}
