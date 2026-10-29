package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Errors of the GitHub install flow and the webhook inbox.
var (
	// ErrGitHubNotConfigured means this deployment has no GitHub App:
	// no App ID in the environment, so the endpoints answer 503 and
	// nothing else changes.
	ErrGitHubNotConfigured = errors.New("integration: this deployment has no GitHub App configured")
	// ErrInstallStateInvalid means the callback's state was unknown,
	// expired or already redeemed.
	ErrInstallStateInvalid = errors.New("integration: the install state is unknown, expired or already used")
	// ErrInstallStateNotYours means the state belongs to someone else's
	// installation attempt.
	ErrInstallStateNotYours = errors.New("integration: the install state was issued to another person")
	// ErrInstallationClaimed means another tenant already holds that
	// GitHub installation. It never says which.
	ErrInstallationClaimed = errors.New("integration: this GitHub installation is already connected to a workspace")
	// ErrConnectionExists means the repository already feeds a project
	// at that path.
	ErrConnectionExists = errors.New("integration: this repository and path are already connected")
	// ErrApplicationNotFound means the project has no such application.
	ErrApplicationNotFound = errors.New("integration: the project has no such application")
	// ErrRepositoryNotVisible means the installation cannot see that
	// repository, so a connection to it would never work.
	ErrRepositoryNotVisible = errors.New("integration: the installation cannot see that repository")

	// Webhook errors. None of them carries payload content: a delivery
	// that fails to verify is answered without detail, so an attacker
	// learns nothing from the answer.

	// ErrWebhookSignature means X-Hub-Signature-256 was missing,
	// malformed or did not match the raw body.
	ErrWebhookSignature = errors.New("integration: the webhook signature is missing or invalid")
	// ErrWebhookTooLarge means the body passed the 5 MB cap.
	ErrWebhookTooLarge = errors.New("integration: the webhook body is too large")
	// ErrWebhookHeaders means X-GitHub-Event or X-GitHub-Delivery was
	// missing.
	ErrWebhookHeaders = errors.New("integration: the webhook lacks its event or delivery headers")
	// ErrWebhookIgnored means a verified event or action Glossa does not
	// act on: acknowledge and drop it.
	ErrWebhookIgnored = errors.New("integration: the webhook event is not one Glossa handles")
	// ErrWebhookPayload means a verified payload was not what GitHub
	// sends for its event.
	ErrWebhookPayload = errors.New("integration: the webhook payload is malformed")
)

// WebhookDelivery is a delivery whose signature verified, with the raw
// body exactly as it was signed.
type WebhookDelivery struct {
	// ID is X-GitHub-Delivery, the inbox key.
	ID string
	// Event is X-GitHub-Event.
	Event string
	Body  []byte
}

// WebhookVerifier checks a delivery against the App's webhook secret,
// over the raw body before anything parses it.
type WebhookVerifier interface {
	Verify(h http.Header, body io.Reader) (WebhookDelivery, error)
}

// WebhookEvent is a verified payload reduced to what the worker acts
// on, so the application layer never sees GitHub's own shapes.
type WebhookEvent struct {
	Event, Action  string
	InstallationID int64
	// Account is the installation's account (`installation` events).
	AccountID    int64
	AccountLogin string
	AccountType  string
	// RepositoryID is the repository the event happened in.
	RepositoryID int64
	// Repositories added to or removed from the installation.
	RepositoriesAdded   []int64
	RepositoriesRemoved []int64
	// Pull request, on `pull_request` events.
	PullRequest int
	HeadRef     string
	HeadSHA     string
	Merged      bool
	FromFork    bool
	// PullRequestOpenedAt is GitHub's `pull_request.created_at`, which a
	// policy's grace is measured against (RFC 0005 §4.3). The zero time
	// is a payload that carried none.
	PullRequestOpenedAt time.Time
	// Check run, on `check_run.rerequested`. The check worker is a later
	// slice (RFC 0004 §6.4); these are what it will read.
	CheckRunID   int64
	CheckRunName string
}

// WebhookEvents parses a verified delivery. ErrWebhookIgnored is an
// event or action Glossa does not handle; ErrWebhookPayload is one that
// lacks the IDs Glossa keys on.
type WebhookEvents interface {
	Parse(d WebhookDelivery) (WebhookEvent, error)
}

// Delivery outcomes, as the §11 metrics label them.
const (
	DeliveryAccepted     = "accepted"
	DeliveryDuplicate    = "duplicate"
	DeliveryRejected     = "rejected"
	DeliveryOversized    = "too_large"
	DeliveryUnconfigured = "unconfigured"
)

// InstallIntent is a started installation: the state Studio carries to
// GitHub, bound to the tenant, the person and an expiry.
type InstallIntent struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Person   string
	// State is the opaque value; only its SHA-256 is stored.
	State string
	// InstallURL is where Studio sends the person, with State on it.
	InstallURL string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// RedeemedIntent is what a redeemed state proves.
type RedeemedIntent struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Person   string
}

// GitHubStore is the GitHub integration's persistence in tenant scope.
type GitHubStore interface {
	// InsertInstallIntent stores a started installation.
	InsertInstallIntent(ctx context.Context, id uuid.UUID, person string, stateHash []byte, created, expires time.Time) error
	// RedeemInstallIntent takes an unexpired, unredeemed intent by its
	// state hash and marks it used; ErrInstallStateInvalid when there is
	// none.
	RedeemInstallIntent(ctx context.Context, stateHash []byte, now time.Time) (RedeemedIntent, error)

	// InsertInstallation claims i for the current tenant;
	// ErrInstallationClaimed when another tenant holds it already.
	InsertInstallation(ctx context.Context, i domain.Installation) error
	Installation(ctx context.Context, id uuid.UUID) (domain.Installation, error)
	InstallationByGitHubID(ctx context.Context, githubID int64) (domain.Installation, error)
	Installations(ctx context.Context) ([]domain.Installation, error)
	// SetInstallationState records what an `installation` webhook said.
	SetInstallationState(ctx context.Context, githubID int64, state domain.InstallationState, login string, now time.Time) error
	DeleteInstallation(ctx context.Context, id uuid.UUID) error

	// InsertGitConnection stores c; ErrConnectionExists when the
	// repository already feeds that path.
	InsertGitConnection(ctx context.Context, c domain.GitConnection) error
	GitConnection(ctx context.Context, id uuid.UUID) (domain.GitConnection, error)
	GitConnections(ctx context.Context, f ConnectionFilter) ([]domain.GitConnection, error)
	// ConnectionsForRepository lists a repository's connections, one per
	// monorepo path.
	ConnectionsForRepository(ctx context.Context, repositoryID int64) ([]domain.GitConnection, error)
	SaveGitConnection(ctx context.Context, c domain.GitConnection) error
	DeleteGitConnection(ctx context.Context, id uuid.UUID) error
}

// ConnectionFilter narrows a connection list.
type ConnectionFilter struct {
	Installation *uuid.UUID
	Project      *uuid.UUID
}

// GitHubTransactor opens a tenant-scoped GitHub store.
type GitHubTransactor interface {
	InGitHub(ctx context.Context, fn func(context.Context, GitHubStore) error) error
}

// ClaimedDelivery is a webhook delivery leased to the inbox worker.
type ClaimedDelivery struct {
	ID                   string
	Event                string
	Action               string
	GitHubInstallationID int64
	// TenantID is set only on a redelivery that already resolved.
	TenantID *uuid.UUID
	Payload  []byte
	Attempts int
	Token    uuid.UUID
}

// InstallationOwner is which tenant holds a GitHub installation.
type InstallationOwner struct {
	Tenant tenancy.ID
	State  domain.InstallationState
}

// DeliveryInbox is the webhook inbox across tenants (system scope
// integration.github). The endpoint stores a delivery before any tenant
// is known; the worker claims, resolves and settles it.
type DeliveryInbox interface {
	// Store writes a verified delivery. stored is false when its ID was
	// seen before, which is what makes a duplicate a no-op.
	Store(ctx context.Context, d domain.Delivery) (stored bool, err error)
	// Claim leases the oldest due delivery; ok is false when none is.
	Claim(ctx context.Context, lease time.Duration) (c ClaimedDelivery, ok bool, err error)
	// Settle ends a claimed delivery: it records the tenant it resolved
	// to (nil when none), drops the payload and keeps the ID.
	Settle(ctx context.Context, id string, token uuid.UUID, state domain.DeliveryState, tenant *uuid.UUID, failure string, now time.Time) error
	// Retry hands a claimed delivery back after delay, keeping its
	// payload.
	Retry(ctx context.Context, id string, token uuid.UUID, delay time.Duration, failure string) error
	// Owner maps a GitHub installation ID to the tenant that claimed it;
	// ok is false when no tenant has.
	Owner(ctx context.Context, githubID int64) (o InstallationOwner, ok bool, err error)
	// Depth reports the pending deliveries per event (the §11 metric).
	Depth(ctx context.Context) (map[string]int, error)
	// Sweep removes settled deliveries received before the replay
	// window's start, and install intents whose own window has closed —
	// an expired state is already useless, so it is not kept for the
	// replay window as a delivery id is.
	Sweep(ctx context.Context, before time.Time) (deliveries, intents int, err error)
}

// RepositoryConnection is a Git connection seen from outside the
// tenant that owns it: which tenant, project and application a
// repository feeds, at which monorepo path.
type RepositoryConnection struct {
	Tenant      tenancy.ID
	Project     uuid.UUID
	Application uuid.UUID
	Path        string
}

// RepositoryDirectory resolves a repository to its Git connections
// across tenants (system scope integration.github).
//
// It exists for the GitHub Actions OIDC exchange (RFC 0004 §6.3), which
// arrives with no tenant: which tenant a CI run belongs to is precisely
// what its verified `repository_id` proves, exactly as a webhook
// delivery's tenant follows from its installation. The read is narrow —
// the mapping and nothing else, no repository name and no creator — so
// a verified run learns which of its own projects it may act on and
// nothing about anyone else's.
type RepositoryDirectory interface {
	// ConnectionsForRepository lists a repository's connections, one per
	// monorepo path, ordered by path. An empty result is not an error.
	ConnectionsForRepository(ctx context.Context, repositoryID int64) ([]RepositoryConnection, error)
}

// GitHubMetrics records the webhook series of RFC 0004 §11. All methods
// must be safe for concurrent use and must not block.
type GitHubMetrics interface {
	// DeliveryReceived counts one delivery at the endpoint, by event and
	// outcome ("accepted", "duplicate", "rejected", "too_large",
	// "unconfigured").
	DeliveryReceived(event, outcome string)
	// DeliveryHandled counts one worker attempt and how long the handler
	// took, by event and outcome ("done", "ignored", "retry", "failed").
	DeliveryHandled(event, outcome string, d time.Duration)
	// InboxDepth reports the pending deliveries per event.
	InboxDepth(depth map[string]int)
}

// NoGitHubMetrics records nothing.
type NoGitHubMetrics struct{}

// DeliveryReceived implements GitHubMetrics.
func (NoGitHubMetrics) DeliveryReceived(string, string) {}

// DeliveryHandled implements GitHubMetrics.
func (NoGitHubMetrics) DeliveryHandled(string, string, time.Duration) {}

// InboxDepth implements GitHubMetrics.
func (NoGitHubMetrics) InboxDepth(map[string]int) {}

// Branches is Catalog's branch service as the webhook worker uses it: a
// pull request opens, pushes to and closes a branch. The Branches API
// and its lifecycle already exist, so this calls them rather than
// touching Catalog's tables (RFC 0004 §4.1). Correctness never depends
// on a webhook — the default branch's push is what activates messages —
// so these calls only keep the branch view current.
type Branches interface {
	// UpsertBranch creates or updates the project's branch with the
	// pull request's number and head commit.
	UpsertBranch(ctx context.Context, project uuid.UUID, branch string, headCommit string, pr *int) error
	// CloseBranch closes an unmerged branch; MergeBranch marks a merged
	// one. Neither activates anything.
	CloseBranch(ctx context.Context, project uuid.UUID, branch string) error
	MergeBranch(ctx context.Context, project uuid.UUID, branch string) error
	// ApplicationExists reports whether the project has that
	// application, so a Git connection cannot name one it does not.
	ApplicationExists(ctx context.Context, project, application uuid.UUID) (bool, error)
}
