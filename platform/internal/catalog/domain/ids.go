package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// ProjectID identifies a project.
type ProjectID uuid.UUID

// ApplicationID identifies an application of a project.
type ApplicationID uuid.UUID

// MessageID is a message's internal, immutable identity. It never
// changes, not even when the message's key is renamed, so source
// revisions, translations and everything else that accumulates on a
// message stay attached to it (intent §8).
type MessageID uuid.UUID

// BranchID identifies a branch of a project.
type BranchID uuid.UUID

// NewProjectID returns a fresh, time-ordered ID.
func NewProjectID() ProjectID { return ProjectID(newV7()) }

// NewApplicationID returns a fresh, time-ordered ID.
func NewApplicationID() ApplicationID { return ApplicationID(newV7()) }

// NewMessageID returns a fresh, time-ordered ID.
func NewMessageID() MessageID { return MessageID(newV7()) }

// NewBranchID returns a fresh, time-ordered ID.
func NewBranchID() BranchID { return BranchID(newV7()) }

// ParseProjectID parses the canonical string form.
func ParseProjectID(s string) (ProjectID, error) {
	u, err := parseID(s)
	return ProjectID(u), err
}

// ParseApplicationID parses the canonical string form.
func ParseApplicationID(s string) (ApplicationID, error) {
	u, err := parseID(s)
	return ApplicationID(u), err
}

// ParseMessageID parses the canonical string form.
func ParseMessageID(s string) (MessageID, error) {
	u, err := parseID(s)
	return MessageID(u), err
}

func (id ProjectID) String() string      { return uuid.UUID(id).String() }
func (id ProjectID) UUID() uuid.UUID     { return uuid.UUID(id) }
func (id ProjectID) IsZero() bool        { return uuid.UUID(id) == uuid.Nil }
func (id ApplicationID) String() string  { return uuid.UUID(id).String() }
func (id ApplicationID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id ApplicationID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }
func (id MessageID) String() string      { return uuid.UUID(id).String() }
func (id MessageID) UUID() uuid.UUID     { return uuid.UUID(id) }
func (id MessageID) IsZero() bool        { return uuid.UUID(id) == uuid.Nil }
func (id BranchID) String() string       { return uuid.UUID(id).String() }
func (id BranchID) UUID() uuid.UUID      { return uuid.UUID(id) }
func (id BranchID) IsZero() bool         { return uuid.UUID(id) == uuid.Nil }

func newV7() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func parseID(s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return u, nil
}
