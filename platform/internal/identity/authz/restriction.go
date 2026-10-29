package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// ErrNotVisible means the caller may not see the thing addressed: a
// project outside their project scope (RFC 0006 §4.1), or a translation
// unit outside the assignments of a member whose visibility is
// `assigned` (§3.3). It is deliberately not ErrForbidden: the answer to
// "is this here?" must be the answer for something that does not exist,
// so every surface renders it exactly as its own not-found — the HTTP
// edge as 404 not_found "no such resource", MCP as not found.
var ErrNotVisible = errors.New("authz: not found")

// assignedTenantPermissions are the permissions a member whose
// visibility is `assigned` holds outside any unit: reading the tenant
// they belong to (its name, for the tenant switcher). Everything else
// they do goes through a coverage-aware check — RequireUnit,
// RequireMessage, RequireProject, RequireLocaleIn or Visible — which
// admits them only to the units their assignments cover. Require,
// RequireFor, RequireIn, RequireForIn and ScopeOf refuse them anything
// else, so a read path that forgot to filter fails closed.
var assignedTenantPermissions = []Permission{domain.PermTenantRead}

// Assigned reports whether the principal reads only the translation
// units of its assignments (RFC 0006 §3.3): every vendor member.
func (p Principal) Assigned() bool { return p.Visibility == domain.VisibilityAssigned }

// InProject reports whether project is inside the principal's project
// scope (RFC 0006 §4.1). An empty scope is every project.
func (p Principal) InProject(project uuid.UUID) bool {
	return p.Projects.Covers(domain.ProjectRef(project))
}

// assignedDenied is the refusal for an `assigned` principal asking for
// something no unit of theirs can justify.
func assignedDenied(perm Permission) error {
	return &DeniedError{Permission: perm, Assigned: true}
}

// InProject returns ErrNotVisible unless project is inside the project
// scope of the principal on ctx. It is the check for a row a use case
// has already loaded by another id — an import job, an AI suggestion, a
// Git connection — whose project decides whether the caller may see
// it; the permission was checked before the load.
func InProject(ctx context.Context, project uuid.UUID) error {
	p, err := inTenant(ctx)
	if err != nil {
		return err
	}
	if !p.InProject(project) {
		return ErrNotVisible
	}
	return nil
}

// RequireUnscoped returns nil if the principal holds perm, as Require,
// and is not limited to some projects. It is for the few acts that
// reach every project or make a new one — creating a project, which a
// project-scoped principal could then not even see.
func RequireUnscoped(ctx context.Context, perm Permission) error {
	if err := Require(ctx, perm); err != nil {
		return err
	}
	if p, _ := From(ctx); !p.Projects.All() {
		return &DeniedError{Permission: perm, ProjectScoped: true}
	}
	return nil
}

// RequireIn returns nil if the principal holds perm in project: the
// project is inside its project scope (otherwise ErrNotVisible, the
// answer for a project that does not exist) and the grant allows perm.
// It is the check for every project-addressed use case. A member whose
// visibility is `assigned` is refused: what they may see in a project
// is decided per unit, through RequireUnit, RequireMessage,
// RequireProject or Visible.
func RequireIn(ctx context.Context, perm Permission, project uuid.UUID) error {
	p, err := inProject(ctx, project)
	if err != nil {
		return err
	}
	if p.Assigned() {
		return assignedDenied(perm)
	}
	if !p.Grant.Allows(perm) {
		return &DeniedError{Permission: perm}
	}
	return nil
}

// RequireForIn is RequireIn for a locale-scoped permission: perm for
// locale in project.
func RequireForIn(ctx context.Context, perm Permission, locale Locale, project uuid.UUID) error {
	p, err := inProject(ctx, project)
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

// RequireInEnvironment is RequireIn for an environment-scoped
// permission (RFC 0006 §4.2): perm for release environment in project.
// It is how approvals.decide is checked on a release request. The
// grant must hold perm at all — in any locale, because a release ships
// every locale and a member's locale scope speaks about text, not about
// environments — and the principal's environment scope must cover
// environment. A project outside the principal's scope is
// ErrNotVisible; an `assigned` member is refused.
func RequireInEnvironment(ctx context.Context, perm Permission, project uuid.UUID, environment string) error {
	p, err := inProject(ctx, project)
	if err != nil {
		return err
	}
	if p.Assigned() {
		return assignedDenied(perm)
	}
	if !perm.EnvironmentScoped() {
		return fmt.Errorf("authz: %s is not environment-scoped; check it with RequireIn", perm)
	}
	if !p.Grant.Holds(perm) || !p.Environments.Covers(environment) {
		return &DeniedError{Permission: perm, Environment: environment}
	}
	return nil
}

// RequireUnit returns nil if the principal holds perm for one
// translation unit — message in locale, in project. A project outside
// its scope, or (for an `assigned` member) a unit no assignment of
// theirs covers, is ErrNotVisible. For a locale-scoped permission the
// grant must allow it for locale; for any other, at all.
func RequireUnit(ctx context.Context, perm Permission, project, message uuid.UUID, locale Locale) error {
	p, err := inProject(ctx, project)
	if err != nil {
		return err
	}
	if !p.Grant.AllowsFor(perm, locale) {
		return &DeniedError{Permission: perm, Locale: locale.String()}
	}
	if !p.Assigned() {
		return nil
	}
	if p.Coverage == nil {
		return ErrNotVisible
	}
	ok, err := p.Coverage.Covers(ctx, p.Member, project, message, locale.String())
	if err != nil {
		return fmt.Errorf("authz: coverage: %w", err)
	}
	if !ok {
		return ErrNotVisible
	}
	return nil
}

// RequireMessage returns nil if the principal holds perm for the source
// side of message in project — the message itself, its description,
// its source history, usages and screenshots. For an `assigned` member
// that is a message one of their units is in, in any locale.
func RequireMessage(ctx context.Context, perm Permission, project, message uuid.UUID) error {
	v, err := Visible(ctx, perm, project)
	if err != nil {
		return err
	}
	if !v.Message(message) {
		return ErrNotVisible
	}
	return nil
}

// RequireProject returns nil if the principal holds perm in project and
// may know the project at all: for an `assigned` member, only while an
// assignment of theirs covers a unit in it. It admits them to what
// working in a project needs and is not a unit — the project itself and
// its locales — and nothing that lists units; those use Visible.
func RequireProject(ctx context.Context, perm Permission, project uuid.UUID) error {
	_, err := Visible(ctx, perm, project)
	return err
}

// RequireLocaleIn returns nil if the principal holds perm for locale in
// project: for an `assigned` member, only for a locale one of their
// units in project is in — the style guide and termbase translating
// those units needs (RFC 0006 §3.3).
func RequireLocaleIn(ctx context.Context, perm Permission, project uuid.UUID, locale Locale) error {
	v, err := Visible(ctx, perm, project)
	if err != nil {
		return err
	}
	if !v.Locale(locale.String()) {
		return ErrNotVisible
	}
	return nil
}

// View is what of one project a principal may see, for a list path to
// filter by — in its query, before pagination, so neither a page's
// size nor its cursor betrays what was filtered out.
type View struct {
	all bool
	set CoveredSet
}

// All reports whether the view is the whole project: everyone whose
// visibility is not `assigned`.
func (v View) All() bool { return v.all }

// Unit reports whether the view includes message in locale (a canonical
// BCP 47 tag).
func (v View) Unit(message uuid.UUID, locale string) bool { return v.all || v.set.Has(message, locale) }

// Message reports whether the view includes message in some locale.
func (v View) Message(message uuid.UUID) bool { return v.all || v.set.HasMessage(message) }

// Locale reports whether the view includes some unit in locale.
func (v View) Locale(locale string) bool {
	if v.all {
		return true
	}
	for u := range v.set.units {
		if u.Locale == locale {
			return true
		}
	}
	return false
}

// Messages returns the messages the view includes, sorted, for a query
// that filters by id; nil when All.
func (v View) Messages() []uuid.UUID {
	if v.all {
		return nil
	}
	out := []uuid.UUID{}
	for u := range v.set.units {
		if !slices.Contains(out, u.Message) {
			out = append(out, u.Message)
		}
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return out
}

// Units returns the units the view includes, sorted by message and
// locale; nil when All.
func (v View) Units() []Unit {
	if v.all {
		return nil
	}
	out := make([]Unit, 0, len(v.set.units))
	for u := range v.set.units {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b Unit) int {
		if c := slices.Compare(a.Message[:], b.Message[:]); c != 0 {
			return c
		}
		switch {
		case a.Locale < b.Locale:
			return -1
		case a.Locale > b.Locale:
			return 1
		}
		return 0
	})
	return out
}

// Visible returns what of project the principal may see with perm: All
// for everyone whose visibility is not `assigned`, the covered units
// for an `assigned` member. A project outside the principal's scope is
// ErrNotVisible, and so — for an `assigned` member — is a project no
// assignment of theirs covers a unit in, and every project when no
// Coverage is wired (fail closed). That answer comes before any lookup
// the caller makes, so a project that exists and one that does not are
// told apart by nothing. A permission the grant lacks entirely is a
// DeniedError. A locale-scoped permission is checked per unit by the
// caller (RequireUnit, or the list's own locale filter).
func Visible(ctx context.Context, perm Permission, project uuid.UUID) (View, error) {
	p, err := inProject(ctx, project)
	if err != nil {
		return View{}, err
	}
	if _, ok := p.Grant.Locales(perm); !ok {
		return View{}, &DeniedError{Permission: perm}
	}
	if !p.Assigned() {
		return View{all: true}, nil
	}
	if p.Coverage == nil {
		return View{}, ErrNotVisible
	}
	set, err := p.Coverage.Covered(ctx, p.Member, project)
	if err != nil {
		return View{}, fmt.Errorf("authz: coverage: %w", err)
	}
	if set.Len() == 0 {
		return View{}, ErrNotVisible
	}
	return View{set: set}, nil
}

// ProjectFilter is which projects a principal may see rows of, for a
// tenant-level list of project-owned rows (import and export jobs, AI
// jobs and suggestions, Git connections) to filter by in its query.
type ProjectFilter struct {
	all      bool
	projects []uuid.UUID
}

// All reports whether every project is visible.
func (f ProjectFilter) All() bool { return f.all }

// IDs returns the visible projects, sorted; nil when All. An empty,
// non-nil slice means none.
func (f ProjectFilter) IDs() []uuid.UUID {
	if f.all {
		return nil
	}
	return slices.Clone(f.projects)
}

// Allows reports whether the filter includes project.
func (f ProjectFilter) Allows(project uuid.UUID) bool {
	return f.all || slices.Contains(f.projects, project)
}

// Projects returns the projects the principal on ctx may see rows of
// with perm, after checking perm as Require does (so an `assigned`
// member is refused: none of these lists is theirs to read).
func Projects(ctx context.Context, perm Permission) (ProjectFilter, error) {
	if err := Require(ctx, perm); err != nil {
		return ProjectFilter{}, err
	}
	p, _ := From(ctx)
	if p.Projects.All() {
		return ProjectFilter{all: true}, nil
	}
	ids := p.Projects.UUIDs()
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ProjectFilter{projects: ids}, nil
}

// inProject is inTenant plus the project-scope check every
// project-addressed check starts with.
func inProject(ctx context.Context, project uuid.UUID) (Principal, error) {
	p, err := inTenant(ctx)
	if err != nil {
		return Principal{}, err
	}
	if !p.InProject(project) {
		return Principal{}, ErrNotVisible
	}
	return p, nil
}

// RequireRow is the check for a project-owned row a use case reads by
// its own id, under no project in the path — an AI fill, job or
// suggestion, a Git connection, an import or export job — whose project
// decides what the caller may know of it (RFC 0006 §3.3, §4.1). load
// reads the row and returns its project.
//
// For a caller who sees the whole tenant it is Require before the load,
// as it always was, so a caller without the permission learns nothing
// about which ids exist. For one limited to part of the tenant — a
// project scope, or visibility `assigned` — the row is read first, so
// that a row of a project the caller cannot see is ErrNotVisible, the
// answer for a row that does not exist, and not a refusal that says it
// is there: the order every project-addressed check has (RequireIn).
// An `assigned` member cannot see a project no assignment of theirs
// covers a unit in, as with Visible; inside one, they are refused.
func RequireRow(ctx context.Context, perm Permission, load func() (uuid.UUID, error)) error {
	p, err := inTenant(ctx)
	if err != nil {
		return err
	}
	if !p.Assigned() && p.Projects.All() {
		if err := Require(ctx, perm); err != nil {
			return err
		}
	}
	project, err := load()
	if err != nil {
		return err
	}
	if !p.InProject(project) {
		return ErrNotVisible
	}
	if p.Assigned() {
		if p.Coverage == nil {
			return ErrNotVisible
		}
		set, err := p.Coverage.Covered(ctx, p.Member, project)
		if err != nil {
			return fmt.Errorf("authz: coverage: %w", err)
		}
		if set.Len() == 0 {
			return ErrNotVisible
		}
	}
	return RequireIn(ctx, perm, project)
}
