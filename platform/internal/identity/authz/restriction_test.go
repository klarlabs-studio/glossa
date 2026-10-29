package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// TestRestrictionEnforcement pins RFC 0006 §3.3 and §4.1 as authz
// answers them, for every kind of principal Identity builds. It is the
// enforcement wave 1's tripwires (domain's
// TestRestrictionIsModelledButNotYetEnforced and identity/app's
// TestRestrictionsAreNotYetEnforced) said would replace them.
func TestRestrictionEnforcement(t *testing.T) {
	tenant := tenancy.NewID()
	bg := context.Background()
	inScope, outOfScope := uuid.New(), uuid.New()
	covered, uncovered := uuid.New(), uuid.New()
	de, _ := authz.ParseLocale("de")
	fr, _ := authz.ParseLocale("fr")

	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(bg, tenant, cov, "de", "fr")
	cov.Assign(member, inScope, covered, "de")
	unwired, _ := authztest.Assigned(bg, tenant, nil, "de")

	type check func(context.Context) error
	notVisible, denied, ok := "not visible", "denied", "ok"
	requireIn := func(perm authz.Permission, project uuid.UUID) check {
		return func(ctx context.Context) error { return authz.RequireIn(ctx, perm, project) }
	}
	unit := func(perm authz.Permission, project, message uuid.UUID, l authz.Locale) check {
		return func(ctx context.Context) error { return authz.RequireUnit(ctx, perm, project, message, l) }
	}
	message := func(project, msg uuid.UUID) check {
		return func(ctx context.Context) error { return authz.RequireMessage(ctx, authz.CatalogRead, project, msg) }
	}
	project := func(p uuid.UUID) check {
		return func(ctx context.Context) error { return authz.RequireProject(ctx, authz.CatalogRead, p) }
	}
	localeIn := func(p uuid.UUID, l authz.Locale) check {
		return func(ctx context.Context) error { return authz.RequireLocaleIn(ctx, authz.KnowledgeRead, p, l) }
	}
	require := func(perm authz.Permission) check {
		return func(ctx context.Context) error { return authz.Require(ctx, perm) }
	}
	unscoped := func(perm authz.Permission) check {
		return func(ctx context.Context) error { return authz.RequireUnscoped(ctx, perm) }
	}

	for _, tc := range []struct {
		name  string
		ctx   context.Context
		check check
		want  string
	}{
		// An unrestricted member sees exactly what they saw before.
		{"unrestricted: any project", authztest.Member(bg, tenant, []string{"developer"}), requireIn(authz.CatalogRead, outOfScope), ok},
		{"unrestricted: any unit", authztest.Member(bg, tenant, []string{"translator"}), unit(authz.TranslationsWrite, inScope, uncovered, fr), ok},
		{"unrestricted: creates projects", authztest.Member(bg, tenant, []string{"developer"}), unscoped(authz.CatalogWrite), ok},

		// Project scope: outside it a project does not exist.
		{"scoped member: in scope", authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, []string{"developer"}), requireIn(authz.CatalogRead, inScope), ok},
		{"scoped member: out of scope", authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, []string{"developer"}), requireIn(authz.CatalogRead, outOfScope), notVisible},
		{"scoped member: out of scope, even without the permission", authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, []string{"translator"}), requireIn(authz.CatalogWrite, outOfScope), notVisible},
		{"scoped member: lacking the permission in scope", authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, []string{"translator"}), requireIn(authz.CatalogWrite, inScope), denied},
		{"scoped member: a unit out of scope", authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, []string{"translator"}), unit(authz.TranslationsWrite, outOfScope, covered, de), notVisible},
		{"scoped member: can't create a project", authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, []string{"developer"}), unscoped(authz.CatalogWrite), denied},
		{"scoped token: in scope", authztest.ScopedToken(bg, tenant, []uuid.UUID{inScope}, "read"), requireIn(authz.ReleasesRead, inScope), ok},
		{"scoped token: out of scope", authztest.ScopedToken(bg, tenant, []uuid.UUID{inScope}, "read"), requireIn(authz.ReleasesRead, outOfScope), notVisible},
		{"scoped token: tenant-level reads stay", authztest.ScopedToken(bg, tenant, []uuid.UUID{inScope}, "read"), require(authz.KnowledgeRead), ok},

		// Visibility `assigned`: covered units, and nothing else.
		{"assigned: covered unit", vendor, unit(authz.TranslationsWrite, inScope, covered, de), ok},
		{"assigned: covered message, other locale", vendor, unit(authz.TranslationsWrite, inScope, covered, fr), notVisible},
		{"assigned: uncovered message", vendor, unit(authz.TranslationsRead, inScope, uncovered, de), notVisible},
		{"assigned: covered message's source", vendor, message(inScope, covered), ok},
		{"assigned: uncovered message's source", vendor, message(inScope, uncovered), notVisible},
		{"assigned: a project they work in", vendor, project(inScope), ok},
		{"assigned: a project they don't", vendor, project(outOfScope), notVisible},
		{"assigned: style and terms for a covered locale", vendor, localeIn(inScope, de), ok},
		{"assigned: style and terms for another locale", vendor, localeIn(inScope, fr), notVisible},
		{"assigned: review is refused", vendor, unit(authz.TranslationsReview, inScope, covered, de), denied},
		{"assigned: project-wide reads are refused", vendor, requireIn(authz.CatalogRead, inScope), denied},
		{"assigned: tenant-wide reads are refused", vendor, require(authz.KnowledgeRead), denied},
		{"assigned: import is refused", vendor, require(authz.IntegrationImport), denied},
		{"assigned: publish is refused", vendor, requireIn(authz.ReleasesPublish, inScope), denied},
		{"assigned: their tenant", vendor, require(authz.TenantRead), ok},
		{"assigned, nothing wired: sees no unit", unwired, unit(authz.TranslationsRead, inScope, covered, de), notVisible},
		{"assigned, nothing wired: sees no project", unwired, project(inScope), notVisible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.check(tc.ctx)
			var got string
			switch {
			case err == nil:
				got = ok
			case errors.Is(err, authz.ErrNotVisible):
				got = notVisible
			case errors.Is(err, authz.ErrForbidden):
				got = denied
			default:
				t.Fatalf("unexpected error %v", err)
			}
			if got != tc.want {
				t.Errorf("got %s (%v), want %s", got, err, tc.want)
			}
		})
	}
}

// A view is what a list filters by: everything for an unrestricted
// principal, exactly the covered units for an assigned member.
func TestVisibleFiltersAnAssignedMembersLists(t *testing.T) {
	tenant := tenancy.NewID()
	bg := context.Background()
	p, a, b := uuid.New(), uuid.New(), uuid.New()
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(bg, tenant, cov, "de")
	cov.Assign(member, p, a, "de", "fr")

	v, err := authz.Visible(vendor, authz.TranslationsRead, p)
	if err != nil {
		t.Fatal(err)
	}
	if v.All() || !v.Unit(a, "de") || v.Unit(b, "de") || !v.Message(a) || v.Message(b) || !v.Locale("fr") || v.Locale("ja") {
		t.Errorf("view = %+v", v)
	}
	if ms := v.Messages(); len(ms) != 1 || ms[0] != a {
		t.Errorf("messages = %v, want only %s", ms, a)
	}
	if us := v.Units(); len(us) != 2 || us[0].Locale != "de" || us[1].Locale != "fr" {
		t.Errorf("units = %v", us)
	}

	all, err := authz.Visible(authztest.Member(bg, tenant, []string{"translator"}), authz.TranslationsRead, p)
	if err != nil || !all.All() || all.Messages() != nil || all.Units() != nil || !all.Unit(b, "ja") {
		t.Errorf("unrestricted view = %+v, %v", all, err)
	}
	if _, err := authz.Visible(vendor, authz.ReleasesPublish, p); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a permission the grant lacks: %v", err)
	}
}

func TestProjectsFilterTenantLevelLists(t *testing.T) {
	tenant := tenancy.NewID()
	bg := context.Background()
	in, out := uuid.New(), uuid.New()
	f, err := authz.Projects(authztest.ScopedToken(bg, tenant, []uuid.UUID{in}, "read"), authz.IntegrationRead)
	if err != nil || f.All() || !f.Allows(in) || f.Allows(out) || len(f.IDs()) != 1 {
		t.Errorf("scoped filter = %+v, %v", f, err)
	}
	f, err = authz.Projects(authztest.Token(bg, tenant, "read"), authz.IntegrationRead)
	if err != nil || !f.All() || f.IDs() != nil || !f.Allows(out) {
		t.Errorf("unscoped filter = %+v, %v", f, err)
	}
	vendor, _ := authztest.Assigned(bg, tenant, &authztest.Coverage{}, "de")
	if _, err := authz.Projects(vendor, authz.IntegrationRead); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("an assigned member lists tenant-level rows: %v", err)
	}
	if err := authz.InProject(authztest.ScopedToken(bg, tenant, []uuid.UUID{in}, "read"), out); !errors.Is(err, authz.ErrNotVisible) {
		t.Errorf("InProject out of scope: %v", err)
	}
	if s, err := authz.ScopeOf(vendor, authz.TranslationsWrite); err != nil || s.Granted {
		t.Errorf("an assigned member's scope snapshot must grant nothing a job could act on later: %+v, %v", s, err)
	}
}

// A row read by its own id (an AI fill, a Git connection, an import
// job) is, for a caller limited to part of the tenant, answered as a
// project-addressed read is: a row of a project they cannot see is not
// there — the answer for an id that does not exist — and never a
// refusal that says it is (RFC 0006 §3.3, §4.1, §12.2). A caller who
// sees the whole tenant is refused before the row is read, so a missing
// permission says nothing about which ids exist.
func TestRequireRowHidesRowsOutsideWhatTheCallerSees(t *testing.T) {
	tenant := tenancy.NewID()
	bg := context.Background()
	inScope, outOfScope, msg := uuid.New(), uuid.New(), uuid.New()
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(bg, tenant, cov, "de")
	cov.Assign(member, inScope, msg, "de")
	unwired, _ := authztest.Assigned(bg, tenant, nil, "de")
	scoped := func(roles ...string) context.Context {
		return authztest.ScopedMember(bg, tenant, []uuid.UUID{inScope}, roles)
	}
	missing := errors.New("no such row")

	notVisible, denied, ok, absent := "not visible", "denied", "ok", "absent"
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		perm    authz.Permission
		project uuid.UUID
		exists  bool
		want    string
		// loaded says the row was read: a caller who sees the whole
		// tenant and lacks the permission is refused before.
		loaded bool
	}{
		{"assigned: a row of a project they work in", vendor, authz.IntelligenceRead, inScope, true, denied, true},
		{"assigned: a row of a project they don't", vendor, authz.IntelligenceRead, outOfScope, true, notVisible, true},
		{"assigned: no such row", vendor, authz.IntelligenceRead, inScope, false, absent, true},
		{"assigned, nothing wired: no row is visible", unwired, authz.IntegrationRead, inScope, true, notVisible, true},
		{"scoped, lacking the permission: a row outside the scope", scoped("translator"), authz.CatalogWrite, outOfScope, true, notVisible, true},
		{"scoped, lacking the permission: a row inside it", scoped("translator"), authz.CatalogWrite, inScope, true, denied, true},
		{"scoped: a row inside the scope", scoped("developer"), authz.CatalogRead, inScope, true, ok, true},
		{"scoped: a row outside it", scoped("developer"), authz.CatalogRead, outOfScope, true, notVisible, true},
		{"unrestricted: any row", authztest.Member(bg, tenant, []string{"developer"}), authz.CatalogRead, outOfScope, true, ok, true},
		{"unrestricted, lacking the permission: refused unread", authztest.Member(bg, tenant, []string{"translator"}), authz.CatalogWrite, outOfScope, true, denied, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := false
			err := authz.RequireRow(tc.ctx, tc.perm, func() (uuid.UUID, error) {
				read = true
				if !tc.exists {
					return uuid.Nil, missing
				}
				return tc.project, nil
			})
			var got string
			switch {
			case err == nil:
				got = ok
			case errors.Is(err, missing):
				got = absent
			case errors.Is(err, authz.ErrNotVisible):
				got = notVisible
			case errors.Is(err, authz.ErrForbidden):
				got = denied
			default:
				t.Fatalf("unexpected error %v", err)
			}
			if got != tc.want {
				t.Errorf("got %s (%v), want %s", got, err, tc.want)
			}
			if read != tc.loaded {
				t.Errorf("the row was read: %v, want %v", read, tc.loaded)
			}
		})
	}
}
