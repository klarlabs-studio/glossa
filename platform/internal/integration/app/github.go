package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// GitHub errors (RFC 0004 §6). Adapters wrap them, so match with
// errors.Is; GitHubRetryAfter reads the wait a rate limit asked for.
var (
	// ErrGitHubNotFound means the resource is gone (a deleted check run
	// or comment, an uninstalled App, a removed repository).
	ErrGitHubNotFound = errors.New("integration: GitHub has no such resource")
	// ErrGitHubRejected means GitHub refused the request for good
	// (permissions, validation, a bad App key); retrying will not help.
	ErrGitHubRejected = errors.New("integration: GitHub rejected the request")
	// ErrGitHubUnavailable means GitHub failed, timed out or the circuit
	// for the installation is open; the call can be retried later.
	ErrGitHubUnavailable = errors.New("integration: GitHub is unavailable")
	// ErrGitHubRateLimited means a primary or secondary rate limit asked
	// for a longer wait than the adapter waits in place.
	ErrGitHubRateLimited = errors.New("integration: GitHub rate limit exceeded")
	// ErrGitHubBusy means the tenant's GitHub calls are at their
	// concurrency cap (the bulkhead is full).
	ErrGitHubBusy = errors.New("integration: too many GitHub calls in flight for the tenant")
	// ErrInstallationNotVisible means the person's OAuth token cannot see
	// the installation they claimed.
	ErrInstallationNotVisible = errors.New("integration: the installation is not visible to the user")
	// ErrOAuthCodeRejected means GitHub refused the install flow's
	// one-time OAuth code (wrong, expired or already redeemed).
	ErrOAuthCodeRejected = errors.New("integration: GitHub refused the authorization code")
)

// GitHubRetryAfter reports how long a rate-limited call asked to wait.
func GitHubRetryAfter(err error) (time.Duration, bool) {
	var ra interface{ RetryAfter() time.Duration }
	if errors.As(err, &ra) && ra.RetryAfter() > 0 {
		return ra.RetryAfter(), true
	}
	return 0, false
}

// GitHubTarget is where a call goes: the tenant owning the installation
// (bulkhead scope), the installation (token and circuit scope) and the
// repository by its numeric ID, so a rename does not break it.
type GitHubTarget struct {
	TenantID       uuid.UUID
	InstallationID int64
	RepositoryID   int64
}

// Check-run states and conclusions (the subset Glossa writes).
const (
	CheckQueued     = "queued"
	CheckInProgress = "in_progress"
	CheckCompleted  = "completed"

	ConclusionSuccess = "success"
	ConclusionFailure = "failure"
	ConclusionNeutral = "neutral"
)

// CheckRunKey identifies a check run idempotently: at most one per
// (repository, head SHA, name).
type CheckRunKey struct {
	HeadSHA string
	Name    string
}

// CheckRun is a check run as GitHub stored it.
type CheckRun struct {
	ID         int64
	Name       string
	HeadSHA    string
	ExternalID string
	Status     string
	Conclusion string
}

// CheckAnnotation is one finding with a location. Level is notice,
// warning or failure.
type CheckAnnotation struct {
	Path      string
	StartLine int
	EndLine   int
	Level     string
	Title     string
	Message   string
}

// CheckRunUpdate is a PATCH of a stored check run. Conclusion is set
// only with Status completed. Annotations may exceed GitHub's 50 per
// request; the adapter batches them.
type CheckRunUpdate struct {
	Status      string
	Conclusion  string
	Title       string
	Summary     string // Markdown
	Annotations []CheckAnnotation
}

// GitHubInstallation is an installation as the person's token sees it.
type GitHubInstallation struct {
	ID           int64
	AccountID    int64
	AccountLogin string
	AccountType  string // User or Organization
}

// GitHubRepository is a repository the App can see through an
// installation. Glossa keys it by ID, so a rename does not break a Git
// connection; the names are labels for Studio.
type GitHubRepository struct {
	ID            int64
	Name          string
	FullName      string
	Private       bool
	DefaultBranch string
}

// GitHub is the GitHub App as Integration uses it (RFC 0004 §6): checks
// and the sticky comment on an installation's repositories, and the
// ownership check of the install flow. Implementations mint installation
// tokens themselves and never hand them out.
type GitHub interface {
	// EnsureCheckRun returns the check run for key, creating it as queued
	// with externalID when none exists: storedID (0 when unknown) is used
	// first, then GitHub is searched, so a retry never creates a second.
	EnsureCheckRun(ctx context.Context, t GitHubTarget, key CheckRunKey, externalID string, storedID int64) (CheckRun, error)
	// UpdateCheckRun patches the stored check run (ErrGitHubNotFound when
	// it is gone). GitHub appends annotations, so a caller that repeats
	// an update repeats them.
	UpdateCheckRun(ctx context.Context, t GitHubTarget, checkRunID int64, u CheckRunUpdate) error
	// UpsertStickyComment writes body as the pull request's one Glossa
	// comment and returns its ID: storedID (0 when unknown) is updated in
	// place; otherwise, or when it is gone, the comment carrying the
	// hidden marker is found and updated; only then is one created.
	UpsertStickyComment(ctx context.Context, t GitHubTarget, pullRequest int, storedID int64, body string) (int64, error)
	// VerifyInstallationOwner checks with the person's OAuth token (used
	// for this call only) that they can see installationID; it answers
	// ErrInstallationNotVisible when they cannot.
	VerifyInstallationOwner(ctx context.Context, userToken string, installationID int64) (GitHubInstallation, error)
	// ExchangeUserCode redeems the install flow's one-time OAuth code
	// for a user access token. The token is the caller's to use once and
	// drop; it is never stored. ErrOAuthCodeRejected means the code was
	// wrong, expired or already redeemed.
	ExchangeUserCode(ctx context.Context, code string) (userToken string, err error)
	// Repositories lists what the installation can see, paging through
	// GitHub. It needs only the installation (uuid.Nil tenant and zero
	// repository on the target are fine).
	Repositories(ctx context.Context, t GitHubTarget) ([]GitHubRepository, error)
	// InstallURL is where a person authorizes the App, carrying state
	// through the round trip. It is configuration, not a call.
	InstallURL(state string) string
	// PullRequestURL is where pull request number of repository
	// ("owner/name") is on the web, or "" when there is nothing to point
	// at. It is configuration, not a call.
	PullRequestURL(repository string, number int) string
}
