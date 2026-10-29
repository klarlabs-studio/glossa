package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Errors. Adapters translate storage and Catalog errors into these.
var (
	// ErrNotFound is anything Quality holds that isn't there.
	ErrNotFound = errors.New("quality: not found")
	// ErrProjectNotFound is a project Catalog doesn't know.
	ErrProjectNotFound = errors.New("quality: no such project")
	// ErrCheckRunNotFound is a run that isn't this project's.
	ErrCheckRunNotFound = errors.New("quality: no such check run in the project")
	// ErrWaiverNotFound is a waiver that isn't this project's.
	ErrWaiverNotFound = errors.New("quality: no such waiver in the project")
	// ErrCaptureNotFound is a capture the path cannot name. Quality
	// holds no captures — Context does — so this is only ever a
	// malformed ID: a capture with no findings reads as an empty list,
	// which is the true answer.
	ErrCaptureNotFound = errors.New("quality: no such capture")
	// ErrInvalidQuery means a list's filter is malformed.
	ErrInvalidQuery = errors.New("quality: invalid query")
	// ErrPreGradedFinding is a finding handed in at severity `waived`.
	// A run records what its layers found; whether a finding is waived
	// is decided from the project's waivers, here and again on read
	// (RFC 0005 §2.3), so a caller may not assert it.
	ErrPreGradedFinding = errors.New("quality: a recorded finding carries the severity its layer emitted, not `waived`")
	// ErrTooManyFindings is a run past domain.MaxRunFindings.
	ErrTooManyFindings = errors.New("quality: too many findings in one run")
	// ErrInvalidFinding is a reported finding the ingest cannot seal: a
	// layer that is not one, a severity no policy can rank, an empty
	// code or an empty explanation. It is refused rather than stored
	// half-formed, because a finding with no identity is a finding
	// nobody can waive.
	ErrInvalidFinding = errors.New("quality: a reported finding is malformed")
	// ErrUnclaimableTrigger is a reported run claiming a trigger only
	// the server's own jobs use. `capture` is the capture upload's
	// visual pass and `write` the write-time catalog check; a caller
	// that could claim one could put words in a job's mouth.
	ErrUnclaimableTrigger = errors.New("quality: this trigger is a server job's, not a reported run's")
	// ErrPolicyVersionNotFound is a policy version this project never
	// had.
	ErrPolicyVersionNotFound = errors.New("quality: no such check-policy version in the project")
	// ErrPolicyConflict is a policy write that lost a race: the project
	// moved between reading the document and replacing it, so saving
	// would drop whatever the other writer said. The caller reads the
	// policy again and decides.
	ErrPolicyConflict = errors.New("quality: the project's check policy changed while this write was being prepared")
)

// MaxRunFindings bounds one run's findings (RFC 0005 §10).
const MaxRunFindings = 10000

// Catalog is Catalog's application port, as Quality uses it: only
// whether a project exists, so an unknown one is a 404 and not an empty
// list. Quality keys its rows by Catalog's project, message and capture
// IDs and learns about them through ports (RFC 0002 §4).
type Catalog interface {
	// Project answers ErrProjectNotFound for an unknown project.
	Project(ctx context.Context, project uuid.UUID) error
	// MessageIDs resolves message keys to the catalog's message IDs, for
	// the keys the catalog knows. A key it does not know is simply
	// absent from the map, and the finding's identity then falls back to
	// the key, exactly as an offline check's does.
	//
	// It is what makes a reported finding's fingerprint the same
	// fingerprint every other surface computes (RFC 0005 §2.1): a
	// reporter has the key, the server has the catalog, and a print over
	// a key is not a print over an ID.
	MessageIDs(ctx context.Context, project uuid.UUID, keys []string) (map[string]uuid.UUID, error)
	// CheckPolicy reads the project's stored check-policy document. A
	// project that has never saved one reads as the zero policy, which
	// is checkpolicy's documented default.
	CheckPolicy(ctx context.Context, project uuid.UUID) (StoredPolicy, error)
	// SaveCheckPolicy replaces the stored document, if the project is
	// still at the version the read saw (ErrPolicyConflict otherwise).
	SaveCheckPolicy(ctx context.Context, project uuid.UUID, ifMatch int, p checkpolicy.Policy) error
	// OpenPullRequests maps the names of the project's open branches to
	// the pull requests they belong to, for branches that have one. It
	// is what turns "these refs would newly fail" into the number
	// RFC 0005 §4.3 asks the impact preview to show: how many people
	// would wake up to a red pull request they did not cause.
	OpenPullRequests(ctx context.Context, project uuid.UUID) (map[string]int, error)
}

// PullRequestLinks says where a project's pull requests are on the web,
// so the impact preview can link to the one that would newly fail
// (RFC 0005 §4.3) instead of printing a number to look up. Integration
// answers it from the repository connected to the project.
type PullRequestLinks interface {
	// PullRequestURLs returns the web address of each of numbers. A
	// number it cannot place is absent from the map; an error is a
	// failure to look, and an authz denial means the caller may not
	// read the integration.
	PullRequestURLs(ctx context.Context, project uuid.UUID, numbers []int) (map[int]string, error)
}

// StoredPolicy is the project's check policy as Catalog holds it.
type StoredPolicy struct {
	// Policy is the document that grades, history and all.
	Policy checkpolicy.Policy
	// ProjectVersion is the project row's version when the document was
	// read. A save hands it back, so a write that lost a race is refused
	// rather than silently overwriting the winner.
	ProjectVersion int
}

// Metrics records Quality in the deployment's metrics (RFC 0005 §11).
type Metrics interface {
	// FindingRecorded counts one finding of a run, by layer, code and
	// the severity it was stored with.
	FindingRecorded(layer domain.Layer, code string, severity domain.Severity)
	// CheckRunRecorded counts a run by what asked for it and what it
	// concluded.
	CheckRunRecorded(trigger domain.Trigger, conclusion domain.Conclusion)
	// LayerChecked observes how long one layer took, so a slow layer is
	// visible before it is unbearable (RFC 0005 §11).
	LayerChecked(layer domain.Layer, d time.Duration)
	// WaiverDecided counts a waiver by what became of the request:
	// created, updated, revoked, refused or expired.
	WaiverDecided(outcome string)
	// VisualProbeRecorded counts one visual probe finding a capture
	// upload handed in, by its code and what the ingest made of it.
	VisualProbeRecorded(code, outcome string)
	// PolicyVersionRead publishes the version of a project's stored
	// check policy, so a dashboard can see which document is grading.
	PolicyVersionRead(project uuid.UUID, version int)
}

// Waiver outcomes, as Metrics counts them.
const (
	WaiverCreated = "created"
	WaiverUpdated = "updated"
	WaiverRevoked = "revoked"
	WaiverRefused = "refused"
	// WaiverExpired is the daily sweep recording an expiry. It is not a
	// revocation: nobody decided anything today, a date passed.
	WaiverExpired = "expired"
)

// Visual probe outcomes, as Metrics counts them (RFC 0005 §5.2).
const (
	// ProbeFirstSighting is a finding stored on its first sighting: a
	// warning, not yet eligible for error.
	ProbeFirstSighting = "first_sighting"
	// ProbeConfirmed is a finding the two-sighting rule confirmed, which
	// is what makes it eligible for error.
	ProbeConfirmed = "confirmed"
	// ProbeDropped is a finding the project's policy switched off, so
	// nothing was stored for it.
	ProbeDropped = "dropped"
)

// NoMetrics records nothing.
type NoMetrics struct{}

// FindingRecorded implements Metrics.
func (NoMetrics) FindingRecorded(domain.Layer, string, domain.Severity) {}

// CheckRunRecorded implements Metrics.
func (NoMetrics) CheckRunRecorded(domain.Trigger, domain.Conclusion) {}

// LayerChecked implements Metrics.
func (NoMetrics) LayerChecked(domain.Layer, time.Duration) {}

// WaiverDecided implements Metrics.
func (NoMetrics) WaiverDecided(string) {}

// VisualProbeRecorded implements Metrics.
func (NoMetrics) VisualProbeRecorded(string, string) {}

// PolicyVersionRead implements Metrics.
func (NoMetrics) PolicyVersionRead(uuid.UUID, int) {}

// RunFilter narrows the check runs a list or a latest-run lookup sees.
// Empty members don't filter.
type RunFilter struct {
	// Ref is the branch or environment the run was of.
	Ref string
	// Commit is the commit it graded.
	Commit string
	// Conclusion is the verdict it reached; a run still in flight has
	// none and so matches no conclusion.
	Conclusion string
	// Trigger is what asked for it.
	Trigger string
}

// RunCursor is where a page of check runs (newest first) continues.
type RunCursor struct {
	StartedAt time.Time
	ID        uuid.UUID
}

// FindingFilter narrows a run's findings. Empty members don't filter;
// Waived is tri-state (nil: both).
type FindingFilter struct {
	Layer     string
	Severity  string
	Code      string
	Locale    string
	Namespace string
	// Key is the message key the finding is about, exactly.
	Key    string
	Waived *bool
}

// FindingRecord is a stored finding as it stands now: the finding the
// layer emitted, with the waiver that accepts it today applied.
type FindingRecord struct {
	domain.Finding
	// Waived says whether a live waiver covers it. The embedded
	// Finding's Severity and Waiver are already set from it.
	Waived bool
	// SortKey is the row's place in the run's stable order, and the page
	// cursor.
	SortKey string
}

// WaiverFilter narrows a project's waivers. The layer, code and key are
// the finding the waiver accepts, which a waiver names only by
// fingerprint: they are matched against the most recent finding
// carrying it. Active is tri-state (nil: both).
type WaiverFilter struct {
	Fingerprint string
	Layer       string
	Code        string
	Key         string
	Active      *bool
}

// WaiverCursor is where a page of waivers (newest first) continues.
type WaiverCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// FindingSummary is what the most recent stored finding carrying a
// fingerprint says about it. Its zero value means no stored finding
// carries that fingerprint any more.
type FindingSummary struct {
	Layer       string
	Code        string
	Locale      string
	Key         string
	Namespace   string
	Explanation string
	// SourceRevision is the source revision that finding was computed
	// against: what a waiver is made against when the caller names none.
	SourceRevision int
}

// WaiverRecord is a stored waiver with what it accepts. Accepts is zero
// where no stored finding carries the fingerprint any more — an
// unexamined waiver, which is exactly what a dashboard should show
// (RFC 0005 §2.3).
type WaiverRecord struct {
	domain.Waiver
	Accepts FindingSummary
	// Active says whether the waiver stands: neither revoked nor
	// expired, as of the read that produced the record.
	Active bool
}

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// Store is Quality's persistence port. Findings are immutable: they are
// inserted with their run and never updated (migration 0027).
type Store interface {
	// InsertCheckRun stores a run and its verdict.
	InsertCheckRun(ctx context.Context, r domain.CheckRun) error
	// InsertFindings stores a run's findings, at the severity their
	// layers emitted.
	InsertFindings(ctx context.Context, run, project uuid.UUID, fs []domain.Finding) error
	// CheckRun reads one of the project's runs (ErrCheckRunNotFound).
	CheckRun(ctx context.Context, project, id uuid.UUID) (domain.CheckRun, error)
	// LatestCheckRun is the newest run matching f (ErrCheckRunNotFound).
	LatestCheckRun(ctx context.Context, project uuid.UUID, f RunFilter) (domain.CheckRun, error)
	// ListCheckRuns pages a project's runs, newest first.
	ListCheckRuns(ctx context.Context, project uuid.UUID, f RunFilter, after *RunCursor, limit int) ([]domain.CheckRun, error)
	// HasCheckRunOf reports whether the project has any run by one of
	// triggers: an existence check, never a read of the rows.
	HasCheckRunOf(ctx context.Context, project uuid.UUID, triggers []domain.Trigger) (bool, error)
	// ListFindings pages a run's findings, graded against the waivers
	// live at now and ordered stably.
	ListFindings(ctx context.Context, run domain.CheckRun, f FindingFilter, after string, limit int, now time.Time) ([]FindingRecord, error)
	// ListCaptureFindings pages the findings on one capture — all of
	// them, or one region's — across runs, graded against the waivers
	// live at now and ordered stably.
	ListCaptureFindings(
		ctx context.Context, project, capture uuid.UUID, region, after string, limit int, now time.Time,
	) ([]FindingRecord, error)
	// CaptureFingerprints are the distinct fingerprints the findings
	// stored against one capture carry: what the previous capture of a
	// scope saw, which is the state the two-sighting rule needs
	// (RFC 0005 §5.2).
	CaptureFingerprints(ctx context.Context, project, capture uuid.UUID) ([]string, error)
	// CountFindings sums a run's findings as they stand at now.
	CountFindings(ctx context.Context, run domain.CheckRun, now time.Time) (domain.Counts, error)
	// CountFindingsByLayer sums a run's findings per locale and layer as
	// they stand at now, in one pass. A layer that reported nothing is
	// not in the result; CheckRun.Layers is what says which layers ran
	// at all.
	CountFindingsByLayer(ctx context.Context, run domain.CheckRun, now time.Time) ([]domain.LocaleLayerCount, error)
	// RollUpFindingsByDay restates one UTC day of the project's
	// findings-by-layer rollup from the findings themselves (migration
	// 0035). It is a recomputation, so calling it twice for the same day
	// is the same as calling it once.
	RollUpFindingsByDay(ctx context.Context, project uuid.UUID, day time.Time) error
	// FindingsByDay reads the rollup between two UTC days, inclusive,
	// oldest first. A day with no rows was never checked.
	FindingsByDay(ctx context.Context, project uuid.UUID, from, to time.Time) ([]domain.DailyFindings, error)
	// LatestFinding is the most recent stored finding carrying a
	// fingerprint; found is false where none does.
	LatestFinding(ctx context.Context, project uuid.UUID, fingerprint string) (summary FindingSummary, found bool, err error)
	// UpsertWaiver stores a waiver, or restates the project's live one
	// for the same fingerprint and reach; created says which happened.
	UpsertWaiver(ctx context.Context, w domain.Waiver) (stored domain.Waiver, created bool, err error)
	// Waiver reads one of the project's waivers (ErrWaiverNotFound).
	Waiver(ctx context.Context, project, id uuid.UUID) (domain.Waiver, error)
	// ListWaivers pages a project's waivers, newest first.
	ListWaivers(ctx context.Context, project uuid.UUID, f WaiverFilter, after *WaiverCursor, limit int, now time.Time) ([]WaiverRecord, error)
	// RevokeWaiver takes a waiver back; revoking an already revoked one
	// changes nothing and is not an error.
	RevokeWaiver(ctx context.Context, project, id uuid.UUID, at time.Time) error
	// LiveWaivers are the project's waivers that stand at now.
	LiveWaivers(ctx context.Context, project uuid.UUID, now time.Time) ([]domain.Waiver, error)
	// ExpireWaivers records the expiry of every waiver of the tenant
	// whose date has passed and that nobody has revoked or recorded
	// yet, and answers how many. It records; it never deletes.
	ExpireWaivers(ctx context.Context, now time.Time) (int, error)
	// DeleteExpiredCheckRuns deletes up to limit of the tenant's check
	// runs that started before cutoff — findings and all — except the
	// newest run of each ref, which is never swept whatever its age.
	DeleteExpiredCheckRuns(ctx context.Context, cutoff time.Time, limit int) (int, error)
	// InsertPolicyVersion appends one saved policy version to the
	// project's history; stored is false where that version is already
	// recorded, which makes a repeated save idempotent rather than an
	// error.
	InsertPolicyVersion(ctx context.Context, v PolicyVersion) (stored bool, err error)
	// PolicyVersion reads one version of the project's policy
	// (ErrPolicyVersionNotFound).
	PolicyVersion(ctx context.Context, project uuid.UUID, version int) (PolicyVersion, error)
	// ListPolicyVersions pages the project's policy versions, newest
	// first, continuing below after.
	ListPolicyVersions(ctx context.Context, project uuid.UUID, after *int, limit int) ([]PolicyVersion, error)
	// InsertLinguisticJob stores a new linguistic-QA job (migration
	// 0038).
	InsertLinguisticJob(ctx context.Context, j domain.LinguisticJob) error
	// LinguisticJob reads one of the project's jobs
	// (ErrLinguisticJobNotFound).
	LinguisticJob(ctx context.Context, project, id uuid.UUID) (domain.LinguisticJob, error)
	// ListLinguisticJobs pages a project's jobs, newest first, narrowed
	// by state where one is given.
	ListLinguisticJobs(
		ctx context.Context, project uuid.UUID, state string, after *LinguisticCursor, limit int,
	) ([]domain.LinguisticJob, error)
	// UpdateLinguisticJob writes a job's progress; moved is false where
	// the job had already finished, which is what settles a job exactly
	// once when two readers reconcile it at the same moment.
	UpdateLinguisticJob(ctx context.Context, j domain.LinguisticJob) (moved bool, err error)

	// Publish records a domain event in the same transaction as the
	// state change that raised it.
	Publish(ctx context.Context, e outbox.Event) error
}

// LinguisticCursor is where a page of linguistic-QA jobs (newest
// first) continues.
type LinguisticCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// PolicyVersion is one saved version of a project's check policy: the
// document as it was stored, who wrote it and when (RFC 0005 §4.3).
//
// The document is the version itself, without the version behind it:
// the history is the sequence of these rows, not a chain inside one of
// them.
type PolicyVersion struct {
	ID      uuid.UUID
	Project uuid.UUID
	Version int
	Policy  checkpolicy.Policy
	// CreatedBy is the principal that saved it, and CreatedAt is when.
	// A policy is an organizational decision about a project, so who
	// took it is part of the record.
	CreatedBy string
	CreatedAt time.Time
}
