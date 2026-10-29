package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Release approvals (RFC 0006 §5.1). An environment may require that a
// publish or a promote into it is approved by people other than the
// one who asked. Such a move does not happen when it is asked for: it
// becomes a ReleaseRequest, and the pointer moves only when the request
// is approved — through the same Point path a publish uses, after the
// publish gate has been run again. A forced publish still waits: force
// overrides the gate, never the approval. Rollback never waits.
//
// Release owns the requirement and refuses to move a pointer until it
// is met, whatever runs the process around it (a workflow definition,
// §5.1: "extending it can add stages; it cannot remove the
// requirement"). Who the approvers are and what they decided is asked
// of the Approvals port; Release does not know Workflow.

// Errors of release approvals.
var (
	// ErrInvalidApproval is an environment approval requirement that is
	// not one: n outside 1–10, no party or more than one, or
	// distinct_from_requester switched off (self-approval is not
	// offered, §15 q6).
	ErrInvalidApproval = errors.New("release: an approval needs n between 1 and 10, exactly one of member, role or group, " +
		"and distinct_from_requester (self-approval is not offered)")
	// ErrApprovalOnBranch is an approval requirement on a branch
	// environment, whose policy is fixed.
	ErrApprovalOnBranch = errors.New("release: a branch environment cannot require approval")
	// ErrApprovalRequired is a publish or a promote into an environment
	// that requires approval: the release (for a publish) is recorded,
	// a ReleaseRequest is made, and no pointer moved. *HeldError
	// carries the request.
	ErrApprovalRequired = errors.New("release: the environment requires approval; a release request was made and no pointer moved")
	// ErrRequestClosed is a decision on a request that is no longer
	// pending.
	ErrRequestClosed = errors.New("release: the release request is no longer pending")
	// ErrApprovalNotMet is a deploy of a request whose environment's
	// approval requirement is not met by the grants so far, or by a
	// deployer who is not one of the approvers.
	ErrApprovalNotMet = errors.New("release: the environment's approval requirement is not met")
	// ErrInvalidWithdrawReason is a withdrawal reason that is too long.
	ErrInvalidWithdrawReason = errors.New("release: a withdrawal reason is at most 1000 characters")
)

// maxApprovals bounds n, as Workflow's request_approval does.
const maxApprovals = 10

var partyRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// ApprovalParty names who may approve: exactly one member, role or
// group of the tenant, spelled as a workflow definition spells a party
// ({"role": "reviewer"}). Release does not resolve it; Workflow asks
// Identity when it requests the approvals. A vendor delivers work, it
// does not sign it off, so a vendor is not a party here.
type ApprovalParty struct {
	Member string `json:"member,omitempty"`
	Role   string `json:"role,omitempty"`
	Group  string `json:"group,omitempty"`
}

// Kind is "member", "role" or "group"; "" when the party names none or
// more than one.
func (p ApprovalParty) Kind() string {
	var kinds []string
	for k, v := range map[string]string{"member": p.Member, "role": p.Role, "group": p.Group} {
		if v != "" {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

// Ref is the one name the party carries.
func (p ApprovalParty) Ref() string { return p.Member + p.Role + p.Group }

// ApprovalPolicy is an environment's approval requirement (RFC 0006
// §5.1): N distinct people of From, none of them the requester.
type ApprovalPolicy struct {
	N    int           `json:"n"`
	From ApprovalParty `json:"from"`
	// DistinctFromRequester is four-eyes against the requester. It is
	// always true: an author never approves their own work (§15 q6),
	// and a policy that says otherwise is refused.
	DistinctFromRequester bool `json:"distinct_from_requester"`
}

// NewApprovalPolicy validates an approval requirement.
func NewApprovalPolicy(n int, from ApprovalParty, distinctFromRequester bool) (ApprovalPolicy, error) {
	if n < 1 || n > maxApprovals || !distinctFromRequester || from.Kind() == "" || !partyRefPattern.MatchString(from.Ref()) {
		return ApprovalPolicy{}, ErrInvalidApproval
	}
	return ApprovalPolicy{N: n, From: from, DistinctFromRequester: true}, nil
}

// Validate checks a policy read back or handed in.
func (a ApprovalPolicy) Validate() error {
	_, err := NewApprovalPolicy(a.N, a.From, a.DistinctFromRequester)
	return err
}

// Equal reports whether a and b are the same requirement (nil is none).
func equalApproval(a, b *ApprovalPolicy) bool {
	switch {
	case a == nil || b == nil:
		return a == b
	default:
		return *a == *b
	}
}

// SatisfiedBy reports whether grants — one entry per granting decision,
// in the outbox spelling of who granted — meet the requirement for a
// request made by requester: N distinct people, never the requester.
func (a ApprovalPolicy) SatisfiedBy(grants []string, requester string) bool {
	return len(Approvers(grants, requester)) >= a.N
}

// Approvers are the distinct grantors who count against a request made
// by requester, in the order they first granted.
func Approvers(grants []string, requester string) []string {
	var out []string
	for _, g := range grants {
		if g == "" || g == requester || slices.Contains(out, g) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// ChangeApproval replaces the environment's approval requirement (nil
// switches it off); false if it is unchanged. It is versioned with the
// environment like a policy change: the request in flight keeps the
// requirement it was made under for what it shows, and the deploy is
// decided by the requirement of the moment. A branch environment cannot
// require approval.
func (e *Environment) ChangeApproval(a *ApprovalPolicy, now time.Time) (bool, error) {
	if a != nil {
		if err := a.Validate(); err != nil {
			return false, err
		}
		a = &ApprovalPolicy{N: a.N, From: a.From, DistinctFromRequester: true}
	}
	if equalApproval(e.Approval, a) {
		return false, nil
	}
	if e.Kind == KindBranch && a != nil {
		return false, ErrApprovalOnBranch
	}
	e.Approval = a
	e.touch(now)
	return true, nil
}

// RequestState is where a release request is.
type RequestState string

// Release request states. Pending is the only open one.
const (
	RequestPending RequestState = "pending"
	// RequestDeployed: approved, the gate passed again, and the pointer
	// moved.
	RequestDeployed RequestState = "deployed"
	// RequestDenied: an approver said no.
	RequestDenied RequestState = "denied"
	// RequestWithdrawn: the requester (or a publisher) took it back, or
	// a newer request into the same environment replaced it.
	RequestWithdrawn RequestState = "withdrawn"
	// RequestRefused: approved, but the publish gate refused the deploy
	// when it was run again — the environment's check policy changed
	// since the request was made (§5.1: "the new policy is the one that
	// decides"). Reason says why.
	RequestRefused RequestState = "refused"
)

// GateVerdict is what the publish gate said when the request was made.
type GateVerdict struct {
	// Met is true when the release met the environment's check policy.
	// A request whose gate was not met exists only because it was
	// forced.
	Met bool `json:"met"`
	// Unmet lists what the policy asked for and the release lacks.
	Unmet []string `json:"unmet,omitempty"`
}

// VerdictOf turns the gate's answer into a verdict.
func VerdictOf(err error) GateVerdict {
	var notMet *PolicyNotMetError
	if !errors.As(err, &notMet) {
		return GateVerdict{Met: true}
	}
	v := GateVerdict{}
	for _, u := range notMet.Unmet {
		v.Unmet = append(v.Unmet, u.Detail)
	}
	return v
}

// ReleaseRequest is a publish or a promote into an environment that
// requires approval (RFC 0006 §5.1): which release, where, asked by
// whom, under which requirement, and what the gate said then.
type ReleaseRequest struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Environment string
	ReleaseID   uuid.UUID
	// Action is what the deploy will record: a publish, or a promote.
	Action Action
	// Requester is who asked, in the outbox spelling. They never count
	// toward the approval.
	Requester string
	// Approval is the environment's requirement when the request was
	// made: what the approvers were asked for.
	Approval ApprovalPolicy
	// Verdict is the publish gate's answer when the request was made.
	Verdict GateVerdict
	// Override is the force the requester asked for, and why. It
	// overrides the gate, not the approval; approvers see it, and the
	// deploy re-runs the gate with it.
	Override Override
	State    RequestState
	// Version increments with every change.
	Version   int
	CreatedAt time.Time
	// DecidedBy is who closed it (the last approver, the denier, the
	// one who withdrew it) and DecidedAt when.
	DecidedBy string
	DecidedAt *time.Time
	// Reason says why it was refused or withdrawn.
	Reason string
}

// NewReleaseRequest makes a pending request.
func NewReleaseRequest(id uuid.UUID, env Environment, release uuid.UUID, action Action, requester string,
	verdict GateVerdict, override Override, now time.Time,
) (ReleaseRequest, error) {
	if env.Approval == nil {
		return ReleaseRequest{}, fmt.Errorf("release: %s requires no approval", env.Name)
	}
	if action != ActionPublish && action != ActionPromote {
		return ReleaseRequest{}, fmt.Errorf("release: a %s is never held for approval", action)
	}
	if requester == "" || release == uuid.Nil || id == uuid.Nil {
		return ReleaseRequest{}, errors.New("release: a release request needs an id, a release and a requester")
	}
	return ReleaseRequest{
		ID: id, ProjectID: env.ProjectID, Environment: env.Name, ReleaseID: release, Action: action,
		Requester: requester, Approval: *env.Approval, Verdict: verdict, Override: override,
		State: RequestPending, Version: 1, CreatedAt: now,
	}, nil
}

// Open reports whether the request still waits.
func (r ReleaseRequest) Open() bool { return r.State == RequestPending }

func (r *ReleaseRequest) close(to RequestState, by, reason string, now time.Time) error {
	if !r.Open() {
		return fmt.Errorf("%w: it is %s", ErrRequestClosed, r.State)
	}
	r.State, r.DecidedBy, r.DecidedAt, r.Reason = to, by, &now, reason
	r.Version++
	return nil
}

// Deploy closes the request as deployed by by, the last approver.
func (r *ReleaseRequest) Deploy(by string, now time.Time) error {
	return r.close(RequestDeployed, by, "", now)
}

// Refuse closes an approved request whose deploy the gate refused.
func (r *ReleaseRequest) Refuse(by, reason string, now time.Time) error {
	return r.close(RequestRefused, by, reason, now)
}

// Deny closes the request as denied by by.
func (r *ReleaseRequest) Deny(by string, now time.Time) error {
	return r.close(RequestDenied, by, "", now)
}

// Withdraw closes the request as withdrawn by by, with an optional
// reason.
func (r *ReleaseRequest) Withdraw(by, reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > MaxForceReasonLen {
		return ErrInvalidWithdrawReason
	}
	return r.close(RequestWithdrawn, by, reason, now)
}

// HeldError is a publish or a promote that became a release request:
// ErrApprovalRequired, carrying the request. It is how the caller
// learns that nothing moved and what to wait for.
type HeldError struct{ Request ReleaseRequest }

func (e *HeldError) Error() string {
	return fmt.Sprintf("%s (request %s: %d approval(s) needed in %s)",
		ErrApprovalRequired.Error(), e.Request.ID, e.Request.Approval.N, e.Request.Environment)
}

// Unwrap makes the error ErrApprovalRequired.
func (e *HeldError) Unwrap() error { return ErrApprovalRequired }

// DeployRefusedError is an approved request the gate refused at deploy
// time: the request is closed as refused, and Cause says why (a
// *PolicyNotMetError or an *IneligibleError).
type DeployRefusedError struct {
	Request ReleaseRequest
	Cause   error
}

func (e *DeployRefusedError) Error() string {
	return fmt.Sprintf("release: request %s was approved but its deploy was refused: %v", e.Request.ID, e.Cause)
}

// Unwrap is the cause: ErrPolicyNotMet or ErrIneligible.
func (e *DeployRefusedError) Unwrap() error { return e.Cause }
