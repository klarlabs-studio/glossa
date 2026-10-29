package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// CreateWaiver is a finding to accept. The reason is the whole point:
// a suppression nobody had to justify is technical debt with no paper
// trail (RFC 0005 §2.3, §14 decision 5).
type CreateWaiver struct {
	// Fingerprint is the finding to accept — a fingerprint and not a
	// finding ID, so the waiver survives the run that found it.
	Fingerprint string
	Reason      string
	// Scope is `project` (the default) or `branch`.
	Scope domain.WaiverScope
	// Ref is the branch, for a branch-scoped waiver.
	Ref string
	// SourceRevision is the source revision the waiver is made against.
	// nil takes it from the most recent stored finding carrying the
	// fingerprint, which is the one the person is looking at; with no
	// such finding it is 0.
	SourceRevision *int
	// ExpiresAt is when the daily sweep retires it; nil never.
	ExpiresAt *time.Time
}

// CreateWaiver accepts a finding, with a reason. It never deletes
// anything: the finding it accepts is still computed, still listed and
// still counted, at severity `waived`, and it comes back on its own
// once the source revision moves.
//
// Waiving the same finding again is not a second waiver: the project
// keeps one live waiver per fingerprint and reach, and a repeat
// restates its reason, expiry and source revision. created says which
// happened.
func (s *Service) CreateWaiver(ctx context.Context, project uuid.UUID, in CreateWaiver) (w WaiverRecord, created bool, err error) {
	actor, err := s.write(ctx, project)
	if err != nil {
		return WaiverRecord{}, false, err
	}
	if in.Scope == "" {
		in.Scope = domain.WaiverProject
	}
	if in.Scope == domain.WaiverProject {
		in.Ref = ""
	}
	now := s.now()
	waiver := domain.Waiver{
		ID: uuid.Must(uuid.NewV7()), Project: project, Fingerprint: in.Fingerprint,
		Reason: strings.TrimSpace(in.Reason), Scope: in.Scope, Ref: in.Ref,
		CreatedBy: actor, CreatedAt: now, ExpiresAt: in.ExpiresAt,
	}
	if in.SourceRevision != nil {
		waiver.SourceRevision = *in.SourceRevision
	}
	if err := waiver.Validate(); err != nil {
		s.metrics.WaiverDecided(WaiverRefused)
		return WaiverRecord{}, false, err
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		s.metrics.WaiverDecided(WaiverRefused)
		return WaiverRecord{}, false, fmt.Errorf("%w: %s", domain.ErrExpiryInThePast, in.ExpiresAt.UTC().Format(time.RFC3339))
	}
	var out WaiverRecord
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		accepts, found, err := st.LatestFinding(ctx, project, waiver.Fingerprint)
		if err != nil {
			return err
		}
		if in.SourceRevision == nil && found {
			waiver.SourceRevision = accepts.SourceRevision
		}
		stored, isNew, err := st.UpsertWaiver(ctx, waiver)
		if err != nil {
			return err
		}
		out, created = WaiverRecord{Waiver: stored, Accepts: accepts, Active: stored.Live(now)}, isNew
		return nil
	})
	if err != nil {
		return WaiverRecord{}, false, err
	}
	if created {
		s.metrics.WaiverDecided(WaiverCreated)
	} else {
		s.metrics.WaiverDecided(WaiverUpdated)
	}
	return out, created, nil
}

// ListWaivers pages a project's waivers, newest first, each with what
// it accepts.
func (s *Service) ListWaivers(ctx context.Context, project uuid.UUID, f WaiverFilter, page pagination.Page) ([]WaiverRecord, *string, error) {
	if err := s.read(ctx, project); err != nil {
		return nil, nil, err
	}
	if f.Layer != "" && !domain.Layer(f.Layer).Valid() {
		return nil, nil, fmt.Errorf("%w: layer %q", ErrInvalidQuery, f.Layer)
	}
	after, err := parseWaiverCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	var rows []WaiverRecord
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.ListWaivers(ctx, project, f, after, page.Limit(), now)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(w WaiverRecord) string {
		return w.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + w.ID.String()
	})
	return items, next, nil
}

// RevokeWaiver takes a waiver back. The findings it accepted come back
// as ordinary findings on the next read; nothing is deleted, and the
// revoked waiver stays as history. Revoking one twice is not an error.
func (s *Service) RevokeWaiver(ctx context.Context, project, id uuid.UUID) error {
	if _, err := s.write(ctx, project); err != nil {
		return err
	}
	now := s.now()
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.Waiver(ctx, project, id); err != nil {
			return err
		}
		return st.RevokeWaiver(ctx, project, id, now)
	})
	if err != nil {
		return err
	}
	s.metrics.WaiverDecided(WaiverRevoked)
	return nil
}

func parseWaiverCursor(s string) (*WaiverCursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // no cursor is the first page
	}
	at, id, ok := strings.Cut(s, "|")
	t, errTime := time.Parse(time.RFC3339Nano, at)
	u, errID := uuid.Parse(id)
	if !ok || errTime != nil || errID != nil {
		return nil, invalidPageToken()
	}
	return &WaiverCursor{CreatedAt: t, ID: u}, nil
}
