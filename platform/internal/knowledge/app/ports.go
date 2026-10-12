// Package app is the Knowledge context's application layer: translation
// memory derived from approved translations (and its lookup and
// concordance), the termbase with recognition and terminology QA, and
// style guides with their effective view. Reader is the narrow port the
// Intelligence context consumes.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// Errors. Adapters translate storage errors into the store ones.
var (
	ErrNotFound             = errors.New("knowledge: not found")
	ErrProjectNotFound      = errors.New("knowledge: no such project")
	ErrStaleVersion         = errors.New("knowledge: version changed")
	ErrPreconditionFailed   = errors.New("knowledge: resource changed since it was read")
	ErrPreconditionRequired = errors.New("knowledge: If-Match is required to change this resource")
	ErrIdempotencyReuse     = errors.New("knowledge: Idempotency-Key reused for a different request")
	ErrStyleGuideExists     = errors.New("knowledge: a style guide exists for this scope")
	ErrInvalidQuery         = errors.New("knowledge: invalid query")
)

// ProjectInfo is what Knowledge needs to know about a Catalog project.
type ProjectInfo struct {
	ID           uuid.UUID
	SourceLocale bcp47.Tag
}

// Projects is Catalog's application port, as Knowledge uses it.
type Projects interface {
	// Project answers ErrProjectNotFound for an unknown project.
	Project(ctx context.Context, id uuid.UUID) (ProjectInfo, error)
}

// SourceMessage is a Catalog message as Knowledge reads it to match a
// unit: its identity and its current source.
type SourceMessage struct {
	ID           uuid.UUID
	Key          string
	Namespace    string
	SourceLocale bcp47.Tag
	Source       mf.Message
	// SourceSyntax is the syntax the source was authored in: the default
	// syntax of the targets offered for it.
	SourceSyntax mfcontent.Syntax
}

// Messages is Catalog's application port for one message, as Knowledge
// uses it to look up memory for a unit. Message answers ErrNotFound for
// a key no message has — and, for an `assigned` member, for a message
// none of their units is in (RFC 0006 §3.3).
type Messages interface {
	Message(ctx context.Context, project uuid.UUID, key string) (SourceMessage, error)
}

// CurrentTranslation is a translation's latest state as Localization
// reports it: its text and source (domain.ApprovedText) and whether it
// is approved.
type CurrentTranslation struct {
	domain.ApprovedText
	Approved bool
}

// Translations is Localization's application port, as Knowledge uses
// it to derive translation memory.
type Translations interface {
	// Current answers ErrNotFound for a translation that no longer
	// exists (its project was deleted).
	Current(ctx context.Context, project, translation uuid.UUID) (CurrentTranslation, error)
	// Currents is Current for several translations of one project in a
	// constant number of reads, whatever their number: by translation ID,
	// those that exist. The rest (deleted with their project) are absent,
	// where Current answers ErrNotFound.
	Currents(ctx context.Context, project uuid.UUID, translations []uuid.UUID) (map[uuid.UUID]CurrentTranslation, error)
	// ProjectTranslations reads a page of a project's translations of
	// active messages with their messages' current source: one read of
	// Localization and one of Catalog, whatever the page size.
	ProjectTranslations(ctx context.Context, project uuid.UUID, q TranslationPageQuery) ([]ProjectTranslation, *string, error)
}

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// UnitFilter narrows a TM unit listing.
type UnitFilter struct {
	SourceLocale  *bcp47.Tag
	TargetLocale  *bcp47.Tag
	ProjectID     *uuid.UUID
	TranslationID *uuid.UUID
	// State is active, retired or all.
	State string
	// Projects, when not nil, keeps project-owned rows to these
	// projects: a project-scoped caller's (RFC 0006 §4.1). The use case
	// sets it.
	Projects []uuid.UUID
}

// MatchScope is where TM lookups look.
type MatchScope struct {
	// ProjectID is the project the text belongs to (nil: tenant-level).
	ProjectID *uuid.UUID
	// AllProjects reaches every project's units; otherwise only
	// tenant-wide units and ProjectID's.
	AllProjects bool
}

// ScoredUnit is a unit with its trigram similarity to a query.
type ScoredUnit struct {
	domain.TMUnit
	Similarity float64
}

// ConcordanceFilter is a concordance search in the store.
type ConcordanceFilter struct {
	MatchScope
	Side         domain.Side
	Query        string
	SourceLocale *bcp47.Tag
	TargetLocale *bcp47.Tag
	Limit        int
}

// ConceptFilter narrows a concept listing.
type ConceptFilter struct {
	// ProjectID lists that project's concepts and the tenant-wide ones.
	ProjectID *uuid.UUID
	Domain    *string
	Locale    *bcp47.Tag
	// Query searches term texts and definitions (substring).
	Query *string
	// Projects, when not nil, keeps project-owned rows to these
	// projects: a project-scoped caller's (RFC 0006 §4.1). The use case
	// sets it.
	Projects []uuid.UUID
}

// RevisionAction says what a history entry records.
type RevisionAction string

// Revision actions.
const (
	ActionCreated RevisionAction = "created"
	ActionUpdated RevisionAction = "updated"
	ActionDeleted RevisionAction = "deleted"
)

// ConceptRevision is one entry of a concept's history: a full snapshot.
type ConceptRevision struct {
	Concept   domain.Concept
	Action    RevisionAction
	Author    string
	CreatedAt time.Time
}

// StyleFilter narrows a style guide listing.
type StyleFilter struct {
	TenantOnly bool
	ProjectID  *uuid.UUID
	Locale     *bcp47.Tag
	// Projects, when not nil, keeps project-owned rows to these
	// projects: a project-scoped caller's (RFC 0006 §4.1). The use case
	// sets it.
	Projects []uuid.UUID
}

// StyleGuideVersion is one entry of a guide's history: a full snapshot.
type StyleGuideVersion struct {
	Guide     domain.StyleGuide
	Action    RevisionAction
	Author    string
	CreatedAt time.Time
}

// Store is Knowledge's persistence in tenant scope.
type Store interface {
	// EnsureDerivation creates a translation's derivation record (at
	// revision 0) unless it exists; LockDerivation then locks it and
	// returns the revision its units reflect.
	EnsureDerivation(ctx context.Context, translation, project uuid.UUID, at time.Time) error
	LockDerivation(ctx context.Context, translation uuid.UUID) (int, error)
	SetDerivation(ctx context.Context, translation uuid.UUID, revision int, at time.Time) error
	// LockActiveUnit returns the translation's active unit, nil if none.
	LockActiveUnit(ctx context.Context, translation uuid.UUID) (*domain.TMUnit, error)
	InsertUnit(ctx context.Context, u domain.TMUnit) error
	// HasActiveUnit reports whether an active unit in u's scope and
	// locale pair has exactly u's source and target.
	HasActiveUnit(ctx context.Context, u domain.TMUnit) (bool, error)
	TouchUnit(ctx context.Context, id uuid.UUID, revision int, at time.Time) error
	// RetireUnit stores u's retirement (ErrNotFound if already retired).
	RetireUnit(ctx context.Context, u domain.TMUnit) error
	Unit(ctx context.Context, id uuid.UUID) (domain.TMUnit, error)
	LockUnit(ctx context.Context, id uuid.UUID) (domain.TMUnit, error)
	Units(ctx context.Context, f UnitFilter, after uuid.UUID, limit int) ([]domain.TMUnit, error)
	ExactMatches(ctx context.Context, s MatchScope, source, target bcp47.Tag, n domain.Normalized, limit int) ([]domain.TMUnit, error)
	// FuzzyMatches returns active units whose normalized source is at
	// least minSimilarity similar to text (pg_trgm), most similar first.
	FuzzyMatches(ctx context.Context, s MatchScope, source, target bcp47.Tag, text string, minSimilarity float64, limit int) ([]ScoredUnit, error)
	Concordance(ctx context.Context, f ConcordanceFilter) ([]ScoredUnit, error)
	CountHits(ctx context.Context, ids []uuid.UUID, at time.Time) error

	// InsertConcept stores c with its terms; inserted is false if a
	// concept with its ID exists.
	InsertConcept(ctx context.Context, c domain.Concept) (inserted bool, err error)
	// UpdateConcept stores c (terms replaced) if the stored version is
	// expected.
	UpdateConcept(ctx context.Context, c domain.Concept, expected int) error
	DeleteConcept(ctx context.Context, id uuid.UUID) error
	Concept(ctx context.Context, id uuid.UUID) (domain.Concept, error)
	LockConcept(ctx context.Context, id uuid.UUID) (domain.Concept, error)
	Concepts(ctx context.Context, f ConceptFilter, after uuid.UUID, limit int) ([]domain.Concept, error)
	// ConceptsWithTermsIn returns the concepts in scope (tenant-wide and
	// project's) with a term in any of locales, with all their terms.
	ConceptsWithTermsIn(ctx context.Context, project *uuid.UUID, locales []bcp47.Tag) ([]domain.Concept, error)
	AppendConceptRevision(ctx context.Context, r ConceptRevision) error
	ConceptRevisions(ctx context.Context, id uuid.UUID, before, limit int) ([]ConceptRevision, error)

	// InsertStyleGuide stores g; inserted is false if a guide with its ID
	// exists; ErrStyleGuideExists if another guide has its scope.
	InsertStyleGuide(ctx context.Context, g domain.StyleGuide) (inserted bool, err error)
	UpdateStyleGuide(ctx context.Context, g domain.StyleGuide, expected int) error
	DeleteStyleGuide(ctx context.Context, id uuid.UUID) error
	StyleGuide(ctx context.Context, id uuid.UUID) (domain.StyleGuide, error)
	LockStyleGuide(ctx context.Context, id uuid.UUID) (domain.StyleGuide, error)
	StyleGuides(ctx context.Context, f StyleFilter, after uuid.UUID, limit int) ([]domain.StyleGuide, error)
	// StyleGuidesInScope returns the tenant-wide guides and project's.
	StyleGuidesInScope(ctx context.Context, project *uuid.UUID) ([]domain.StyleGuide, error)
	AppendStyleGuideVersion(ctx context.Context, v StyleGuideVersion) error
	StyleGuideVersions(ctx context.Context, id uuid.UUID, before, limit int) ([]StyleGuideVersion, error)

	// DeleteProjectData erases everything Knowledge holds for a project,
	// history included.
	DeleteProjectData(ctx context.Context, project uuid.UUID) error

	Publish(ctx context.Context, e outbox.Event) error
}
