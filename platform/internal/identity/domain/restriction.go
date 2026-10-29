package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// RestrictionEnforced is true: project scope and assignment visibility
// are enforced, by package authz, in every context's read and write
// paths (RFC 0006 §13, wave 2):
//
//   - authz.Principal carries the restriction — project scope,
//     visibility and member — and, for a token, its project scope; a CI
//     token and an in-context grant are scoped to their one project;
//   - a project outside a principal's scope answers as if it did not
//     exist (authz.ErrNotVisible, rendered as each surface's not-found);
//   - a member with VisibilityAssigned reads only the units their
//     assignments cover (authz.Coverage), plus what translating them
//     needs, writes translations only in them, and is refused every
//     other permission but tenant.read; with no Coverage wired they see
//     nothing.
//
// TestRestrictionIsEnforced in this package and authz's tests pin the
// rules; cmd/glossa-server's route-enforcement table records the
// decision for every operation in the OpenAPI document and fails for
// one that has none.
const RestrictionEnforced = true

// maxProjectScope bounds a member's or token's project list.
const maxProjectScope = 100

// ProjectScope is the projects a member's roles, or a token's scopes,
// apply to (RFC 0006 §4.1). The zero value is every project — which is
// every membership and token without one.
type ProjectScope struct{ projects []ProjectRef }

// ParseProjectScope canonicalizes, sorts and de-duplicates project ids.
// An empty list is every project.
func ParseProjectScope(ids []string) (ProjectScope, error) {
	var out []ProjectRef
	for _, s := range ids {
		p, err := ParseProjectRef(s)
		if err != nil {
			return ProjectScope{}, err
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) > maxProjectScope {
		return ProjectScope{}, fmt.Errorf("%w: %d (at most %d)", ErrTooManyProjects, len(out), maxProjectScope)
	}
	slices.SortFunc(out, func(a, b ProjectRef) int { return strings.Compare(a.String(), b.String()) })
	return ProjectScope{projects: out}, nil
}

// ProjectScopeOf is the scope of exactly project: a CI token's and an
// in-context grant's, each minted for one project.
func ProjectScopeOf(project ProjectRef) ProjectScope {
	return ProjectScope{projects: []ProjectRef{project}}
}

// Within reports whether outer covers every project s covers: the check
// that a token or a member is never scoped wider than the actor who
// scoped it.
func (s ProjectScope) Within(outer ProjectScope) bool {
	if outer.All() {
		return true
	}
	if s.All() {
		return false
	}
	for _, p := range s.projects {
		if !outer.Covers(p) {
			return false
		}
	}
	return true
}

// All reports whether the scope is every project.
func (s ProjectScope) All() bool { return len(s.projects) == 0 }

// Covers reports whether the scope includes project.
func (s ProjectScope) Covers(project ProjectRef) bool {
	return s.All() || slices.Contains(s.projects, project)
}

// Strings returns the project ids, sorted; empty for every project.
func (s ProjectScope) Strings() []string {
	out := make([]string, len(s.projects))
	for i, p := range s.projects {
		out[i] = p.String()
	}
	return out
}

// UUIDs returns the project ids, sorted; empty for every project.
func (s ProjectScope) UUIDs() []uuid.UUID {
	out := make([]uuid.UUID, len(s.projects))
	for i, p := range s.projects {
		out[i] = p.UUID()
	}
	return out
}

// maxEnvironmentScope bounds an environment scope.
const maxEnvironmentScope = 50

// environmentPattern is Release's environment name (release/delivery's
// ValidEnvironment), mirrored: Identity imports no other context.
var environmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// EnvironmentScope is the release environments an environment-scoped
// permission applies to (RFC 0006 §4.2): approvals.decide on a release
// request is decided per environment. The zero value is every
// environment — which is every member and every token today: nothing
// stores a narrower one yet, so the scope exists to be checked
// (authz.RequireInEnvironment) and pinned, and a stored per-member
// scope is additive. Names are Release's environment names.
type EnvironmentScope struct{ names []string }

// ParseEnvironmentScope validates, sorts and de-duplicates environment
// names. An empty list is every environment.
func ParseEnvironmentScope(names []string) (EnvironmentScope, error) {
	var out []string
	for _, n := range names {
		if !environmentPattern.MatchString(n) || n == "a" {
			return EnvironmentScope{}, fmt.Errorf("%w: %q is not an environment name", ErrInvalidEnvironmentScope, n)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(out) > maxEnvironmentScope {
		return EnvironmentScope{}, fmt.Errorf("%w: %d environments (at most %d)", ErrInvalidEnvironmentScope, len(out), maxEnvironmentScope)
	}
	slices.Sort(out)
	return EnvironmentScope{names: out}, nil
}

// All reports whether the scope is every environment.
func (s EnvironmentScope) All() bool { return len(s.names) == 0 }

// Covers reports whether the scope includes environment.
func (s EnvironmentScope) Covers(environment string) bool {
	return s.All() || slices.Contains(s.names, environment)
}

// Strings returns the names, sorted; empty for every environment.
func (s EnvironmentScope) Strings() []string { return slices.Clone(s.names) }

// Visibility says how much of the tenant a member reads (RFC 0006 §3.3).
type Visibility string

const (
	// VisibilityAll reads whatever the member's roles allow: every
	// member today.
	VisibilityAll Visibility = "all"
	// VisibilityAssigned reads only the translation units of the
	// member's assignments and what translating them needs. Every vendor
	// member has it.
	VisibilityAssigned Visibility = "assigned"
)

// ParseVisibility validates a visibility; empty means VisibilityAll.
func ParseVisibility(s string) (Visibility, error) {
	switch v := Visibility(s); v {
	case "":
		return VisibilityAll, nil
	case VisibilityAll, VisibilityAssigned:
		return v, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidVisibility, s)
	}
}

// Restriction narrows a member beyond roles and locales: the projects
// their roles apply to (§4.1), the vendor they work for, and whether
// they read the tenant or only their assignments (§3.3). The zero value
// is unrestricted.
//
// Identity puts it on every principal it builds for the member, and
// package authz enforces it (see RestrictionEnforced).
type Restriction struct {
	Projects ProjectScope
	// Vendor is zero unless the member works for a vendor.
	Vendor     VendorID
	Visibility Visibility
}

// normalized fills the zero visibility in.
func (r Restriction) normalized() Restriction {
	if r.Visibility == "" {
		r.Visibility = VisibilityAll
	}
	return r
}

// check validates r against the roles it narrows.
//
// A vendor member reads only their assignments (§3.3), and someone who
// reads only their assignments writes translations in them and does
// nothing else — they "can't review, publish, export, import, or list
// anything else" — so the only role that fits is translator. An owner
// answers for the whole tenant, so an owner is never project-scoped.
func (r Restriction) check(roles Roles) error {
	if _, err := ParseVisibility(string(r.Visibility)); err != nil {
		return err
	}
	if !r.Vendor.IsZero() && r.Visibility != VisibilityAssigned {
		return ErrVendorMemberVisibility
	}
	if r.Visibility == VisibilityAssigned && !slices.Equal(roles, Roles{RoleTranslator}) {
		return ErrAssignedVisibilityRole
	}
	if !r.Projects.All() && roles.Has(RoleOwner) {
		return ErrOwnerProjectScoped
	}
	return nil
}

// VendorID identifies a vendor.
type VendorID uuid.UUID

// GroupID identifies a group.
type GroupID uuid.UUID

// NewVendorID returns a fresh, time-ordered ID.
func NewVendorID() VendorID { return VendorID(newV7()) }

// NewGroupID returns a fresh, time-ordered ID.
func NewGroupID() GroupID { return GroupID(newV7()) }

// ParseVendorID parses the canonical string form.
func ParseVendorID(s string) (VendorID, error) {
	u, err := parseID(s)
	return VendorID(u), err
}

// ParseGroupID parses the canonical string form.
func ParseGroupID(s string) (GroupID, error) {
	u, err := parseID(s)
	return GroupID(u), err
}

func (id VendorID) String() string  { return uuid.UUID(id).String() }
func (id VendorID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id VendorID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }
func (id GroupID) String() string   { return uuid.UUID(id).String() }
func (id GroupID) UUID() uuid.UUID  { return uuid.UUID(id) }
func (id GroupID) IsZero() bool     { return uuid.UUID(id) == uuid.Nil }
