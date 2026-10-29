package domain

import (
	"time"

	"github.com/google/uuid"
)

// DeliveryReplayWindow is how long a delivery ID is kept after it was
// processed, so GitHub replaying one is a no-op rather than a repeat
// (RFC 0004 §6.2). The sweep removes older rows.
const DeliveryReplayWindow = 7 * 24 * time.Hour

// InstallIntentTTL is how long an install intent's state is good for:
// long enough to pick an account and a set of repositories on GitHub,
// short enough that a link left in a browser tab is useless.
const InstallIntentTTL = 15 * time.Minute

// MaxDeliveryAttempts is how often a delivery is retried before it is
// left failed for an operator. Correctness never depends on a webhook
// (RFC 0004 §4.1: merge is the default branch's push), so a delivery
// that keeps failing is a diagnostic, not an outage.
const MaxDeliveryAttempts = 8

// DeliveryState is where an inbox row stands.
type DeliveryState string

// Delivery states.
const (
	// DeliveryPending: stored, not processed yet.
	DeliveryPending DeliveryState = "pending"
	// DeliveryDone: processed; the payload is gone and the ID remains
	// for the replay window.
	DeliveryDone DeliveryState = "done"
	// DeliveryIgnored: a verified event or action Glossa does not act
	// on, or one whose installation no tenant has claimed.
	DeliveryIgnored DeliveryState = "ignored"
	// DeliveryFailed: it kept failing and was given up on.
	DeliveryFailed DeliveryState = "failed"
)

// Delivery is one webhook delivery in the inbox, keyed by GitHub's
// X-GitHub-Delivery. It is tenantless until the worker resolves its
// installation, and its payload is dropped once it is processed.
type Delivery struct {
	// ID is X-GitHub-Delivery.
	ID     string
	Event  string
	Action string
	// GitHubInstallationID is the installation named in the payload
	// (0 when it named none).
	GitHubInstallationID int64
	// TenantID is set once the installation resolves.
	TenantID *uuid.UUID
	State    DeliveryState
	Attempts int
	// Payload is the raw signed body; nil after processing.
	Payload []byte
	Failure string
	// ClaimToken fences a settlement to the worker that claimed it.
	ClaimToken  uuid.UUID
	ReceivedAt  time.Time
	ProcessedAt *time.Time
}

// RetryDelay is how long a failed attempt waits before the next one:
// exponential from a second, capped at five minutes.
func RetryDelay(attempts int) time.Duration {
	d := time.Second
	for range min(max(attempts-1, 0), 9) {
		d *= 2
	}
	return min(d, 5*time.Minute)
}

// GaveUp reports whether a delivery has used up its attempts.
func GaveUp(attempts int) bool { return attempts >= MaxDeliveryAttempts }
