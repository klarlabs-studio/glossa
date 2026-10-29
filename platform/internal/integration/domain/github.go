package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GitHub errors. The HTTP adapter maps each to a problem code.
var (
	// ErrInvalidInstallation: an installation GitHub would not describe
	// that way.
	ErrInvalidInstallation = errors.New("integration: invalid GitHub installation")
	// ErrInvalidConnection: a Git connection that names no repository,
	// project, application or default branch, or an impossible path.
	ErrInvalidConnection = errors.New("integration: invalid Git connection")
	// ErrInstallationRevoked: the App was uninstalled on GitHub, so
	// nothing can be connected to it any more.
	ErrInstallationRevoked = errors.New("integration: the GitHub App was uninstalled from this account")
)

// InstallationState is where an installation stands. Only GitHub
// changes it; Glossa learns through the `installation` webhook.
type InstallationState string

// Installation states.
const (
	// InstallationActive: the App is installed and usable.
	InstallationActive InstallationState = "active"
	// InstallationSuspended: the account suspended the App. Calls fail
	// until it is unsuspended; the connections stay.
	InstallationSuspended InstallationState = "suspended"
	// InstallationRevoked: the App was uninstalled. The row stays so
	// Studio can say what happened, and so the connections are removed
	// deliberately rather than silently.
	InstallationRevoked InstallationState = "revoked"
)

// ParseInstallationState canonicalizes a stored state.
func ParseInstallationState(s string) (InstallationState, error) {
	switch st := InstallationState(s); st {
	case InstallationActive, InstallationSuspended, InstallationRevoked:
		return st, nil
	}
	return "", fmt.Errorf("%w: state %q", ErrInvalidInstallation, s)
}

// Account types GitHub reports.
const (
	AccountUser         = "User"
	AccountOrganization = "Organization"
)

// Limits, as GitHub bounds them.
const (
	MaxAccountLogin   = 100
	MaxRepositoryName = 255
	MaxBranchName     = 255
	// MaxConnectionPath bounds a monorepo path.
	MaxConnectionPath = 512
)

// Installation is the Glossa App installed on one GitHub account, and
// the tenant that claimed it (RFC 0004 §6.1). An installation maps to
// exactly one tenant.
type Installation struct {
	ID uuid.UUID
	// GitHubID is GitHub's own installation ID.
	GitHubID     int64
	AccountID    int64
	AccountLogin string
	AccountType  string
	State        InstallationState
	ConnectedBy  string
	ConnectedAt  time.Time
	UpdatedAt    time.Time
}

// InstallationInput is what the callback learned from GitHub through
// the person's one-time OAuth token.
type InstallationInput struct {
	GitHubID     int64
	AccountID    int64
	AccountLogin string
	AccountType  string
}

func (in InstallationInput) validate() error {
	switch {
	case in.GitHubID <= 0:
		return fmt.Errorf("%w: installation id %d", ErrInvalidInstallation, in.GitHubID)
	case in.AccountID <= 0:
		return fmt.Errorf("%w: account id %d", ErrInvalidInstallation, in.AccountID)
	case !textWithin(in.AccountLogin, 1, MaxAccountLogin):
		return fmt.Errorf("%w: account login must be 1–%d characters", ErrInvalidInstallation, MaxAccountLogin)
	case in.AccountType != AccountUser && in.AccountType != AccountOrganization:
		return fmt.Errorf("%w: account type %q", ErrInvalidInstallation, in.AccountType)
	}
	return nil
}

// NewInstallation records a verified installation for the acting tenant.
func NewInstallation(in InstallationInput, by string, now time.Time) (Installation, error) {
	if err := in.validate(); err != nil {
		return Installation{}, err
	}
	return Installation{
		ID: uuid.Must(uuid.NewV7()), GitHubID: in.GitHubID, AccountID: in.AccountID, AccountLogin: in.AccountLogin,
		AccountType: in.AccountType, State: InstallationActive, ConnectedBy: by, ConnectedAt: now, UpdatedAt: now,
	}, nil
}

// Usable reports whether GitHub calls on this installation can succeed.
func (i Installation) Usable() bool { return i.State == InstallationActive }

// GitConnection ties an installation's repository — by numeric ID, so a
// rename does not break it — to one project and application, with the
// repository's default branch and an optional monorepo path. One
// repository feeds several projects, one per path (RFC 0004 §6.1).
type GitConnection struct {
	ID uuid.UUID
	// InstallationID is Glossa's installation row, not GitHub's ID.
	InstallationID uuid.UUID
	RepositoryID   int64
	// RepositoryName is a label for Studio ("acme/shop"), refreshed from
	// GitHub. Nothing is ever keyed on it.
	RepositoryName string
	ProjectID      uuid.UUID
	ApplicationID  uuid.UUID
	DefaultBranch  string
	// Path is the subdirectory this connection covers ("": the whole
	// repository), normalized without leading or trailing slashes.
	Path      string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConnectionInput is a Git connection as asked for.
type ConnectionInput struct {
	RepositoryID   int64
	RepositoryName string
	ProjectID      uuid.UUID
	ApplicationID  uuid.UUID
	DefaultBranch  string
	Path           string
}

// pathPattern is what a monorepo path may look like once normalized:
// slash-separated segments, no traversal, no backslashes.
var pathPattern = regexp.MustCompile(`^[^/\\]+(/[^/\\]+)*$`)

// NormalizeConnectionPath trims the slashes and whitespace around a
// monorepo path and refuses traversal. "" and "." are the repository
// root, which is what an ordinary single-project repository uses.
func NormalizeConnectionPath(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" || p == "." {
		return "", nil
	}
	if len(p) > MaxConnectionPath || !pathPattern.MatchString(p) {
		return "", fmt.Errorf("%w: path %q is not a repository subdirectory", ErrInvalidConnection, p)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: path %q walks out of the repository", ErrInvalidConnection, p)
		}
	}
	return p, nil
}

func (in *ConnectionInput) validate() error {
	path, err := NormalizeConnectionPath(in.Path)
	if err != nil {
		return err
	}
	in.Path = path
	in.RepositoryName = strings.TrimSpace(in.RepositoryName)
	in.DefaultBranch = strings.TrimSpace(in.DefaultBranch)
	switch {
	case in.RepositoryID <= 0:
		return fmt.Errorf("%w: repository id %d", ErrInvalidConnection, in.RepositoryID)
	case in.ProjectID == uuid.Nil:
		return fmt.Errorf("%w: a project is required", ErrInvalidConnection)
	case in.ApplicationID == uuid.Nil:
		return fmt.Errorf("%w: an application is required", ErrInvalidConnection)
	case !textWithin(in.DefaultBranch, 1, MaxBranchName):
		return fmt.Errorf("%w: default branch must be 1–%d characters", ErrInvalidConnection, MaxBranchName)
	case len(in.RepositoryName) > MaxRepositoryName:
		return fmt.Errorf("%w: repository name must be at most %d characters", ErrInvalidConnection, MaxRepositoryName)
	}
	return nil
}

// NewGitConnection validates a connection on an installation.
func NewGitConnection(inst Installation, in ConnectionInput, by string, now time.Time) (GitConnection, error) {
	if inst.State == InstallationRevoked {
		return GitConnection{}, ErrInstallationRevoked
	}
	if err := in.validate(); err != nil {
		return GitConnection{}, err
	}
	return GitConnection{
		ID: uuid.Must(uuid.NewV7()), InstallationID: inst.ID, RepositoryID: in.RepositoryID,
		RepositoryName: in.RepositoryName, ProjectID: in.ProjectID, ApplicationID: in.ApplicationID,
		DefaultBranch: in.DefaultBranch, Path: in.Path, CreatedBy: by, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// Change applies an edit to a connection. The repository and the
// installation are fixed: moving a connection to another repository is
// a different connection, so it is created and the old one removed.
func (c *GitConnection) Change(in ConnectionInput, now time.Time) error {
	in.RepositoryID = c.RepositoryID
	if err := in.validate(); err != nil {
		return err
	}
	c.ProjectID, c.ApplicationID = in.ProjectID, in.ApplicationID
	c.DefaultBranch, c.Path, c.UpdatedAt = in.DefaultBranch, in.Path, now
	return nil
}

func textWithin(s string, lo, hi int) bool {
	n := len([]rune(s))
	return n >= lo && n <= hi
}
