package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// PersonID identifies a person across the deployment.
type PersonID uuid.UUID

// MemberID identifies a membership (a person's place in one tenant).
type MemberID uuid.UUID

// TokenID identifies an API token.
type TokenID uuid.UUID

// NewPersonID returns a fresh, time-ordered ID.
func NewPersonID() PersonID { return PersonID(newV7()) }

// NewMemberID returns a fresh, time-ordered ID.
func NewMemberID() MemberID { return MemberID(newV7()) }

// NewTokenID returns a fresh, time-ordered ID.
func NewTokenID() TokenID { return TokenID(newV7()) }

// ParsePersonID parses the canonical string form.
func ParsePersonID(s string) (PersonID, error) {
	u, err := parseID(s)
	return PersonID(u), err
}

// ParseMemberID parses the canonical string form.
func ParseMemberID(s string) (MemberID, error) {
	u, err := parseID(s)
	return MemberID(u), err
}

// ParseTokenID parses the canonical string form.
func ParseTokenID(s string) (TokenID, error) {
	u, err := parseID(s)
	return TokenID(u), err
}

func (id PersonID) String() string  { return uuid.UUID(id).String() }
func (id PersonID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id PersonID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }
func (id MemberID) String() string  { return uuid.UUID(id).String() }
func (id MemberID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id MemberID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }
func (id TokenID) String() string   { return uuid.UUID(id).String() }
func (id TokenID) UUID() uuid.UUID  { return uuid.UUID(id) }
func (id TokenID) IsZero() bool     { return uuid.UUID(id) == uuid.Nil }

func newV7() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func parseID(s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return u, nil
}
