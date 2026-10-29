package authz_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestEventActorIsThePrincipal(t *testing.T) {
	person := domain.PersonActor(domain.NewPersonID())
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{Actor: person})
	a, err := authz.EventActor(ctx)
	if err != nil || a.String() != person.String() || a.Kind() != "person" {
		t.Errorf("EventActor = %q, %v; want %s", a, err, person)
	}

	bg, err := authz.Background(tenancy.ContextWithTenant(context.Background(), tenancy.NewID()), "quality.sweep")
	if err != nil {
		t.Fatal(err)
	}
	a, err = authz.EventActor(bg)
	if err != nil || a != authz.SystemEventActor("quality.sweep") || a.Kind() != "system" {
		t.Errorf("background EventActor = %q, %v; want the sweep's system actor", a, err)
	}
}

func TestEventActorWithoutAPrincipalFails(t *testing.T) {
	if a, err := authz.EventActor(context.Background()); !errors.Is(err, authz.ErrUnauthenticated) || a != "" {
		t.Errorf("EventActor = %q, %v; want ErrUnauthenticated and no actor", a, err)
	}
}

func TestSystemEventActorIsStableAndValid(t *testing.T) {
	a := authz.SystemEventActor("localization.track_message")
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a != authz.SystemEventActor("localization.track_message") || a == authz.SystemEventActor("knowledge.derive_tm") {
		t.Error("a system actor is not one stable id per name")
	}
}
