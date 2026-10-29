package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// The ports of the read and export API (RFC 0006 §6.2, wave 5).

var (
	// ErrNotFound: no such entry or export job in the tenant.
	ErrNotFound = errors.New("audit: not found")
	// ErrLeaseLost means another worker holds the export job now.
	ErrLeaseLost = errors.New("audit: the export job's lease was lost")
)

// EntryFilter narrows a listing of entries. A zero field filters
// nothing.
type EntryFilter struct {
	// From and To bound occurred_at, [From, To).
	From, To time.Time
	// FirstSequence and LastSequence bound the sequence, inclusive.
	FirstSequence, LastSequence int64
	Actor, Action               string
	Source                      domain.Source
	AggregateType, AggregateID  string
	Project                     uuid.NullUUID
	// Projects, when not nil, limits the entries to these projects — a
	// project-scoped caller's — which leaves out every tenant-level
	// entry. The use case sets it.
	Projects []uuid.UUID
	// Ascending lists in chain order; otherwise newest first.
	Ascending bool
}

// Span is the chain segment the entries of a time range occupy.
type Span struct {
	// First and Last are the least and greatest sequence that occurred
	// in the range (0 when none did); Inside is how many did.
	First, Last, Inside int64
}

// Contiguous reports whether every entry of the segment occurred in the
// range: then the segment is exactly the range's entries.
func (s Span) Contiguous() bool { return s.Inside == 0 || s.Last-s.First+1 == s.Inside }

// EntryReader reads ctx's tenant's chain for the API and the export
// jobs. The Postgres store implements it beside Store.
type EntryReader interface {
	// ListEntries lists entries matching f, past cursor (the last
	// sequence of the previous page; 0 for the first), at most limit.
	ListEntries(ctx context.Context, f EntryFilter, cursor int64, limit int) ([]domain.Entry, error)
	// EntryAt reads one entry; ErrNotFound if the chain has none there.
	EntryAt(ctx context.Context, sequence int64) (domain.Entry, error)
	// ChainHead is the chain's last entry (the zero Head when empty).
	ChainHead(ctx context.Context) (domain.Head, error)
	// OccurredSpan is the segment the entries that occurred in [from,
	// to) occupy.
	OccurredSpan(ctx context.Context, from, to time.Time) (Span, error)
	// Entries reads the chain in order, after a sequence.
	Entries(ctx context.Context, after int64, limit int) ([]domain.Entry, error)
}

// ExportCursor is the keyset position after a listed export job.
type ExportCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ExportJobs is audit_export_jobs in ctx's tenant.
type ExportJobs interface {
	// Create stores j and publishes e in one transaction; inserted is
	// false, and nothing is published, when a job with j.ID exists.
	Create(ctx context.Context, j domain.ExportJob, e outbox.Event) (inserted bool, err error)
	// Get reads a job; ErrNotFound if there is none.
	Get(ctx context.Context, id uuid.UUID) (domain.ExportJob, error)
	// List lists jobs newest first, before a cursor.
	List(ctx context.Context, before *ExportCursor, limit int) ([]domain.ExportJob, error)
	// Finish stores a claimed job's end and publishes e; ErrLeaseLost
	// when the claim is no longer the caller's.
	Finish(ctx context.Context, j domain.ExportJob, token uuid.UUID, e outbox.Event) error
	// Retry queues a claimed job again after delay; ErrLeaseLost as
	// Finish.
	Retry(ctx context.Context, j domain.ExportJob, token uuid.UUID, cause string, delay time.Duration) error
}

// ExportClaim is an export job leased to a worker.
type ExportClaim struct {
	JobID    uuid.UUID
	TenantID uuid.UUID
	Token    uuid.UUID
	Attempts int
}

// ExpiredExport is a job whose objects are past retention.
type ExpiredExport struct {
	JobID    uuid.UUID
	TenantID uuid.UUID
	Keys     []string
}

// ExportClaimer works across tenants (system scope audit.export_jobs).
type ExportClaimer interface {
	// Claim leases the next due job; ok is false when none is due.
	Claim(ctx context.Context, lease time.Duration) (c ExportClaim, ok bool, err error)
	// Expired lists up to limit jobs whose objects are past retention.
	Expired(ctx context.Context, limit int) ([]ExpiredExport, error)
	// MarkDeleted records that the jobs' objects are gone.
	MarkDeleted(ctx context.Context, jobs []uuid.UUID) error
}

// Objects is object storage, where an export's two objects live.
type Objects interface {
	PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}
