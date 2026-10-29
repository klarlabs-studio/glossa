package domain

import (
	"fmt"
	"slices"
)

// Permission is one thing a principal may do in a tenant. Other bounded
// contexts check them through package authz; they are named
// "<resource>.<action>" and never renamed once released.
type Permission string

// The permissions. Locale-scoped ones (see LocaleScoped) can be limited
// to some locales for translators and reviewers.
const (
	PermTenantRead         Permission = "tenant.read"
	PermTenantManage       Permission = "tenant.manage"
	PermMembersRead        Permission = "members.read"
	PermMembersManage      Permission = "members.manage"
	PermOwnersManage       Permission = "owners.manage"
	PermTokensRead         Permission = "tokens.read"
	PermTokensManage       Permission = "tokens.manage"
	PermCatalogRead        Permission = "catalog.read"
	PermCatalogWrite       Permission = "catalog.write"
	PermKnowledgeRead      Permission = "knowledge.read"
	PermKnowledgeWrite     Permission = "knowledge.write"
	PermTranslationsRead   Permission = "translations.read"
	PermTranslationsWrite  Permission = "translations.write"
	PermTranslationsReview Permission = "translations.review"
	PermReleasesRead       Permission = "releases.read"
	PermReleasesPublish    Permission = "releases.publish"
	// PermIntelligenceRead sees AI configuration (never provider keys),
	// jobs, suggestions, disclosures and metrics.
	PermIntelligenceRead Permission = "intelligence.read"
	// PermIntelligenceManage configures AI providers, routing, prices,
	// budgets, privacy (consent, sensitive namespaces), auto-translate
	// and review routing.
	PermIntelligenceManage Permission = "intelligence.manage"
	// PermIntelligenceTranslate requests AI fills, cancels jobs and
	// accepts, edits or rejects suggestions — per locale.
	PermIntelligenceTranslate Permission = "intelligence.translate"
	// PermIntegrationRead sees import and export jobs, their results,
	// and downloads exports.
	PermIntegrationRead Permission = "integration.read"
	// PermIntegrationImport imports translations from interchange files
	// (XLIFF, JSON, PO) — per locale.
	PermIntegrationImport Permission = "integration.import"
	// PermIntegrationManage imports what is not a translation — source
	// messages, translation memory (TMX), termbases (TBX) — and runs
	// imports in overwrite mode.
	PermIntegrationManage Permission = "integration.manage"

	// The operations permissions (RFC 0006 §4.2). They are granted and
	// pinned now; the Workflow, Assignment, Approval and Audit use cases
	// that check them arrive in later waves.

	// PermWorkflowsRead sees workflow definitions, bindings and instances.
	PermWorkflowsRead Permission = "workflows.read"
	// PermWorkflowsManage creates, versions and binds workflow
	// definitions.
	PermWorkflowsManage Permission = "workflows.manage"
	// PermAssignmentsRead sees assignments.
	PermAssignmentsRead Permission = "assignments.read"
	// PermAssignmentsManage creates, reassigns and withdraws assignments.
	PermAssignmentsManage Permission = "assignments.manage"
	// PermApprovalsDecide grants or denies an approval. It is
	// locale-scoped for translation subjects and environment-scoped for
	// release subjects (RFC 0006 §4.2, §5.1; see EnvironmentScoped), and
	// human-only: no token scope, CI ceiling or background principal
	// ever holds it (RFC 0006 §3.2, §9.3).
	PermApprovalsDecide Permission = "approvals.decide"
	// PermVendorsManage creates and changes vendors and who belongs to
	// them.
	PermVendorsManage Permission = "vendors.manage"
	// PermAuditRead reads the audit log.
	PermAuditRead Permission = "audit.read"
	// PermAuditExport exports the audit log; only an owner holds it by
	// default.
	PermAuditExport Permission = "audit.export"
	// PermAuditImport writes entries into the tenant's audit trail that
	// no act on this platform caused: v0.3's history (RFC 0006 §7.2,
	// amended in wave 4). Only an owner holds it, no token scope reaches
	// it and no background principal is given it — an entry attributed
	// to someone else is the one thing a tamper-evident trail must not
	// take from anyone less.
	PermAuditImport Permission = "audit.import"
)

// AllPermissions lists every permission, sorted.
func AllPermissions() []Permission {
	return []Permission{
		PermApprovalsDecide,
		PermAssignmentsManage, PermAssignmentsRead,
		PermAuditExport, PermAuditImport, PermAuditRead,
		PermCatalogRead, PermCatalogWrite,
		PermIntegrationImport, PermIntegrationManage, PermIntegrationRead,
		PermIntelligenceManage, PermIntelligenceRead, PermIntelligenceTranslate,
		PermKnowledgeRead, PermKnowledgeWrite,
		PermMembersManage, PermMembersRead,
		PermOwnersManage,
		PermReleasesPublish, PermReleasesRead,
		PermTenantManage, PermTenantRead,
		PermTokensManage, PermTokensRead,
		PermTranslationsRead, PermTranslationsReview, PermTranslationsWrite,
		PermVendorsManage,
		PermWorkflowsManage, PermWorkflowsRead,
	}
}

// LocaleScoped reports whether a member's locale scope limits p.
func (p Permission) LocaleScoped() bool {
	return p == PermTranslationsWrite || p == PermTranslationsReview || p == PermIntelligenceTranslate ||
		p == PermIntegrationImport || p == PermApprovalsDecide
}

// EnvironmentScoped reports whether p, when it decides about a release
// request, is limited by a principal's environment scope instead of its
// locale scope (RFC 0006 §4.2): approvals.decide is locale-scoped for a
// translation and environment-scoped for a release, which ships every
// locale at once. Holding it in any locale is holding it; where it may
// be used is the environment scope's to say (authz.RequireInEnvironment).
func (p Permission) EnvironmentScoped() bool { return p == PermApprovalsDecide }

// HumanOnly reports whether p is a human decision — reviewing or
// approving text — that no API token scope grants (RFC 0006 §9.3).
// TestNoScopeCombinationReachesAHumanOnlyPermission holds scopes to it.
func (p Permission) HumanOnly() bool {
	return p == PermTranslationsReview || p == PermApprovalsDecide
}

// Role is a named bundle of permissions a member holds in a tenant.
type Role string

// The roles.
const (
	RoleOwner      Role = "owner"
	RoleAdmin      Role = "admin"
	RoleDeveloper  Role = "developer"
	RoleTranslator Role = "translator"
	RoleReviewer   Role = "reviewer"
)

var readAll = []Permission{
	PermTenantRead, PermMembersRead, PermCatalogRead, PermTranslationsRead, PermReleasesRead, PermKnowledgeRead,
	PermIntelligenceRead, PermIntegrationRead, PermWorkflowsRead, PermAssignmentsRead,
}

// rolePermissions is the role matrix; TestRolePermissionMatrix pins it.
var rolePermissions = map[Role][]Permission{
	RoleOwner: AllPermissions(),
	// Only an owner manages owners and, by default, exports the audit log
	// or imports history into it.
	RoleAdmin: slices.DeleteFunc(AllPermissions(), func(p Permission) bool {
		return p == PermOwnersManage || p == PermAuditExport || p == PermAuditImport
	}),
	RoleDeveloper: append(slices.Clone(readAll),
		PermTokensRead, PermTokensManage, PermCatalogWrite, PermTranslationsWrite, PermReleasesPublish,
		PermKnowledgeWrite, PermIntelligenceTranslate, PermIntegrationImport, PermIntegrationManage),
	RoleTranslator: append(slices.Clone(readAll), PermTranslationsWrite, PermIntelligenceTranslate, PermIntegrationImport),
	RoleReviewer: append(slices.Clone(readAll), PermTranslationsWrite, PermTranslationsReview, PermIntelligenceTranslate,
		PermIntegrationImport, PermApprovalsDecide),
}

// localeRoles are the roles a locale scope applies to.
func (r Role) localeScoped() bool { return r == RoleTranslator || r == RoleReviewer }

// ParseRole validates a role name.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if _, ok := rolePermissions[r]; !ok {
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, s)
	}
	return r, nil
}

// Roles is a non-empty, sorted, duplicate-free set of roles.
type Roles []Role

// ParseRoles validates, sorts and de-duplicates role names.
func ParseRoles(names []string) (Roles, error) {
	var out Roles
	for _, n := range names {
		r, err := ParseRole(n)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoRoles
	}
	slices.Sort(out)
	return out, nil
}

// Has reports whether r is in the set.
func (rs Roles) Has(r Role) bool { return slices.Contains(rs, r) }

// Strings returns the role names.
func (rs Roles) Strings() []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

func (rs Roles) anyLocaleScoped() bool { return slices.ContainsFunc(rs, Role.localeScoped) }

// Scope is a coarse capability an API token is created with.
type Scope string

// The token scopes. Every scope implies read: a token that may write or
// publish must be able to see what it writes or publishes.
const (
	// ScopeRead reads the tenant's catalog, translations, releases,
	// members and tokens.
	ScopeRead Scope = "read"
	// ScopeWrite pushes source messages, translations and linguistic
	// knowledge — terms, style guides, TM retirement (CLI push, CI
	// import, agents) — requests AI fills and runs import jobs. Review is a human
	// decision and no scope grants it.
	ScopeWrite Scope = "write"
	// ScopePublish creates releases.
	ScopePublish Scope = "publish"
	// ScopeAdmin manages the tenant, its members, its tokens and its AI
	// configuration — never its owners.
	ScopeAdmin Scope = "admin"
	// ScopeWorkflows reads, saves and binds workflow definitions (RFC
	// 0006 §4.2) — what `glossa workflow push` from CI needs. It is
	// opt-in: no other scope implies it and no default set holds it, so
	// an existing token never starts changing how work flows. It grants
	// no review and no approvals.decide: a token that writes the process
	// still cannot take the human decisions in it (§3.2, §9.3).
	ScopeWorkflows Scope = "workflows"
)

var scopePermissions = map[Scope][]Permission{
	ScopeRead: append(slices.Clone(readAll), PermTokensRead),
	ScopeWrite: {PermCatalogWrite, PermTranslationsWrite, PermKnowledgeWrite, PermIntelligenceTranslate,
		PermIntegrationImport, PermIntegrationManage},
	ScopePublish:   {PermReleasesPublish},
	ScopeAdmin:     {PermTenantManage, PermMembersManage, PermTokensManage, PermIntelligenceManage},
	ScopeWorkflows: {PermWorkflowsRead, PermWorkflowsManage},
}

// Scopes is a non-empty, sorted, duplicate-free set of token scopes.
type Scopes []Scope

// ParseScopes validates, sorts and de-duplicates scope names.
func ParseScopes(names []string) (Scopes, error) {
	var out Scopes
	for _, n := range names {
		s := Scope(n)
		if _, ok := scopePermissions[s]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidScope, n)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: at least one scope is required", ErrInvalidScope)
	}
	slices.Sort(out)
	return out, nil
}

// Strings returns the scope names.
func (ss Scopes) Strings() []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}

// Grant is what a principal may do in one tenant: a set of permissions,
// each for every locale or for a LocaleScope. The zero Grant allows
// nothing.
//
// A Grant carries no project scope or assignment visibility (RFC 0006
// §4.1, §3.3): those ride beside it on authz.Principal, which checks
// them with every project-addressed permission.
type Grant struct {
	perms map[Permission]LocaleScope
}

// GrantForMember derives a member's grant. The locale scope limits only
// locale-scoped permissions that come from translator or reviewer; the
// same permission from any other role is unrestricted.
func GrantForMember(roles Roles, locales LocaleScope) Grant {
	g := Grant{perms: map[Permission]LocaleScope{}}
	for _, r := range roles {
		for _, p := range rolePermissions[r] {
			scope := LocaleScope{}
			if p.LocaleScoped() && r.localeScoped() {
				scope = locales
			}
			g.add(p, scope)
		}
	}
	return g
}

// GrantForScopes derives an API token's grant. Tokens are never
// locale-scoped.
func GrantForScopes(scopes Scopes) Grant {
	g := Grant{perms: map[Permission]LocaleScope{}}
	for _, s := range scopes {
		for _, p := range slices.Concat(scopePermissions[ScopeRead], scopePermissions[s]) {
			g.add(p, LocaleScope{})
		}
	}
	return g
}

// GrantOf grants exactly perms, for every locale: a background
// process's grant (authz.Background), which no role or scope derives.
func GrantOf(perms ...Permission) Grant {
	g := Grant{perms: map[Permission]LocaleScope{}}
	for _, p := range perms {
		g.add(p, LocaleScope{})
	}
	return g
}

// add merges p into the grant: unrestricted wins, scopes union.
func (g Grant) add(p Permission, scope LocaleScope) {
	cur, ok := g.perms[p]
	switch {
	case !ok:
		g.perms[p] = scope
	case cur.All() || scope.All():
		g.perms[p] = LocaleScope{}
	default:
		merged, _ := ParseLocaleScope(append(cur.Strings(), scope.Strings()...))
		g.perms[p] = merged
	}
}

// Intersect returns the part of g that is in perms, each permission
// keeping the locale scope g gave it. It is how a narrower credential
// is cut from a wider one — an in-context grant from the person's
// grant (RFC 0004 §5.2) — without ever widening it: a translator
// limited to de stays limited to de.
func (g Grant) Intersect(perms ...Permission) Grant {
	out := Grant{perms: map[Permission]LocaleScope{}}
	for _, p := range perms {
		if scope, ok := g.perms[p]; ok {
			out.perms[p] = scope
		}
	}
	return out
}

// LocaleScopes returns the grant as data: each granted permission with
// the locales it holds for, empty meaning every locale. It is what a
// stored credential keeps, so GrantFromLocaleScopes can rebuild it.
func (g Grant) LocaleScopes() map[Permission][]string {
	out := make(map[Permission][]string, len(g.perms))
	for p, scope := range g.perms {
		out[p] = scope.Strings()
	}
	return out
}

// GrantFromLocaleScopes rebuilds a grant LocaleScopes wrote down.
func GrantFromLocaleScopes(m map[Permission][]string) (Grant, error) {
	g := Grant{perms: map[Permission]LocaleScope{}}
	for p, locales := range m {
		if !slices.Contains(AllPermissions(), p) {
			return Grant{}, fmt.Errorf("%w: %q", ErrInvalidPermission, p)
		}
		scope, err := ParseLocaleScope(locales)
		if err != nil {
			return Grant{}, err
		}
		g.perms[p] = scope
	}
	return g, nil
}

// Allows reports whether p is granted for every locale.
func (g Grant) Allows(p Permission) bool {
	scope, ok := g.perms[p]
	return ok && scope.All()
}

// AllowsFor reports whether p is granted for locale l.
func (g Grant) AllowsFor(p Permission, l Locale) bool {
	scope, ok := g.perms[p]
	return ok && scope.Covers(l)
}

// Holds reports whether p is granted at all, for every locale or for
// some. It is the grant's half of an environment-scoped check
// (Permission.EnvironmentScoped): a reviewer limited to de holds
// approvals.decide, and whether they may decide a release request is a
// question of environments, not of locales.
func (g Grant) Holds(p Permission) bool {
	_, ok := g.perms[p]
	return ok
}

// Locales returns the locales p is granted for; ok is false when p isn't
// granted at all. An unrestricted scope means every locale.
func (g Grant) Locales(p Permission) (scope LocaleScope, ok bool) {
	scope, ok = g.perms[p]
	return scope, ok
}

// Covers reports whether g allows, for every locale, everything other
// allows — the check that a token never exceeds its creator.
func (g Grant) Covers(other Grant) bool {
	for p := range other.perms {
		if !g.Allows(p) {
			return false
		}
	}
	return true
}

// Permissions lists the granted permissions, sorted.
func (g Grant) Permissions() []Permission {
	out := make([]Permission, 0, len(g.perms))
	for p := range g.perms {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}
