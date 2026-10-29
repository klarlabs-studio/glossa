package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// CI's credential (RFC 0004 §6.3).
//
// A GitHub Actions run holds no Glossa secret. It asks GitHub for an
// OIDC ID token with audience "glossa", and Glossa exchanges that for a
// bearer token of its own. The exchange is what ties a run to a tenant:
// the ID token's repository_id — GitHub's own immutable number, never
// the repository's name — must match a Git connection, and the minted
// token may act on that connection's project and nowhere else.
//
// The token is deliberately small: half an hour, one project, and the
// two permissions the documented CI commands need. It cannot be
// refreshed (the next job asks GitHub for a new ID token), it cannot be
// widened, and it dies with the clock.

// CITokenPrefix starts every CI token secret, so a leaked credential's
// kind is obvious from the string alone and secret scanners can tell it
// from an API token or an in-context grant: the full pattern is
// glossa_ci_[A-Za-z0-9_-]{43}.
const CITokenPrefix = "glossa_ci_"

// CITokenTTL is how long a CI token lives (RFC 0004 §6.3). It outlasts
// a normal CI run and nothing more; there is no refresh, because the
// next run asks GitHub for a fresh ID token.
const CITokenTTL = 30 * time.Minute

// GitHubActionsAudience is the `aud` a run must ask GitHub for. The
// default audience GitHub issues is the repository owner's URL, which
// any other service could also be handed, so Glossa insists on its own
// name.
const GitHubActionsAudience = "glossa"

// GitHubActionsIssuer is the only issuer github.com runs come from. A
// GitHub Enterprise Server deployment configures its own.
const GitHubActionsIssuer = "https://token.actions.githubusercontent.com"

// Bounds on the workflow facts kept for the audit trail. They are
// attacker-influenced strings from a verified token, so they are capped
// rather than trusted.
const (
	maxRepositoryName = 255
	maxGitRef         = 255
	maxWorkflowRef    = 512
	maxEventName      = 64
	maxRunID          = 64
	maxRunnerEnv      = 64
	maxCommitSHA      = 64
)

// NewCISecret returns a fresh CI token secret.
func NewCISecret() (TokenSecret, error) { return newSecret(CITokenPrefix) }

// ParseCISecret validates an inbound CI token's shape before anything is
// hashed or looked up. It refuses an API token and an in-context grant:
// the three credentials are never interchangeable.
func ParseCISecret(s string) (TokenSecret, error) { return parseSecret(CITokenPrefix, s) }

// IsCISecret reports whether a bearer credential claims to be a CI
// token, so the HTTP edge routes it to the right verification instead of
// trying each table in turn.
func IsCISecret(s string) bool { return strings.HasPrefix(s, CITokenPrefix) }

// CIPermissions is what a CI token may do, and the whole of it
// (RFC 0004 §6.3). The documented CI job is:
//
//	glossa push --branch --pr      source messages, the branch, its PR
//	glossa extract --upload        the same upsert, from Go and templates
//	glossa context push            a usages build
//	glossa capture --upload        screenshots of a preview deployment
//	glossa preview register        where CI deployed the branch
//
// Every one of them reads the project and its branch (catalog.read) and
// writes messages, usages or captures (catalog.write) — Context's
// ingest and capture upload check catalog.write too. Nothing here
// imports translations, edits terminology, publishes a release, asks an
// AI provider for anything, or reads the tenant's members or tokens.
// `glossa push --translations` therefore needs an API token, on purpose:
// a repository's CI proving it is that repository says nothing about
// who may write a locale.
func CIPermissions() []Permission {
	return []Permission{PermCatalogRead, PermCatalogWrite}
}

// CITokenID identifies a minted CI token.
type CITokenID uuid.UUID

// NewCITokenID returns a fresh, time-ordered ID.
func NewCITokenID() CITokenID { return CITokenID(newV7()) }

// ParseCITokenID parses the canonical string form.
func ParseCITokenID(s string) (CITokenID, error) {
	u, err := parseID(s)
	return CITokenID(u), err
}

func (id CITokenID) String() string  { return uuid.UUID(id).String() }
func (id CITokenID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id CITokenID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }

// WorkflowRun is the run a verified ID token names: the authoritative
// numeric repository, and the strings that say which workflow of it ran.
//
// RepositoryID is the only field an authorization decision rests on. A
// repository can be renamed, deleted and the name registered by someone
// else; its number cannot be taken over. The rest is for the audit
// trail and for the operator reading a log line.
type WorkflowRun struct {
	// RepositoryID is GitHub's immutable numeric repository ID
	// (the `repository_id` claim).
	RepositoryID int64
	// RepositoryOwnerID is the owning account's immutable numeric ID.
	RepositoryOwnerID int64
	// Repository is "owner/name" when the token was issued. A label.
	Repository string
	// Ref is the git ref the run is for ("refs/heads/main",
	// "refs/pull/42/merge").
	Ref string
	// SHA is the commit the run is for.
	SHA string
	// EventName is the triggering event ("push", "pull_request").
	EventName string
	// WorkflowRef is the workflow file, pinned to a ref
	// ("acme/shop/.github/workflows/ci.yml@refs/heads/main").
	WorkflowRef string
	// RunID is GitHub's workflow run ID, so a token can be traced back
	// to the run that asked for it.
	RunID string
	// RunnerEnvironment is "github-hosted" or "self-hosted".
	RunnerEnvironment string
}

// Valid reports whether a run names a usable repository and fits the
// bounds a stored audit trail keeps. A verified token that does not is
// refused rather than truncated.
func (r WorkflowRun) Valid() bool {
	return r.RepositoryID > 0 &&
		within(r.Repository, maxRepositoryName) && within(r.Ref, maxGitRef) &&
		within(r.SHA, maxCommitSHA) && within(r.EventName, maxEventName) &&
		within(r.WorkflowRef, maxWorkflowRef) && within(r.RunID, maxRunID) &&
		within(r.RunnerEnvironment, maxRunnerEnv)
}

func within(s string, max int) bool { return utf8.RuneCountInString(s) <= max }

// CIToken is the credential a GitHub Actions run holds: a short-lived
// bearer token bound to one project, allowing exactly CIPermissions,
// with the run that asked for it recorded beside it. Only its hash is
// stored; the secret is handed to the exchange's caller once.
type CIToken struct {
	ID        CITokenID
	TenantID  tenancy.ID
	ProjectID ProjectRef
	// Run is the workflow that presented the ID token: the audit trail
	// of who acted, since no person did.
	Run WorkflowRun
	// Permissions is CIPermissions as a grant, so authentication hands
	// the same shape to authz as any other credential.
	Permissions Grant
	Hash        string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// NewCIToken mints a CI token for one project of one tenant, on behalf
// of run. The caller has already verified the ID token and matched
// run.RepositoryID against a Git connection of that project; this only
// refuses what it can see for itself.
func NewCIToken(tenant tenancy.ID, project ProjectRef, run WorkflowRun, now time.Time) (CIToken, TokenSecret, error) {
	if tenant.IsZero() || project.IsZero() {
		return CIToken{}, TokenSecret{}, ErrInvalidID
	}
	if !run.Valid() {
		return CIToken{}, TokenSecret{}, ErrInvalidWorkflowRun
	}
	secret, err := NewCISecret()
	if err != nil {
		return CIToken{}, TokenSecret{}, err
	}
	return CIToken{
		ID: NewCITokenID(), TenantID: tenant, ProjectID: project, Run: run,
		Permissions: GrantOf(CIPermissions()...), Hash: secret.Hash(),
		CreatedAt: now, ExpiresAt: now.Add(CITokenTTL),
	}, secret, nil
}

// CheckUsable returns ErrTokenExpired past the token's half hour.
func (t CIToken) CheckUsable(now time.Time) error {
	if !now.Before(t.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

// Actor is who a CI token acts as. There is no person behind it, so it
// is a token actor like an API token — the run itself is recorded on
// the token's own row, which the id points at.
func (t CIToken) Actor() Actor { return Actor{Kind: ActorToken, ID: uuid.UUID(t.ID)} }
