package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrLeaseLost means a settlement's claim token no longer matches: the
// lease expired and another dispatcher re-claimed the event. The other
// holder now owns the outcome, so the settlement is dropped.
var ErrLeaseLost = errors.New("outbox: claim lease lost")

// Claim is an event leased to this dispatcher for delivery.
type Claim struct {
	Delivery
	// ClaimToken fences the lease; settlements must present it.
	ClaimToken uuid.UUID
	// DeliveredTo lists subscribers that already succeeded.
	DeliveredTo []string
	// TraceContext is the publisher's W3C trace context.
	TraceContext map[string]string
}

// Outcome is how a claim is settled.
type Outcome int

const (
	// OutcomeDelivered: every subscriber succeeded.
	OutcomeDelivered Outcome = iota + 1
	// OutcomeRetry: some subscriber failed; deliver again after RetryAfter.
	OutcomeRetry
	// OutcomeDead: give up; the event is kept for inspection.
	OutcomeDead
	// OutcomeRelease: never attempted (shutdown, lease exhausted); make
	// it due again without charging an attempt.
	OutcomeRelease
)

// String names the outcome for logs and metrics.
func (o Outcome) String() string {
	switch o {
	case OutcomeDelivered:
		return "delivered"
	case OutcomeRetry:
		return "retry"
	case OutcomeDead:
		return "dead"
	case OutcomeRelease:
		return "release"
	}
	return "unknown"
}

// Settlement records the result of delivering one claim.
type Settlement struct {
	EventID     uuid.UUID
	ClaimToken  uuid.UUID
	Outcome     Outcome
	DeliveredTo []string
	RetryAfter  time.Duration
	LastError   string
}

// Store is the dispatcher's persistence port.
type Store interface {
	// Claim leases up to limit due events for lease, counting an attempt
	// on each.
	Claim(ctx context.Context, limit int, lease time.Duration) ([]Claim, error)
	// Settle records a claim's outcome, or returns ErrLeaseLost.
	Settle(ctx context.Context, s Settlement) error
}

// BatchSettler is a Store that can record a batch's settlements in one
// round trip (#89). SettleAll returns one result per settlement, as
// Settle would have: nil, ErrLeaseLost, or an error. A store that fails
// as a whole returns nil, and the dispatcher settles one at a time.
type BatchSettler interface {
	SettleAll(ctx context.Context, ss []Settlement) []error
}
