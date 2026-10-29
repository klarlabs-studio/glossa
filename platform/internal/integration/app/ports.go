// Package app is the Integration context's application layer: import
// and export jobs over the interchange converters (RFC 0003 §5–§6).
// Imports are created, then their file is streamed to object storage;
// exports are generated into it. Workers in glossa-server run both,
// reading and writing Catalog, Localization and Knowledge only through
// their application services (the ports below), within what each job's
// requester was allowed to do when they asked.
package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// Errors. Adapters translate storage errors into the store ones.
var (
	ErrNotFound         = errors.New("integration: not found")
	ErrProjectNotFound  = errors.New("integration: no such project")
	ErrLocaleNotFound   = errors.New("integration: the project has no such locale")
	ErrIdempotencyReuse = errors.New("integration: Idempotency-Key reused for a different request")
	ErrUploadTooLarge   = errors.New("integration: the file exceeds the upload limit")
	ErrEmptyUpload      = errors.New("integration: the file is empty")
	// ErrUploadInterrupted means the request body broke off mid-file.
	ErrUploadInterrupted = errors.New("integration: the upload was interrupted")
	ErrInvalidQuery      = errors.New("integration: invalid query")
	// ErrStorage means object storage failed; the request can be retried.
	ErrStorage = errors.New("integration: file storage is unavailable")
	// ErrLeaseLost means another worker holds the job now.
	ErrLeaseLost = errors.New("integration: the job's lease was lost")
)

// ProjectInfo is what Integration needs to know about a Catalog project.
type ProjectInfo struct {
	ID             uuid.UUID
	Slug           string
	SourceLocale   bcp47.Tag
	ReviewRequired bool
}

// Message is a Catalog message as an import compares it.
type Message struct {
	Key    string
	Source mfcontent.Content
}

// MessageWrite creates or revises a message (Catalog's bulk upsert).
type MessageWrite struct {
	Key         string
	Namespace   string
	Description string
	// MaxLength 0 leaves it unset.
	MaxLength int
	Source    mfcontent.Content
	// SourceOnly revises the source and leaves the stored details
	// (namespace, description, max length) as they are.
	SourceOnly bool
}

// WriteResult is what another context did with one item of a batch:
// Status is its own word (created, revised, updated, unchanged, …);
// Code and Detail are set when it refused the item.
type WriteResult struct {
	Status string
	Code   string
	Detail string
}

// Catalog is Catalog's application port, as Integration uses it.
type Catalog interface {
	// Project answers ErrProjectNotFound for an unknown project.
	Project(ctx context.Context, id uuid.UUID) (ProjectInfo, error)
	// MessagesByKeys returns the existing messages among keys, by key.
	MessagesByKeys(ctx context.Context, project uuid.UUID, keys []string) (map[string]Message, error)
	// CheckMessage validates a write without storing it (a dry run's
	// prediction): "" when Catalog would accept it, else its item code.
	CheckMessage(p ProjectInfo, w MessageWrite) (code, detail string)
	// UpsertMessages creates or revises messages by key, at most 500.
	UpsertMessages(ctx context.Context, project uuid.UUID, items []MessageWrite) ([]WriteResult, error)
}

// TranslationWrite is one imported translation.
type TranslationWrite struct {
	Key     string
	Locale  bcp47.Tag
	Content mfcontent.Content
	// State is the review state asked for (already capped).
	State string
	// OriginDetail is the provenance detail (job, file, format).
	OriginDetail []byte
}

// Snapshot is a project's catalog as an export writes it.
type Snapshot struct {
	Project ProjectInfo
	// Locales are the project's target locales (not the source).
	Locales []bcp47.Tag
	// Messages are the active messages, in key order.
	Messages []SnapshotMessage
}

// SnapshotMessage is one message with its translations in the eligible
// review states.
type SnapshotMessage struct {
	Key          string
	Namespace    string
	Description  string
	MaxLength    int
	Source       mfcontent.Content
	Translations map[bcp47.Tag]SnapshotTranslation
}

// SnapshotTranslation is one translation of a snapshot.
type SnapshotTranslation struct {
	Content mfcontent.Content
	State   string
}

// Localization is Localization's application port, as Integration uses
// it.
type Localization interface {
	// ImportTranslations writes up to 500 translations with provenance
	// import; keepApproved never lowers an approved translation, dryRun
	// stores nothing.
	ImportTranslations(ctx context.Context, project uuid.UUID, items []TranslationWrite, keepApproved, dryRun bool) ([]WriteResult, error)
	// Locales returns the project's target locales.
	Locales(ctx context.Context, project uuid.UUID) ([]bcp47.Tag, error)
	// Snapshot reads the project's active messages and their
	// translations in states (Catalog and Localization joined).
	Snapshot(ctx context.Context, project uuid.UUID, states []string) (Snapshot, error)
}

// TMWrite is one imported translation-memory unit.
type TMWrite struct {
	SourceLocale bcp47.Tag
	TargetLocale bcp47.Tag
	Source       mf.Message
	Target       mf.Message
}

// ConceptWrite is one imported termbase concept.
type ConceptWrite struct {
	ID         uuid.UUID
	Definition string
	Domain     string
	Note       string
	Terms      []TermWrite
}

// TermWrite is one term of an imported concept.
type TermWrite struct {
	Locale       string
	Text         string
	Status       string
	PartOfSpeech string
	Note         string
}

// TMUnitView is a stored TM unit, as an export writes it.
type TMUnitView struct {
	ID           uuid.UUID
	ProjectID    *uuid.UUID
	MessageKey   string
	SourceLocale bcp47.Tag
	TargetLocale bcp47.Tag
	SourceMF2    string
	TargetMF2    string
	HitCount     int
	LastHitAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TMFilter narrows a TM export.
type TMFilter struct {
	// ProjectID nil exports the whole tenant's memory.
	ProjectID     *uuid.UUID
	SourceLocale  *bcp47.Tag
	TargetLocales []bcp47.Tag
}

// ConceptView is a stored concept, as an export writes it.
type ConceptView struct {
	ID         uuid.UUID
	ProjectID  *uuid.UUID
	Definition string
	Domain     string
	Note       string
	Terms      []TermWrite
}

// Knowledge is Knowledge's application port, as Integration uses it.
type Knowledge interface {
	// ImportTMUnits adds up to 500 units (Status created, unchanged or
	// invalid).
	ImportTMUnits(ctx context.Context, project *uuid.UUID, units []TMWrite, dryRun bool) ([]WriteResult, error)
	// ImportConcepts creates or replaces up to 500 concepts (Status
	// created, updated, unchanged, conflict or invalid).
	ImportConcepts(ctx context.Context, project *uuid.UUID, concepts []ConceptWrite, overwrite, dryRun bool) ([]WriteResult, error)
	// ConceptExists reports whether the tenant has a concept with id.
	ConceptExists(ctx context.Context, id uuid.UUID) (bool, error)
	// TMUnits pages through active units in a stable order ("" starts).
	TMUnits(ctx context.Context, f TMFilter, after string, limit int) ([]TMUnitView, string, error)
	// Concepts pages through concepts ("" starts): all of the tenant's
	// for project nil, else the project's own.
	Concepts(ctx context.Context, project *uuid.UUID, after string, limit int) ([]ConceptView, string, error)
}

// Objects is object storage, where uploads and exports live.
type Objects interface {
	PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// JobFilter narrows a job listing.
type JobFilter struct {
	Direction domain.Direction
	ProjectID *uuid.UUID
	// TenantWide lists only jobs without a project (the workspace's
	// translation memory and termbase).
	TenantWide bool
	Kind       *domain.Kind
	State      *domain.State
	// Projects, when not nil, limits the jobs to these projects: a
	// project-scoped caller's (RFC 0006 §4.1). The use case sets it.
	Projects []uuid.UUID
}

// JobCursor is the keyset position after a listed job (newest first).
type JobCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ItemFilter narrows an import's results.
type ItemFilter struct {
	Status *domain.ItemStatus
	Kind   *domain.ItemKind
}

// Store is Integration's persistence in tenant scope.
type Store interface {
	// InsertJob stores j; inserted is false if a job with its ID exists.
	InsertJob(ctx context.Context, j domain.Job) (inserted bool, err error)
	Job(ctx context.Context, id uuid.UUID) (domain.Job, error)
	LockJob(ctx context.Context, id uuid.UUID) (domain.Job, error)
	// LockClaimedJob locks a running job the caller still holds.
	LockClaimedJob(ctx context.Context, id, token uuid.UUID) (domain.Job, error)
	// SaveJob stores j's mutable fields; a claimed job keeps its claim.
	SaveJob(ctx context.Context, j domain.Job) error
	// RetryJob queues j again after delay, releasing its claim.
	RetryJob(ctx context.Context, j domain.Job, delay time.Duration) error
	Jobs(ctx context.Context, f JobFilter, before *JobCursor, limit int) ([]domain.Job, error)
	// ReusableJob finds a succeeded, unexpired import (not itself a
	// reuse) with fingerprint, other than exclude; ErrNotFound if none.
	ReusableJob(ctx context.Context, fingerprint string, exclude uuid.UUID) (domain.Job, error)
	// PutItems stores results (replacing any with the same seq).
	PutItems(ctx context.Context, job uuid.UUID, items []domain.Item) error
	Items(ctx context.Context, job uuid.UUID, f ItemFilter, afterSeq, limit int) ([]domain.Item, error)
	ProjectJobs(ctx context.Context, project uuid.UUID) ([]domain.Job, error)
	DeleteProjectJobs(ctx context.Context, project uuid.UUID) error
	// CheckHealth is the project's concluded pull-request checks since a
	// time, with the percentiles of how long they took. Tenant scope,
	// not the system scope the check queue's worker runs in: this is a
	// person reading their own project, and RLS is what says so.
	CheckHealth(ctx context.Context, project uuid.UUID, since time.Time) (CheckHealth, error)
	Publish(ctx context.Context, e outbox.Event) error
}

// Claim is a job leased to a worker.
type Claim struct {
	JobID    uuid.UUID
	TenantID uuid.UUID
	Token    uuid.UUID
	Attempts int
}

// ExpiredFile is a job's file past its retention.
type ExpiredFile struct {
	JobID    uuid.UUID
	TenantID uuid.UUID
	Key      string
}

// Claimer works across tenants (system scope integration.jobs).
type Claimer interface {
	// Claim leases the next due job; ok is false when none is due.
	Claim(ctx context.Context, lease time.Duration) (c Claim, ok bool, err error)
	// ExpiredFiles lists up to limit jobs whose files are past retention.
	ExpiredFiles(ctx context.Context, limit int) ([]ExpiredFile, error)
	// MarkFilesDeleted records that the jobs' files are gone.
	MarkFilesDeleted(ctx context.Context, jobs []uuid.UUID) error
	// ExpireUploads fails imports still waiting for their file after
	// window; it returns how many.
	ExpireUploads(ctx context.Context, window time.Duration) (int, error)
}
