package outbox_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

const (
	personActor outbox.Actor = "person:0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e"
	tokenActor  outbox.Actor = "token:0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e"
	systemActor outbox.Actor = "system:5b8f1c2e-4d3a-5e6f-8a9b-0c1d2e3f4a5b"
)

func TestActorValidate(t *testing.T) {
	for _, a := range []outbox.Actor{personActor, tokenActor, systemActor} {
		if err := a.Validate(); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
	for _, a := range []outbox.Actor{
		"",
		outbox.ActorUnknown,
		"system",
		"system:",
		"system:knowledge.derive_tm", // a name, not the id identity derives from it
		"person:00000000-0000-0000-0000-000000000000",
		":0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e", // a zero identity.Actor rendered
		"agent:0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e",
		"person:{0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e}",
	} {
		if err := a.Validate(); !errors.Is(err, outbox.ErrInvalidEvent) {
			t.Errorf("Validate(%q) = %v, want ErrInvalidEvent", a, err)
		}
	}
}

func TestActorKind(t *testing.T) {
	for a, want := range map[outbox.Actor]string{
		personActor: "person", tokenActor: "token", systemActor: "system", outbox.ActorUnknown: "", "": "",
	} {
		if got := a.Kind(); got != want {
			t.Errorf("Kind(%q) = %q, want %q", a, got, want)
		}
	}
}

// Publish refuses an event that names no actor before it touches the
// transaction: there is no default, so the audit trail can never say
// "system" for something a person did.
func TestPublishRefusesAnEventWithoutAnActor(t *testing.T) {
	base := outbox.Event{Type: "a.b.created", AggregateType: "b", AggregateID: "1"}
	for _, a := range []outbox.Actor{"", outbox.ActorUnknown, "person:not-a-uuid"} {
		e := base
		e.Actor = a
		if _, err := outbox.Publish(context.Background(), nil, e); !errors.Is(err, outbox.ErrInvalidEvent) {
			t.Errorf("Publish with actor %q: err = %v, want ErrInvalidEvent", a, err)
		}
		if err := e.Validate(); !errors.Is(err, outbox.ErrInvalidEvent) {
			t.Errorf("Validate with actor %q: err = %v, want ErrInvalidEvent", a, err)
		}
	}
	base.Actor = personActor
	if err := base.Validate(); err != nil {
		t.Errorf("complete event: %v", err)
	}
}
