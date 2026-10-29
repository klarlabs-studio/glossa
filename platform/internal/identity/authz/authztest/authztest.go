// Package authztest builds request contexts for other contexts' tests:
// a tenant plus a principal holding a member's or a token's grant,
// exactly as Identity's HTTP edge would put them on a request. Import it
// from tests only.
package authztest

import (
	"context"
	"fmt"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Member returns ctx acting in tenant as a new person with roles,
// limited to locales for translator and reviewer roles.
func Member(ctx context.Context, tenant tenancy.ID, roles []string, locales ...string) context.Context {
	rs, err := domain.ParseRoles(roles)
	if err != nil {
		panic(fmt.Sprintf("authztest: %v", err))
	}
	ls, err := domain.ParseLocaleScope(locales)
	if err != nil {
		panic(fmt.Sprintf("authztest: %v", err))
	}
	person := domain.NewPersonID()
	p := authz.Principal{
		Actor: domain.PersonActor(person), Person: person, Tenant: tenant,
		Member: domain.NewMemberID(), Grant: domain.GrantForMember(rs, ls),
	}
	return authz.WithPrincipal(tenancy.ContextWithTenant(ctx, tenant), p)
}

// Person returns ctx acting as a new signed-in person outside any
// tenant, as on /v1/me and other tenantless routes: no grant.
func Person(ctx context.Context) context.Context {
	person := domain.NewPersonID()
	return authz.WithPrincipal(ctx, authz.Principal{Actor: domain.PersonActor(person), Person: person})
}

// Token returns ctx acting in tenant as an API token with scopes.
func Token(ctx context.Context, tenant tenancy.ID, scopes ...string) context.Context {
	ss, err := domain.ParseScopes(scopes)
	if err != nil {
		panic(fmt.Sprintf("authztest: %v", err))
	}
	p := authz.Principal{
		Actor: domain.TokenActor(domain.NewTokenID()), Tenant: tenant, TokenTenant: tenant,
		Grant: domain.GrantForScopes(ss),
	}
	return authz.WithPrincipal(tenancy.ContextWithTenant(ctx, tenant), p)
}

// CIToken returns ctx acting in tenant as a repository's CI token: the
// CI ceiling and nothing else (catalog.read and catalog.write), exactly
// what Identity mints from a GitHub Actions ID token. It is how a test
// asks "could CI do this?" without inventing a grant CI never has.
func CIToken(ctx context.Context, tenant tenancy.ID) context.Context {
	p := authz.Principal{
		Actor: domain.TokenActor(domain.NewTokenID()), Tenant: tenant, TokenTenant: tenant,
		Grant: domain.GrantOf(domain.CIPermissions()...),
	}
	return authz.WithPrincipal(tenancy.ContextWithTenant(ctx, tenant), p)
}
