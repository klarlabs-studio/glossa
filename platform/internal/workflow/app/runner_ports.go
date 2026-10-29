package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The instance runner's ports (RFC 0006 §2.5). Workflow depends on the
// other contexts' application services through these; adapters in
// workflow/adapters implement them by calling those services, so every
// check those services make still holds. None of those contexts
// imports Workflow (§2.1, architecture_test.go).

var (
	// ErrRefused is an action its owning context refused for
	// permission. Adapters wrap their context's own refusal in it;
	// authz.ErrForbidden counts as the same. The transition is recorded
	// as refused and the instance stays where it was (§2.5).
	ErrRefused = errors.New("workflow: action refused")
	// ErrUnavailable is an action that cannot be carried out at all —
	// the owning context is not wired in this deployment, or the
	// subject is gone. Like a refusal it leaves the instance where it
	// was: a workflow never moves past work it could not ask for.
	ErrUnavailable = errors.New("workflow: action unavailable")
	// ErrInvalidCursor is an InstanceFilter.After that no page returned:
	// an invalid query.
	ErrInvalidCursor = fmt.Errorf("%w: the cursor is not one a page returned", ErrInvalidQuery)
)

// SubjectRef names what an instance is about.
type SubjectRef struct {
	Kind    domain.SubjectKind
	Project uuid.UUID
	// ID is the message of a translation unit, the release request
	// otherwise.
	ID uuid.UUID
	// Locale is a translation unit's canonical BCP 47 tag; empty for a
	// release request.
	Locale string
}

// UnitFacts is what Localization (and Catalog behind it) says about a
// translation unit.
type UnitFacts struct {
	// Found is false when the message has no translation in the locale
	// yet; the review fields are then empty.
	Found       bool
	Key         string
	Namespace   string
	ReviewState string
	Origin      string
	// Author is who wrote the current text (the latest content
	// revision's By).
	Author         string
	SourceRevision int
}

// Translations is Localization's side of a translation unit: the facts
// guards read, and the review decision set_review_state carries out.
type Translations interface {
	// Unit reads a translation unit. A message that does not exist is
	// ErrUnavailable.
	Unit(ctx context.Context, project, message uuid.UUID, locale string) (UnitFacts, error)
	// Review moves the translation to state as the principal on ctx,
	// through Localization's ReviewTranslation, so its own
	// translations.review check for the locale decides. Moving it to
	// the state it is already in is done, not an error: an outbox
	// redelivery runs actions again.
	Review(ctx context.Context, project, message uuid.UUID, locale, state string) error
}

// Findings is Quality's side: the open findings guards count, and the
// deterministic check run_check runs.
type Findings interface {
	// Open counts the unit's open (unwaived) findings in the project's
	// newest check run, by layer and severity.
	Open(ctx context.Context, project, message uuid.UUID, locale, key string) ([]domain.FindingCount, error)
	// Run runs the deterministic layers (all, or layers) over the
	// project as the principal on ctx and says what it concluded.
	Run(ctx context.Context, project uuid.UUID, layers []string) (detail string, err error)
}

// Suggestions is Intelligence's side.
type Suggestions interface {
	// Latest is the routing band of the unit's newest suggestion ("" when
	// there is none) and its best translation-memory match in [0, 1].
	Latest(ctx context.Context, project, message uuid.UUID, locale string) (band string, tm float64, err error)
	// Fill asks for a fill job for the unit as the principal on ctx,
	// idempotent on key; Intelligence's intelligence.translate check
	// decides.
	Fill(ctx context.Context, project uuid.UUID, key, locale, idemKey string) (detail string, err error)
}

// ReleaseRequestFacts is what Release says about a release request
// (RFC 0006 §5.1).
type ReleaseRequestFacts struct {
	Environment string
	// Requester is who asked, in the outbox spelling: the request's
	// author for four-eyes.
	Requester string
	// State is Release's: pending, deployed, denied, withdrawn or
	// refused.
	State string
	// Required and From are the approval the request was made under:
	// how many people, and of which party.
	Required int
	From     domain.Party
}

// ReleaseRequests is Release's side of a release request: the facts
// guards read and four-eyes is decided against, and the two decisions
// a definition's deploy_release and deny_release carry out — as the
// principal on ctx, so Release's own checks decide (approvals.decide in
// the environment; for a deploy, enough approvals and the publish gate
// run again). Release knows nothing of Workflow; an adapter calls its
// service.
type ReleaseRequests interface {
	// Request reads a request. One that does not exist is
	// ErrUnavailable.
	Request(ctx context.Context, project, id uuid.UUID) (ReleaseRequestFacts, error)
	// Deploy deploys the request as the principal on ctx. A deploy
	// Release refuses — the approvals do not meet the requirement, or
	// the publish gate refused it — is ErrRefused; deploying one that
	// is already deployed is done.
	Deploy(ctx context.Context, project, id uuid.UUID) (detail string, err error)
	// Deny denies the request as the principal on ctx; denying one that
	// is already denied is done.
	Deny(ctx context.Context, project, id uuid.UUID) error
}

// AssignmentsPort is what the instance runner needs from assignments and
// approvals (RFC 0006 §3.1–§3.2). *WorkService implements it.
//
// AssignForInstance and RequestApprovalForInstance are called with the
// triggering actor's principal on the step's transaction context: the
// work joins that transaction, so an assignment or an approval request
// commits — its events published — with the transition that asked for
// it, or not at all. That is also what makes them safe under the
// outbox's redelivery. Approvers is a read under Workflow's reader.
type AssignmentsPort interface {
	AssignForInstance(ctx context.Context, in WorkflowAssign) (domain.Assignment, error)
	RequestApprovalForInstance(ctx context.Context, in WorkflowApproval) (domain.Approval, error)
	// Approvers lists the actors (outbox spelling) who have granted the
	// subject's current approval so far, one entry per grant.
	Approvers(ctx context.Context, project uuid.UUID, subject domain.ApprovalSubject) ([]string, error)
}

var _ AssignmentsPort = (*WorkService)(nil)

// Actors turns an event's actor into the principal its actions run as
// (§2.5): a person's current membership grant in the tenant, or an API
// token's scopes. Identity owns both, so it is asked.
type Actors interface {
	// Principal returns the actor's principal in the tenant on ctx. ok
	// is false when the actor holds nothing there any more (a removed
	// member, a revoked or unknown token): its actions are then
	// refused.
	Principal(ctx context.Context, actor outbox.Actor) (p authz.Principal, ok bool, err error)
	// Held lists the permissions p holds for locale (tenant-wide ones
	// when locale is empty): what an actor_has_permission guard reads.
	Held(p authz.Principal, locale string) []string
}

// InstanceTransactor runs fn in one tenant transaction.
type InstanceTransactor interface {
	InTenant(ctx context.Context, fn func(context.Context, InstanceStore) error) error
}

// Transition is one row of an instance's log, as the runner appends it.
type Transition struct {
	From    string
	Event   domain.EventName
	To      string
	Outcome string
	Guards  []GuardOutcome
	Actions []ActionOutcome
	Actor   outbox.Actor
	EventID uuid.UUID
	At      time.Time
}

// InstanceStore is the instance runner's persistence inside a tenant
// transaction. Every Lock* holds the rows until the transaction ends.
type InstanceStore interface {
	// LockActive locks the subject's active instances, in id order.
	LockActive(ctx context.Context, s SubjectRef) ([]domain.Instance, error)
	// LockActiveInProject locks up to limit of a project's active
	// instances of kind, in id order.
	LockActiveInProject(ctx context.Context, project uuid.UUID, kind domain.SubjectKind, limit int) ([]domain.Instance, error)
	// LockInstance locks one instance.
	LockInstance(ctx context.Context, id uuid.UUID) (domain.Instance, error)
	// InsertInstance stores a new, not yet started instance. false: a
	// concurrent first trigger created the active instance of this
	// definition and subject first.
	InsertInstance(ctx context.Context, i domain.Instance) (bool, error)
	// SaveInstance stores a stepped instance with its snapshot (nil
	// once finished).
	SaveInstance(ctx context.Context, i domain.Instance, snapshot []byte) error
	// Snapshot reads a locked instance's stored snapshot.
	Snapshot(ctx context.Context, id uuid.UUID) ([]byte, error)
	// HasTransition reports whether event already stepped instance.
	HasTransition(ctx context.Context, instance, event uuid.UUID) (bool, error)
	AppendTransition(ctx context.Context, instance uuid.UUID, t Transition) error
	// Version reads the definition version an instance runs on.
	Version(ctx context.Context, definition uuid.UUID, n int) (domain.Version, error)

	// LockDueTimers locks up to limit active instances whose timer has
	// fallen due at now, skipping those another sweep holds.
	LockDueTimers(ctx context.Context, now time.Time, limit int) ([]domain.Instance, error)
	// Publish records an event in this transaction.
	Publish(ctx context.Context, e outbox.Event) error

	// SeedDefault records that the tenant's default definition was
	// seeded, inserting rec and v first unless rec is nil. false: the
	// tenant was already seeded (or a concurrent seeding won).
	SeedDefault(ctx context.Context, rec *domain.DefinitionRecord, v *domain.Version, at time.Time) (bool, error)
	// Seeded reports whether the tenant's default has been seeded.
	Seeded(ctx context.Context) (bool, error)
	// HasTenantDefinition reports whether a tenant-wide live definition
	// is called name.
	HasTenantDefinition(ctx context.Context, name string) (bool, error)
	// TenantDefinition is the tenant-wide live definition called name
	// (ErrNotFound when there is none).
	TenantDefinition(ctx context.Context, name string) (domain.DefinitionRecord, error)
	// InsertDefinition stores a new definition and its first version: the
	// release approval default, seeded on first use.
	InsertDefinition(ctx context.Context, rec domain.DefinitionRecord, v domain.Version) error

	// LatestVersion is the number of a definition's latest version
	// (ErrNotFound for a definition this tenant does not have).
	LatestVersion(ctx context.Context, definition uuid.UUID) (int, error)
	// RebaseInstance stores a locked, active instance moved to another
	// version, with the snapshot to Restore from there (RFC 0006 §2.3).
	RebaseInstance(ctx context.Context, i domain.Instance, snapshot []byte) error
	// DeleteFinished deletes up to limit instances that finished before
	// cutoff, oldest first, with their transition logs, and says how
	// many. It never deletes an active instance.
	DeleteFinished(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

// TimerScanner finds, outside any tenant, the tenants holding an
// instance whose timer has fallen due.
type TimerScanner interface {
	TenantsWithDueTimers(ctx context.Context, now time.Time, limit int) ([]tenancy.ID, error)
}

// RetentionScanner finds, outside any tenant, the tenants holding an
// instance that finished before cutoff.
type RetentionScanner interface {
	TenantsWithExpiredInstances(ctx context.Context, cutoff time.Time, limit int) ([]tenancy.ID, error)
}
