package domain_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestNewPerson(t *testing.T) {
	p, err := domain.NewPerson(mustEmail(t, "Ada.Lovelace+glossa@Example.com"), "  Ada Lovelace ", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID.IsZero() || p.IndividualTenantID.IsZero() {
		t.Error("a person needs an id and a reserved individual tenant id")
	}
	if p.DisplayName != "Ada Lovelace" || p.EmailVerified() {
		t.Errorf("person = %+v", p)
	}
	if _, err := domain.NewPerson(mustEmail(t, "a@example.com"), strings.Repeat("n", 201), now); !errors.Is(err, domain.ErrInvalidDisplayName) {
		t.Errorf("overlong display name err = %v", err)
	}
}

func TestIndividualTenant(t *testing.T) {
	slug := regexp.MustCompile(`^ada-lovelace-glossa-[a-z0-9]{6}$`)
	p, err := domain.NewPerson(mustEmail(t, "Ada.Lovelace+glossa@example.com"), "", now)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := p.IndividualTenant()
	if err != nil {
		t.Fatal(err)
	}
	if tn.ID != p.IndividualTenantID || tn.Kind != tenancy.KindIndividual {
		t.Errorf("tenant = %+v", tn)
	}
	if !slug.MatchString(string(tn.Slug)) {
		t.Errorf("slug = %q", tn.Slug)
	}
	if tn.Name != "ada.lovelace+glossa" {
		t.Errorf("name = %q, want the email's local part when there is no display name", tn.Name)
	}

	named, _ := domain.NewPerson(mustEmail(t, "x@example.com"), "Grace", now)
	if tn, _ := named.IndividualTenant(); tn.Name != "Grace" {
		t.Errorf("name = %q, want the display name", tn.Name)
	}
	weird, _ := domain.NewPerson(mustEmail(t, "___@example.com"), "", now)
	if tn, err := weird.IndividualTenant(); err != nil || !strings.HasPrefix(string(tn.Slug), "user-") {
		t.Errorf("slug for an unusable local part = %q, %v", tn.Slug, err)
	}
}
