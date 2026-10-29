package domain

import (
	"time"

	"github.com/google/uuid"
)

// Branch environments publish again on their own (RFC 0004 §4.2): a
// branch push, or a translation change on the branch's messages, asks
// for a publish, and a burst of them becomes one publish PublishDebounce
// after the last — but never later than MaxPublishDelay after the first,
// so a branch that keeps changing still gets previews.
const (
	PublishDebounce = 30 * time.Second
	MaxPublishDelay = 5 * time.Minute
)

// PublishRequest is a pending, debounced publish of an environment.
type PublishRequest struct {
	ProjectID   uuid.UUID
	Environment string
	// ID changes with every request; the publish uses it as its
	// idempotency key, so a retried publish replays instead of
	// publishing twice, and a request made during a publish survives it.
	ID               uuid.UUID
	FirstRequestedAt time.Time
	NotBefore        time.Time
	By               string
}

// NewPublishRequest is the first request of a burst.
func NewPublishRequest(project uuid.UUID, environment, by string, now time.Time) PublishRequest {
	now = now.UTC().Truncate(time.Microsecond)
	return PublishRequest{
		ProjectID: project, Environment: environment, ID: uuid.Must(uuid.NewV7()),
		FirstRequestedAt: now, NotBefore: now.Add(PublishDebounce), By: by,
	}
}

// Renew records another request of the same burst: the publish moves to
// PublishDebounce after now, capped at MaxPublishDelay after the first.
func (r *PublishRequest) Renew(by string, now time.Time) {
	now = now.UTC().Truncate(time.Microsecond)
	r.ID, r.By = uuid.Must(uuid.NewV7()), by
	r.NotBefore = now.Add(PublishDebounce)
	if limit := r.FirstRequestedAt.Add(MaxPublishDelay); r.NotBefore.After(limit) {
		r.NotBefore = limit
	}
	if r.NotBefore.Before(r.FirstRequestedAt) { // a clock that went back
		r.NotBefore = r.FirstRequestedAt
	}
}

// Due reports whether the request should be published at now.
func (r PublishRequest) Due(now time.Time) bool { return !now.Before(r.NotBefore) }
