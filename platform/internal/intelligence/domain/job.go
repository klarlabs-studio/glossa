package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// JobState is where a job is in the queue (RFC 0003 §3.4).
type JobState string

// Job states. queued → running → succeeded | skipped | failed | dead;
// queued jobs can be cancelled.
const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	// JobSkipped: nothing to do any more (the message changed, was
	// translated meanwhile, or is gone).
	JobSkipped JobState = "skipped"
	// JobFailed: a permanent failure (no consent, sensitive, invalid
	// output, budget, no route), never retried.
	JobFailed JobState = "failed"
	// JobDead: transient failures exhausted the attempts (dead letter).
	JobDead      JobState = "dead"
	JobCancelled JobState = "cancelled"
)

// ParseJobState validates s.
func ParseJobState(s string) (JobState, error) {
	switch st := JobState(s); st {
	case JobQueued, JobRunning, JobSucceeded, JobSkipped, JobFailed, JobDead, JobCancelled:
		return st, nil
	}
	return "", fmt.Errorf("intelligence: invalid job state %q", s)
}

// Final reports whether the job is over.
func (s JobState) Final() bool { return s != JobQueued && s != JobRunning }

// Trigger is why a job exists.
type Trigger string

// Triggers.
const (
	TriggerMessageCreated      Trigger = "message_created"
	TriggerTranslationOutdated Trigger = "translation_outdated"
	TriggerLocaleAdded         Trigger = "locale_added"
	TriggerFill                Trigger = "fill"
)

// Failure codes a job ends with. They are part of the API contract.
const (
	FailureProviderConsent = "provider_consent"
	FailureSensitive       = "sensitive"
	FailureInvalidOutput   = "invalid_output"
	FailureBudgetExceeded  = "budget_exceeded"
	FailureNoRoute         = "no_route"
	FailureProviderError   = "provider_error"
	FailureInvalidSource   = "invalid_source"
	FailureRetriesExceeded = "retries_exhausted"
	FailureInternal        = "internal"
	// Skip reasons.
	SkipSuperseded = "superseded"
	SkipUpToDate   = "up_to_date"
	SkipMessage    = "message_gone"
	SkipLocale     = "locale_gone"
)

// Job is one message × locale to translate.
type Job struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	MessageID      uuid.UUID
	MessageKey     string
	Namespace      string
	Locale         string
	SourceRevision int
	// Fingerprint identifies the knowledge the job was queued against;
	// with the message, locale and source revision it is the job's
	// idempotency key.
	Fingerprint string
	Trigger     Trigger
	FillID      *uuid.UUID
	// Forced means the job was asked for even though the translation was
	// already current (RFC 0004 §5.3), so the worker's own up-to-date
	// check must not skip it.
	Forced      bool
	State       JobState
	Attempts    int
	MaxAttempts int
	AvailableAt time.Time
	FailureCode string
	LastError   string
	// SuggestionID is set once the job produced a suggestion.
	SuggestionID *uuid.UUID
	CreatedBy    string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	UpdatedAt    time.Time
}

// DefaultMaxAttempts is how often a job is tried before it is
// dead-lettered.
const DefaultMaxAttempts = 5

// Backoff is the delay before attempt n+1 after n transient failures:
// 30 s doubling, at most 30 min.
func Backoff(n int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < n && d < 30*time.Minute; i++ {
		d *= 2
	}
	return min(d, 30*time.Minute)
}

// Fingerprint is the knowledge fingerprint of a job: the prompt
// versions, the style guide versions and the termbase concepts (with
// their versions) the source mentions. Changing any of them makes a new
// job for the same message, locale and source revision; a duplicate
// event with unchanged knowledge finds the existing one. Translation
// memory is deliberately left out: it changes with every approval.
type Fingerprint struct {
	Prompts  string
	Styles   []string // "<guide id>@<version>"
	Concepts []string // "<concept id>@<version>"
	Routing  string
}

// Sum returns the fingerprint's hex SHA-256.
func (f Fingerprint) Sum() string {
	styles, concepts := slices.Clone(f.Styles), slices.Clone(f.Concepts)
	slices.Sort(styles)
	slices.Sort(concepts)
	h := sha256.New()
	for _, part := range []string{"v1", f.Prompts, strings.Join(styles, ","), strings.Join(concepts, ","), f.Routing} {
		h.Write([]byte(strconv.Itoa(len(part)) + ":" + part + ";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Job errors.
var (
	ErrJobNotCancellable = errors.New("intelligence: only queued jobs can be cancelled")
	ErrLeaseLost         = errors.New("intelligence: the job's lease was lost to another worker")
)
