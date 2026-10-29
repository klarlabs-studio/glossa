// Package outbox is glossa-server's transactional outbox (RFC 0002 §4).
//
// A bounded context records a domain event with [Publish] inside the
// same tenant-scoped transaction as the state change that raised it, so
// the event exists if and only if the change committed. The
// [Dispatcher] then claims due events in batches (FOR UPDATE SKIP
// LOCKED, so replicas share the work) and delivers each one to the
// in-process handlers subscribed to its type.
//
// # Delivery contract
//
//   - At least once. A handler can see the same event more than once:
//     after a crash, an expired lease, or a failed sibling handler's
//     retry racing a lease. Handlers MUST be idempotent, keyed on
//     [Delivery.EventID] (e.g. a processed-events table or an
//     idempotent upsert in the handler's own transaction).
//   - Per subscriber. A retry re-runs only the subscribers that have
//     not succeeded yet.
//   - Tenant-scoped. The handler's context carries the event's tenant,
//     so db.UnitOfWork.InTenantTx works as it does in a request.
//   - Traced. The publisher's trace context travels with the event, so
//     one trace spans API write → outbox → handler.
//   - No ordering guarantee, not even per aggregate. Replicas claim
//     disjoint batches concurrently, a failed delivery is retried after
//     later events, and a claim's rows come back in no defined order.
//     Within one claimed batch a dispatcher does keep claim order: a
//     per-event subscriber sees the claims one by one in that order,
//     and a batch subscriber ([BatchHandler]) gets each tenant's
//     deliveries in that order, handled before the per-event
//     subscribers. Handlers that need an order carry a version (the
//     message projection keeps the highest) rather than rely on it.
//   - Batched or not. A subscriber registered with
//     [Registry.SubscribeBatch] gets a batch's deliveries together, in
//     as few transactions as it likes; whatever it reports unhandled is
//     delivered again one at a time, so a batch adds no failure mode.
//
// # Failure handling
//
// A failing handler is retried in-process a few times (fortify retry
// with jittered exponential backoff). If it still fails, the event is
// rescheduled with a persistent exponential backoff; after MaxAttempts
// deliveries it moves to the dead-letter state (status 'dead') for an
// operator to inspect. Return [Permanent] for errors a retry cannot fix
// (an undecodable payload) to dead-letter immediately.
package outbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// ErrInvalidEvent is returned by Publish for an incomplete event.
var ErrInvalidEvent = errors.New("outbox: invalid event")

// Event is a domain event to publish.
type Event struct {
	// ID identifies the event; a UUIDv7 is generated when zero.
	ID uuid.UUID
	// Type names the event "<context>.<aggregate>.<past-tense verb>" in
	// snake_case, e.g. "identity.member.added" (platform/README.md,
	// "Domain events"). Handlers subscribe by type.
	Type string
	// AggregateType and AggregateID identify the aggregate that raised it.
	AggregateType string
	AggregateID   string
	// Actor is who caused the event: the person, the token or the
	// background process whose act it records (RFC 0006 §6.1). It is
	// required — Publish refuses an event without one — and is never
	// defaulted: background work says so with its own system actor.
	Actor Actor
	// Payload is marshalled to JSON (json.RawMessage passes through).
	Payload any
	// OccurredAt defaults to the publish time.
	OccurredAt time.Time
}

const maxNameLen = 200

// Validate reports whether Publish would accept e: a type, an
// aggregate and an actor. Test doubles of a context's store call it so
// a unit test refuses what the database would.
func (e Event) Validate() error {
	for field, v := range map[string]string{
		"Type": e.Type, "AggregateType": e.AggregateType, "AggregateID": e.AggregateID,
	} {
		if v == "" || len(v) > maxNameLen {
			return fmt.Errorf("%w: %s must be 1–%d bytes", ErrInvalidEvent, field, maxNameLen)
		}
	}
	if err := e.Actor.Validate(); err != nil {
		return fmt.Errorf("%s: %w", e.Type, err)
	}
	return nil
}

// Delivery is an event as a handler receives it.
type Delivery struct {
	EventID       uuid.UUID
	TenantID      tenancy.ID
	Type          string
	AggregateType string
	AggregateID   string
	// Actor is who caused the event; ActorUnknown for an event recorded
	// before events named their actors (migration 0042).
	Actor      Actor
	Payload    json.RawMessage
	OccurredAt time.Time
	// TraceID is the W3C trace id of the publisher's trace — the request
	// that caused the event — or "" when it published untraced. It is
	// the event's correlation id (the audit trail records it).
	TraceID string
	// Attempt counts deliveries of this event, starting at 1.
	Attempt int
}

// traceIDOf reads the trace id out of a stored W3C trace context
// carrier ("traceparent": "00-<trace id>-<span id>-<flags>"), or "".
func traceIDOf(carrier map[string]string) string {
	parts := strings.Split(carrier["traceparent"], "-")
	if len(parts) != 4 || len(parts[1]) != 32 || parts[1] == strings.Repeat("0", 32) {
		return ""
	}
	for _, r := range parts[1] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return ""
		}
	}
	return parts[1]
}

// Decode unmarshals the payload into v.
func (d Delivery) Decode(v any) error {
	if err := json.Unmarshal(d.Payload, v); err != nil {
		return Permanent(fmt.Errorf("outbox: decode %s payload: %w", d.Type, err))
	}
	return nil
}

type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent marks err as not worth retrying: the event is dead-lettered
// right away instead of backing off.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// batchFailure is a whole batch's failure, reported for each delivery
// in it. It deliberately does not unwrap: a permanent error one
// delivery caused must not dead-letter the rest.
type batchFailure struct{ msg string }

func (b batchFailure) Error() string { return b.msg }

// BatchFailure is what a [BatchHandler] reports for each delivery when
// the batch failed as a whole (its transaction rolled back): never
// permanent, so the dispatcher delivers each one again on its own, and
// the delivery at fault then fails alone.
func BatchFailure(err error) error {
	if err == nil {
		return nil
	}
	return batchFailure{"batch failed: " + err.Error()}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}
