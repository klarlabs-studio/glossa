// Package ghoidc verifies GitHub Actions ID tokens for Identity's CI
// exchange (RFC 0004 §6.3), over auth-go's oidc package.
//
// One Verifier serves the process. It discovers the issuer's jwks_uri
// once, caches the key set for its Cache-Control max-age, refetches
// (single-flight, rate-limited) when a token names an unknown kid, and
// keeps serving the last good set for MaxStale after its fetch when
// refetches fail — after which verification fails closed, so a key
// GitHub revoked cannot stay trusted just because its JWKS endpoint is
// unreachable. Building one per request would throw all of that away
// and fetch GitHub's keys on every CI job besides.
//
// Nothing here logs a token, and the errors it returns carry the
// verification detail for the server's own log only: the caller answers
// `invalid_id_token` whatever went wrong.
package ghoidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/klarlabs-studio/auth-go/oidc"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// Config is the exchange's verifier configuration. The defaults are
// github.com; a GitHub Enterprise Server deployment sets Issuer to its
// own `token.actions` host, and MaxStale to whatever its operators can
// promise about that host's availability.
type Config struct {
	// Issuer is the exact `iss` of the ID tokens. Empty means
	// domain.GitHubActionsIssuer.
	Issuer string
	// Audience is the `aud` a run must ask GitHub for. Empty means
	// domain.GitHubActionsAudience ("glossa"). GitHub's own default
	// audience — the repository owner's URL — is refused, because any
	// other service could be handed one too.
	Audience string
	// MaxStale bounds how long a cached key set keeps verifying when
	// refetches fail. Zero means auth-go's default (24 h).
	MaxStale time.Duration
	// ClockSkew is the leeway on exp, nbf and iat. Zero means auth-go's
	// default (60 s).
	ClockSkew time.Duration
}

// Verifier implements identityapp.IDTokenVerifier.
type Verifier struct {
	v        *oidc.Verifier
	issuer   string
	audience string
}

var _ identityapp.IDTokenVerifier = (*Verifier)(nil)

// New builds the process's verifier.
func New(cfg Config) (*Verifier, error) {
	issuer, audience := cfg.Issuer, cfg.Audience
	if issuer == "" {
		issuer = domain.GitHubActionsIssuer
	}
	if audience == "" {
		audience = domain.GitHubActionsAudience
	}
	// GitHubActions already pins RS256, requires sub and jti, and takes
	// the github.com issuer; only the deployment's own settings change.
	oc := oidc.GitHubActions(audience)
	oc.Issuer = issuer
	oc.MaxStale = cfg.MaxStale
	oc.ClockSkew = cfg.ClockSkew
	// An issuer on the developer's own machine may be plain http, as a
	// preview origin may (RFC 0004 §5.2): a fake issuer in a test, or a
	// local GitHub Enterprise Server stand-in. Nothing else may, and
	// this cannot be turned on for a real host — the rule is read off
	// the issuer URL, not configured.
	if host, ok := loopbackHTTP(issuer); ok {
		oc.AllowInsecureHTTPHosts = []string{host}
	}
	v, err := oidc.New(oc)
	if err != nil {
		return nil, fmt.Errorf("identity: GitHub Actions OIDC verifier: %w", err)
	}
	return &Verifier{v: v, issuer: issuer, audience: audience}, nil
}

// Issuer and Audience say what this verifier accepts, for the log line
// a deployment prints at startup.
func (v *Verifier) Issuer() string   { return v.issuer }
func (v *Verifier) Audience() string { return v.audience }

// Verify implements identityapp.IDTokenVerifier. Every failure — a bad
// signature, the wrong issuer, the wrong audience, an expired token, a
// key set that cannot be fetched — comes back wrapping
// domain.ErrInvalidIDToken, so the exchange has one thing to say and
// the log has the detail.
func (v *Verifier) Verify(ctx context.Context, idToken string) (identityapp.WorkflowClaims, error) {
	tok, err := v.v.Verify(ctx, idToken)
	if err != nil {
		return identityapp.WorkflowClaims{}, fmt.Errorf("%w: %w", domain.ErrInvalidIDToken, err)
	}
	claims, err := oidc.ParseGitHubActionsClaims(tok)
	if err != nil {
		return identityapp.WorkflowClaims{}, fmt.Errorf("%w: %w", domain.ErrInvalidIDToken, err)
	}
	return domain.WorkflowRun{
		RepositoryID:      claims.RepositoryID,
		RepositoryOwnerID: claims.RepositoryOwnerID,
		Repository:        claims.Repository,
		Ref:               claims.Ref,
		SHA:               claims.SHA,
		EventName:         claims.EventName,
		WorkflowRef:       claims.JobWorkflowRef,
		RunID:             runID(tok),
		RunnerEnvironment: claims.RunnerEnvironment,
	}, nil
}

// runID reads the `run_id` claim, which auth-go's GitHub profile does
// not parse. GitHub sends it as a JSON number, so it arrives as a
// json.Number and is kept as text: nothing computes with it, and an
// audit trail wants what GitHub wrote. A token without one is not an
// error — the field is for the operator, not for the decision.
func runID(tok *oidc.Token) string {
	switch v := tok.Raw["run_id"].(type) {
	case json.Number:
		return v.String()
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

// Unavailable reports whether err means the issuer's keys could not be
// reached at all, which is an operator's problem rather than a caller's.
func Unavailable(err error) bool { return errors.Is(err, oidc.ErrKeySetUnavailable) }

// loopbackHTTP reports whether issuer is a plain-http URL on this
// machine — localhost, 127.0.0.1, ::1 or a .localhost name — and
// returns its host. Everything else must be https.
func loopbackHTTP(issuer string) (string, bool) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "http" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	ok := host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
	return host, ok
}
