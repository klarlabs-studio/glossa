package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The ports of assignments and approvals (RFC 0006 §3.1–3.2).

// The permissions RFC 0006 §4.2 adds for assignments and approvals, by
// Identity's names.
const (
	PermAssignmentsRead   = authz.AssignmentsRead
	PermAssignmentsManage = authz.AssignmentsManage
	PermApprovalsDecide   = authz.ApprovalsDecide
)

var (
	// ErrUnknownParty is an assignee or an eligible party that names a
	// member, group or vendor this tenant does not have.
	ErrUnknownParty = errors.New("workflow: no such member, group or vendor")
	// ErrNotAssignee is acting on an assignment that is not given to
	// the caller. It is not found, not forbidden: reading someone else's
	// assignment is not found (WorkService.Assignment), and answering
	// acting on it differently — 403 where reading says 404 — would tell
	// the caller the assignment exists after all.
	ErrNotAssignee = fmt.Errorf("%w: no such assignment among yours", ErrNotFound)
	// ErrSuperseded is a decision on an approval a newer request for the
	// same subject has replaced.
	ErrSuperseded = errors.New("workflow: a newer approval request replaced this one")
	// ErrApprovalNotRequested is a decision on a release request whose
	// workflow has not asked for its approval yet: the instance starts
	// from release_request.created, a moment after the publish answered.
	ErrApprovalNotRequested = errors.New("workflow: the release request's approval has not been asked for yet; retry shortly")
	// ErrReleaseRequestClosed is a decision on a release request that is
	// no longer pending: deployed, denied, withdrawn or refused.
	ErrReleaseRequestClosed = errors.New("workflow: the release request is no longer pending")
	// ErrUnsupportedSubject is an assign action on a subject that is
	// not a translation unit: assignments are batches of translation
	// units (§3.1).
	ErrUnsupportedSubject = errors.New("workflow: assignments cover translation units only")
	// ErrIdempotencyReuse is an Idempotency-Key used before for a
	// different request.
	ErrIdempotencyReuse = errors.New("workflow: this Idempotency-Key was used for a different request")
)

// WorkTransactor runs fn in the tenant transaction ctx is already in
// — the instance runner's step, so an action's assignment commits with
// the transition that asked for it — or in a new one.
type WorkTransactor interface {
	InTenant(ctx context.Context, fn func(context.Context, WorkStore) error) error
}

// AssignmentFilter narrows a list of assignments. Zero fields don't
// filter.
type AssignmentFilter struct {
	Project uuid.UUID
	States  []domain.AssignmentState
	// Assignees are stored spellings (domain.Assignee.String()); an
	// assignment matches any of them.
	Assignees []string
	// Message and Locale keep only assignments covering a unit of that
	// message, in that locale, or both.
	Message uuid.UUID
	Locale  string
	// Within, when set, keeps only assignments in these projects: the
	// caller's project scope, set by the service, never by a caller.
	Within *[]uuid.UUID
	// After is the last id of the previous page.
	After uuid.UUID
	Limit int
}

// ApprovalFilter narrows a list of approvals. Zero fields don't filter.
type ApprovalFilter struct {
	Project uuid.UUID
	Kind    domain.SubjectKind
	// SubjectID is a translation unit's message or a release request.
	SubjectID uuid.UUID
	// Locale is a canonical BCP 47 tag.
	Locale string
	States []domain.ApprovalState
	// Within is the caller's project scope, set by the service.
	Within *[]uuid.UUID
	After  uuid.UUID
	Limit  int
}

// CoverageQuery asks which units assignments to any of Assignees cover
// in Project: live ones, and those completed at or after DoneSince.
// Unit narrows it to one unit.
type CoverageQuery struct {
	Project   uuid.UUID
	Assignees []string
	DoneSince time.Time
	Unit      *domain.Unit
}

// WorkStore is the persistence of assignments and approvals inside a
// tenant transaction.
type WorkStore interface {
	InsertAssignment(ctx context.Context, a domain.Assignment) error
	// LiveAssignmentsOf counts the assignee's live (open or accepted)
	// assignments, holding a lock on that assignee until the
	// transaction ends, so two concurrent assignments cannot both take
	// the last place under the limit (RFC 0006 §9.6).
	LiveAssignmentsOf(ctx context.Context, assignee domain.Assignee) (int, error)
	GetAssignment(ctx context.Context, id uuid.UUID) (domain.Assignment, error)
	// LockAssignment reads an assignment and locks it until the
	// transaction ends.
	LockAssignment(ctx context.Context, id uuid.UUID) (domain.Assignment, error)
	// UpdateAssignment saves state, version and how it closed; it is
	// ErrConflict unless the stored version is a.Version-1.
	UpdateAssignment(ctx context.Context, a domain.Assignment) error
	// ListAssignments lists in id order after f.After.
	ListAssignments(ctx context.Context, f AssignmentFilter) ([]domain.Assignment, error)
	CoveredUnits(ctx context.Context, q CoverageQuery) ([]domain.Unit, error)

	InsertApproval(ctx context.Context, a domain.Approval) error
	GetApproval(ctx context.Context, id uuid.UUID) (domain.Approval, error)
	LockApproval(ctx context.Context, id uuid.UUID) (domain.Approval, error)
	// LatestApproval is the newest approval requested for subject in
	// project (ErrNotFound when there is none).
	LatestApproval(ctx context.Context, project uuid.UUID, subject domain.ApprovalSubject) (domain.Approval, error)
	// ListApprovals lists approvals with their decisions, in id order
	// after f.After.
	ListApprovals(ctx context.Context, f ApprovalFilter) ([]domain.Approval, error)
	// AppendDecision appends the approval's seq-th decision (1-based).
	AppendDecision(ctx context.Context, approval uuid.UUID, seq int, d domain.Decision) error
	// UpdateApproval saves state, version and closed_at, like
	// UpdateAssignment.
	UpdateApproval(ctx context.Context, a domain.Approval) error

	Publish(ctx context.Context, e outbox.Event) error
}

// Affiliation is who a member is, as far as assignments and approvals
// care: their roles, the groups they are in and the vendor they work
// for.
type Affiliation struct {
	Member uuid.UUID
	Roles  []string
	Groups []uuid.UUID
	// Vendor is uuid.Nil unless the member works for one.
	Vendor uuid.UUID
}

// Includes reports whether a names the member: directly, by a role
// they hold, by a group they are in or by their vendor.
func (f Affiliation) Includes(a domain.Assignee) bool {
	switch a.Kind {
	case domain.AssigneeMember:
		return a.ID == f.Member
	case domain.AssigneeRole:
		return slices.Contains(f.Roles, a.Role)
	case domain.AssigneeGroup:
		return slices.Contains(f.Groups, a.ID)
	case domain.AssigneeVendor:
		return f.Vendor != uuid.Nil && a.ID == f.Vendor
	}
	return false
}

// visibilityKeys are the stored assignee spellings whose assignments
// let the member see units: given to them, to a group they are in, or
// to their vendor (§3.3). Not roles: a role names everyone who holds
// it, and "every translator" must not make every vendor's translator
// see the work.
func (f Affiliation) visibilityKeys() []string {
	keys := []string{domain.MemberAssignee(f.Member).String()}
	for _, g := range f.Groups {
		keys = append(keys, domain.GroupAssignee(g).String())
	}
	if f.Vendor != uuid.Nil {
		keys = append(keys, domain.VendorAssignee(f.Vendor).String())
	}
	return keys
}

// workKeys are visibilityKeys plus the member's roles: everything a
// member may pick up and work on.
func (f Affiliation) workKeys() []string {
	keys := f.visibilityKeys()
	for _, r := range f.Roles {
		keys = append(keys, domain.RoleAssignee(r).String())
	}
	return keys
}

// Directory is Identity's read side as assignments need it. Identity
// owns members, groups and vendors; Workflow asks.
type Directory interface {
	// Affiliation is the member's roles, groups and vendor
	// (ErrNotFound for a member this tenant does not have).
	Affiliation(ctx context.Context, member uuid.UUID) (Affiliation, error)
	// Resolve finds the member, group or vendor ref names: its id, or
	// for a group or a vendor also its name, ignoring case
	// (ErrUnknownParty when there is none).
	Resolve(ctx context.Context, kind domain.AssigneeKind, ref string) (uuid.UUID, error)
}

// Authors answers who wrote the text under approval: the actor of a
// translation unit's latest content revision. Four-eyes is decided
// against it at the moment of each decision, so a grant given before
// its granter rewrote the text stops counting. A release request's
// author is its requester, which WorkService asks Release for
// (WithReleaseRequests).
type Authors interface {
	Author(ctx context.Context, project uuid.UUID, subject domain.ApprovalSubject) (string, error)
}
