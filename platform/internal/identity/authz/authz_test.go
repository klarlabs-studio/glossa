package authz_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func principal(t *testing.T, tenant tenancy.ID, roles []string, locales []string) authz.Principal {
	t.Helper()
	r, err := domain.ParseRoles(roles)
	if err != nil {
		t.Fatal(err)
	}
	l, err := domain.ParseLocaleScope(locales)
	if err != nil {
		t.Fatal(err)
	}
	person := domain.NewPersonID()
	return authz.Principal{
		Actor: domain.PersonActor(person), Person: person, Tenant: tenant,
		Grant: domain.GrantForMember(r, l),
	}
}

func TestRequire(t *testing.T) {
	tenant := tenancy.NewID()
	ctx := tenancy.ContextWithTenant(context.Background(), tenant)
	ctx = authz.WithPrincipal(ctx, principal(t, tenant, []string{"translator"}, []string{"de"}))

	if err := authz.Require(ctx, authz.CatalogRead); err != nil {
		t.Errorf("translator reads the catalog: %v", err)
	}
	err := authz.Require(ctx, authz.CatalogWrite)
	var denied *authz.DeniedError
	if !errors.Is(err, authz.ErrForbidden) || !errors.As(err, &denied) || denied.Permission != authz.CatalogWrite {
		t.Errorf("translator writing the catalog err = %v", err)
	}

	de, _ := domain.ParseLocale("de-CH")
	ja, _ := domain.ParseLocale("ja")
	if err := authz.RequireFor(ctx, authz.TranslationsWrite, de); err != nil {
		t.Errorf("translator scoped to de writes de-CH: %v", err)
	}
	if err := authz.RequireFor(ctx, authz.TranslationsWrite, ja); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator scoped to de writing ja err = %v", err)
	}
}

func TestScopeOfSnapshotsWhereAPermissionHolds(t *testing.T) {
	tenant := tenancy.NewID()
	ctx := tenancy.ContextWithTenant(context.Background(), tenant)
	translator := authz.WithPrincipal(ctx, principal(t, tenant, []string{"translator"}, []string{"de"}))
	s, err := authz.ScopeOf(translator, authz.IntegrationImport)
	if err != nil || !s.Granted || s.All() {
		t.Fatalf("translator scope = %+v, %v", s, err)
	}
	deAT, _ := authz.ParseLocale("de-AT")
	ja, _ := authz.ParseLocale("ja")
	if !s.Covers(deAT) || s.Covers(ja) {
		t.Errorf("scope %v: de-AT %t, ja %t", s.Locales, s.Covers(deAT), s.Covers(ja))
	}
	if s, _ := authz.ScopeOf(translator, authz.IntegrationManage); s.Granted || s.Covers(deAT) {
		t.Errorf("ungranted permission scope = %+v", s)
	}
	owner := authz.WithPrincipal(ctx, principal(t, tenant, []string{"owner"}, nil))
	if s, _ := authz.ScopeOf(owner, authz.TranslationsReview); !s.All() || !s.Covers(ja) {
		t.Errorf("owner scope = %+v", s)
	}
	if _, err := authz.ScopeOf(ctx, authz.TranslationsRead); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: %v", err)
	}
}

func TestRequireWithoutPrincipal(t *testing.T) {
	ctx := tenancy.ContextWithTenant(context.Background(), tenancy.NewID())
	if err := authz.Require(ctx, authz.TenantRead); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

// A grant belongs to one tenant: it must not authorize work in another,
// even if a bug put the two side by side on one context.
func TestRequireRefusesAGrantFromAnotherTenant(t *testing.T) {
	mine, other := tenancy.NewID(), tenancy.NewID()
	ctx := tenancy.ContextWithTenant(context.Background(), other)
	ctx = authz.WithPrincipal(ctx, principal(t, mine, []string{"owner"}, nil))
	if err := authz.Require(ctx, authz.TenantRead); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	if err := authz.Require(authz.WithPrincipal(context.Background(), principal(t, mine, []string{"owner"}, nil)), authz.TenantRead); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("no tenant on the context: err = %v, want ErrForbidden", err)
	}
}

// Tenantless operations (stateless tools) need only an authenticated
// caller, person or token.
func TestAuthenticated(t *testing.T) {
	if _, err := authz.Authenticated(context.Background()); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: err = %v", err)
	}
	p := principal(t, tenancy.ID{}, []string{"owner"}, nil)
	got, err := authz.Authenticated(authz.WithPrincipal(context.Background(), p))
	if err != nil || got.Actor != p.Actor {
		t.Errorf("person: %+v, %v", got, err)
	}
}

func TestBackgroundActsInTheContextTenantWithExactlyItsPermissions(t *testing.T) {
	tenant := tenancy.NewID()
	ctx := tenancy.ContextWithTenant(context.Background(), tenant)
	bg, err := authz.Background(ctx, "knowledge.derive_tm", authz.TranslationsRead, authz.CatalogRead)
	if err != nil {
		t.Fatal(err)
	}
	if err := authz.Require(bg, authz.TranslationsRead); err != nil {
		t.Errorf("granted permission refused: %v", err)
	}
	if err := authz.Require(bg, authz.TranslationsWrite); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("ungranted permission: %v", err)
	}
	p, _ := authz.From(bg)
	if p.Actor.Kind != domain.ActorSystem || p.Actor != domain.SystemActor("knowledge.derive_tm") {
		t.Errorf("actor = %s", p.Actor)
	}
	if p.Actor == domain.SystemActor("release.sync") {
		t.Error("different background processes must be different actors")
	}
	if !p.Person.IsZero() {
		t.Error("a background principal is not a person")
	}
}

func TestBackgroundRefusals(t *testing.T) {
	tenant := tenancy.NewID()
	scoped := tenancy.ContextWithTenant(context.Background(), tenant)
	if _, err := authz.Background(context.Background(), "x", authz.CatalogRead); err == nil {
		t.Error("no tenant on the context: must refuse")
	}
	member := authz.WithPrincipal(scoped, principal(t, tenant, []string{"translator"}, nil))
	if _, err := authz.Background(member, "x", authz.CatalogRead); err == nil {
		t.Error("a request's principal must never be swapped for a background one")
	}
	for _, p := range []authz.Permission{
		authz.TenantManage, authz.MembersManage, authz.OwnersManage, authz.TokensManage, authz.VendorsManage,
	} {
		if _, err := authz.Background(scoped, "x", p); err == nil {
			t.Errorf("background work must not administer identity (%s)", p)
		}
	}
	// RFC 0006 §9.1, §9.3: a workflow acts as the actor whose event
	// moved it, and approvals are human. No background process — the
	// workflow runner included — ever holds approvals.decide.
	if _, err := authz.Background(scoped, "workflow.runner", authz.CatalogRead, authz.ApprovalsDecide); err == nil {
		t.Error("a background principal was granted approvals.decide")
	}
	if _, err := authz.Background(scoped, "", authz.CatalogRead); err == nil {
		t.Error("a background principal needs a name")
	}
}
