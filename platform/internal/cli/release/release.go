// Package release is the Release context as the CLI sees it: publish,
// list, diff, promote and roll back releases, list environments, manage
// delivery keys, and write a release's bundle for build-time catalogs
// (`glossa pull --release`, runtimes/SPEC.md §3.4).
//
// Commands are written against Service; remote.ReleaseService implements
// it over the generated /v1 client. The types here are the CLI's own:
// their JSON tags are part of the `glossa.cli.release.*/v1` output
// schemas (cmd/glossa/README.md), so they change only additively.
package release

import (
	"context"
	"time"
)

// Scope is the tenant and project.
type Scope struct{ Tenant, Project string }

// Policy decides which translations ship to an environment.
type Policy struct {
	States          []string `json:"states"`
	IncludeOutdated bool     `json:"include_outdated"`
}

// LocaleCounts counts what a release ships in one locale.
type LocaleCounts struct {
	Messages int `json:"messages"`
	// Outdated translations shipped (the policy allowed them).
	Outdated int `json:"outdated"`
}

// Counts sizes a release.
type Counts struct {
	// Messages is the number of source messages.
	Messages  int `json:"messages"`
	Artifacts int `json:"artifacts"`
	// NewArtifacts is what the publish uploaded; the rest were stored.
	NewArtifacts int                     `json:"new_artifacts"`
	Bytes        int                     `json:"bytes"`
	Locales      map[string]LocaleCounts `json:"locales"`
}

// Release is a published, immutable release.
type Release struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// Environment is the one it was published to.
	Environment string `json:"environment"`
	// ParentID is what that environment served before it.
	ParentID       string    `json:"parent_id,omitempty"`
	Note           string    `json:"note,omitempty"`
	Author         string    `json:"author"`
	CreatedAt      time.Time `json:"created_at"`
	SourceLocale   string    `json:"source_locale"`
	Locales        []string  `json:"locales"`
	ManifestDigest string    `json:"manifest_digest"`
	Policy         Policy    `json:"policy"`
	Counts         Counts    `json:"counts"`
}

// Environment points at the release it serves.
type Environment struct {
	Name string `json:"name"`
	// CurrentReleaseID is empty until something was published or
	// promoted to it.
	CurrentReleaseID string    `json:"current_release_id,omitempty"`
	Policy           Policy    `json:"policy"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// LocaleDiff lists the message IDs that changed in one locale.
type LocaleDiff struct {
	Locale  string   `json:"locale"`
	Added   []string `json:"added"`
	Changed []string `json:"changed"`
	Removed []string `json:"removed"`
}

// Diff is what changed between two releases.
type Diff struct {
	ReleaseID string `json:"release_id"`
	// BaseReleaseID is empty when the release had nothing to compare
	// with (everything is added).
	BaseReleaseID string       `json:"base_release_id,omitempty"`
	Locales       []LocaleDiff `json:"locales"`
}

// DeliveryKey is a publishable, read-only key runtimes fetch releases
// from glossa-edge with. It is public by design (it ships in browser
// bundles) and scoped to one project.
type DeliveryKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
	// Scope is what the key reads at the edge (RFC 0004 4.3).
	Scope     KeyScope   `json:"scope"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// KeyScope is the environments a delivery key may read and, with
// Branches, every branch preview.
type KeyScope struct {
	Environments []string `json:"environments"`
	Branches     bool     `json:"branches"`
}

// PublishRequest publishes the project's eligible translations to an
// environment.
type PublishRequest struct {
	Environment string
	Note        string
	// IdempotencyKey makes a retry return the first release instead of
	// publishing again.
	IdempotencyKey string
}

// Published is the outcome of a publish.
type Published struct {
	// Release is the release published. It is zero when Held is set:
	// the server names only its ID then.
	Release Release
	// Replayed is true when the idempotency key had already published
	// it: nothing new was published.
	Replayed bool
	// Held is set when the environment requires approvals: the release
	// was recorded, a release request was made, and no pointer moved.
	Held *Held
}

// Held is a publish or promote held for approval (RFC 0006 §5.1): the
// server answered 202 and the environment still serves what it served.
type Held struct {
	// ReleaseID is the release the request would deploy.
	ReleaseID string
	RequestID string
	Request   Request
}

// Request is a release request: a publish or promote into an
// environment that requires approvals, deployed once enough distinct
// people other than the requester grant it.
type Request struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	ReleaseID   string `json:"release_id"`
	// Action is publish or promote: what the deploy records.
	Action string `json:"action"`
	// Requester never counts toward the approval.
	Requester string      `json:"requester"`
	Approval  Requirement `json:"approval"`
	// Gate is what the completeness requirement said when it was made.
	Gate        Gate   `json:"gate"`
	Forced      bool   `json:"forced"`
	ForceReason string `json:"force_reason,omitempty"`
	// State is pending, deployed, denied, withdrawn or refused.
	State     string     `json:"state"`
	DecidedBy string     `json:"decided_by,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Requirement is an environment's approval: n distinct people of from,
// none of them the requester.
type Requirement struct {
	N                     int   `json:"n"`
	From                  Party `json:"from"`
	DistinctFromRequester bool  `json:"distinct_from_requester"`
}

// Party is exactly one of a member, a role or a group.
type Party struct {
	Member string `json:"member,omitempty"`
	Role   string `json:"role,omitempty"`
	Group  string `json:"group,omitempty"`
}

// Gate is the completeness requirement's verdict.
type Gate struct {
	Met   bool     `json:"met"`
	Unmet []string `json:"unmet,omitempty"`
}

// RequestFilter narrows a list of release requests (empty: any).
type RequestFilter struct{ Environment, State string }

// Rollout is a staged rollout (RFC 0006 §5.2): the environment's
// manifest serves a candidate release to percent of installations.
type Rollout struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	// ReleaseID is the candidate; StableReleaseID what the environment
	// served when the rollout started.
	ReleaseID       string `json:"release_id"`
	StableReleaseID string `json:"stable_release_id"`
	Percent         int    `json:"percent"`
	// Status is active, completed or aborted; End says how an ended one
	// ended (completed, aborted, expired, rolled_back).
	Status             string     `json:"status"`
	End                string     `json:"end,omitempty"`
	MaxDurationSeconds int        `json:"max_duration_seconds"`
	ExpiresAt          time.Time  `json:"expires_at"`
	Forced             bool       `json:"forced"`
	ForceReason        string     `json:"force_reason,omitempty"`
	StartedBy          string     `json:"started_by"`
	StartedAt          time.Time  `json:"started_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	EndedBy            string     `json:"ended_by,omitempty"`
	EndedAt            *time.Time `json:"ended_at,omitempty"`
}

// StartRollout starts serving a candidate to a share of installations.
type StartRollout struct {
	Environment string
	ReleaseID   string
	Percent     int
	// MaxDurationSeconds is 0 for the server's default (14 days).
	MaxDurationSeconds int
	Force              bool
	ForceReason        string
	IdempotencyKey     string
}

// RolloutVersion is a rollout with its ETag, for If-Match on the next
// change.
type RolloutVersion struct {
	Rollout Rollout
	ETag    string
	// Replayed is true when a start's idempotency key had already
	// started it.
	Replayed bool
}

// Problem is one reason the catalog can't be released.
type Problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Key    string `json:"key,omitempty"`
	Locale string `json:"locale,omitempty"`
}

// PreviewRelease is what a publish would build.
type PreviewRelease struct {
	SourceLocale   string   `json:"source_locale"`
	Locales        []string `json:"locales"`
	ManifestDigest string   `json:"manifest_digest"`
	// Counts.NewArtifacts is what the publish would upload.
	Counts Counts `json:"counts"`
}

// Preview is a publish's dry run: what it would ship to an environment,
// compared with what the environment serves. Nothing is stored.
type Preview struct {
	Environment string
	Policy      Policy
	// BaseReleaseID is what the environment serves now (empty: nothing).
	BaseReleaseID string
	Releasable    bool
	Problems      []Problem
	// Release is nil when the catalog isn't releasable.
	Release *PreviewRelease
	Changes []LocaleDiff
}

// Service is what the release commands need from the server.
type Service interface {
	Environments(ctx context.Context, s Scope) ([]Environment, error)
	Environment(ctx context.Context, s Scope, name string) (Environment, error)
	// Releases lists releases newest first; limit 0 lists them all.
	Releases(ctx context.Context, s Scope, limit int) ([]Release, error)
	Release(ctx context.Context, s Scope, id string) (Release, error)
	// Diff compares a release with base (empty: its parent).
	Diff(ctx context.Context, s Scope, id, base string) (Diff, error)
	Publish(ctx context.Context, s Scope, r PublishRequest) (Published, error)
	// PreviewPublish runs a publish's build for environment and stores
	// nothing.
	PreviewPublish(ctx context.Context, s Scope, environment string) (Preview, error)
	// Promote points environment at a release. A non-nil Held means the
	// environment requires approvals: a release request was made and
	// nothing moved.
	Promote(ctx context.Context, s Scope, releaseID, environment string) (Environment, *Held, error)
	// Rollback points environment back at toRelease (empty: the newest
	// release it served before the current one).
	Rollback(ctx context.Context, s Scope, environment, toRelease string) (Environment, error)
	DeliveryKeys(ctx context.Context, s Scope) ([]DeliveryKey, error)
	// CreateDeliveryKey creates a key; a nil scope reads production only.
	CreateDeliveryKey(ctx context.Context, s Scope, name string, scope *KeyScope, idempotencyKey string) (DeliveryKey, error)
	// SetDeliveryKeyScope replaces what a key reads; the key itself
	// doesn't change, so bundles that ship it keep working.
	SetDeliveryKeyScope(ctx context.Context, s Scope, id string, scope KeyScope) (DeliveryKey, error)
	RevokeDeliveryKey(ctx context.Context, s Scope, id string) error
	// ReleaseRequests lists release requests newest first.
	ReleaseRequests(ctx context.Context, s Scope, f RequestFilter) ([]Request, error)
	ReleaseRequest(ctx context.Context, s Scope, id string) (Request, error)
	WithdrawReleaseRequest(ctx context.Context, s Scope, id, reason string) (Request, error)
	// Rollouts lists an environment's rollouts newest first; limit 0
	// lists them all.
	Rollouts(ctx context.Context, s Scope, environment string, limit int) ([]Rollout, error)
	Rollout(ctx context.Context, s Scope, environment, id string) (RolloutVersion, error)
	StartRollout(ctx context.Context, s Scope, r StartRollout) (RolloutVersion, error)
	// AdvanceRollout needs ifMatch; CompleteRollout and AbortRollout
	// send it only when it is not empty.
	AdvanceRollout(ctx context.Context, s Scope, environment, id, ifMatch string, percent int) (RolloutVersion, error)
	CompleteRollout(ctx context.Context, s Scope, environment, id, ifMatch string) (RolloutVersion, error)
	AbortRollout(ctx context.Context, s Scope, environment, id, ifMatch string) (RolloutVersion, error)
	// BundleSource serves a release's manifest as environment serves it,
	// and its artifacts.
	BundleSource(s Scope, releaseID, environment string) BundleSource
}
