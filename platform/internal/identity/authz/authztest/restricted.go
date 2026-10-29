package authztest

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The restricted principals of RFC 0006 §3.3 and §4.1, exactly as
// Identity builds them: a project-scoped member or token, and a member
// whose visibility is `assigned` (every vendor member), whose units
// come from an authz.Coverage — in tests, a Coverage.

// ScopedMember returns ctx acting in tenant as a new person with roles,
// limited to projects (and, for translator and reviewer roles, to
// locales).
func ScopedMember(ctx context.Context, tenant tenancy.ID, projects []uuid.UUID, roles []string, locales ...string) context.Context {
	ctx = Member(ctx, tenant, roles, locales...)
	p, _ := authz.From(ctx)
	p.Projects = projectScope(projects)
	return authz.WithPrincipal(ctx, p)
}

// ScopedToken returns ctx acting in tenant as an API token with scopes,
// limited to projects.
func ScopedToken(ctx context.Context, tenant tenancy.ID, projects []uuid.UUID, scopes ...string) context.Context {
	ctx = Token(ctx, tenant, scopes...)
	p, _ := authz.From(ctx)
	p.Projects = projectScope(projects)
	return authz.WithPrincipal(ctx, p)
}

// InEnvironments narrows the principal on ctx to environments for
// environment-scoped permissions (RFC 0006 §4.2): a member whose
// approvals.decide on release requests covers only some environments.
func InEnvironments(ctx context.Context, environments ...string) context.Context {
	p, ok := authz.From(ctx)
	if !ok {
		panic("authztest: InEnvironments needs a principal")
	}
	scope, err := domain.ParseEnvironmentScope(environments)
	if err != nil {
		panic(fmt.Sprintf("authztest: %v", err))
	}
	p.Environments = scope
	return authz.WithPrincipal(ctx, p)
}

// Assigned returns ctx acting in tenant as a vendor's translator — role
// translator, visibility `assigned`, limited to locales — whose units
// are what cov says, and the member they are. A nil cov is a deployment
// with no Coverage wired: the member sees nothing.
func Assigned(ctx context.Context, tenant tenancy.ID, cov authz.Coverage, locales ...string) (context.Context, domain.MemberID) {
	ctx = Member(ctx, tenant, []string{"translator"}, locales...)
	p, _ := authz.From(ctx)
	p.Visibility = domain.VisibilityAssigned
	p.Coverage = cov
	return authz.WithPrincipal(ctx, p), p.Member
}

func projectScope(ids []uuid.UUID) domain.ProjectScope {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.String()
	}
	scope, err := domain.ParseProjectScope(s)
	if err != nil {
		panic(fmt.Sprintf("authztest: %v", err))
	}
	return scope
}

// Coverage is an in-memory authz.Coverage: the units each member's
// assignments cover, per project. The zero value covers nothing.
type Coverage struct {
	mu    sync.Mutex
	units map[coverKey][]authz.Unit
}

type coverKey struct {
	member  domain.MemberID
	project uuid.UUID
}

// Assign makes member see message in each of locales within project.
func (c *Coverage) Assign(member domain.MemberID, project, message uuid.UUID, locales ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.units == nil {
		c.units = map[coverKey][]authz.Unit{}
	}
	k := coverKey{member, project}
	for _, l := range locales {
		c.units[k] = append(c.units[k], authz.Unit{Message: message, Locale: l})
	}
}

// Covers implements authz.Coverage.
func (c *Coverage) Covers(_ context.Context, member domain.MemberID, project, message uuid.UUID, locale string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range c.units[coverKey{member, project}] {
		if u.Message == message && u.Locale == locale {
			return true, nil
		}
	}
	return false, nil
}

// Covered implements authz.Coverage.
func (c *Coverage) Covered(_ context.Context, member domain.MemberID, project uuid.UUID) (authz.CoveredSet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return authz.NewCoveredSet(c.units[coverKey{member, project}]...), nil
}

var _ authz.Coverage = (*Coverage)(nil)
