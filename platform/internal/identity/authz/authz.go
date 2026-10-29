// Package authz is how every bounded context asks "may the caller do
// this here?". Identity's HTTP edge puts a Principal on the request
// context after authenticating the caller and checking the tenant in the
// path against their membership or token; application services then
// call Require before acting:
//
//	if err := authz.Require(ctx, authz.CatalogWrite); err != nil {
//		return err // errors.Is(err, authz.ErrForbidden)
//	}
//	if err := authz.RequireFor(ctx, authz.TranslationsWrite, locale); err != nil { … }
//
// Checks are explicit and local: a permission, optionally a locale. There
// is no policy language and no remote call. The role matrix and token
// scopes that produce a Grant live in the Identity domain.
//
// # Project scope and assignment visibility
//
// RFC 0006 narrows a principal beyond its grant in two ways, and every
// check here honours both:
//
//   - Project scope (§4.1): the projects a member's roles or a token's
//     scopes apply to; empty is every project. A CI token and an
//     in-context grant are scoped to the one project they were minted
//     for and never widen it. Every project-addressed use case checks
//     with RequireIn or RequireForIn (or, after loading a row by another
//     id, InProject), and a tenant-level list of project-owned rows
//     filters by Projects. A project outside the scope answers
//     ErrNotVisible, which every surface renders as its own not-found:
//     to the caller it does not exist.
//   - Visibility `assigned` (§3.3), which every vendor member has: they
//     read only the translation units of their assignments and what
//     translating those units needs, write translations only in them,
//     and do nothing else. Require, RequireFor, RequireIn, RequireForIn,
//     ScopeOf and Projects refuse such a member every permission but
//     tenant.read, so a path that forgot them fails closed. The paths a
//     vendor does use admit them through the coverage-aware checks —
//     RequireUnit, RequireMessage, RequireProject, RequireLocaleIn and
//     Visible — which consult the Coverage port Identity puts on their
//     principal. Without one wired, they see nothing.
//
// RLS stays tenant-level (RFC 0006 §14 decision 7): project scope and
// visibility are enforced here, at the application layer.
package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Permission is re-exported so other contexts depend on this package
// only, not on Identity's domain.
type Permission = domain.Permission

// Locale is the locale RequireFor checks, re-exported for the same
// reason.
type Locale = domain.Locale

// ParseLocale canonicalizes a BCP 47 tag for RequireFor.
func ParseLocale(s string) (Locale, error) { return domain.ParseLocale(s) }

// The permissions other contexts check.
const (
	TenantRead         = domain.PermTenantRead
	TenantManage       = domain.PermTenantManage
	MembersRead        = domain.PermMembersRead
	MembersManage      = domain.PermMembersManage
	OwnersManage       = domain.PermOwnersManage
	TokensRead         = domain.PermTokensRead
	TokensManage       = domain.PermTokensManage
	CatalogRead        = domain.PermCatalogRead
	CatalogWrite       = domain.PermCatalogWrite
	KnowledgeRead      = domain.PermKnowledgeRead
	KnowledgeWrite     = domain.PermKnowledgeWrite
	TranslationsRead   = domain.PermTranslationsRead
	TranslationsWrite  = domain.PermTranslationsWrite
	TranslationsReview = domain.PermTranslationsReview
	ReleasesRead       = domain.PermReleasesRead
	ReleasesPublish    = domain.PermReleasesPublish
	// IntelligenceRead, IntelligenceManage and IntelligenceTranslate are
	// the AI permissions (RFC 0003 §6); IntelligenceTranslate is
	// locale-scoped.
	IntelligenceRead      = domain.PermIntelligenceRead
	IntelligenceManage    = domain.PermIntelligenceManage
	IntelligenceTranslate = domain.PermIntelligenceTranslate
	// IntegrationRead, IntegrationImport and IntegrationManage are the
	// import/export permissions (RFC 0003 §5–§6); IntegrationImport is
	// locale-scoped.
	IntegrationRead   = domain.PermIntegrationRead
	IntegrationImport = domain.PermIntegrationImport
	IntegrationManage = domain.PermIntegrationManage
	// The operations permissions (RFC 0006 §4.2). ApprovalsDecide is
	// locale-scoped for translations, environment-scoped for release
	// requests (RequireInEnvironment), and human-only: no token scope
	// grants it and Background refuses it.
	WorkflowsRead     = domain.PermWorkflowsRead
	WorkflowsManage   = domain.PermWorkflowsManage
	AssignmentsRead   = domain.PermAssignmentsRead
	AssignmentsManage = domain.PermAssignmentsManage
	ApprovalsDecide   = domain.PermApprovalsDecide
	VendorsManage     = domain.PermVendorsManage
	AuditRead         = domain.PermAuditRead
	AuditExport       = domain.PermAuditExport
	// AuditImport writes v0.3's history into the trail (RFC 0006 §7.2):
	// owner-only, no token scope, never background.
	AuditImport = domain.PermAuditImport
)

var (
	// ErrUnauthenticated means the context carries no principal.
	ErrUnauthenticated = errors.New("authz: unauthenticated")
	// ErrForbidden means the principal lacks the permission here.
	ErrForbidden = errors.New("authz: forbidden")
)

// DeniedError says which permission was missing. It matches ErrForbidden.
type DeniedError struct {
	Permission Permission
	Locale     string // empty unless the check was for a locale
	// Environment is set when the check was for a release environment
	// (RequireInEnvironment).
	Environment string
	// Assigned is set when the permission is refused because the
	// principal reads only its assignments (RFC 0006 §3.3).
	Assigned bool
	// ProjectScoped is set when the permission is refused because it
	// reaches beyond any one project and the principal is limited to
	// some (RFC 0006 §4.1).
	ProjectScoped bool
}

func (e *DeniedError) Error() string {
	if e.ProjectScoped {
		return fmt.Sprintf("authz: %s is not granted to a principal limited to some projects", e.Permission)
	}
	if e.Assigned {
		return fmt.Sprintf("authz: %s is not granted to a member who sees only their assignments", e.Permission)
	}
	if e.Environment != "" {
		return fmt.Sprintf("authz: %s is not granted in environment %s", e.Permission, e.Environment)
	}
	if e.Locale != "" {
		return fmt.Sprintf("authz: %s is not granted for %s", e.Permission, e.Locale)
	}
	return fmt.Sprintf("authz: %s is not granted", e.Permission)
}

// Is makes errors.Is(err, ErrForbidden) true.
func (e *DeniedError) Is(target error) bool { return target == ErrForbidden }

// Principal is the authenticated caller.
type Principal struct {
	// Actor is the person or API token acting.
	Actor domain.Actor
	// Person is set when a person is acting (session), zero for tokens.
	Person domain.PersonID
	// Tenant is the tenant the grant applies to; zero outside tenant
	// routes, where the grant is empty.
	Tenant tenancy.ID
	// Member is the person's membership in Tenant, when there is one.
	Member domain.MemberID
	// TokenTenant is the tenant an API token belongs to, also on
	// tenantless routes; zero for people.
	TokenTenant tenancy.ID
	// Grant is what the principal may do in Tenant.
	Grant domain.Grant
	// Projects is the projects Grant applies to (RFC 0006 §4.1); empty
	// means every project. A CI token's and an in-context grant's is
	// the one project they were minted for.
	Projects domain.ProjectScope
	// Environments is the release environments an environment-scoped
	// permission (approvals.decide on a release request) applies to
	// (RFC 0006 §4.2); empty means every environment, which is every
	// principal until a narrower scope is stored.
	Environments domain.EnvironmentScope
	// Visibility is VisibilityAssigned for a member who reads only the
	// units of their assignments (§3.3); empty or VisibilityAll
	// otherwise.
	Visibility domain.Visibility
	// Coverage answers which units an `assigned` member may see. Nil
	// for everyone else — and for an `assigned` member when no
	// implementation is wired, who then sees nothing.
	Coverage Coverage
}

type ctxKey struct{}

// WithPrincipal returns a context carrying p. Only Identity's HTTP edge
// (and tests) should call it.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal on ctx.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Authenticated returns the principal for operations that read no
// tenant data and so need no permission, only a caller (a person or an
// API token) — stateless tools such as the message preview. Anything
// tenant-scoped uses Require or RequireFor instead.
func Authenticated(ctx context.Context) (Principal, error) {
	p, ok := From(ctx)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	return p, nil
}

// neverBackground are permissions background work never holds: identity
// administration, and deciding approvals, which is a human decision
// (RFC 0006 §9.1, §9.3) — a workflow acts as the actor whose event
// moved it and holds no approval of its own.
var neverBackground = []Permission{
	domain.PermTenantManage, domain.PermMembersManage, domain.PermOwnersManage, domain.PermTokensManage,
	domain.PermVendorsManage, domain.PermApprovalsDecide, domain.PermAuditImport,
}

// Background returns ctx acting as a bounded context's background
// process — an outbox subscriber or a job, named stably
// ("knowledge.derive_tm") — in the tenant already on ctx, holding
// exactly perms. It is how such work reads (or writes) another context
// through that context's authorized application ports instead of
// around them: the ports keep checking permissions, and the grant
// states what the process may do. It refuses a context without a
// tenant, one that already carries a principal (a request's caller is
// never swapped for a broader one) and identity administration.
func Background(ctx context.Context, name string, perms ...Permission) (context.Context, error) {
	if name == "" {
		return nil, errors.New("authz: a background principal needs a name")
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("authz: background %s: no tenant on the context", name)
	}
	if _, ok := From(ctx); ok {
		return nil, fmt.Errorf("authz: background %s: the context already carries a principal", name)
	}
	for _, p := range perms {
		for _, never := range neverBackground {
			if p == never {
				return nil, fmt.Errorf("authz: background %s: %s is never granted to background work", name, p)
			}
		}
	}
	return WithPrincipal(ctx, Principal{
		Actor: domain.SystemActor(name), Tenant: tenant, Grant: domain.GrantOf(perms...),
	}), nil
}

// Require returns nil if the principal holds perm for every locale in
// the context's tenant. It does not consult project scope: a
// project-addressed use case uses RequireIn. An `assigned` member is
// refused everything but tenant.read.
func Require(ctx context.Context, perm Permission) error {
	p, err := inTenant(ctx)
	if err != nil {
		return err
	}
	if p.Assigned() && !slices.Contains(assignedTenantPermissions, perm) {
		return assignedDenied(perm)
	}
	if !p.Grant.Allows(perm) {
		return &DeniedError{Permission: perm}
	}
	return nil
}

// RequireFor returns nil if the principal holds perm for locale in the
// context's tenant. Use it for locale-scoped work (translate, review).
func RequireFor(ctx context.Context, perm Permission, locale domain.Locale) error {
	p, err := inTenant(ctx)
	if err != nil {
		return err
	}
	if p.Assigned() {
		return assignedDenied(perm)
	}
	if !p.Grant.AllowsFor(perm, locale) {
		return &DeniedError{Permission: perm, Locale: locale.String()}
	}
	return nil
}

// Scope says where a principal holds a permission, as data: what a
// queued job keeps to act within its requester's rights later (an
// import job's worker writes only the locales its requester could).
type Scope struct {
	// Granted is false when the permission isn't held at all.
	Granted bool `json:"granted"`
	// Locales limit a locale-scoped permission; empty means every locale.
	Locales []string `json:"locales,omitempty"`
}

// ScopeOf returns where the principal on ctx holds perm in the context's
// tenant.
func ScopeOf(ctx context.Context, perm Permission) (Scope, error) {
	p, err := inTenant(ctx)
	if err != nil {
		return Scope{}, err
	}
	scope, ok := p.Grant.Locales(perm)
	if !ok || p.Assigned() {
		return Scope{}, nil
	}
	return Scope{Granted: true, Locales: scope.Strings()}, nil
}

// All reports whether s grants its permission for every locale.
func (s Scope) All() bool { return s.Granted && len(s.Locales) == 0 }

// Covers reports whether s grants its permission for locale (a scope's
// locale covers its CLDR descendants, as RequireFor does).
func (s Scope) Covers(locale Locale) bool {
	if !s.Granted {
		return false
	}
	scope, err := domain.ParseLocaleScope(s.Locales)
	return err == nil && scope.Covers(locale)
}

// inTenant returns the principal, refusing one whose grant belongs to a
// different tenant than the one the context (and so the database
// transaction) is scoped to.
func inTenant(ctx context.Context) (Principal, error) {
	p, ok := From(ctx)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant != p.Tenant {
		return Principal{}, fmt.Errorf("%w: principal is not scoped to this tenant", ErrForbidden)
	}
	return p, nil
}
