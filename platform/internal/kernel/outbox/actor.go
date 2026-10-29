package outbox

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Actor names who caused an event (RFC 0006 §6.1), spelled the way
// identity's domain.Actor renders itself, so the envelope and the
// `by` fields already in payloads and revision logs agree:
//
//	person:<uuid>   a person, signed in
//	token:<uuid>    an API token — CI, the CLI, an MCP agent
//	system:<uuid>   a bounded context's background work, named stably
//	                (identity's SystemActor derives the uuid from the name)
//
// Every published event carries one; [Publish] refuses an event
// without it rather than defaulting it, because a defaulted actor would
// make the audit trail claim the system did what a person did.
//
// [ActorUnknown] is only ever read, never published: it is what an
// event written before the envelope had an actor (migration 0042)
// reports. It says "not recorded", and attributes the event to no one.
type Actor string

// ActorUnknown is the actor of an event recorded before events named
// their actors. Publish refuses it.
const ActorUnknown Actor = "unknown"

// The kinds of actor an event can name.
const (
	ActorKindPerson = "person"
	ActorKindToken  = "token"
	ActorKindSystem = "system"
)

// Validate reports whether a can be published: one of the three kinds,
// followed by a non-nil UUID.
func (a Actor) Validate() error {
	kind, id, ok := strings.Cut(string(a), ":")
	if !ok {
		return fmt.Errorf("%w: actor %q is not kind:id", ErrInvalidEvent, string(a))
	}
	switch kind {
	case ActorKindPerson, ActorKindToken, ActorKindSystem:
	default:
		return fmt.Errorf("%w: actor %q has no known kind", ErrInvalidEvent, string(a))
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil || len(id) != 36 {
		return fmt.Errorf("%w: actor %q has no id", ErrInvalidEvent, string(a))
	}
	return nil
}

// Kind returns the actor's kind ("person", "token", "system"), or ""
// for ActorUnknown and anything else that does not validate.
func (a Actor) Kind() string {
	if a.Validate() != nil {
		return ""
	}
	kind, _, _ := strings.Cut(string(a), ":")
	return kind
}

// String returns the actor as stored.
func (a Actor) String() string { return string(a) }

// actorRead maps a stored actor to the one a handler sees. Rows written
// before migration 0042 hold "unknown"; anything that no longer
// validates is reported the same way rather than passed on as if it
// named someone.
func actorRead(stored string) Actor {
	a := Actor(stored)
	if a.Validate() != nil {
		return ActorUnknown
	}
	return a
}
