package authz

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// EventActor returns the principal on ctx as the actor of the events
// its act publishes (RFC 0006 §6.1): the person, the API token (the
// CLI, CI, an MCP agent) or the background process Background named.
// Without a principal it fails rather than inventing one.
func EventActor(ctx context.Context) (outbox.Actor, error) {
	p, ok := From(ctx)
	if !ok {
		return "", ErrUnauthenticated
	}
	return outbox.Actor(p.Actor.String()), nil
}

// SystemEventActor is the actor of background work that publishes with
// no principal on its context — an outbox subscriber keeping its own
// projection current — named as Background names it, so the same name
// is the same actor wherever it appears.
func SystemEventActor(name string) outbox.Actor {
	return outbox.Actor(domain.SystemActor(name).String())
}
