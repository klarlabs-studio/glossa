// Package app is the Release context's application layer: environments
// and their policies, publishing (build, upload, record, point), promote
// and rollback, diffs, delivery keys, and keeping object storage — the
// only thing glossa-edge reads — in step with the database.
//
// The database is the source of truth; storage is a projection of it.
// Every change commits first and then writes the objects it affects
// (an environment's manifest, a key's index object), both right away
// and again from an outbox subscriber, so a storage outage delays
// delivery but never loses a change. Writes are state-based — they read
// the current row under a lock and write what it says — so retries and
// reordering converge on the latest state.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Errors. Adapters translate storage errors into these.
var (
	ErrNotFound            = errors.New("release: not found")
	ErrEnvironmentExists   = errors.New("release: the project already has an environment with that name")
	ErrStaleVersion        = errors.New("release: version changed")
	ErrPreconditionFailed  = errors.New("release: resource changed since it was read")
	ErrIdempotencyReuse    = errors.New("release: Idempotency-Key was used for a different request")
	ErrStorage             = errors.New("release: artifact storage is unavailable")
	ErrReleaseNotInProject = errors.New("release: no such release in this project")
	ErrInvalidPageToken    = errors.New("release: page_token is not one this list issued")
)

// Source is what a release is built from: Catalog's releasable source
// and Localization's releasable translations, joined (RFC 0002 §4:
// through their application ports, never their tables).
type Source interface {
	// CheckProject returns ErrNotFound if the project doesn't exist.
	CheckProject(ctx context.Context, project uuid.UUID) error
	// Snapshot reads the project's active messages and its translations
	// in the review states given.
	Snapshot(ctx context.Context, project uuid.UUID, states []string) (domain.Snapshot, error)
	// BranchOverlay reads what branch adds to the main catalog: its
	// proposed messages and its source proposals (RFC 0004 §4.1). A
	// branch the catalog doesn't know yet proposes nothing.
	BranchOverlay(ctx context.Context, project uuid.UUID, branch string) (domain.Overlay, error)
	// BranchesProposing names the project's open branches that propose
	// message: the branch previews a change to it shows, and so the
	// ones to publish again when its translations change.
	BranchesProposing(ctx context.Context, project, message uuid.UUID) ([]string, error)
	// CheckPolicy reads the project's check-policy document, which is
	// what the publish gate is made of (RFC 0005 §4.1): the completeness
	// and the review state an environment requires. A project that
	// stored none reads as checkpolicy's documented default.
	CheckPolicy(ctx context.Context, project uuid.UUID) (checkpolicy.Policy, error)
}

// EnvironmentRef names an environment of a tenant's project.
type EnvironmentRef struct {
	Tenant      tenancy.ID
	Project     uuid.UUID
	Environment string
}

// KeyRef names a delivery key of a tenant's project.
type KeyRef struct {
	Tenant  tenancy.ID
	Project uuid.UUID
	Key     uuid.UUID
}

// Scanner finds Release's background work across tenants (system
// scope); the work itself runs in each tenant's scope.
type Scanner interface {
	// DuePublishRequests lists publish requests due at now.
	DuePublishRequests(ctx context.Context, now time.Time, limit int) ([]EnvironmentRef, error)
	// StaleKeyIndexes lists active keys whose index object was last
	// written in a format older than version.
	StaleKeyIndexes(ctx context.Context, version, limit int) ([]KeyRef, error)
	// ExpiredRollouts lists active rollouts whose max_duration has
	// passed at now (RFC 0006 §5.2).
	ExpiredRollouts(ctx context.Context, now time.Time, limit int) ([]RolloutRef, error)
	// ActiveRollouts counts the deployment's active rollouts.
	ActiveRollouts(ctx context.Context) (int, error)
}

// RolloutRef names a rollout of a tenant's project environment.
type RolloutRef struct {
	EnvironmentRef
	Rollout uuid.UUID
}

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// Store is Release's persistence in tenant scope.
type Store interface {
	// InsertEnvironment adds e; false if one with its name exists.
	InsertEnvironment(ctx context.Context, e domain.Environment, by string) (bool, error)
	Environment(ctx context.Context, project uuid.UUID, name string, lock bool) (domain.Environment, error)
	Environments(ctx context.Context, project uuid.UUID, after string, limit int) ([]domain.Environment, error)
	// LockProjectEnvironments locks every environment of the project, in
	// name order: it serializes publishes (versions have no gaps).
	LockProjectEnvironments(ctx context.Context, project uuid.UUID) ([]domain.Environment, error)
	// UpdateEnvironment saves e if the stored version is expected
	// (ErrStaleVersion otherwise).
	UpdateEnvironment(ctx context.Context, e domain.Environment, expected int) error
	DeleteProjectEnvironments(ctx context.Context, project uuid.UUID) ([]string, error)
	// BranchEnvironment is the environment of branch (ErrNotFound if
	// none).
	BranchEnvironment(ctx context.Context, project uuid.UUID, branch string, lock bool) (domain.Environment, error)
	CountBranchEnvironments(ctx context.Context, project uuid.UUID) (int, error)
	// DeleteEnvironment removes an environment (its pending publish
	// request with it); its deployments stay as history.
	DeleteEnvironment(ctx context.Context, project uuid.UUID, name string) (bool, error)

	// PublishRequest locks an environment's pending publish request
	// (ErrNotFound if none).
	PublishRequest(ctx context.Context, project uuid.UUID, environment string) (domain.PublishRequest, error)
	SavePublishRequest(ctx context.Context, r domain.PublishRequest) error
	// DeletePublishRequest removes r if it is still the pending request
	// (its ID): a request made since stays.
	DeletePublishRequest(ctx context.Context, r domain.PublishRequest) error

	// InsertRelease records r; false if a release with its ID exists.
	InsertRelease(ctx context.Context, r domain.Release) (bool, error)
	Release(ctx context.Context, project, id uuid.UUID) (domain.Release, error)
	// Releases lists releases below beforeVersion, newest first.
	Releases(ctx context.Context, project uuid.UUID, beforeVersion, limit int) ([]domain.Release, error)
	MaxReleaseVersion(ctx context.Context, project uuid.UUID) (int, error)

	AppendDeployment(ctx context.Context, d domain.Deployment) error
	LastDeploymentNumber(ctx context.Context, project uuid.UUID, environment string) (int, error)
	// Deployments lists an environment's history below before, newest first.
	Deployments(ctx context.Context, project uuid.UUID, environment string, before, limit int) ([]domain.Deployment, error)
	// RollbackTarget is the newest release environment served that is
	// older than version (ErrNotFound if none).
	RollbackTarget(ctx context.Context, project uuid.UUID, environment string, version int) (domain.Release, error)
	ServedIn(ctx context.Context, project uuid.UUID, environment string, release uuid.UUID) (bool, error)

	// InsertDeliveryKey adds k; false if a key with its ID exists.
	InsertDeliveryKey(ctx context.Context, k domain.DeliveryKey) (bool, error)
	DeliveryKey(ctx context.Context, project, id uuid.UUID, lock bool) (domain.DeliveryKey, error)
	DeliveryKeys(ctx context.Context, project, after uuid.UUID, limit int) ([]domain.DeliveryKey, error)
	ActiveDeliveryKeys(ctx context.Context, project uuid.UUID) ([]domain.DeliveryKey, error)
	// SetDeliveryKeyScope saves what an active key reads.
	SetDeliveryKeyScope(ctx context.Context, k domain.DeliveryKey) error
	RevokeDeliveryKey(ctx context.Context, k domain.DeliveryKey) error
	// MarkKeyIndexed records the format the key's index object was last
	// written in.
	MarkKeyIndexed(ctx context.Context, id uuid.UUID, version int) error

	// InsertRollout records r; false if the environment has an active
	// rollout already, or one with r's ID exists.
	InsertRollout(ctx context.Context, r domain.Rollout) (bool, error)
	Rollout(ctx context.Context, project, id uuid.UUID) (domain.Rollout, error)
	// ActiveRollout is the environment's active rollout (ErrNotFound if
	// none); lock it under the environment's lock only.
	ActiveRollout(ctx context.Context, project uuid.UUID, environment string, lock bool) (domain.Rollout, error)
	// Rollouts lists an environment's rollouts, newest first, after the
	// rollout after (none: from the newest).
	Rollouts(ctx context.Context, project uuid.UUID, environment string, after uuid.UUID, limit int) ([]domain.Rollout, error)
	// UpdateRollout saves r if the stored version is expected
	// (ErrStaleVersion otherwise).
	UpdateRollout(ctx context.Context, r domain.Rollout, expected int) error

	// InsertReleaseRequest records a new request (RFC 0006 §5.1). The
	// environment has at most one pending request: inserting a second is
	// a unique violation, so the caller withdraws the first beforehand.
	InsertReleaseRequest(ctx context.Context, r domain.ReleaseRequest) error
	ReleaseRequest(ctx context.Context, project, id uuid.UUID, lock bool) (domain.ReleaseRequest, error)
	// PendingReleaseRequest locks the environment's open request
	// (ErrNotFound if none).
	PendingReleaseRequest(ctx context.Context, project uuid.UUID, environment string) (domain.ReleaseRequest, error)
	// ReleaseRequestFor is the newest request to deploy release into
	// environment (ErrNotFound if none).
	ReleaseRequestFor(ctx context.Context, project, release uuid.UUID, environment string) (domain.ReleaseRequest, error)
	ReleaseRequests(ctx context.Context, f RequestFilter) ([]domain.ReleaseRequest, error)
	// UpdateReleaseRequest saves r if the stored version is expected
	// (ErrStaleVersion otherwise).
	UpdateReleaseRequest(ctx context.Context, r domain.ReleaseRequest, expected int) error

	Publish(ctx context.Context, e outbox.Event) error
}

// RequestFilter narrows a list of release requests: newest first, below
// Before. Zero fields do not filter.
type RequestFilter struct {
	Project     uuid.UUID
	Environment string
	State       domain.RequestState
	Before      uuid.UUID
	Limit       int
}

// Approvals is who has approved a release request (RFC 0006 §5.1):
// every grant on its current approval, one entry per granting decision,
// in the outbox spelling ("person:<id>"), or none once it was denied.
// Workflow keeps the approvals; an adapter of Workflow's implements
// this, so Release never imports Workflow (RFC 0006 §2.1). Release
// counts the grants itself against the environment's requirement and
// refuses to move a pointer until it is met, whatever a workflow
// definition says.
type Approvals interface {
	Granters(ctx context.Context, project, request uuid.UUID) ([]string, error)
}
