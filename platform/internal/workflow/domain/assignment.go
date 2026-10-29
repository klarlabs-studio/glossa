package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Assignments (RFC 0006 §3.1): a piece of work given to someone — a
// batch of translation units, the job a translator actually works
// through — not a decision about the text. Completing one is a claim
// that raises assignment.completed; what follows is the definition's to
// decide. An assignment never changes a review state by itself.

// AssignmentState is where an assignment is in its life.
type AssignmentState string

// The assignment states. Open and accepted are live; the other three
// are final.
const (
	AssignmentOpen     AssignmentState = "open"
	AssignmentAccepted AssignmentState = "accepted"
	AssignmentDone     AssignmentState = "done"
	AssignmentDeclined AssignmentState = "declined"
	AssignmentExpired  AssignmentState = "expired"
)

// Live reports whether the assignment is still being worked on.
func (s AssignmentState) Live() bool { return s == AssignmentOpen || s == AssignmentAccepted }

// CoverageWindow is how long a completed assignment still lets its
// assignee see its units (§3.3): long enough to answer a reviewer's
// question about the text they delivered.
const CoverageWindow = 30 * 24 * time.Hour

// Bounds on an assignment.
const (
	// MaxAssignmentUnits bounds one assignment's batch (RFC 0006 §9.6).
	MaxAssignmentUnits = 10000
	// MaxAssignmentBodyBytes is createAssignment's request-body limit:
	// MaxAssignmentUnits units at the longest key and locale fit with
	// room to spare, above the server's 1 MiB default.
	MaxAssignmentBodyBytes = 4 << 20
	// MaxOpenAssignments bounds the live (open or accepted) assignments
	// one assignee — a member, a role, a group or a vendor — holds at
	// once in a tenant (RFC 0006 §9.6).
	MaxOpenAssignments = 1000
	// MaxReason bounds a decline's or a decision's reason.
	MaxReason = 2000
)

// Assignment and approval errors.
var (
	ErrInvalidAssignment = errors.New("invalid assignment")
	// ErrAssignmentState is a transition the assignment's state does not
	// allow: completing a declined one, accepting a done one.
	ErrAssignmentState = errors.New("the assignment does not allow that in its state")
	// ErrNotDue is expiring an assignment whose due date has not passed.
	ErrNotDue = errors.New("the assignment is not due yet")
)

// AssigneeKind is who an assignment or an approval names.
type AssigneeKind string

// The assignee kinds, as domain.Party spells them in a definition.
const (
	AssigneeMember AssigneeKind = "member"
	AssigneeRole   AssigneeKind = "role"
	AssigneeGroup  AssigneeKind = "group"
	AssigneeVendor AssigneeKind = "vendor"
)

// Assignee is exactly one member, role, group or vendor, resolved to
// Identity's ids. A group and a vendor are data the organisation
// chooses (§4.3): nothing here knows what one means.
type Assignee struct {
	Kind AssigneeKind
	// ID is the member, group or vendor; zero for a role.
	ID uuid.UUID
	// Role is set for a role only.
	Role string
}

// MemberAssignee names one member.
func MemberAssignee(id uuid.UUID) Assignee { return Assignee{Kind: AssigneeMember, ID: id} }

// RoleAssignee names everyone holding a role.
func RoleAssignee(role string) Assignee { return Assignee{Kind: AssigneeRole, Role: role} }

// GroupAssignee names a group's members.
func GroupAssignee(id uuid.UUID) Assignee { return Assignee{Kind: AssigneeGroup, ID: id} }

// VendorAssignee names a vendor's members.
func VendorAssignee(id uuid.UUID) Assignee { return Assignee{Kind: AssigneeVendor, ID: id} }

// Validate checks that a names exactly one party.
func (a Assignee) Validate() error {
	switch a.Kind {
	case AssigneeRole:
		if !slices.Contains(Roles, a.Role) || a.ID != uuid.Nil {
			return fmt.Errorf("%w: role %q is not one of %s", ErrInvalidAssignment, a.Role, strings.Join(Roles, ", "))
		}
	case AssigneeMember, AssigneeGroup, AssigneeVendor:
		if a.ID == uuid.Nil || a.Role != "" {
			return fmt.Errorf("%w: a %s assignee needs an id", ErrInvalidAssignment, a.Kind)
		}
	default:
		return fmt.Errorf("%w: assignee kind %q", ErrInvalidAssignment, a.Kind)
	}
	return nil
}

// String is the stored spelling: "member:<uuid>", "group:<uuid>",
// "vendor:<uuid>" or "role:<name>".
func (a Assignee) String() string {
	if a.Kind == AssigneeRole {
		return "role:" + a.Role
	}
	return string(a.Kind) + ":" + a.ID.String()
}

// ParseAssignee reads the stored spelling.
func ParseAssignee(s string) (Assignee, error) {
	kind, ref, ok := strings.Cut(s, ":")
	if !ok {
		return Assignee{}, fmt.Errorf("%w: assignee %q", ErrInvalidAssignment, s)
	}
	a := Assignee{Kind: AssigneeKind(kind)}
	if a.Kind == AssigneeRole {
		a.Role = ref
	} else {
		id, err := uuid.Parse(ref)
		if err != nil {
			return Assignee{}, fmt.Errorf("%w: assignee %q", ErrInvalidAssignment, s)
		}
		a.ID = id
	}
	return a, a.Validate()
}

// Unit is one translation unit: a message in a locale.
type Unit struct {
	Message uuid.UUID
	// Locale is a canonical BCP 47 tag.
	Locale string
}

// CanonicalUnits canonicalizes the locales, drops duplicates and sorts
// units, refusing an empty or oversized batch.
func CanonicalUnits(units []Unit) ([]Unit, error) {
	if len(units) == 0 {
		return nil, fmt.Errorf("%w: an assignment covers at least one translation unit", ErrInvalidAssignment)
	}
	out := make([]Unit, 0, len(units))
	for _, u := range units {
		if u.Message == uuid.Nil {
			return nil, fmt.Errorf("%w: a unit needs a message", ErrInvalidAssignment)
		}
		tag, err := bcp47.Parse(u.Locale)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not a BCP 47 locale", ErrInvalidAssignment, u.Locale)
		}
		out = append(out, Unit{Message: u.Message, Locale: tag.String()})
	}
	slices.SortFunc(out, compareUnits)
	out = slices.Compact(out)
	if len(out) > MaxAssignmentUnits {
		return nil, fmt.Errorf("%w: at most %d units per assignment", ErrInvalidAssignment, MaxAssignmentUnits)
	}
	return out, nil
}

func compareUnits(a, b Unit) int {
	if c := strings.Compare(a.Message.String(), b.Message.String()); c != 0 {
		return c
	}
	return strings.Compare(a.Locale, b.Locale)
}

// Locales lists the distinct locales of units, sorted.
func Locales(units []Unit) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		out = append(out, u.Locale)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Assignment is a batch of translation units given to one assignee.
type Assignment struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// InstanceID is the workflow instance whose assign action created
	// it; zero for one made by hand.
	InstanceID uuid.UUID
	Units      []Unit
	Assignee   Assignee
	// Permission is what doing the work takes (translations.write for
	// translating); whoever accepts or completes it must hold it for
	// every unit's locale.
	Permission string
	DueAt      *time.Time
	State      AssignmentState
	// Version increments with every change.
	Version   int
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	// ClosedBy, ClosedAt and Reason record how it ended: completed,
	// declined (with a reason) or expired.
	ClosedBy string
	ClosedAt *time.Time
	Reason   string
}

// NewAssignment makes an open assignment.
func NewAssignment(project uuid.UUID, units []Unit, to Assignee, permission string, due *time.Time, by string, now time.Time) (Assignment, error) {
	if project == uuid.Nil {
		return Assignment{}, fmt.Errorf("%w: no project", ErrInvalidAssignment)
	}
	if err := to.Validate(); err != nil {
		return Assignment{}, err
	}
	if !permissionPattern.MatchString(permission) {
		return Assignment{}, fmt.Errorf("%w: permission %q", ErrInvalidAssignment, permission)
	}
	if due != nil && !due.After(now) {
		return Assignment{}, fmt.Errorf("%w: due before it was made", ErrInvalidAssignment)
	}
	us, err := CanonicalUnits(units)
	if err != nil {
		return Assignment{}, err
	}
	if by == "" {
		return Assignment{}, fmt.Errorf("%w: no actor", ErrInvalidAssignment)
	}
	return Assignment{
		ID: newID(), ProjectID: project, Units: us, Assignee: to, Permission: permission, DueAt: due,
		State: AssignmentOpen, Version: 1, CreatedBy: by, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// Accept marks an open assignment as taken on.
func (a *Assignment) Accept(now time.Time) error {
	if a.State != AssignmentOpen {
		return fmt.Errorf("%w: %s", ErrAssignmentState, a.State)
	}
	a.State = AssignmentAccepted
	a.touch(now)
	return nil
}

// Complete claims the work done. It is a claim, not a decision: it
// changes no review state, and the definition decides what follows.
func (a *Assignment) Complete(by string, now time.Time) error {
	return a.close(AssignmentDone, by, "", now)
}

// Decline hands the work back, with an optional reason.
func (a *Assignment) Decline(by, reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > MaxReason {
		return fmt.Errorf("%w: reason longer than %d characters", ErrInvalidAssignment, MaxReason)
	}
	return a.close(AssignmentDeclined, by, reason, now)
}

// Expire ends a live assignment whose due date has passed.
func (a *Assignment) Expire(by string, now time.Time) error {
	if a.DueAt == nil || now.Before(*a.DueAt) {
		return ErrNotDue
	}
	return a.close(AssignmentExpired, by, "", now)
}

func (a *Assignment) close(to AssignmentState, by, reason string, now time.Time) error {
	if !a.State.Live() {
		return fmt.Errorf("%w: %s", ErrAssignmentState, a.State)
	}
	a.State, a.ClosedBy, a.Reason = to, by, reason
	a.ClosedAt = &now
	a.touch(now)
	return nil
}

func (a *Assignment) touch(now time.Time) {
	a.Version++
	a.UpdatedAt = now
}

// CoversAt reports whether the assignment still lets its assignee see
// its units at now: while it is live, or for CoverageWindow after it
// was completed. A declined or expired assignment covers nothing.
func (a Assignment) CoversAt(now time.Time) bool {
	if a.State.Live() {
		return true
	}
	return a.State == AssignmentDone && a.ClosedAt != nil && now.Sub(*a.ClosedAt) <= CoverageWindow
}

// newID is a time-ordered id, so the newest of a subject's approvals,
// and a page of assignments, read in the order they were made.
func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
