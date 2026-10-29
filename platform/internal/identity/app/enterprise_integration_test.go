//go:build integration

package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func newProjectID() string { return uuid.Must(uuid.NewV7()).String() }

func TestVendorsAndRestrictedMembers(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)

	v, replayed, err := h.svc.CreateVendor(inAcme, app.VendorInput{Name: "Lingo GmbH", Contact: "pm@lingo.example", Locales: []string{"fr", "de"}}, "v-1")
	if err != nil || replayed {
		t.Fatalf("create vendor = %+v, %t, %v", v, replayed, err)
	}
	if again, replayed, err := h.svc.CreateVendor(inAcme, app.VendorInput{Name: "Lingo GmbH"}, "v-1"); err != nil || !replayed || again.ID != v.ID {
		t.Errorf("replay = %+v, %t, %v", again, replayed, err)
	}
	if _, _, err := h.svc.CreateVendor(inAcme, app.VendorInput{Name: "LINGO gmbh"}, ""); !errors.Is(err, app.ErrNameTaken) {
		t.Errorf("a second vendor of the same name err = %v", err)
	}
	if got, err := h.svc.GetVendor(inAcme, v.ID); err != nil || !reflect.DeepEqual(got.LocaleStrings(), []string{"de", "fr"}) || got.Contact != "pm@lingo.example" {
		t.Errorf("get vendor = %+v, %v", got, err)
	}

	project := newProjectID()
	m, _, err := h.svc.InviteMember(inAcme, app.Invitation{
		Email: "vera@lingo.example", Roles: []string{"translator"}, Locales: []string{"de"},
		Projects: []string{project}, Vendor: v.ID.String(), Visibility: "assigned",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := h.svc.GetMember(inAcme, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r := stored.Restriction; r.Vendor != v.ID || r.Visibility != domain.VisibilityAssigned || !reflect.DeepEqual(r.Projects.Strings(), []string{project}) {
		t.Errorf("stored restriction = %+v", r)
	}
	if _, _, err := h.svc.InviteMember(inAcme, app.Invitation{
		Email: "x@lingo.example", Roles: []string{"translator"}, Vendor: domain.NewVendorID().String(), Visibility: "assigned",
	}, ""); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("inviting into an unknown vendor err = %v", err)
	}

	if err := h.svc.DeleteVendor(inAcme, v.ID); !errors.Is(err, domain.ErrVendorHasMembers) {
		t.Errorf("deleting a vendor with members err = %v", err)
	}
	cleared, err := h.svc.RestrictMember(inAcme, m.ID, stored.Version, app.RestrictionChange{
		Vendor: ptr(""), Visibility: ptr("all"), Projects: &[]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := cleared.Restriction; !r.Vendor.IsZero() || r.Visibility != domain.VisibilityAll || !r.Projects.All() || cleared.Version != stored.Version+1 {
		t.Errorf("cleared = %+v", cleared)
	}
	if _, err := h.svc.RestrictMember(inAcme, m.ID, stored.Version, app.RestrictionChange{Visibility: ptr("assigned")}); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale restriction change err = %v", err)
	}
	updated, err := h.svc.UpdateVendor(inAcme, v.ID, v.Version, app.VendorInput{Name: "Lingo AG", Locales: []string{"ja"}})
	if err != nil || updated.Name != "Lingo AG" || updated.Version != 2 {
		t.Errorf("update vendor = %+v, %v", updated, err)
	}
	if err := h.svc.DeleteVendor(inAcme, v.ID); err != nil {
		t.Errorf("deleting a vendor nobody works for: %v", err)
	}

	for typ, want := range map[string]int{
		domain.EventVendorCreated: 1, domain.EventVendorChanged: 1, domain.EventVendorDeleted: 1,
		domain.EventMemberRestrictionChanged: 1,
	} {
		if n := count(t, `SELECT count(*) FROM outbox_events WHERE tenant_id = $1 AND event_type = $2`, acme.ID.UUID(), typ); n != want {
			t.Errorf("%d %s events, want %d", n, typ, want)
		}
	}
}

// Naming a vendor takes vendors.manage on top of members.manage. An
// admin-scoped token manages members but not vendors.
func TestNamingAVendorNeedsVendorsManage(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	v, _, err := h.svc.CreateVendor(inAcme, app.VendorInput{Name: "Lingo"}, "")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := h.svc.CreateToken(inAcme, "admin", []string{"admin"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	asToken := h.asToken(t, tok.Secret.String())
	if _, _, err := h.svc.InviteMember(asToken, app.Invitation{
		Email: "v@lingo.example", Roles: []string{"translator"}, Vendor: v.ID.String(), Visibility: "assigned",
	}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("an admin token naming a vendor err = %v", err)
	}
	if _, _, err := h.svc.CreateVendor(asToken, app.VendorInput{Name: "Other"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("an admin token creating a vendor err = %v", err)
	}
}

func TestGroups(t *testing.T) {
	h := newHarness(t)
	ada, bob := h.signUp(t, "ada@example.com"), h.signUp(t, "bob@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	bolt, _, err := h.svc.CreateOrganization(h.as(t, bob, tenancy.ID{}), "bolt", "Bolt", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme, inBolt := h.as(t, ada, acme.ID), h.as(t, bob, bolt.ID)

	g, _, err := h.svc.CreateGroup(inAcme, "de reviewers", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateGroup(inAcme, "DE Reviewers", ""); !errors.Is(err, app.ErrNameTaken) {
		t.Errorf("a second group of the same name err = %v", err)
	}
	carol, _, err := h.svc.AddMember(inAcme, "carol@example.com", []string{"reviewer"}, []string{"de"}, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err = h.svc.AddGroupMember(inAcme, g.ID, carol.ID)
	if err != nil || !g.Has(carol.ID) || g.Version != 2 {
		t.Fatalf("add = %+v, %v", g, err)
	}
	if _, err := h.svc.AddGroupMember(inAcme, g.ID, carol.ID); !errors.Is(err, domain.ErrAlreadyInGroup) {
		t.Errorf("adding twice err = %v", err)
	}
	if got, err := h.svc.GetGroup(inAcme, g.ID); err != nil || !reflect.DeepEqual(got.Members, []domain.MemberID{carol.ID}) {
		t.Errorf("get = %+v, %v", got, err)
	}
	if renamed, err := h.svc.RenameGroup(inAcme, g.ID, g.Version, "German reviewers"); err != nil || renamed.Name != "German reviewers" {
		t.Errorf("rename = %+v, %v", renamed, err)
	}

	// Bob's tenant can neither see Acme's group nor put Acme's member in
	// a group of its own.
	if _, err := h.svc.GetGroup(inBolt, g.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("bolt reading acme's group err = %v", err)
	}
	boltGroup, _, err := h.svc.CreateGroup(inBolt, "legal", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AddGroupMember(inBolt, boltGroup.ID, carol.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("bolt grouping acme's member err = %v", err)
	}
	if groups, _, err := h.svc.ListGroups(inBolt, firstPage()); err != nil || len(groups) != 1 || groups[0].ID != boltGroup.ID {
		t.Errorf("bolt's groups = %+v, %v", groups, err)
	}

	// A member who leaves the tenant leaves its groups.
	if err := h.svc.RemoveMember(inAcme, carol.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.GetGroup(inAcme, g.ID); err != nil || len(got.Members) != 0 {
		t.Errorf("after removing the member = %+v, %v", got, err)
	}
	if _, err := h.svc.RemoveGroupMember(inAcme, g.ID, carol.ID); !errors.Is(err, domain.ErrNotInGroup) {
		t.Errorf("removing a non-member err = %v", err)
	}
	if err := h.svc.DeleteGroup(inAcme, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GetGroup(inAcme, g.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("deleted group err = %v", err)
	}
}

func TestProjectScopedTokenIsStored(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	project := newProjectID()
	created, err := h.svc.IssueToken(inAcme, app.TokenRequest{Name: "ci", Scopes: []string{"read", "write"}, Projects: []string{project}}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.GetToken(inAcme, created.Token.ID)
	if err != nil || !reflect.DeepEqual(got.Projects.Strings(), []string{project}) {
		t.Errorf("stored token = %+v, %v", got, err)
	}
	if _, err := h.svc.IssueToken(inAcme, app.TokenRequest{Name: "bad", Scopes: []string{"read"}, Projects: []string{"nope"}}, ""); !errors.Is(err, domain.ErrInvalidID) {
		t.Errorf("malformed project err = %v", err)
	}
}

// TestRestrictionsAreEnforced is what wave 1's tripwire
// (TestRestrictionsAreNotYetEnforced) said would replace it: the
// principals Identity builds now carry the restriction — through the
// system-scope lookups that build them (migration 0046) — and authz
// holds them to it. A vendor member reads only their assignments and is
// refused the tenant's member list; a project-scoped token does not see
// a project outside its scope; and nobody scopes a token or an
// invitation wider than themselves.
func TestRestrictionsAreEnforced(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	v, _, err := h.svc.CreateVendor(inAcme, app.VendorInput{Name: "Lingo"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, _, err := h.svc.InviteMember(inAcme, app.Invitation{
		Email: "vera@lingo.example", Roles: []string{"translator"}, Locales: []string{"de"},
		Projects: []string{project.String()}, Vendor: v.ID.String(), Visibility: "assigned",
	}, ""); err != nil {
		t.Fatal(err)
	}
	vera := h.signUp(t, "vera@lingo.example")
	asVera := h.as(t, vera, acme.ID)
	p, _ := authz.From(asVera)
	if !p.Assigned() || p.Member.IsZero() || !p.InProject(project) || p.InProject(other) {
		t.Fatalf("a vendor member's principal = visibility %q, projects %v; want assigned, limited to %s",
			p.Visibility, p.Projects.Strings(), project)
	}
	if p.Coverage != nil {
		t.Error("no Coverage is wired in this harness, so the principal must carry none (and see nothing)")
	}
	if _, _, err := h.svc.ListMembers(asVera, firstPage()); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a vendor member lists the tenant's members: %v", err)
	}
	if _, err := h.svc.GetTenant(asVera); err != nil {
		t.Errorf("a vendor member reads their own tenant: %v", err)
	}
	if err := authz.RequireProject(asVera, authz.CatalogRead, project); !errors.Is(err, authz.ErrNotVisible) {
		t.Errorf("with no Coverage wired an assigned member must see no project, got %v", err)
	}
	if _, err := h.svc.MintInContextGrant(asVera, domain.ProjectRef(project), "https://preview.acme.example"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("an assigned member minted an in-context grant: %v", err)
	}

	tok, err := h.svc.IssueToken(inAcme, app.TokenRequest{Name: "ci", Scopes: []string{"read"}, Projects: []string{project.String()}}, "")
	if err != nil {
		t.Fatal(err)
	}
	asTok := h.asToken(t, tok.Secret.String())
	tp, _ := authz.From(asTok)
	if !tp.InProject(project) || tp.InProject(other) {
		t.Fatalf("a project-scoped token's principal = projects %v", tp.Projects.Strings())
	}
	if err := authz.RequireIn(asTok, authz.CatalogRead, other); !errors.Is(err, authz.ErrNotVisible) {
		t.Errorf("a project-scoped token reaches another project: %v", err)
	}
	if err := authz.RequireIn(asTok, authz.CatalogRead, project); err != nil {
		t.Errorf("a project-scoped token in its project: %v", err)
	}

	// Never wider than the one who scopes it.
	if _, _, err := h.svc.InviteMember(inAcme, app.Invitation{
		Email: "dev@acme.example", Roles: []string{"admin"}, Projects: []string{project.String()},
	}, ""); err != nil {
		t.Fatal(err)
	}
	dev := h.signUp(t, "dev@acme.example")
	asDev := h.as(t, dev, acme.ID)
	cut, err := h.svc.IssueToken(asDev, app.TokenRequest{Name: "mine", Scopes: []string{"read"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := cut.Token.Projects.Strings(); len(got) != 1 || got[0] != project.String() {
		t.Errorf("a project-scoped admin's unscoped token = %v; want it cut to %s", got, project)
	}
	if _, err := h.svc.IssueToken(asDev, app.TokenRequest{Name: "wide", Scopes: []string{"read"}, Projects: []string{other.String()}}, ""); !errors.Is(err, domain.ErrScopeExceedsGrant) {
		t.Errorf("a project-scoped admin issued a token for another project: %v", err)
	}
	invited, _, err := h.svc.InviteMember(asDev, app.Invitation{Email: "alt@acme.example", Roles: []string{"admin"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := invited.Restriction.Projects.Strings(); len(got) != 1 || got[0] != project.String() {
		t.Errorf("a project-scoped admin invited an unscoped member: projects %v", got)
	}
}

func mustRoles(t *testing.T, rs ...string) domain.Roles {
	t.Helper()
	r, err := domain.ParseRoles(rs)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustLocales(t *testing.T, ls ...string) domain.LocaleScope {
	t.Helper()
	s, err := domain.ParseLocaleScope(ls)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
