//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/ghoidc"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/ghoidc/ghoidctest"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The GitHub Actions OIDC exchange against a real verifier and a real
// database (RFC 0004 §6.3). A CI job proves it is a repository, and the
// repository's Git connection is what says which tenant and project it
// may act on.

const ciRepositoryID = int64(9001)

// fakeRepositories stands in for Integration's Git connections.
type fakeRepositories struct {
	conns map[int64][]app.RepositoryConnection
	err   error
}

func (f *fakeRepositories) ConnectionsForRepository(_ context.Context, repo int64) ([]app.RepositoryConnection, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.conns[repo], nil
}

// ciFixture is a harness with the exchange enabled, a tenant, and one
// repository connected to one project of it.
type ciFixture struct {
	*harness
	issuer   *ghoidctest.Issuer
	repos    *fakeRepositories
	metrics  *countingMetrics
	tenant   tenancy.ID
	project  uuid.UUID
	project2 uuid.UUID
}

type countingMetrics struct{ byOutcome map[string]int }

func (m *countingMetrics) Exchanged(outcome string) { m.byOutcome[outcome]++ }

func newCIFixture(t *testing.T) *ciFixture {
	t.Helper()
	h := newHarness(t)
	in := h.signUp(t, "ci@example.com")
	f := &ciFixture{
		harness: h,
		issuer:  ghoidctest.New(t),
		repos:   &fakeRepositories{conns: map[int64][]app.RepositoryConnection{}},
		metrics: &countingMetrics{byOutcome: map[string]int{}},
		tenant:  in.Person.IndividualTenantID,
		// Catalog owns projects; Identity keeps only the id, so a test
		// needs no project row.
		project:  uuid.MustParse("0192a1b2-0000-7000-8000-0000000000a1"),
		project2: uuid.MustParse("0192a1b2-0000-7000-8000-0000000000a2"),
	}
	f.issuer.SetClock(h.clock.Now)
	verifier, err := ghoidc.New(ghoidc.Config{
		Issuer: f.issuer.URL, Audience: "glossa",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.repos.conns[ciRepositoryID] = []app.RepositoryConnection{
		{Tenant: f.tenant, Project: f.project, Application: uuid.New()},
	}
	h.svc.SetGitHubOIDC(app.GitHubOIDC{Verifier: verifier, Repositories: f.repos, Metrics: f.metrics})
	return f
}

// token signs an ID token for the connected repository.
func (f *ciFixture) token(t *testing.T) string {
	t.Helper()
	return f.issuer.Token(t, f.claims())
}

func (f *ciFixture) claims() ghoidctest.Claims {
	return ghoidctest.Claims{
		RepositoryID: ciRepositoryID, RepositoryOwnerID: 77, Repository: "acme/shop",
		Ref: "refs/pull/42/merge", SHA: "cafebabe1234", EventName: "pull_request",
		JobWorkflowRef: "acme/shop/.github/workflows/glossa.yml@refs/heads/main",
		RunID:          "77889900", RunnerEnvironment: "github-hosted",
	}
}

func TestExchangeMintsATokenForTheConnectedProject(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()

	m, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), "")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if m.Token.TenantID != f.tenant || m.Token.ProjectID.UUID() != f.project {
		t.Errorf("minted for tenant %s project %s", m.Token.TenantID, m.Token.ProjectID)
	}
	if want := f.clock.Now().Add(30 * time.Minute); !m.Token.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %s, want %s (30 minutes)", m.Token.ExpiresAt, want)
	}
	if got := m.Secret.String(); len(got) != len(domain.CITokenPrefix)+43 ||
		got[:len(domain.CITokenPrefix)] != domain.CITokenPrefix {
		t.Errorf("secret = %q, want a glossa_ci_ credential", got)
	}
	// The run is the audit trail: no person acted, so the workflow is
	// what the row remembers.
	if n := count(t, `SELECT count(*) FROM identity_ci_tokens
		WHERE tenant_id = $1 AND repository_id = $2 AND repository = 'acme/shop'
		  AND git_ref = 'refs/pull/42/merge' AND run_id = '77889900'
		  AND workflow_ref = 'acme/shop/.github/workflows/glossa.yml@refs/heads/main'`,
		f.tenant.UUID(), ciRepositoryID); n != 1 {
		t.Errorf("audit rows = %d, want the run recorded", n)
	}
	// The secret is never stored, only its hash.
	if n := count(t, "SELECT count(*) FROM identity_ci_tokens WHERE token_hash = $1", m.Secret.Hash()); n != 1 {
		t.Error("the token is not stored by its hash")
	}
	if f.metrics.byOutcome["minted"] != 1 {
		t.Errorf("metrics = %v, want one minted", f.metrics.byOutcome)
	}
}

// What the minted token may do, and the whole of it. A generous CI
// credential would be the point of the exchange lost.
func TestTheMintedTokenAllowsExactlyTheCICeiling(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()

	m, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), "")
	if err != nil {
		t.Fatal(err)
	}
	authn, err := f.svc.AuthenticateCIToken(ctx, m.Secret.String())
	if err != nil {
		t.Fatalf("authenticate the minted token: %v", err)
	}
	if authn.CI == nil || authn.CI.Project.UUID() != f.project {
		t.Fatalf("authn = %+v", authn)
	}
	p, err := f.svc.Authorize(ctx, authn, f.tenant)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	allowed := p.Grant.Permissions()
	if len(allowed) != 2 || allowed[0] != domain.PermCatalogRead || allowed[1] != domain.PermCatalogWrite {
		t.Fatalf("permissions = %v, want exactly [catalog.read catalog.write]", allowed)
	}
	// Everything a CI run has no business doing, spelled out: the CI job
	// pushes messages, usages and captures and nothing else.
	for _, perm := range []domain.Permission{
		domain.PermTranslationsRead, domain.PermTranslationsWrite, domain.PermTranslationsReview,
		domain.PermIntegrationImport, domain.PermIntegrationManage, domain.PermIntegrationRead,
		domain.PermKnowledgeRead, domain.PermKnowledgeWrite,
		domain.PermIntelligenceRead, domain.PermIntelligenceTranslate, domain.PermIntelligenceManage,
		domain.PermReleasesRead, domain.PermReleasesPublish,
		domain.PermTokensRead, domain.PermTokensManage,
		domain.PermMembersRead, domain.PermMembersManage, domain.PermOwnersManage,
		domain.PermTenantRead, domain.PermTenantManage,
	} {
		if p.Grant.Allows(perm) {
			t.Errorf("a CI token allows %s", perm)
		}
	}
	// And only in its own tenant.
	other := tenancy.ID(uuid.New())
	if _, err := f.svc.Authorize(ctx, authn, other); !errors.Is(err, app.ErrForbidden) {
		t.Errorf("authorize in another tenant = %v, want ErrForbidden", err)
	}
}

func TestTheMintedTokenDiesWithTheClock(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()

	m, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), "")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(29 * time.Minute)
	if _, err := f.svc.AuthenticateCIToken(ctx, m.Secret.String()); err != nil {
		t.Fatalf("at 29 minutes: %v", err)
	}
	f.clock.Advance(2 * time.Minute)
	if _, err := f.svc.AuthenticateCIToken(ctx, m.Secret.String()); !errors.Is(err, app.ErrUnauthenticated) {
		t.Fatalf("at 31 minutes: %v, want ErrUnauthenticated", err)
	}
	// The sweep only keeps the table small; expiry is already enforced.
	n, err := f.svc.PurgeExpiredCITokens(ctx, f.clock.Now())
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
}

// A CI token is its own credential: it is not an API token and not an
// in-context grant, and the parsers never confuse the three.
func TestACITokenIsNotAnAPIToken(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()

	m, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthenticateToken(ctx, m.Secret.String()); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("a CI token authenticated as an API token: %v", err)
	}
	if _, err := f.svc.AuthenticateInContextGrant(ctx, m.Secret.String(), "https://preview.example"); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("a CI token authenticated as an in-context grant: %v", err)
	}
}

func TestExchangeRefusesTokensThatDoNotVerify(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()
	now := f.clock.Now()

	wrongAudience := f.claims()
	wrongAudience.Audience = "https://github.com/acme"
	wrongIssuer := f.claims()
	wrongIssuer.Issuer = "https://token.actions.githubusercontent.com"
	expired := f.claims()
	expired.IssuedAt, expired.Expiry = now.Add(-2*time.Hour), now.Add(-90*time.Minute)

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"the wrong audience", f.issuer.Token(t, wrongAudience)},
		{"the wrong issuer", f.issuer.Token(t, wrongIssuer)},
		{"an expired token", f.issuer.Token(t, expired)},
		{"a token signed by the wrong key", f.issuer.TokenFromAnotherKey(t, f.claims())},
		{"no token at all", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.ExchangeGitHubOIDC(ctx, tc.token, ""); !errors.Is(err, domain.ErrInvalidIDToken) {
				t.Fatalf("err = %v, want ErrInvalidIDToken", err)
			}
		})
	}
	if n := count(t, "SELECT count(*) FROM identity_ci_tokens"); n != 0 {
		t.Errorf("CI tokens = %d after five refusals, want 0", n)
	}
	if got := f.metrics.byOutcome["invalid_id_token"]; got != 5 {
		t.Errorf("invalid_id_token metric = %d, want 5", got)
	}
}

// A repository nobody connected, and a repository connected to someone
// else's project, get the same answer: a verified run learns nothing
// about projects it cannot reach.
func TestExchangeRefusesAnUnconnectedRepository(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()

	other := f.claims()
	other.RepositoryID = 4242
	if _, err := f.svc.ExchangeGitHubOIDC(ctx, f.issuer.Token(t, other), ""); !errors.Is(err, domain.ErrRepositoryNotConnected) {
		t.Fatalf("unknown repository = %v, want ErrRepositoryNotConnected", err)
	}
	stranger := uuid.New().String()
	if _, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), stranger); !errors.Is(err, domain.ErrRepositoryNotConnected) {
		t.Fatalf("a project the repository does not feed = %v, want ErrRepositoryNotConnected", err)
	}
	if _, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), "not-a-uuid"); !errors.Is(err, domain.ErrRepositoryNotConnected) {
		t.Fatalf("a malformed project = %v, want ErrRepositoryNotConnected", err)
	}
	if n := count(t, "SELECT count(*) FROM identity_ci_tokens"); n != 0 {
		t.Errorf("CI tokens = %d, want 0", n)
	}
}

// A monorepo: one repository, two projects, one connection per path.
// A token is for exactly one project, so the run has to say which.
func TestAmbiguousRepositoryAsksTheRunToNameAProject(t *testing.T) {
	f := newCIFixture(t)
	ctx := context.Background()
	f.repos.conns[ciRepositoryID] = []app.RepositoryConnection{
		{Tenant: f.tenant, Project: f.project, Path: "apps/web"},
		{Tenant: f.tenant, Project: f.project2, Path: "apps/admin"},
	}

	_, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), "")
	if !errors.Is(err, domain.ErrAmbiguousProject) {
		t.Fatalf("err = %v, want ErrAmbiguousProject", err)
	}
	var ambiguous *app.AmbiguousProjectError
	if !errors.As(err, &ambiguous) || len(ambiguous.Projects) != 2 {
		t.Fatalf("the refusal names no projects: %v", err)
	}
	// Naming one of them works, and mints for that one only.
	m, err := f.svc.ExchangeGitHubOIDC(ctx, f.token(t), f.project2.String())
	if err != nil {
		t.Fatalf("naming a project: %v", err)
	}
	if m.Token.ProjectID.UUID() != f.project2 {
		t.Errorf("minted for %s, want %s", m.Token.ProjectID, f.project2)
	}
	if f.metrics.byOutcome["ambiguous_project"] != 1 || f.metrics.byOutcome["minted"] != 1 {
		t.Errorf("metrics = %v", f.metrics.byOutcome)
	}
}

// A deployment with no GitHub App cannot exchange anything, and says so
// rather than pretending the token was bad.
func TestExchangeIsOffWithoutAGitHubApp(t *testing.T) {
	f := newCIFixture(t)
	f.svc.SetGitHubOIDC(app.GitHubOIDC{})

	_, err := f.svc.ExchangeGitHubOIDC(context.Background(), f.token(t), "")
	if !errors.Is(err, domain.ErrGitHubOIDCUnavailable) {
		t.Fatalf("err = %v, want ErrGitHubOIDCUnavailable", err)
	}
}
