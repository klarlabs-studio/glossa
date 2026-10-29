package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestNewGroup(t *testing.T) {
	tenant := tenancy.NewID()
	g, err := domain.NewGroup(tenant, tenancy.KindOrganization, "  de reviewers ", now)
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "de reviewers" || g.TenantID != tenant || g.Version != 1 || len(g.Members) != 0 {
		t.Errorf("group = %+v", g)
	}
	if _, err := domain.NewGroup(tenant, tenancy.KindIndividual, "legal", now); !errors.Is(err, domain.ErrIndividualTenant) {
		t.Errorf("a group in an individual tenant err = %v", err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("x", 101)} {
		if _, err := domain.NewGroup(tenant, tenancy.KindOrganization, name, now); !errors.Is(err, domain.ErrInvalidGroupName) {
			t.Errorf("name %q err = %v", name, err)
		}
	}
}

func TestGroupMembers(t *testing.T) {
	tenant := tenancy.NewID()
	g, err := domain.NewGroup(tenant, tenancy.KindOrganization, "legal", now)
	if err != nil {
		t.Fatal(err)
	}
	member := func(email string, in tenancy.ID) domain.Member {
		m, err := domain.Invite(in, tenancy.KindOrganization, mustEmail(t, email), mustRoles(t, "reviewer"), domain.LocaleScope{}, adminGrant(t), now)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := member("a@example.com", tenant), member("b@example.com", tenant)

	if err := g.AddMember(b, now); err != nil {
		t.Fatal(err)
	}
	if err := g.AddMember(a, now); err != nil {
		t.Fatal(err)
	}
	if err := g.AddMember(a, now); !errors.Is(err, domain.ErrAlreadyInGroup) {
		t.Errorf("adding a member twice err = %v", err)
	}
	if !g.Has(a.ID) || !g.Has(b.ID) || g.Version != 3 {
		t.Errorf("group = %+v", g)
	}
	want := []domain.MemberID{a.ID, b.ID}
	if a.ID.String() > b.ID.String() {
		want = []domain.MemberID{b.ID, a.ID}
	}
	if !reflect.DeepEqual(g.Members, want) {
		t.Errorf("members = %v, want sorted %v", g.Members, want)
	}

	if err := g.AddMember(member("x@example.com", tenancy.NewID()), now); !errors.Is(err, domain.ErrMemberOfAnotherTenant) {
		t.Errorf("a member of another tenant err = %v", err)
	}
	if err := g.RemoveMember(a.ID, now); err != nil || g.Has(a.ID) {
		t.Errorf("remove = %v, has = %t", err, g.Has(a.ID))
	}
	if err := g.RemoveMember(a.ID, now); !errors.Is(err, domain.ErrNotInGroup) {
		t.Errorf("removing a non-member err = %v", err)
	}
	if err := g.Rename("Legal & Compliance", now); err != nil || g.Name != "Legal & Compliance" {
		t.Errorf("rename = %v, name = %q", err, g.Name)
	}
}

// Groups carry no permissions (§4.3): a group is a name for people,
// and nothing in it can widen or narrow what a member may do.
func TestGroupsCarryNoPermissions(t *testing.T) {
	if f, ok := reflect.TypeOf(domain.Group{}).FieldByName("Roles"); ok {
		t.Errorf("a group must not carry roles, found field %v", f)
	}
}

func TestGroupMemberLimit(t *testing.T) {
	tenant := tenancy.NewID()
	g, err := domain.NewGroup(tenant, tenancy.KindOrganization, "everyone", now)
	if err != nil {
		t.Fatal(err)
	}
	for range domain.MaxGroupMembers {
		g.Members = append(g.Members, domain.NewMemberID())
	}
	m, err := domain.Invite(tenant, tenancy.KindOrganization, mustEmail(t, "late@example.com"), mustRoles(t, "translator"), domain.LocaleScope{}, adminGrant(t), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.AddMember(m, now); !errors.Is(err, domain.ErrGroupFull) {
		t.Errorf("a full group err = %v", err)
	}
}

func TestNewVendor(t *testing.T) {
	tenant := tenancy.NewID()
	v, err := domain.NewVendor(tenant, tenancy.KindOrganization, " Acme Translations ", "pm@acme.example", []string{"fr", "de", "de"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Acme Translations" || v.Contact != "pm@acme.example" || v.Version != 1 {
		t.Errorf("vendor = %+v", v)
	}
	if got := v.LocaleStrings(); !reflect.DeepEqual(got, []string{"de", "fr"}) {
		t.Errorf("locales = %v", got)
	}
	if _, err := domain.NewVendor(tenant, tenancy.KindIndividual, "Acme", "", nil, now); !errors.Is(err, domain.ErrIndividualTenant) {
		t.Errorf("a vendor in an individual tenant err = %v", err)
	}
	if _, err := domain.NewVendor(tenant, tenancy.KindOrganization, "", "", nil, now); !errors.Is(err, domain.ErrInvalidVendorName) {
		t.Errorf("empty name err = %v", err)
	}
	if _, err := domain.NewVendor(tenant, tenancy.KindOrganization, "Acme", strings.Repeat("c", 201), nil, now); !errors.Is(err, domain.ErrInvalidVendorContact) {
		t.Errorf("long contact err = %v", err)
	}
	if _, err := domain.NewVendor(tenant, tenancy.KindOrganization, "Acme", "", []string{"und"}, now); !errors.Is(err, domain.ErrInvalidLocale) {
		t.Errorf("bad locale err = %v", err)
	}
	if err := v.Change("Acme GmbH", "", []string{"ja"}, now); err != nil || v.Name != "Acme GmbH" || v.Version != 2 ||
		!reflect.DeepEqual(v.LocaleStrings(), []string{"ja"}) {
		t.Errorf("change = %v, vendor = %+v", err, v)
	}
}
