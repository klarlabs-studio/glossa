package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The read side of workflow instances (RFC 0006 §2.5), declared ahead of
// both of its ends so the instance runner (which writes instances and
// transitions) and the Workflow API (which reads them) were built in
// parallel against one shape.

// Instance statuses.
const (
	InstanceActive   = "active"
	InstanceFinished = "finished"
)

// Transition outcomes. "ignored" is an event the instance's state no
// longer accepts (statekit's SendResult false): the world moved on, which
// is normal, not an error. "refused" is a transition whose action was
// refused for permission; the instance stays where it was.
const (
	TransitionApplied = "applied"
	TransitionIgnored = "ignored"
	TransitionRefused = "refused"
)

// InstanceView is one workflow instance.
type InstanceView struct {
	ID         uuid.UUID
	Project    uuid.UUID
	Definition uuid.UUID
	Version    int
	Kind       domain.SubjectKind
	// SubjectID is the message for a translation unit, the release
	// request otherwise.
	SubjectID uuid.UUID
	// Locale is the translation unit's canonical BCP 47 tag; empty for a
	// release request.
	Locale    string
	State     string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GuardOutcome is one guard evaluated during a transition.
type GuardOutcome struct {
	Guard  string
	Passed bool
}

// ActionOutcome is one action a transition ran, and what became of it.
type ActionOutcome struct {
	Action string
	// Outcome is "done", "refused" or "failed".
	Outcome string
	Detail  string
}

// TransitionView is one row of an instance's transition log.
type TransitionView struct {
	Seq     int64
	From    string
	Event   string
	To      string
	Outcome string
	Guards  []GuardOutcome
	Actions []ActionOutcome
	// Actor is the outbox spelling of who caused the event.
	Actor         string
	OutboxEventID uuid.UUID
	At            time.Time
}

// InstanceFilter narrows a list of instances. Zero fields don't filter.
type InstanceFilter struct {
	Project    uuid.UUID
	Definition uuid.UUID
	Status     string
	Locale     string
	SubjectID  uuid.UUID
	// After is the opaque cursor a previous page returned.
	After string
	Limit int
}

// InstanceQueries reads instances and their transition logs. The
// runner's store implements it; the API checks `workflows.read` before
// calling it.
type InstanceQueries interface {
	ListInstances(ctx context.Context, f InstanceFilter) (page []InstanceView, next string, err error)
	GetInstance(ctx context.Context, id uuid.UUID) (InstanceView, error)
	ListTransitions(ctx context.Context, instance uuid.UUID) ([]TransitionView, error)
}
