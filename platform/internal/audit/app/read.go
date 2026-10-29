package app

import (
	"context"
	"errors"
	"math"
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// ReadService is the read side of the trail (RFC 0006 §6.2): a tenant's
// entries, listed and read one at a time.
//
// It needs audit.read — owner and admin; no API token scope grants it —
// and answers a project-scoped principal (§4.1) with the entries of its
// projects only. The tenant-level entries — sign-ins, members, tokens,
// vendors, groups, audit exports — belong to no project: they are the
// organisation's, and a principal limited to some projects does not see
// them, as it does not see the tenant's people through any other route
// it could not reach unscoped. A member whose visibility is `assigned`
// is refused (authz.Projects): the trail is never a unit of their
// assignments.
type ReadService struct {
	entries EntryReader
}

// NewReadService returns the read side on entries.
func NewReadService(entries EntryReader) *ReadService { return &ReadService{entries: entries} }

// ErrInvalidCursor is a page cursor this service did not issue.
var ErrInvalidCursor = errors.New("audit: invalid page cursor")

// ListEntries lists ctx's tenant's entries matching f, a page of at most
// limit after cursor ("" for the first page). next is the cursor of the
// following page, "" on the last.
func (s *ReadService) ListEntries(ctx context.Context, f EntryFilter, cursor string, limit int) ([]domain.Entry, string, error) {
	visible, err := authz.Projects(ctx, authz.AuditRead)
	if err != nil {
		return nil, "", err
	}
	if !visible.All() {
		if f.Project.Valid && !visible.Allows(f.Project.UUID) {
			return nil, "", nil // a project outside the scope has no entries the caller may see
		}
		f.Projects = visible.IDs()
	}
	after, err := parseCursor(cursor, f.Ascending)
	if err != nil {
		return nil, "", err
	}
	rows, err := s.entries.ListEntries(ctx, f, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(rows) <= limit {
		return rows, "", nil
	}
	rows = rows[:limit]
	return rows, strconv.FormatInt(rows[len(rows)-1].Sequence, 10), nil
}

// parseCursor reads a cursor: the sequence the previous page ended at.
// The first page starts before every sequence (ascending) or after
// every one (newest first).
func parseCursor(cursor string, ascending bool) (int64, error) {
	if cursor == "" {
		if ascending {
			return 0, nil
		}
		return math.MaxInt64, nil
	}
	n, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || n < 1 {
		return 0, ErrInvalidCursor
	}
	return n, nil
}

// Entry reads one entry by its sequence. One outside a project-scoped
// caller's projects — or tenant-level, for such a caller — is
// authz.ErrNotVisible, the answer for an entry that does not exist.
func (s *ReadService) Entry(ctx context.Context, sequence int64) (domain.Entry, error) {
	visible, err := authz.Projects(ctx, authz.AuditRead)
	if err != nil {
		return domain.Entry{}, err
	}
	if sequence < 1 {
		return domain.Entry{}, ErrNotFound
	}
	e, err := s.entries.EntryAt(ctx, sequence)
	if err != nil {
		return domain.Entry{}, err
	}
	if !visible.All() && (!e.Project.Valid || !visible.Allows(e.Project.UUID)) {
		return domain.Entry{}, authz.ErrNotVisible
	}
	return e, nil
}
