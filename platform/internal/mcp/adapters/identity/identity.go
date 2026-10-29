// Package identity implements MCP's Authenticator over the Identity
// context's application service.
//
// MCP mints no credential (RFC 0005 decision 8). A session presents an
// existing tenant API token as a bearer, and this adapter resolves it
// through exactly the calls Identity's HTTP guard makes —
// AuthenticateToken, then Authorize in the token's own tenant — so the
// grant an MCP session holds is the grant the REST API would have
// produced for the same token, permission for permission.
//
// What it adds is a narrowing. Two other bearer credentials authenticate
// against the same endpoint today, and MCP refuses both:
//
//   - a CI token (glossa_ci_…) is minted for one workflow run, bound to
//     one project and expires in half an hour;
//   - an in-context grant (glossa_ctx_…) is a person's rights cut down
//     to one project and one browser origin, for fifteen minutes.
//
// Each is deliberately small. Lending either to a long-lived agent
// session would widen it past what it was minted for, so both are
// refused at connect rather than quietly accepted (RFC 0005 §7.2).
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identitydomain "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Tokens is the part of Identity this adapter needs. Taking it as an
// interface keeps the MCP context testable without a database;
// *identityapp.Service satisfies it.
type Tokens interface {
	AuthenticateToken(ctx context.Context, bearer string) (identityapp.Authn, error)
	Authorize(ctx context.Context, a identityapp.Authn, tenant tenancy.ID) (authz.Principal, error)
}

// unauthenticated is the one answer every unusable credential gets:
// unknown, malformed, revoked, expired and "belongs to a tenant that no
// longer wants it" are indistinguishable on the wire, so a caller
// learns nothing by probing.
var unauthenticated = fmt.Errorf(
	"%w: present a tenant API token (%s…) as the bearer token",
	domain.ErrUnauthenticated, identitydomain.TokenPrefix)

// Authenticator resolves a bearer credential to an MCP caller.
type Authenticator struct{ tokens Tokens }

// New returns the authenticator over Identity's service.
func New(tokens Tokens) *Authenticator { return &Authenticator{tokens: tokens} }

var _ app.Authenticator = (*Authenticator)(nil)

// Authenticate implements app.Authenticator.
func (a *Authenticator) Authenticate(ctx context.Context, bearer string) (app.Caller, error) {
	cred := strings.TrimSpace(bearer)
	if cred == "" {
		return app.Caller{}, unauthenticated
	}
	// The prefix says which credential this is, exactly as Identity's
	// guard reads it, so a refusal is about what the credential *is* and
	// never about whether it happens to be valid. The message names the
	// credential, because an agent that is told what it presented fixes
	// its configuration, and one that is told "unauthorized" retries.
	switch {
	case identitydomain.IsCISecret(cred):
		return app.Caller{}, fmt.Errorf("%w: a CI token is minted for one workflow run; "+
			"present a tenant API token (%s…) instead", domain.ErrCredentialNotAccepted, identitydomain.TokenPrefix)
	case identitydomain.IsInContextSecret(cred):
		return app.Caller{}, fmt.Errorf("%w: an in-context grant is minted for one project and one browser "+
			"origin; present a tenant API token (%s…) instead", domain.ErrCredentialNotAccepted, identitydomain.TokenPrefix)
	}
	authn, err := a.tokens.AuthenticateToken(ctx, cred)
	if errors.Is(err, identityapp.ErrUnauthenticated) {
		return app.Caller{}, unauthenticated
	}
	if err != nil {
		return app.Caller{}, err
	}
	if authn.Token == nil {
		return app.Caller{}, unauthenticated
	}
	// The token's tenant is the session's tenant. Nothing else is
	// offered, so no tool can take one.
	p, err := a.tokens.Authorize(ctx, authn, authn.Token.Tenant)
	if errors.Is(err, identityapp.ErrForbidden) {
		return app.Caller{}, unauthenticated
	}
	if err != nil {
		return app.Caller{}, err
	}
	return app.Caller{
		Tenant: authn.Token.Tenant, Token: authn.Token.ID, Scopes: authn.Token.Scopes, Principal: p,
	}, nil
}
