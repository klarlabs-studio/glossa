package app

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// The read ports other contexts use. They are ordinary use cases —
// authorized, one tenant transaction each — so a caller can't learn
// more through them than through the API: the per-message ones answer
// an assigned member for the messages their units are in and nothing
// else (RFC 0006 §3.3), so a vendor's translation write can read its
// source and no other.

// MessagesByKeys returns the messages among keys that exist, by key.
// Malformed keys are simply absent.
func (s *Service) MessagesByKeys(ctx context.Context, project domain.ProjectID, keys []string) (map[string]domain.Message, error) {
	view, err := authz.Visible(ctx, authz.CatalogRead, project.UUID())
	if err != nil {
		return nil, err
	}
	var parsed []domain.MessageKey
	for _, k := range keys {
		if key, err := domain.ParseMessageKey(k); err == nil {
			parsed = append(parsed, key)
		}
	}
	out := map[string]domain.Message{}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		found, err := st.MessagesByKeys(ctx, project, parsed)
		for k, m := range found {
			if view.Message(m.ID.UUID()) {
				out[string(k)] = m
			}
		}
		return err
	})
	return out, err
}

// MessagesByIDs returns the project's messages among ids, whatever
// their state, in one query — how Intelligence shows the current source
// of a page of suggestions.
func (s *Service) MessagesByIDs(ctx context.Context, project domain.ProjectID, ids []domain.MessageID) (map[domain.MessageID]domain.Message, error) {
	view, err := authz.Visible(ctx, authz.CatalogRead, project.UUID())
	if err != nil {
		return nil, err
	}
	var out map[domain.MessageID]domain.Message
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = st.MessagesByIDs(ctx, project, ids)
		return err
	})
	for id := range out {
		if !view.Message(id.UUID()) {
			delete(out, id)
		}
	}
	return out, err
}

// MessageByID returns a message by its immutable ID — how Intelligence
// finds the message an event or job names, whatever its key is now.
func (s *Service) MessageByID(ctx context.Context, project domain.ProjectID, id domain.MessageID) (domain.Message, error) {
	if err := authz.RequireMessage(ctx, authz.CatalogRead, project.UUID(), id.UUID()); err != nil {
		return domain.Message{}, notVisible(err)
	}
	var m domain.Message
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Project(ctx, project); err != nil {
			return err
		}
		var err error
		m, err = st.MessageByID(ctx, project, id)
		return err
	})
	return m, err
}

// SourceRevisionOf returns revision n of a message's source.
func (s *Service) SourceRevisionOf(ctx context.Context, project domain.ProjectID, id domain.MessageID, n int) (domain.SourceRevision, error) {
	if err := authz.RequireMessage(ctx, authz.CatalogRead, project.UUID(), id.UUID()); err != nil {
		return domain.SourceRevision{}, notVisible(err)
	}
	var rev domain.SourceRevision
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.MessageByID(ctx, project, id); err != nil {
			return err
		}
		var err error
		rev, err = st.SourceRevision(ctx, id, n)
		return err
	})
	return rev, err
}

// SourceRef names one revision of a message's source.
type SourceRef struct {
	Message  domain.MessageID
	Revision int
}

// SourceRevisionsOf is SourceRevisionOf for many revisions in one read:
// those of refs that exist, of messages in the caller's view. A ref that
// names no visible message or no such revision is absent, where
// SourceRevisionOf answers ErrNotFound.
func (s *Service) SourceRevisionsOf(ctx context.Context, project domain.ProjectID, refs []SourceRef) (map[SourceRef]domain.SourceRevision, error) {
	view, err := authz.Visible(ctx, authz.CatalogRead, project.UUID())
	if err != nil {
		return nil, err
	}
	var out map[SourceRef]domain.SourceRevision
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = st.SourceRevisionsOf(ctx, project, refs)
		return err
	})
	for ref := range out {
		if !view.Message(ref.Message.UUID()) {
			delete(out, ref)
		}
	}
	return out, err
}

// notVisible answers a message an assigned member's units are not in
// as Catalog's own not-found, which the ports that call these already
// translate.
func notVisible(err error) error {
	if errors.Is(err, authz.ErrNotVisible) {
		return ErrNotFound
	}
	return err
}

// SourceSnapshot is what a release takes from the catalog: the project
// (source locale) and every active message with its current source, in
// key order. Obsolete messages are not released.
type SourceSnapshot struct {
	Project  domain.Project
	Messages []domain.Message
}

// ReleaseSource reads a project's releasable source in one transaction.
// The Release context joins it with Localization's
// TranslationSnapshot by message ID.
func (s *Service) ReleaseSource(ctx context.Context, project domain.ProjectID) (SourceSnapshot, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return SourceSnapshot{}, err
	}
	var snap SourceSnapshot
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if snap.Project, err = st.Project(ctx, project); err != nil {
			return err
		}
		snap.Messages, err = st.ActiveMessages(ctx, project)
		return err
	})
	return snap, err
}
