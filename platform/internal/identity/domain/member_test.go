package domain_test

import (
	"errors"
	"testing"
	"time"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func mustEmail(t *testing.T, s string) authgo.Email {
	t.Helper()
	e, err := authgo.NewEmail(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func ownerGrant(t *testing.T) domain.Grant {
	return domain.GrantForMember(mustRoles(t, "owner"), domain.LocaleScope{})
}

func adminGrant(t *testing.T) domain.Grant {
	return domain.GrantForMember(mustRoles(t, "admin"), domain.LocaleScope{})
}

func TestNewOwnerIsActive(t *testing.T) {
	person := domain.NewPersonID()
	m := domain.NewOwner(tenancy.NewID(), person, mustEmail(t, "Ada@Example.com"), now)
	if m.Status != domain.MemberActive || m.PersonID != person || !m.IsOwner() || m.Version != 1 {
		t.Errorf("owner = %+v", m)
	}
	if m.Email.String() != "ada@example.com" {
		t.Errorf("email = %s, want it normalized", m.Email)
	}
}

func TestInvite(t *testing.T) {
	tenant := tenancy.NewID()
	email := mustEmail(t, "tr@example.com")

	m, err := domain.Invite(tenant, tenancy.KindOrganization, email, mustRoles(t, "translator"), mustLocales(t, "de"), adminGrant(t), now)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != domain.MemberInvited || !m.PersonID.IsZero() || m.Version != 1 {
		t.Errorf("invitation = %+v", m)
	}

	if _, err := domain.Invite(tenant, tenancy.KindIndividual, email, mustRoles(t, "translator"), domain.LocaleScope{}, ownerGrant(t), now); !errors.Is(err, domain.ErrIndividualTenant) {
		t.Errorf("inviting into an individual tenant err = %v", err)
	}
	if _, err := domain.Invite(tenant, tenancy.KindOrganization, email, mustRoles(t, "developer"), mustLocales(t, "de"), adminGrant(t), now); !errors.Is(err, domain.ErrLocalesNeedLocaleRole) {
		t.Errorf("locales on a developer err = %v", err)
	}
	if _, err := domain.Invite(tenant, tenancy.KindOrganization, email, mustRoles(t, "owner"), domain.LocaleScope{}, adminGrant(t), now); !errors.Is(err, domain.ErrOwnerChangeForbidden) {
		t.Errorf("admin inviting an owner err = %v", err)
	}
	if _, err := domain.Invite(tenant, tenancy.KindOrganization, email, mustRoles(t, "owner"), domain.LocaleScope{}, ownerGrant(t), now); err != nil {
		t.Errorf("owner inviting an owner: %v", err)
	}
}

func TestActivate(t *testing.T) {
	m, err := domain.Invite(tenancy.NewID(), tenancy.KindOrganization, mustEmail(t, "tr@example.com"), mustRoles(t, "translator"), domain.LocaleScope{}, adminGrant(t), now)
	if err != nil {
		t.Fatal(err)
	}
	person := domain.NewPersonID()
	later := now.Add(time.Hour)
	if err := m.Activate(person, later); err != nil {
		t.Fatal(err)
	}
	if m.Status != domain.MemberActive || m.PersonID != person || m.Version != 2 || !m.UpdatedAt.Equal(later) {
		t.Errorf("activated = %+v", m)
	}
	if err := m.Activate(domain.NewPersonID(), later); !errors.Is(err, domain.ErrMemberNotInvited) {
		t.Errorf("re-activation err = %v", err)
	}
}

func TestChangeAccess(t *testing.T) {
	newTranslator := func() domain.Member {
		m, err := domain.Invite(tenancy.NewID(), tenancy.KindOrganization, mustEmail(t, "tr@example.com"), mustRoles(t, "translator"), domain.LocaleScope{}, adminGrant(t), now)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	owner := func() domain.Member {
		return domain.NewOwner(tenancy.NewID(), domain.NewPersonID(), mustEmail(t, "o@example.com"), now)
	}

	t.Run("admin changes a translator's locales", func(t *testing.T) {
		m := newTranslator()
		if err := m.ChangeAccess(mustRoles(t, "translator", "reviewer"), mustLocales(t, "fr"), adminGrant(t), 1, now); err != nil {
			t.Fatal(err)
		}
		if m.Version != 2 || m.Locales.Strings()[0] != "fr" {
			t.Errorf("member = %+v", m)
		}
	})
	t.Run("admin cannot promote to owner", func(t *testing.T) {
		m := newTranslator()
		err := m.ChangeAccess(mustRoles(t, "owner"), domain.LocaleScope{}, adminGrant(t), 1, now)
		if !errors.Is(err, domain.ErrOwnerChangeForbidden) || m.Version != 1 {
			t.Errorf("err = %v, version = %d", err, m.Version)
		}
	})
	t.Run("admin cannot demote an owner", func(t *testing.T) {
		m := owner()
		if err := m.ChangeAccess(mustRoles(t, "admin"), domain.LocaleScope{}, adminGrant(t), 2, now); !errors.Is(err, domain.ErrOwnerChangeForbidden) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("the last owner cannot be demoted", func(t *testing.T) {
		m := owner()
		if err := m.ChangeAccess(mustRoles(t, "admin"), domain.LocaleScope{}, ownerGrant(t), 1, now); !errors.Is(err, domain.ErrLastOwner) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("an owner is demoted when another remains", func(t *testing.T) {
		m := owner()
		if err := m.ChangeAccess(mustRoles(t, "admin"), domain.LocaleScope{}, ownerGrant(t), 2, now); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("locales need a locale role", func(t *testing.T) {
		m := newTranslator()
		if err := m.ChangeAccess(mustRoles(t, "developer"), mustLocales(t, "de"), adminGrant(t), 1, now); !errors.Is(err, domain.ErrLocalesNeedLocaleRole) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCheckRemoval(t *testing.T) {
	o := domain.NewOwner(tenancy.NewID(), domain.NewPersonID(), mustEmail(t, "o@example.com"), now)
	if err := o.CheckRemoval(ownerGrant(t), 1); !errors.Is(err, domain.ErrLastOwner) {
		t.Errorf("removing the last owner err = %v", err)
	}
	if err := o.CheckRemoval(adminGrant(t), 2); !errors.Is(err, domain.ErrOwnerChangeForbidden) {
		t.Errorf("admin removing an owner err = %v", err)
	}
	if err := o.CheckRemoval(ownerGrant(t), 2); err != nil {
		t.Errorf("owner removing one of two owners: %v", err)
	}
}

func TestMemberGrantIgnoresInvitations(t *testing.T) {
	m, err := domain.Invite(tenancy.NewID(), tenancy.KindOrganization, mustEmail(t, "tr@example.com"), mustRoles(t, "admin"), domain.LocaleScope{}, adminGrant(t), now)
	if err != nil {
		t.Fatal(err)
	}
	if m.Grant().Allows(domain.PermTenantRead) {
		t.Error("an unaccepted invitation must grant nothing")
	}
	_ = m.Activate(domain.NewPersonID(), now)
	if !m.Grant().Allows(domain.PermMembersManage) {
		t.Error("an active admin must manage members")
	}
}
