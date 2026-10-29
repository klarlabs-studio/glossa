package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// CI's credential, without a stored secret (RFC 0004 §6.3).
//
// A GitHub Actions run asks GitHub for an OIDC ID token with audience
// "glossa" and presents it here. The ID token is the credential — the
// endpoint takes no other — and everything the exchange decides comes
// from claims GitHub signed:
//
//   - the verifier pins the issuer, the audience, the algorithms and
//     the clock, so a token meant for another service, another issuer
//     or another hour is not a token here;
//   - the repository is matched on `repository_id`, GitHub's immutable
//     number. A name can be renamed, deleted and re-registered by a
//     stranger; a number cannot, so a policy keyed on the name could be
//     inherited. Nothing here ever reads `repository`.
//
// A fork's pull request gets no ID token from GitHub at all, so there is
// nothing to present and nothing to refuse: its Glossa check completes
// as `neutral` instead (RFC 0004 §6.3, §6.4). The claim checks above are
// what make that safe rather than merely true — a fork that somehow held
// a token would still be minting for its own repository's connections,
// which are not the upstream's.

// WorkflowClaims is a verified GitHub Actions ID token, reduced to what
// the exchange acts on. The verifier adapter produces it; nothing in
// the application layer parses a JWT.
type WorkflowClaims = domain.WorkflowRun

// IDTokenVerifier verifies a GitHub Actions ID token against the
// issuer's published keys. One verifier serves the process: it caches
// the key set, refetches on an unknown kid and serves a stale set for a
// bounded time when the issuer is unreachable, so a verifier per request
// would throw all of that away and hammer GitHub besides.
//
// Every failure is ErrInvalidIDToken. The verifier may wrap detail for
// the log; the caller is told one thing.
type IDTokenVerifier interface {
	Verify(ctx context.Context, idToken string) (WorkflowClaims, error)
}

// RepositoryConnection is one Git connection as the exchange needs it:
// which tenant and project a repository feeds, at which path. It comes
// from Integration, across tenants, because the caller has no tenant
// yet — proving it is a repository is how it gets one.
type RepositoryConnection struct {
	Tenant tenancy.ID
	// Project is Catalog's project id.
	Project uuid.UUID
	// Application is the project's application this connection covers.
	Application uuid.UUID
	// Path is the monorepo subdirectory ("" for the whole repository).
	// It is how a run tells one of a monorepo's projects from another.
	Path string
}

// GitRepositories resolves a repository to the projects it feeds. It is
// Integration's Git connections (RFC 0004 §6.1) seen from Identity, and
// it is a cross-tenant read on purpose: an exchange has no tenant until
// the repository names one.
type GitRepositories interface {
	// ConnectionsForRepository lists a repository's connections, one per
	// monorepo path, across tenants. An empty result is not an error.
	ConnectionsForRepository(ctx context.Context, repositoryID int64) ([]RepositoryConnection, error)
}

// CIMetrics counts the exchanges of RFC 0004 §11. Implementations must
// be safe for concurrent use and must not block.
type CIMetrics interface {
	// Exchanged counts one exchange by outcome: "minted",
	// "invalid_id_token", "repository_not_connected",
	// "ambiguous_project", "project_not_connected" or "error".
	Exchanged(outcome string)
}

// NoCIMetrics records nothing.
type NoCIMetrics struct{}

// Exchanged implements CIMetrics.
func (NoCIMetrics) Exchanged(string) {}

// GitHubOIDC is the exchange's collaborators. A deployment without a
// GitHub App configures none, and the endpoint then refuses every
// exchange with ErrGitHubOIDCUnavailable.
type GitHubOIDC struct {
	// Verifier is the one process-wide verifier.
	Verifier IDTokenVerifier
	// Repositories resolves repository_id to its Git connections.
	Repositories GitRepositories
	// Metrics counts exchanges by outcome; nil means none.
	Metrics CIMetrics
}

// SetGitHubOIDC enables the GitHub Actions exchange. The composition
// root calls it once, after Integration is built: Identity is
// constructed first, and the exchange is the one thing it needs from a
// later context. Passing a zero GitHubOIDC turns the exchange off
// again, which is what a deployment with no GitHub App leaves it as.
func (s *Service) SetGitHubOIDC(g GitHubOIDC) {
	if g.Metrics == nil {
		g.Metrics = NoCIMetrics{}
	}
	s.oidc = g
}

// MintedCIToken is a fresh CI token and its one-time secret.
type MintedCIToken struct {
	Token  domain.CIToken
	Secret domain.TokenSecret
}

// ExchangeGitHubOIDC turns a GitHub Actions ID token into a Glossa CI
// token (RFC 0004 §6.3).
//
// project is optional and names a Catalog project id. It is needed only
// when the repository feeds several projects — a monorepo with one
// connection per path — because a token is minted for exactly one of
// them and guessing which would be the wrong kind of helpful.
//
// It is deliberately unauthenticated: the ID token is the credential.
// Nothing about the caller's network position, headers or claimed
// identity is consulted.
func (s *Service) ExchangeGitHubOIDC(ctx context.Context, idToken, project string) (MintedCIToken, error) {
	if s.oidc.Verifier == nil || s.oidc.Repositories == nil {
		return MintedCIToken{}, domain.ErrGitHubOIDCUnavailable
	}
	out, outcome, err := s.exchange(ctx, idToken, project)
	s.oidc.Metrics.Exchanged(outcome)
	return out, err
}

// exchange is ExchangeGitHubOIDC's body, split out so every return
// records an outcome in one place.
func (s *Service) exchange(ctx context.Context, idToken, project string) (MintedCIToken, string, error) {
	run, err := s.oidc.Verifier.Verify(ctx, idToken)
	if err != nil {
		// The detail says which check failed; the caller is told only
		// that the token did not verify.
		s.logger.InfoContext(ctx, "identity: a GitHub Actions ID token did not verify", slog.Any("error", err))
		return MintedCIToken{}, "invalid_id_token", domain.ErrInvalidIDToken
	}
	if !run.Valid() {
		return MintedCIToken{}, "invalid_id_token", domain.ErrInvalidWorkflowRun
	}
	conns, err := s.oidc.Repositories.ConnectionsForRepository(ctx, run.RepositoryID)
	if err != nil {
		return MintedCIToken{}, "error", fmt.Errorf("identity: resolve repository %d: %w", run.RepositoryID, err)
	}
	conn, outcome, err := chooseConnection(conns, project)
	if err != nil {
		return MintedCIToken{}, outcome, err
	}
	tok, secret, err := domain.NewCIToken(conn.Tenant, domain.ProjectRef(conn.Project), run, s.now())
	if err != nil {
		return MintedCIToken{}, "error", err
	}
	// The insert is tenant-scoped, like every other write: the tenant
	// comes from the connection the repository proved, never from the
	// request.
	err = s.tx.InTenant(tenancy.ContextWithTenant(ctx, conn.Tenant), func(ctx context.Context, st TenantStore) error {
		return st.InsertCIToken(ctx, tok)
	})
	if err != nil {
		return MintedCIToken{}, "error", err
	}
	s.logger.InfoContext(ctx, "identity: minted a CI token for a GitHub Actions run",
		slog.String("ci_token_id", tok.ID.String()), slog.String("tenant_id", conn.Tenant.String()),
		slog.String("project_id", conn.Project.String()), slog.Int64("repository_id", run.RepositoryID),
		slog.String("repository", run.Repository), slog.String("ref", run.Ref),
		slog.String("workflow_ref", run.WorkflowRef), slog.String("run_id", run.RunID))
	return MintedCIToken{Token: tok, Secret: secret}, "minted", nil
}

// AmbiguousProjectError says which projects a repository feeds, so the
// caller can name one. It matches domain.ErrAmbiguousProject.
type AmbiguousProjectError struct {
	Projects []RepositoryConnection
}

func (e *AmbiguousProjectError) Error() string { return domain.ErrAmbiguousProject.Error() }

// Is makes errors.Is(err, domain.ErrAmbiguousProject) true.
func (e *AmbiguousProjectError) Is(target error) bool { return target == domain.ErrAmbiguousProject }

// chooseConnection picks the one connection a token is minted for.
//
// No connection at all is ErrRepositoryNotConnected — the same answer
// whether the repository is unknown to Glossa or connected to someone
// else's tenant, so an exchange never reports on a repository the caller
// does not already control.
func chooseConnection(conns []RepositoryConnection, project string) (RepositoryConnection, string, error) {
	switch {
	case len(conns) == 0:
		return RepositoryConnection{}, "repository_not_connected", domain.ErrRepositoryNotConnected
	case project == "":
		if len(conns) > 1 {
			// The run has already proved it is this repository, so
			// listing that repository's own projects tells it nothing
			// it could not ask GitHub for — and without the list there
			// is nothing to put in `--project`.
			sorted := slices.Clone(conns)
			slices.SortFunc(sorted, func(a, b RepositoryConnection) int {
				return strings.Compare(a.Project.String(), b.Project.String())
			})
			return RepositoryConnection{}, "ambiguous_project", &AmbiguousProjectError{Projects: sorted}
		}
		return conns[0], "", nil
	}
	want, err := domain.ParseProjectRef(project)
	if err != nil {
		return RepositoryConnection{}, "project_not_connected", domain.ErrRepositoryNotConnected
	}
	i := slices.IndexFunc(conns, func(c RepositoryConnection) bool { return c.Project == want.UUID() })
	if i < 0 {
		// The repository is connected, but not to that project. It is
		// the same refusal as an unconnected repository: a run learns
		// nothing about projects it cannot reach.
		return RepositoryConnection{}, "project_not_connected", domain.ErrRepositoryNotConnected
	}
	return conns[i], "", nil
}

// AuthenticateCIToken resolves a CI token to its record, refusing one
// that is malformed, unknown or past its half hour. Its project binding
// is enforced at the HTTP edge, like an in-context grant's: the record
// carries the project, and a route naming another one is refused.
func (s *Service) AuthenticateCIToken(ctx context.Context, bearer string) (Authn, error) {
	secret, err := domain.ParseCISecret(bearer)
	if err != nil {
		return Authn{}, ErrUnauthenticated
	}
	now := s.now()
	var rec CITokenRecord
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		if rec, err = st.CITokenByHash(ctx, secret.Hash()); err != nil {
			return err
		}
		if !now.Before(rec.ExpiresAt) {
			return ErrUnauthenticated
		}
		return st.TouchCIToken(ctx, rec.ID, now)
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnauthenticated) {
		return Authn{}, ErrUnauthenticated
	}
	if err != nil {
		return Authn{}, err
	}
	return Authn{Actor: domain.Actor{Kind: domain.ActorToken, ID: rec.ID.UUID()}, CI: &rec}, nil
}

// PurgeExpiredCITokens drops CI tokens past their half hour. Nothing
// depends on it for safety — authentication checks expires_at on every
// request — it only keeps the table small.
func (s *Service) PurgeExpiredCITokens(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		n, err = st.PurgeExpiredCITokens(ctx, before)
		return err
	})
	return n, err
}
