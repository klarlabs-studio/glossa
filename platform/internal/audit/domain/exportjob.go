package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Audit export jobs (RFC 0006 §6.2, wave 5): a job writes one range of a
// tenant's chain as the two objects of a glossa.audit/v1 export —
// entries.jsonl and its signed manifest.json — into object storage, and
// serves them until retention deletes them. The job row stays.

// ExportState is where an export job is.
type ExportState string

// The states: queued → running → succeeded | failed. A running job whose
// lease passed is claimed again; there is no cancellation, because a job
// writes one bounded range in one go.
const (
	ExportQueued    ExportState = "queued"
	ExportRunning   ExportState = "running"
	ExportSucceeded ExportState = "succeeded"
	ExportFailed    ExportState = "failed"
)

// Why an export job failed. A code here is part of the API contract.
const (
	// FailureRangeNotContiguous: a time range whose entries are not one
	// unbroken segment of the chain — entries appended inside the
	// segment occurred outside the range (imported v0.3 history, whose
	// occurred_at is years back) — so no export could both hold them
	// and keep its promise that every entry falls inside the range.
	// Export it by sequence instead.
	FailureRangeNotContiguous = "range_not_contiguous"
	// FailureInternal: storage or the database kept failing.
	FailureInternal = "internal"
)

// The bounds of one export (RFC 0006 §9.6).
const (
	// MaxExportSpan is the longest time range one job exports.
	MaxExportSpan = 31 * 24 * time.Hour
	// MaxExportEntries is the longest sequence range one job exports.
	// Entries are content-free and small (a few hundred bytes), so this
	// is a few hundred megabytes at most.
	MaxExportEntries = 1_000_000
	// ExportMaxAttempts is how often a job is tried before it fails.
	ExportMaxAttempts = 3
)

// Errors of a range request; each is a client error with its own code.
var (
	// ErrInvalidRange: neither range, both, an incomplete time range, or
	// one that ends before it starts.
	ErrInvalidRange = errors.New("audit: invalid export range")
	// ErrRangeTooLong: longer than MaxExportSpan or MaxExportEntries.
	ErrRangeTooLong = errors.New("audit: export range too long")
	// ErrSequenceOutOfRange: a sequence past the chain's head.
	ErrSequenceOutOfRange = errors.New("audit: sequence past the chain's head")
)

// ExportRange is what a job exports: a time range [From, To) of when
// entries occurred, or a sequence range [FirstSequence, LastSequence].
// Exactly one of the two is set.
type ExportRange struct {
	From, To      time.Time
	FirstSequence int64
	LastSequence  int64
}

// ByTime reports whether r is a time range.
func (r ExportRange) ByTime() bool { return !r.From.IsZero() }

// ExportRangeRequest is a range as a caller asked for it; the sequence
// range's end may be omitted (the chain's head).
type ExportRangeRequest struct {
	From, To      *time.Time
	FirstSequence *int64
	LastSequence  *int64
}

// Resolve checks req and returns the range a job will export: a time
// range's end in the future is cut to now (an export never claims a
// range that has not happened yet), and a sequence range without an end
// ends at head, the chain's last sequence now.
func (req ExportRangeRequest) Resolve(now time.Time, head int64) (ExportRange, error) {
	byTime := req.From != nil || req.To != nil
	bySeq := req.FirstSequence != nil || req.LastSequence != nil
	switch {
	case byTime == bySeq:
		return ExportRange{}, fmt.Errorf("%w: send a time range (from and to) or a sequence range (first_sequence), exactly one", ErrInvalidRange)
	case byTime:
		return req.timeRange(now)
	}
	return req.sequenceRange(head)
}

func (req ExportRangeRequest) timeRange(now time.Time) (ExportRange, error) {
	if req.From == nil || req.To == nil {
		return ExportRange{}, fmt.Errorf("%w: a time range needs both from and to", ErrInvalidRange)
	}
	from, to := Instant(*req.From), Instant(*req.To)
	if !from.Before(to) {
		return ExportRange{}, fmt.Errorf("%w: from must be before to", ErrInvalidRange)
	}
	if to.Sub(from) > MaxExportSpan {
		return ExportRange{}, fmt.Errorf("%w: at most 31 days per export", ErrRangeTooLong)
	}
	if now = Instant(now); to.After(now) {
		to = now
	}
	if !from.Before(to) {
		return ExportRange{}, fmt.Errorf("%w: the range starts in the future", ErrInvalidRange)
	}
	return ExportRange{From: from, To: to}, nil
}

func (req ExportRangeRequest) sequenceRange(head int64) (ExportRange, error) {
	if req.FirstSequence == nil {
		return ExportRange{}, fmt.Errorf("%w: a sequence range needs first_sequence", ErrInvalidRange)
	}
	first, last := *req.FirstSequence, head
	if req.LastSequence != nil {
		last = *req.LastSequence
	}
	switch {
	case first < 1:
		return ExportRange{}, fmt.Errorf("%w: sequences start at 1", ErrInvalidRange)
	case req.LastSequence != nil && last < first:
		return ExportRange{}, fmt.Errorf("%w: last_sequence is before first_sequence", ErrInvalidRange)
	case last > head || first > head:
		return ExportRange{}, fmt.Errorf("%w: the chain ends at %d", ErrSequenceOutOfRange, head)
	case last-first+1 > MaxExportEntries:
		return ExportRange{}, fmt.Errorf("%w: at most %d entries per export", ErrRangeTooLong, MaxExportEntries)
	}
	return ExportRange{FirstSequence: first, LastSequence: last}, nil
}

// ExportObject is one of an export's two stored objects.
type ExportObject struct {
	Key    string
	SHA256 string
	Bytes  int64
}

// ExportJob is one audit export.
type ExportJob struct {
	ID    uuid.UUID
	State ExportState
	// Range is what was asked: for a time range, the sequences are set
	// once the job has run.
	Range ExportRange
	// The result, once succeeded: the chain segment and the key.
	EntryCount    int64
	FirstPrevHash string
	LastHash      string
	KeyID         string
	Entries       ExportObject
	Manifest      ExportObject
	// FailureCode is one of the Failure… codes once failed.
	FailureCode    string
	FailureMessage string
	Attempts       int
	MaxAttempts    int
	CreatedBy      string
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	UpdatedAt      time.Time
	// ExpiresAt is when retention deletes the objects; FilesDeletedAt
	// when it did.
	ExpiresAt      time.Time
	FilesDeletedAt *time.Time
}

// Kept reports whether the job's objects can be downloaded.
func (j ExportJob) Kept() bool { return j.State == ExportSucceeded && j.FilesDeletedAt == nil }

// ExportObjectKey is where one of an export's objects lives: under
// audit/, prefixed by the tenant (RFC 0006 §9.4), which glossa-edge
// never serves.
func ExportObjectKey(tenant, job uuid.UUID, name string) string {
	return fmt.Sprintf("audit/v1/%s/exports/%s/%s", tenant, job, name)
}

// Outbox event types of export jobs. Making an export and its end are
// recorded in the trail they export (RFC 0006 §6.1: "audit exports
// themselves").
const (
	EventExportRequested = "audit.export.requested"
	EventExportCompleted = "audit.export.completed"
)

// AggregateExportJob is the aggregate of export job events.
const AggregateExportJob = "audit_export_job"

// ExportEvent is the payload of both export events: identifiers, the
// range and the outcome, never an entry.
type ExportEvent struct {
	JobID         string `json:"job_id"`
	State         string `json:"state"`
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	FirstSequence int64  `json:"first_sequence,omitempty"`
	LastSequence  int64  `json:"last_sequence,omitempty"`
	EntryCount    int64  `json:"entry_count,omitempty"`
	KeyID         string `json:"key_id,omitempty"`
	FailureCode   string `json:"failure_code,omitempty"`
	// By is the requester.
	By string `json:"by"`
}

// Event returns the job's event payload.
func (j ExportJob) Event() ExportEvent {
	e := ExportEvent{
		JobID: j.ID.String(), State: string(j.State), FirstSequence: j.Range.FirstSequence, LastSequence: j.Range.LastSequence,
		EntryCount: j.EntryCount, KeyID: j.KeyID, FailureCode: j.FailureCode, By: j.CreatedBy,
	}
	if j.Range.ByTime() {
		e.From, e.To = Instant(j.Range.From).Format(timeLayout), Instant(j.Range.To).Format(timeLayout)
	}
	return e
}

// ExportBackoff is how long a failed attempt waits before the next.
func ExportBackoff(attempt int) time.Duration {
	return time.Duration(min(attempt, 6)) * 30 * time.Second
}
