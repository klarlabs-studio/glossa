package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// The wiring's ports (RFC 0003 §3.4): persistence, the other contexts'
// application services, providers built from tenant configuration, and
// metrics. Adapters implement them; the Service depends only on these.

// Errors. Adapters translate storage and other contexts' errors into
// these.
var (
	ErrNotFound              = errors.New("intelligence: not found")
	ErrProjectNotFound       = errors.New("intelligence: no such project")
	ErrLocaleNotFound        = errors.New("intelligence: the project has no such locale")
	ErrProviderNameTaken     = errors.New("intelligence: a provider with this name exists")
	ErrProviderInUse         = errors.New("intelligence: a routing policy uses this provider")
	ErrStaleVersion          = errors.New("intelligence: version changed")
	ErrPreconditionFailed    = errors.New("intelligence: resource changed since it was read")
	ErrPreconditionRequired  = errors.New("intelligence: If-Match is required to change this resource")
	ErrIdempotencyReuse      = errors.New("intelligence: Idempotency-Key reused for a different request")
	ErrInvalidQuery          = errors.New("intelligence: invalid query")
	ErrAutoApproveIneligible = errors.New("intelligence: auto_approve needs environments whose policy ships approved text")
	ErrTranslationConflict   = errors.New("intelligence: the translation changed while the suggestion was applied; retry")
	ErrTranslationRejected   = errors.New("intelligence: Localization refused the translation")
	ErrTooManyLocales        = errors.New("intelligence: a fill covers 1 to 20 locales")
	ErrTooManyKeys           = errors.New("intelligence: a fill lists at most 500 keys")
)

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// StoredProvider is a provider with its sealed key, for building calls.
type StoredProvider struct {
	domain.ProviderConfig
	SealedKey []byte
}

// SpendEntry is one priced call in the ledger.
type SpendEntry struct {
	ID         uuid.UUID
	JobID      *uuid.UUID
	ProjectID  *uuid.UUID
	Spend      domain.Spend
	Priced     bool
	OccurredAt time.Time
}

// ProviderSpend sums a month's calls per provider and model.
type ProviderSpend struct {
	Provider, Model           string
	Cost                      domain.MicroUSD
	Calls                     int
	InputTokens, OutputTokens int64
}

// Cursor is a (time, id) keyset position, newest first.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// Fill is an explicit or locale-added batch request.
type Fill struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	Trigger      domain.Trigger
	Locales      []string
	Filter       FillFilter
	JobsCreated  int
	JobsExisting int
	// JobIDs are the jobs this fill queued or reused, for a caller that
	// wants to follow them without listing every job in the tenant. It
	// is filled on the fill's own response only, and only up to
	// MaxReportedJobIDs.
	JobIDs      []uuid.UUID
	Skipped     map[string]int
	RequestedBy string
	CreatedAt   time.Time
}

// FillFilter narrows the messages a fill translates.
type FillFilter struct {
	Namespace string `json:"namespace,omitempty"`
	KeyPrefix string `json:"key_prefix,omitempty"`
	// Keys lists messages explicitly (at most MaxFillKeys).
	Keys []string `json:"keys,omitempty"`
	// IncludeOutdated also re-translates outdated translations; by
	// default only missing ones are filled. It is Select
	// missing_or_outdated, kept for clients that predate Select.
	IncludeOutdated bool `json:"include_outdated,omitempty"`
	// Select chooses messages by their translation's state; a fill
	// records the effective one.
	Select FillSelect `json:"select,omitempty"`
	// Force translates the listed Keys even where the translation is
	// current, instead of skipping them as up_to_date. It is the one
	// case no Select can express: someone reading the text in the
	// running product wants a second opinion on what is already there
	// (RFC 0004 §5.3). It needs Keys, and it queues an existing job
	// again rather than reusing its answer.
	Force bool `json:"force,omitempty"`
}

// FillSelect chooses a fill's messages by the state of their
// translation in each locale.
type FillSelect string

// Fill selections.
const (
	// SelectMissing: no usable translation (none, or rejected).
	SelectMissing FillSelect = "missing"
	// SelectOutdated: a usable translation made against an older source
	// revision.
	SelectOutdated          FillSelect = "outdated"
	SelectMissingOrOutdated FillSelect = "missing_or_outdated"
)

// Selection is the filter's effective select: as asked, or — for
// requests and fills without one — missing, both with include_outdated,
// and both for listed keys (which were always filled when missing or
// outdated).
func (f FillFilter) Selection() FillSelect {
	switch {
	case f.Select != "":
		return f.Select
	case f.IncludeOutdated || len(f.Keys) > 0:
		return SelectMissingOrOutdated
	}
	return SelectMissing
}

func (s FillSelect) missing() bool  { return s != SelectOutdated }
func (s FillSelect) outdated() bool { return s != SelectMissing }

// validate refuses an unknown select and one include_outdated
// contradicts.
func (f FillFilter) validate() error {
	switch f.Select {
	case "", SelectMissing, SelectOutdated, SelectMissingOrOutdated:
	default:
		return fmt.Errorf("%w: select must be missing, outdated or missing_or_outdated", ErrInvalidQuery)
	}
	if f.IncludeOutdated && f.Select != "" && f.Select != SelectMissingOrOutdated {
		return fmt.Errorf("%w: include_outdated means select missing_or_outdated; send one of them", ErrInvalidQuery)
	}
	// Force is for a handful of messages someone is looking at, never a
	// project-wide re-translation: that is a bill, not a request.
	if f.Force && len(f.Keys) == 0 {
		return fmt.Errorf("%w: force needs keys", ErrInvalidQuery)
	}
	return nil
}

// JobView is a job with its audit ledger.
type JobView struct {
	domain.Job
	Audit json.RawMessage
}

// JobFilter narrows a job listing.
type JobFilter struct {
	ProjectID *uuid.UUID
	State     *domain.JobState
	Locale    string
	FillID    *uuid.UUID
	MessageID *uuid.UUID
	// Projects, when not nil, limits the rows to these projects: a
	// project-scoped caller's (RFC 0006 §4.1). The use case sets it.
	Projects []uuid.UUID
}

// JobOutcome is how a claimed job ends.
type JobOutcome struct {
	JobID        uuid.UUID
	State        domain.JobState
	FailureCode  string
	LastError    string
	SuggestionID *uuid.UUID
	Audit        json.RawMessage
	At           time.Time
}

// SuggestionFilter narrows a suggestion listing.
type SuggestionFilter struct {
	ProjectID *uuid.UUID
	Status    *domain.SuggestionStatus
	Locale    string
	MessageID *uuid.UUID
	JobID     *uuid.UUID
	// Projects, when not nil, limits the rows to these projects: a
	// project-scoped caller's (RFC 0006 §4.1). The use case sets it.
	Projects []uuid.UUID
}

// QueueCursor is a review-queue keyset position.
type QueueCursor struct {
	Score float64
	Risk  int
	ID    uuid.UUID
}

// LocaleDecisions are people's decisions on one locale's suggestions.
// LocaleQueueAge is one locale's review queue: how deep it is and how
// long the items in it have been waiting (RFC 0005 §8). The queue
// itself already existed; its age is what a dashboard needs.
//
// The percentiles stand for something only while Waiting > 0. An empty
// queue has a measured depth of 0 and no median wait at all, and the
// two are different facts: "nobody waits" and "nobody is waiting".
type LocaleQueueAge struct {
	Locale  string
	Waiting int
	// P50, P90 and Oldest are how long the pending items have waited.
	P50, P90, Oldest time.Duration
}

type LocaleDecisions struct {
	Locale           string
	Accepted         int
	Edited           int
	Rejected         int
	MeanEditDistance float64
	MeanEditRatio    float64
}

// DisclosureRecord is a stored disclosure.
type DisclosureRecord struct {
	ID        uuid.UUID
	JobID     uuid.UUID
	ProjectID uuid.UUID
	MessageID uuid.UUID
	Locale    string
	Disclosure
	OccurredAt time.Time
}

// DisclosureFilter narrows a disclosure listing.
type DisclosureFilter struct {
	JobID     *uuid.UUID
	MessageID *uuid.UUID
	ProjectID *uuid.UUID
	Provider  string
	// Projects, when not nil, limits the rows to these projects: a
	// project-scoped caller's (RFC 0006 §4.1). The use case sets it.
	Projects []uuid.UUID
}

// Store is Intelligence's persistence in tenant scope. Row-level
// security, not these methods, keeps it inside the tenant.
type Store interface {
	// InsertProvider stores p; inserted is false if its ID exists.
	// ErrProviderNameTaken if another provider has its name.
	InsertProvider(ctx context.Context, p domain.ProviderConfig, sealedKey []byte) (inserted bool, err error)
	Provider(ctx context.Context, id uuid.UUID) (domain.ProviderConfig, error)
	LockProvider(ctx context.Context, id uuid.UUID) (StoredProvider, error)
	Providers(ctx context.Context, afterName string, limit int) ([]domain.ProviderConfig, error)
	AllProviders(ctx context.Context) ([]StoredProvider, error)
	UpdateProvider(ctx context.Context, p domain.ProviderConfig, sealedKey []byte, expected int) error
	DeleteProvider(ctx context.Context, id uuid.UUID) error

	// Settings returns the tenant's settings; found is false for a
	// tenant that saved none (the defaults apply).
	Settings(ctx context.Context, lock bool) (s domain.TenantSettings, found bool, err error)
	// SaveSettings inserts (expected 0) or updates at version expected.
	SaveSettings(ctx context.Context, s domain.TenantSettings, expected int) error
	ProjectSettings(ctx context.Context, project uuid.UUID) (domain.ProjectSettings, bool, error)
	SaveProjectSettings(ctx context.Context, s domain.ProjectSettings, expected int) error
	// RoutingPolicy returns the tenant's (project nil) or a project's.
	RoutingPolicy(ctx context.Context, project *uuid.UUID) (domain.RoutingRecord, bool, error)
	// RoutingPolicies lists every stored policy.
	RoutingPolicies(ctx context.Context) ([]domain.RoutingRecord, error)
	SaveRoutingPolicy(ctx context.Context, r domain.RoutingRecord, expected int) error
	DeleteRoutingPolicy(ctx context.Context, project uuid.UUID) error

	InsertSpend(ctx context.Context, e SpendEntry) error
	// SpendSince sums the ledger from since on.
	SpendSince(ctx context.Context, since time.Time) (domain.MicroUSD, int, error)
	SpendByProvider(ctx context.Context, since time.Time) ([]ProviderSpend, error)
	SpendEntries(ctx context.Context, since time.Time, before *Cursor, limit int) ([]SpendEntry, error)

	InsertFill(ctx context.Context, f Fill) error
	FinishFill(ctx context.Context, f Fill) error
	Fill(ctx context.Context, id uuid.UUID) (Fill, error)
	FillJobCounts(ctx context.Context, id uuid.UUID) (map[domain.JobState]int, error)

	// EnqueueJob inserts j unless a job with its idempotency key exists;
	// with requeue, such a job that failed, died or was cancelled is
	// queued again. It returns the stored job and whether it is new or
	// requeued.
	EnqueueJob(ctx context.Context, j domain.Job, requeue bool) (domain.Job, bool, error)
	// JobStates returns, by message ID, the state of each job that
	// exists with one of jobs' idempotency keys (jobs share one locale).
	JobStates(ctx context.Context, jobs []domain.Job) (map[uuid.UUID]domain.JobState, error)
	Job(ctx context.Context, id uuid.UUID) (JobView, error)
	Jobs(ctx context.Context, f JobFilter, before *Cursor, limit int) ([]domain.Job, error)
	CancelJob(ctx context.Context, id uuid.UUID, by string, at time.Time) (bool, error)
	CancelFillJobs(ctx context.Context, fill uuid.UUID, by string, at time.Time) (int, error)
	// LockClaimedJob locks a job the caller holds by its claim token
	// (domain.ErrLeaseLost otherwise).
	LockClaimedJob(ctx context.Context, id, token uuid.UUID) (domain.Job, error)
	FinishJob(ctx context.Context, o JobOutcome) error
	RetryJob(ctx context.Context, id uuid.UUID, at time.Time, o JobOutcome) error

	InsertSuggestion(ctx context.Context, r domain.SuggestionRecord) error
	SupersedePending(ctx context.Context, message uuid.UUID, locale string, keep uuid.UUID) error
	Suggestion(ctx context.Context, id uuid.UUID, lock bool) (domain.SuggestionRecord, error)
	// DecideSuggestion saves r's status and decision at version expected.
	DecideSuggestion(ctx context.Context, r domain.SuggestionRecord, expected int) error
	Suggestions(ctx context.Context, f SuggestionFilter, before *Cursor, limit int) ([]domain.SuggestionRecord, error)
	ReviewQueue(ctx context.Context, project uuid.UUID, locales []string, after *QueueCursor, limit int) ([]domain.SuggestionRecord, error)
	DecisionStats(ctx context.Context, project uuid.UUID, since time.Time) ([]LocaleDecisions, error)
	// QueueAges is, per locale, how many suggestions are still pending
	// and how long they have been waiting as of now.
	QueueAges(ctx context.Context, project uuid.UUID, locales []string, now time.Time) ([]LocaleQueueAge, error)

	InsertDisclosures(ctx context.Context, ds []DisclosureRecord) error
	Disclosures(ctx context.Context, f DisclosureFilter, before *Cursor, limit int) ([]DisclosureRecord, error)

	// DeleteProjectData erases a deleted project's jobs, fills,
	// suggestions, disclosures, settings and routing. Spend stays.
	DeleteProjectData(ctx context.Context, project uuid.UUID) error
}

// Claim is a job a worker leased.
type Claim struct {
	JobID       uuid.UUID
	TenantID    uuid.UUID
	Token       uuid.UUID
	Attempts    int
	MaxAttempts int
}

// Claimer leases jobs across tenants (system scope).
type Claimer interface {
	// Claim leases the next due job of a tenant under its concurrency
	// cap; ok is false when none is due.
	Claim(ctx context.Context, lease time.Duration) (c Claim, ok bool, err error)
	// QueueDepth counts queued and running jobs across tenants.
	QueueDepth(ctx context.Context) (map[domain.JobState]int, error)
}

// ProjectInfo is what Intelligence needs to know about a project.
type ProjectInfo struct {
	ID           uuid.UUID
	SourceLocale string
}

// SourceMessage is a Catalog message as Intelligence sees it.
type SourceMessage struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Key       string
	Namespace string
	// Active says the message is translated: live, or proposed by a
	// branch (RFC 0004 §4.1) — not obsolete.
	Active   bool
	Revision int
	Source   mf.Message
	// SourceText and SourceSyntax are the source as authored.
	SourceText   string
	SourceSyntax string
	Description  string
	MaxLength    *int
}

// MessageQuery narrows Catalog's message listing for a fill.
type MessageQuery struct {
	Namespace  string
	KeyPrefix  string
	MissingIn  string
	OutdatedIn string
}

// Catalog is Catalog's application service, as Intelligence reads it.
// Unknown projects answer ErrProjectNotFound, unknown messages
// ErrNotFound.
type Catalog interface {
	Project(ctx context.Context, id uuid.UUID) (ProjectInfo, error)
	MessageByID(ctx context.Context, project, id uuid.UUID) (SourceMessage, error)
	MessagesByKeys(ctx context.Context, project uuid.UUID, keys []string) ([]SourceMessage, error)
	// MessagesByIDs returns the project's messages among ids (active or
	// not) in one read; unknown IDs are left out.
	MessagesByIDs(ctx context.Context, project uuid.UUID, ids []uuid.UUID) ([]SourceMessage, error)
	// Messages lists active messages in key order after afterKey.
	Messages(ctx context.Context, project uuid.UUID, q MessageQuery, afterKey string, limit int) ([]SourceMessage, error)
}

// TranslationState is a message's translation in one locale.
type TranslationState struct {
	Exists         bool
	Revision       int
	SourceRevision int
	State          string
	// Text is the translation's canonical MF2 syntax.
	Text string
}

// Usage is where a message appears in the product's code (RFC 0004
// §2.2).
type Usage struct {
	File string
	Line int
	// Component and Route are empty where the collector didn't know them.
	Component string
	Route     string
}

// UsageContext is the Context context's application service, as
// Intelligence reads it: a message's current usages and the messages
// shown together with it (RFC 0004 §8). Unknown projects answer
// ErrProjectNotFound.
type UsageContext interface {
	// Usages returns up to limit of the message's current usages, the
	// default branch's first.
	Usages(ctx context.Context, project, message uuid.UUID, limit int) ([]Usage, error)
	// CoLocated returns up to limit messages that share a route or a
	// capture with message, most shared first.
	CoLocated(ctx context.Context, project, message uuid.UUID, limit int) ([]uuid.UUID, error)
}

// Neighbour is a message near another, with its translation.
type Neighbour struct {
	Key         string
	SourceMF2   string
	Translation string
}

// TranslationWrite is a suggestion becoming a translation revision.
type TranslationWrite struct {
	// Text is canonical MF2 syntax.
	Text           string
	Origin         domain.Origin
	OriginDetail   json.RawMessage
	SourceRevision int
	// State nil lets the project's review policy decide.
	State *string
}

// Localization is Localization's application service, as Intelligence
// reads and writes it.
type Localization interface {
	// Locales lists the project's target locales (not the source).
	Locales(ctx context.Context, project uuid.UUID) ([]string, error)
	Translation(ctx context.Context, project uuid.UUID, key, locale string) (TranslationState, error)
	// Neighbours lists up to limit messages whose keys share keyPrefix,
	// other than key, with their translations in locale.
	Neighbours(ctx context.Context, project uuid.UUID, keyPrefix, key, locale string, limit int) ([]Neighbour, error)
	// Write creates or revises the translation (If-Match on revision; 0
	// for a new one) and returns the new revision. The caller's principal
	// is checked (translations.write, review for approved).
	Write(ctx context.Context, project uuid.UUID, key, locale string, w TranslationWrite, ifMatch int) (int, error)
}

// Environments are Release's environments, as Intelligence reads their
// eligibility policies.
type Environments interface {
	// ShipsApproved reports whether the environment exists and its policy
	// ships approved translations.
	ShipsApproved(ctx context.Context, project uuid.UUID, environment string) (bool, error)
}

// Knowledge is the Knowledge context's read port, adapted: the agent's
// translation memory, termbase and style guides, plus what fingerprints
// and edit diffs need.
type Knowledge interface {
	domain.TranslationMemory
	domain.Termbase
	domain.StyleGuides
	// StyleSources names the guides ("<id>@<version>") the effective style
	// of project, locale and namespace merges.
	StyleSources(ctx context.Context, scope domain.Scope, locale, namespace string) ([]string, error)
	// TermbaseVersion identifies the termbase a pair's translations use:
	// a digest of every concept in scope (tenant-wide and the project's)
	// with a term in either locale, with its version.
	TermbaseVersion(ctx context.Context, scope domain.Scope, pair domain.LocalePair) (string, error)
	// Terms lists the texts of termbase terms recognized in a text of
	// locale (for an edit's structured diff).
	Terms(ctx context.Context, scope domain.Scope, locale, text string) ([]string, error)
}

// ProviderFactory builds a callable provider from a tenant's
// configuration and its opened key. Implementations wrap it for
// resilience and cap its concurrency.
type ProviderFactory interface {
	Provider(cfg domain.ProviderConfig, apiKey string) (domain.Provider, error)
}

// Sealer seals provider keys at rest.
type Sealer interface {
	Seal(plaintext []byte, context ...string) ([]byte, error)
	Open(sealed []byte, context ...string) ([]byte, error)
}

// Metrics records what the Intelligence context does (RFC 0003 §3.3,
// intent §70). Implementations must be safe for concurrent use.
type Metrics interface {
	JobFinished(trigger domain.Trigger, state domain.JobState, failure string, d time.Duration)
	Spent(tenant uuid.UUID, provider, model string, cost domain.MicroUSD)
	SuggestionCreated(locale string, origin domain.Origin, action domain.Action, score float64)
	SuggestionDecided(locale string, status domain.SuggestionStatus, editRatio float64)
	QueueDepth(depth map[domain.JobState]int)
}

// NoMetrics records nothing.
type NoMetrics struct{}

// JobFinished implements Metrics.
func (NoMetrics) JobFinished(domain.Trigger, domain.JobState, string, time.Duration) {}

// Spent implements Metrics.
func (NoMetrics) Spent(uuid.UUID, string, string, domain.MicroUSD) {}

// SuggestionCreated implements Metrics.
func (NoMetrics) SuggestionCreated(string, domain.Origin, domain.Action, float64) {}

// SuggestionDecided implements Metrics.
func (NoMetrics) SuggestionDecided(string, domain.SuggestionStatus, float64) {}

// QueueDepth implements Metrics.
func (NoMetrics) QueueDepth(map[domain.JobState]int) {}
