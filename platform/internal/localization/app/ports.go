// Package app is the Localization context's application layer: locales
// and fallback graphs, translation writes with structural QA and
// provenance, review, bulk import, the subscribers that keep its view of
// Catalog's messages current, and the ports other contexts read
// (translation coverage for the message list, the release snapshot).
package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// Errors. Adapters translate storage errors into the store ones.
var (
	ErrNotFound             = errors.New("localization: not found")
	ErrLocaleNotFound       = errors.New("localization: the project has no such locale")
	ErrStaleVersion         = errors.New("localization: version changed")
	ErrConcurrentWrite      = errors.New("localization: a concurrent write created this translation first")
	ErrPreconditionFailed   = errors.New("localization: resource changed since it was read")
	ErrPreconditionRequired = errors.New("localization: If-Match is required to change an existing resource")
	ErrLocaleInFallback     = errors.New("localization: the locale is part of the fallback graph")
	ErrTooManyItems         = errors.New("localization: a batch holds 1 to 500 items")
)

// Reviewers is Identity's read port as Localization uses it, for the
// author rule of review (RFC 0006 §15 Q6).
type Reviewers interface {
	// OthersCanReview reports whether any active member of the tenant
	// other than the principal excluding (as "person:<id>") holds
	// translations.review for locale in project: through a role whose
	// locale scope covers it, and a project scope that includes it.
	OthersCanReview(ctx context.Context, project uuid.UUID, locale bcp47.Tag, excluding string) (bool, error)
}

// ProjectInfo is what Localization needs to know about a Catalog project.
type ProjectInfo struct {
	ID             uuid.UUID
	SourceLocale   bcp47.Tag
	DefaultSyntax  mfcontent.Syntax
	ReviewRequired bool
}

// SourceMessage is a Catalog message as Localization sees it: identity,
// the snapshot fields its projection keeps, and the current source.
type SourceMessage struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Key       string
	Namespace string
	State     string
	Revision  int
	Version   int
	Content   mfcontent.Content
	MaxLength *int
}

// NamedTranslationRow is a translation row with its message's key and
// namespace from the projection ("" while unknown).
type NamedTranslationRow struct {
	TranslationRow
	Key       string
	Namespace string
}

// SourceCatalog is Catalog's application port, as Localization uses it.
// Every method answers ErrNotFound for an unknown project or message.
type SourceCatalog interface {
	Project(ctx context.Context, id uuid.UUID) (ProjectInfo, error)
	Message(ctx context.Context, project uuid.UUID, key string) (SourceMessage, error)
	// MessagesByKeys returns the existing messages among keys, by key.
	MessagesByKeys(ctx context.Context, project uuid.UUID, keys []string) (map[string]SourceMessage, error)
	// SourceAt returns revision n of a message's source.
	SourceAt(ctx context.Context, project, message uuid.UUID, n int) (mfcontent.Content, error)
	// SourcesAt is SourceAt for many revisions in one read: those of
	// refs that exist; a ref that names none is absent.
	SourcesAt(ctx context.Context, project uuid.UUID, refs []SourceRef) (map[SourceRef]mfcontent.Content, error)
}

// SourceRef names one revision of a message's source.
type SourceRef struct {
	Message  uuid.UUID
	Revision int
}

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
	// InCurrent runs fn in the tenant transaction ctx is already inside
	// (another context's unit of work), committing with it; it fails
	// when there is none.
	InCurrent(ctx context.Context, fn func(context.Context, Store) error) error
}

// MessageState is Localization's projection of one Catalog message.
type MessageState struct {
	MessageID      uuid.UUID
	ProjectID      uuid.UUID
	Key            string
	Namespace      string
	State          string
	SourceRevision int
	Version        int
	UpdatedAt      time.Time
}

// TranslationRow is a stored translation with the message's current
// source revision from the projection (0 if unknown).
type TranslationRow struct {
	domain.Translation
	CurrentSourceRevision int
}

// CoverageFilter narrows a coverage query.
type CoverageFilter struct {
	Locale    string
	Outdated  bool // false: missing
	Namespace *string
	State     *string
	KeyPrefix string
	AfterKey  string
	Limit     int
}

// Graph is a stored fallback graph; Version 0 means none is stored.
type Graph struct {
	Edges   map[string][]string
	Version int
}

// Store is Localization's persistence in tenant scope.
type Store interface {
	// InsertLocale adds l; inserted is false if it already existed.
	InsertLocale(ctx context.Context, l domain.Locale, by string) (inserted bool, err error)
	Locale(ctx context.Context, project uuid.UUID, code bcp47.Tag) (domain.Locale, error)
	Locales(ctx context.Context, project uuid.UUID, after string, limit int) ([]domain.Locale, error)
	AllLocales(ctx context.Context, project uuid.UUID) ([]domain.Locale, error)
	// DeleteLocale removes a non-source locale (ErrNotFound otherwise).
	DeleteLocale(ctx context.Context, project uuid.UUID, code bcp47.Tag) error

	FallbackGraph(ctx context.Context, project uuid.UUID, lock bool) (Graph, error)
	// SaveFallbackGraph inserts (expected 0) or updates (expected = the
	// stored version) the graph at version.
	SaveFallbackGraph(ctx context.Context, project uuid.UUID, edges map[string][]string, version, expected int, by string, at time.Time) error

	LockMessageState(ctx context.Context, id uuid.UUID) (MessageState, bool, error)
	// SaveMessageState stores s unless a higher version is stored.
	SaveMessageState(ctx context.Context, s MessageState) error
	MessagesWithCoverage(ctx context.Context, project uuid.UUID, f CoverageFilter) ([]uuid.UUID, error)
	// CurrentTranslationCounts counts, per locale, the usable translations
	// of ids that are not outdated.
	CurrentTranslationCounts(ctx context.Context, project uuid.UUID, ids []uuid.UUID) (map[string]int, error)

	Translation(ctx context.Context, message uuid.UUID, locale bcp47.Tag) (TranslationRow, error)
	// TranslationByID returns a translation with its message's key and
	// namespace from the projection ("" while unknown).
	TranslationByID(ctx context.Context, id domain.TranslationID) (row TranslationRow, key, namespace string, err error)
	// TranslationsByID is TranslationByID for several translations of
	// project, in one query, by ID; an ID that is no translation of the
	// project is absent.
	TranslationsByID(ctx context.Context, project uuid.UUID, ids []domain.TranslationID) (map[domain.TranslationID]NamedTranslationRow, error)
	LockTranslation(ctx context.Context, message uuid.UUID, locale bcp47.Tag) (domain.Translation, bool, error)
	InsertTranslation(ctx context.Context, t domain.Translation) error
	// UpdateTranslation saves t if the stored revision is expected.
	UpdateTranslation(ctx context.Context, t domain.Translation, expected int) error
	AppendRevision(ctx context.Context, r domain.Revision) error
	// TranslationsOfMessage lists a message's translations by locale;
	// only, when not nil, limits them to those locales (an assigned
	// member's units of the message).
	TranslationsOfMessage(ctx context.Context, message uuid.UUID, after string, only []string, limit int) ([]TranslationRow, error)
	Revisions(ctx context.Context, translation domain.TranslationID, before, limit int) ([]domain.Revision, error)
	// NewlyOutdated lists a message's translations made against a
	// revision in [old, new).
	NewlyOutdated(ctx context.Context, message uuid.UUID, old, new int) ([]domain.Translation, error)
	// SnapshotTranslations reads a project's translations in states;
	// includeObsolete false leaves out those of messages this projection
	// knows to be obsolete.
	SnapshotTranslations(ctx context.Context, project uuid.UUID, states []domain.ReviewState, includeObsolete bool) ([]TranslationRow, error)
	// ProjectTranslations lists translations across a project's messages
	// in (key, message ID, locale) order after q.After, in one query.
	ProjectTranslations(ctx context.Context, project uuid.UUID, q ProjectTranslationQuery) ([]ProjectTranslationRow, error)
	// TranslationStats counts a project's active messages and, per
	// locale it has, the translations of them, in one query.
	TranslationStats(ctx context.Context, project uuid.UUID) (StoredStats, error)
	// LeadTimeSamples are recent source changes with the moment a
	// translation in one of q.States caught up with them, newest source
	// change first and bounded by q.Limit.
	LeadTimeSamples(ctx context.Context, project uuid.UUID, q LeadTimeQuery) ([]LeadTimeSample, error)

	DeleteProjectData(ctx context.Context, project uuid.UUID) error

	Publish(ctx context.Context, e outbox.Event) error

	// The bulk forms of the projection and translation writes, for
	// Catalog's bulk upsert (ProjectMessages) and bulk imports: one
	// statement per call however many rows (#77).

	// LockMessageStates locks the stored snapshots among ids, in key
	// order, and returns them by message.
	LockMessageStates(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]MessageState, error)
	// SaveMessageStates stores each snapshot unless a higher version is
	// stored; the messages must be distinct.
	SaveMessageStates(ctx context.Context, ms []MessageState) error
	// NewlyOutdatedFor is NewlyOutdated for several messages at once,
	// in the order of rs, then by locale.
	NewlyOutdatedFor(ctx context.Context, rs []RevisionRange) ([]domain.Translation, error)
	// LockTranslations locks the stored translations among slots and
	// returns them by slot.
	LockTranslations(ctx context.Context, slots []TranslationSlot) (map[TranslationSlot]domain.Translation, error)
	InsertTranslations(ctx context.Context, ts []domain.Translation) error
	// UpdateTranslations saves each translation if its stored revision
	// is still the expected one, and fails the call (ErrStaleVersion)
	// otherwise.
	UpdateTranslations(ctx context.Context, ts []TranslationUpdate) error
	AppendRevisions(ctx context.Context, rs []domain.Revision) error
	// PublishAll records events in order.
	PublishAll(ctx context.Context, es []outbox.Event) error
}

// RevisionRange asks for a message's translations made against a
// source revision in [Old, New).
type RevisionRange struct {
	MessageID uuid.UUID
	Old, New  int
}

// TranslationSlot is where a translation lives: a message and a locale.
type TranslationSlot struct {
	MessageID uuid.UUID
	Locale    bcp47.Tag
}

// TranslationUpdate is a translation for Store.UpdateTranslations, with
// the revision it must still be stored at.
type TranslationUpdate struct {
	Translation domain.Translation
	Expected    int
}
