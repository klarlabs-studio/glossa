package ghoidc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/ghoidc"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/ghoidc/ghoidctest"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// The verifier stands between a CI job and a tenant's project, so these
// pin what it refuses (RFC 0004 §6.3). Each case is a token a real
// attacker could present: one for another service, one from another
// issuer, one signed by a key the issuer never published, one that has
// expired. None of them verifies, and each fails as itself — not
// because a field happened to be empty.

func newVerifier(t *testing.T, iss *ghoidctest.Issuer, audience string) *ghoidc.Verifier {
	t.Helper()
	v, err := ghoidc.New(ghoidc.Config{
		Issuer: iss.URL, Audience: audience,
	})
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}
	return v
}

func goodClaims() ghoidctest.Claims {
	return ghoidctest.Claims{
		RepositoryID: 9001, RepositoryOwnerID: 77, Repository: "acme/shop",
		Ref: "refs/heads/feature/checkout-copy", SHA: "cafebabe", EventName: "pull_request",
		JobWorkflowRef: "acme/shop/.github/workflows/glossa.yml@refs/heads/main",
		RunID:          "1234567890", RunnerEnvironment: "github-hosted",
	}
}

func TestAVerifiedTokenBecomesTheWorkflowRun(t *testing.T) {
	iss := ghoidctest.New(t)
	v := newVerifier(t, iss, "glossa")

	run, err := v.Verify(context.Background(), iss.Token(t, goodClaims()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	want := domain.WorkflowRun{
		RepositoryID: 9001, RepositoryOwnerID: 77, Repository: "acme/shop",
		Ref: "refs/heads/feature/checkout-copy", SHA: "cafebabe", EventName: "pull_request",
		WorkflowRef: "acme/shop/.github/workflows/glossa.yml@refs/heads/main",
		RunID:       "1234567890", RunnerEnvironment: "github-hosted",
	}
	if run != want {
		t.Errorf("run = %+v, want %+v", run, want)
	}
	if !run.Valid() {
		t.Error("a verified run is not Valid()")
	}
}

func TestTheVerifierRefuses(t *testing.T) {
	iss := ghoidctest.New(t)
	v := newVerifier(t, iss, "glossa")
	now := time.Now()

	forged := goodClaims()
	wrongAudience := goodClaims()
	wrongAudience.Audience = "https://github.com/acme" // GitHub's own default audience
	wrongIssuer := goodClaims()
	wrongIssuer.Issuer = "https://token.actions.githubusercontent.com"
	expired := goodClaims()
	expired.IssuedAt, expired.Expiry = now.Add(-2*time.Hour), now.Add(-90*time.Minute)
	future := goodClaims()
	future.IssuedAt, future.Expiry = now.Add(2*time.Hour), now.Add(3*time.Hour)
	noRepo := goodClaims()
	noRepo.RepositoryID = 0

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"a token for another service's audience", iss.Token(t, wrongAudience)},
		{"a token claiming another issuer", iss.Token(t, wrongIssuer)},
		{"a token signed by a key the issuer never published", iss.TokenFromAnotherKey(t, forged)},
		{"an expired token", iss.Token(t, expired)},
		{"a token from the future", iss.Token(t, future)},
		{"a token with no repository_id", iss.Token(t, noRepo)},
		{"a token whose repository_id is not a number", iss.Token(t, withExtra(goodClaims(), "repository_id", "acme/shop"))},
		{"nothing at all", ""},
		{"not a JWS", "glossa_api_not-a-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, err := v.Verify(context.Background(), tc.token)
			if !errors.Is(err, domain.ErrInvalidIDToken) {
				t.Fatalf("err = %v, want ErrInvalidIDToken", err)
			}
			if run != (domain.WorkflowRun{}) {
				t.Errorf("a refused token still produced %+v", run)
			}
		})
	}
}

// A verifier configured for another audience refuses the token this
// issuer signs for "glossa" — the mirror of the case above, so the
// audience check cannot pass by both sides being wrong together.
func TestTheAudienceIsTheVerifiersToChoose(t *testing.T) {
	iss := ghoidctest.New(t)
	v := newVerifier(t, iss, "some-other-product")

	if _, err := v.Verify(context.Background(), iss.Token(t, goodClaims())); !errors.Is(err, domain.ErrInvalidIDToken) {
		t.Fatalf("err = %v, want ErrInvalidIDToken", err)
	}
}

// The key set is fetched once and reused: a CI job per push must not
// mean a JWKS fetch per push.
func TestTheKeySetIsCachedAcrossVerifications(t *testing.T) {
	iss := ghoidctest.New(t)
	v := newVerifier(t, iss, "glossa")
	ctx := context.Background()

	for range 5 {
		if _, err := v.Verify(ctx, iss.Token(t, goodClaims())); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}
	if n := iss.KeySetFetches(); n != 1 {
		t.Errorf("JWKS fetches = %d, want 1", n)
	}
}

func withExtra(c ghoidctest.Claims, key string, value any) ghoidctest.Claims {
	c.Extra = map[string]any{key: value}
	return c
}
