package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Authn is an authenticated caller before a tenant is chosen: a person
// (from a session cookie), an API token, or an in-context grant, which
// is a person acting through the in-product editor (RFC 0004 §5.2).
type Authn struct {
	Actor domain.Actor
	// Person is set for sessions and for in-context grants.
	Person domain.PersonID
	// Token is set for API tokens.
	Token *TokenRecord
	// Grant is set for in-context grants. It already carries the tenant,
	// the project and the permissions, so nothing is looked up again
	// when the tenant is resolved.
	Grant *GrantRecord
	// CI is set for a token minted from a GitHub Actions ID token
	// (RFC 0004 §6.3). Like a grant it carries its own tenant, project
	// and permissions.
	CI *CITokenRecord
}

// Principal returns the tenantless principal, for routes outside
// /v1/tenants/{tenant}: it identifies the caller and grants nothing.
func (a Authn) Principal() authz.Principal {
	p := authz.Principal{Actor: a.Actor, Person: a.Person}
	if a.Token != nil {
		p.TokenTenant = a.Token.Tenant
	}
	if a.Grant != nil {
		p.TokenTenant = a.Grant.Tenant
	}
	if a.CI != nil {
		p.TokenTenant = a.CI.Tenant
	}
	return p
}

// AuthenticateSession resolves a session cookie to its person.
func (s *Service) AuthenticateSession(ctx context.Context, cookie string) (Authn, error) {
	return s.authenticateSessionWith(ctx, s.sessions, cookie)
}

// authenticateSessionWith resolves a raw session token through one kind
// of session: sessions (the cookie) or deviceSessions (a glossa_dev_
// bearer). Either way the caller is the person, with nothing narrowed.
func (s *Service) authenticateSessionWith(ctx context.Context, sessions *authgo.SessionService, raw string) (Authn, error) {
	tok, err := authgo.TokenFromString(raw)
	if err != nil {
		return Authn{}, ErrUnauthenticated
	}
	sess, err := sessions.Validate(ctx, tok)
	if errors.Is(err, authgo.ErrNotFound) || errors.Is(err, authgo.ErrExpired) {
		return Authn{}, ErrUnauthenticated
	}
	if err != nil {
		return Authn{}, fmt.Errorf("identity: validate session: %w", err)
	}
	person, err := personOf(sess.UserID())
	if err != nil {
		return Authn{}, fmt.Errorf("identity: session names %q: %w", sess.UserID(), err)
	}
	return Authn{Actor: domain.PersonActor(person), Person: person}, nil
}

// AuthenticateToken resolves a bearer token to its record, refusing
// malformed, unknown, revoked and expired tokens alike.
func (s *Service) AuthenticateToken(ctx context.Context, bearer string) (Authn, error) {
	secret, err := domain.ParseTokenSecret(bearer)
	if err != nil {
		return Authn{}, ErrUnauthenticated
	}
	now := s.now()
	var rec TokenRecord
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		if rec, err = st.TokenByHash(ctx, secret.Hash()); err != nil {
			return err
		}
		if err := (domain.APIToken{RevokedAt: rec.RevokedAt, ExpiresAt: rec.ExpiresAt}).CheckUsable(now); err != nil {
			return ErrUnauthenticated
		}
		return st.TouchToken(ctx, rec.ID, now)
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnauthenticated) {
		return Authn{}, ErrUnauthenticated
	}
	if err != nil {
		return Authn{}, err
	}
	return Authn{Actor: domain.TokenActor(rec.ID), Token: &rec}, nil
}

// Authorize grants a caller access to tenant: a person through an active
// membership, a token only in its own tenant. The principal carries the
// caller's restriction beside its grant (RFC 0006 §3.3, §4.1): a
// member's project scope and visibility, a token's project scope, and
// the one project a CI token or an in-context grant was minted for. Anything else — including
// a tenant that doesn't exist — is ErrForbidden, so the answer never
// reveals whether a tenant exists.
func (s *Service) Authorize(ctx context.Context, a Authn, tenant tenancy.ID) (authz.Principal, error) {
	p := a.Principal()
	p.Tenant = tenant
	if a.Grant != nil {
		// An in-context grant carries its own permissions: the person's,
		// already cut down when it was minted. It is never re-derived
		// from the membership, so widening a role mid-session does not
		// widen a grant already in a page's memory.
		if a.Grant.Tenant != tenant {
			return authz.Principal{}, ErrForbidden
		}
		p.Grant = a.Grant.Permissions
		// It acts on the one project it was minted for, which was inside
		// the person's project scope when it was minted (RFC 0006 §4.1).
		p.Projects = domain.ProjectScopeOf(a.Grant.Project)
		return p, nil
	}
	if a.CI != nil {
		// A CI token carries the permissions it was minted with — the
		// CI ceiling, fixed at mint time — in the one tenant the
		// repository's Git connection named. It is never widened by a
		// membership, because there is no person behind it.
		if a.CI.Tenant != tenant {
			return authz.Principal{}, ErrForbidden
		}
		p.Grant = a.CI.Permissions
		p.Projects = domain.ProjectScopeOf(a.CI.Project)
		return p, nil
	}
	if a.Token != nil {
		if a.Token.Tenant != tenant {
			return authz.Principal{}, ErrForbidden
		}
		p.Grant = domain.GrantForScopes(a.Token.Scopes)
		p.Projects = a.Token.Projects
		return p, nil
	}
	var g MembershipGrant
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		g, err = st.ActiveMembership(ctx, a.Person, tenant)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return authz.Principal{}, ErrForbidden
	}
	if err != nil {
		return authz.Principal{}, err
	}
	p.Member = g.Member
	p.Grant = domain.GrantForMember(g.Roles, g.Locales)
	p.Projects, p.Visibility = g.Projects, g.Visibility
	if p.Visibility == domain.VisibilityAssigned {
		p.Coverage = s.coverage
	}
	return p, nil
}

// Me is the signed-in person and their tenants.
type Me struct {
	Person      PersonRecord
	Memberships []MembershipView
}

// maxMemberships bounds GET /v1/me; GET /v1/tenants pages beyond it.
const maxMemberships = 100

// GetMe returns the signed-in person and their tenants, accepting any
// invitations that arrived since they signed in.
func (s *Service) GetMe(ctx context.Context) (Me, error) {
	p, ok := authz.From(ctx)
	if !ok || p.Person.IsZero() {
		return Me{}, ErrPersonOnly
	}
	rec, err := s.Person(ctx, p.Person)
	if err != nil {
		return Me{}, err
	}
	s.acceptInvitations(ctx, rec)
	var ms []MembershipView
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		ms, err = st.MembershipsOf(ctx, p.Person, tenancy.ID{}, maxMemberships)
		return err
	})
	return Me{Person: rec, Memberships: ms}, err
}

// sessionLifetime is exposed for the HTTP edge's cookie Max-Age.
func (s *Service) SessionLifetime() time.Duration { return s.cfg.SessionTTL }
