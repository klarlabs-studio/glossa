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
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// Approvals (RFC 0006 §3.2): n distinct eligible people signing off a
// subject. Approving is not a fifth review state — the definition's
// set_review_state action, run as the last approver, is what moves a
// translation (§2.5) — and it is a human decision: no token, no agent
// and no background process can make one.

// ApprovalState is where an approval is.
type ApprovalState string

// The approval states. Pending collects decisions; the other two are
// final.
const (
	ApprovalPending ApprovalState = "pending"
	ApprovalGranted ApprovalState = "granted"
	ApprovalDenied  ApprovalState = "denied"
)

// Verdict is one decision's answer.
type Verdict string

// The verdicts.
const (
	VerdictGranted Verdict = "granted"
	VerdictDenied  Verdict = "denied"
)

// Approval errors. Each is a refusal: nothing is recorded.
var (
	ErrInvalidApproval = errors.New("invalid approval")
	// ErrApprovalClosed is a decision on an approval already granted or
	// denied.
	ErrApprovalClosed = errors.New("the approval is already decided")
	// ErrNotHuman is a decision by anything but a signed-in person: an
	// API token, an MCP agent, a background process (§3.2, §9.3).
	ErrNotHuman = errors.New("only a person can decide an approval")
	// ErrNotEligible is a decision by someone the approval does not ask.
	ErrNotEligible = errors.New("not eligible to decide this approval")
	// ErrOwnText is four-eyes: the author of the text under approval
	// cannot approve it.
	ErrOwnText = errors.New("the author of the text cannot approve it")
)

// ApprovalSubject is what an approval is about: a translation unit, or a
// release request (§5.1).
type ApprovalSubject struct {
	Kind SubjectKind
	// ID is the message of a translation unit, or the release request.
	ID uuid.UUID
	// Locale is a translation unit's canonical BCP 47 tag; empty for a
	// release request.
	Locale string
}

// Canonical validates s and returns it with its locale canonical.
func (s ApprovalSubject) Canonical() (ApprovalSubject, error) {
	if !s.Kind.Valid() || s.ID == uuid.Nil {
		return ApprovalSubject{}, fmt.Errorf("%w: subject %q %s", ErrInvalidApproval, s.Kind, s.ID)
	}
	if s.Kind == SubjectReleaseRequest {
		if s.Locale != "" {
			return ApprovalSubject{}, fmt.Errorf("%w: a release request has no locale", ErrInvalidApproval)
		}
		return s, nil
	}
	tag, err := bcp47.Parse(s.Locale)
	if err != nil {
		return ApprovalSubject{}, fmt.Errorf("%w: %q is not a BCP 47 locale", ErrInvalidApproval, s.Locale)
	}
	s.Locale = tag.String()
	return s, nil
}

// Decision is one recorded answer. Decisions are append-only.
type Decision struct {
	// Principal is who decided, in the outbox spelling ("person:<id>").
	Principal string
	Verdict   Verdict
	Reason    string
	At        time.Time
}

// Approval asks Required distinct eligible people to grant a subject.
type Approval struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// InstanceID is the workflow instance whose request_approval action
	// created it; zero for one made by hand.
	InstanceID uuid.UUID
	Subject    ApprovalSubject
	Required   int
	// Eligible is who may decide: a member, a role or a group. A vendor
	// delivers work; it does not sign it off.
	Eligible Assignee
	// DistinctFromAuthor is four-eyes: the author of the latest content
	// revision neither decides nor counts.
	DistinctFromAuthor bool
	DueAt              *time.Time
	State              ApprovalState
	// Decisions are in the order they were made.
	Decisions []Decision
	Version   int
	CreatedBy string
	CreatedAt time.Time
	ClosedAt  *time.Time
}

// NewApproval makes a pending approval.
func NewApproval(project uuid.UUID, subject ApprovalSubject, required int, eligible Assignee, distinctFromAuthor bool, due *time.Time, by string, now time.Time) (Approval, error) {
	if project == uuid.Nil || by == "" {
		return Approval{}, fmt.Errorf("%w: no project or actor", ErrInvalidApproval)
	}
	subject, err := subject.Canonical()
	if err != nil {
		return Approval{}, err
	}
	if required < 1 || required > maxApprovals {
		return Approval{}, fmt.Errorf("%w: n must be between 1 and %d", ErrInvalidApproval, maxApprovals)
	}
	if err := eligible.Validate(); err != nil {
		return Approval{}, fmt.Errorf("%w: %w", ErrInvalidApproval, err)
	}
	if eligible.Kind == AssigneeVendor {
		return Approval{}, fmt.Errorf("%w: a vendor cannot be asked to approve", ErrInvalidApproval)
	}
	if due != nil && !due.After(now) {
		return Approval{}, fmt.Errorf("%w: due before it was made", ErrInvalidApproval)
	}
	return Approval{
		ID: newID(), ProjectID: project, Subject: subject, Required: required, Eligible: eligible,
		DistinctFromAuthor: distinctFromAuthor, DueAt: due, State: ApprovalPending, Version: 1,
		CreatedBy: by, CreatedAt: now,
	}, nil
}

// Ballot is a decision someone is trying to make, with the facts only
// the application can establish: whether the approval asks them, and
// who wrote the text now under approval.
type Ballot struct {
	Principal string
	Verdict   Verdict
	Reason    string
	// Eligible is whether the approval's Eligible party includes the
	// principal.
	Eligible bool
	// Author is the actor of the subject's latest content revision.
	Author string
	At     time.Time
}

// Decide records b. A repeated grant by someone who already granted
// records nothing and changes nothing (recorded is false): one person
// counts once however often they click. One denial ends the approval
// denied; Required distinct grants end it granted.
func (a *Approval) Decide(b Ballot) (recorded bool, err error) {
	if a.State != ApprovalPending {
		return false, fmt.Errorf("%w: %s", ErrApprovalClosed, a.State)
	}
	if outbox.Actor(b.Principal).Kind() != outbox.ActorKindPerson {
		return false, ErrNotHuman
	}
	if !b.Eligible {
		return false, ErrNotEligible
	}
	if a.DistinctFromAuthor && b.Principal == b.Author {
		return false, ErrOwnText
	}
	reason := strings.TrimSpace(b.Reason)
	if utf8.RuneCountInString(reason) > MaxReason {
		return false, fmt.Errorf("%w: reason longer than %d characters", ErrInvalidApproval, MaxReason)
	}
	switch b.Verdict {
	case VerdictGranted:
		if a.granted(b.Principal) {
			return false, nil
		}
	case VerdictDenied:
	default:
		return false, fmt.Errorf("%w: verdict %q", ErrInvalidApproval, b.Verdict)
	}
	a.Decisions = append(a.Decisions, Decision{Principal: b.Principal, Verdict: b.Verdict, Reason: reason, At: b.At})
	switch {
	case b.Verdict == VerdictDenied:
		a.close(ApprovalDenied, b.At)
	case len(a.Granters(b.Author)) >= a.Required:
		a.close(ApprovalGranted, b.At)
	default:
		a.Version++
	}
	return true, nil
}

func (a *Approval) close(to ApprovalState, at time.Time) {
	a.State, a.ClosedAt = to, &at
	a.Version++
}

func (a Approval) granted(principal string) bool {
	for _, d := range a.Decisions {
		if d.Verdict == VerdictGranted && d.Principal == principal {
			return true
		}
	}
	return false
}

// Granters are the distinct people whose grants count, in the order
// they granted. With DistinctFromAuthor the author never counts — also
// when they granted before they wrote the text now under approval.
func (a Approval) Granters(author string) []string {
	var out []string
	for _, d := range a.Decisions {
		if d.Verdict != VerdictGranted || (a.DistinctFromAuthor && d.Principal == author) {
			continue
		}
		if !slices.Contains(out, d.Principal) {
			out = append(out, d.Principal)
		}
	}
	return out
}

// Approvers is every grant so far, one entry per granting decision: the
// approvals_at_least guard's input (Subject.Approvers), which applies
// its own distinct_from_author.
func (a Approval) Approvers() []string {
	var out []string
	for _, d := range a.Decisions {
		if d.Verdict == VerdictGranted {
			out = append(out, d.Principal)
		}
	}
	return out
}
