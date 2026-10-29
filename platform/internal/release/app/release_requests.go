package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Release approvals (RFC 0006 §5.1). An environment with an `approval`
// requirement holds every publish and promote into it as a release
// request; the pointer moves when the request is deployed, which
// Release does only once the requirement is met by people other than
// the requester, and only after running the publish gate again.
// Rollback is never held (§5.1, intent §74.2).
//
// A workflow (Workflow's seeded release-approval definition, or one a
// tenant extends) asks for the approvals and deploys as the last
// approver; it calls DeployRequest and DenyRequest like any other
// caller, and Release checks for itself.

// ErrApprovalsUnavailable is a deploy in a deployment where nothing can
// say who approved: the requirement cannot be checked, so the pointer
// does not move.
var ErrApprovalsUnavailable = errors.New("release: approvals cannot be read in this deployment, so no release request is deployed")

// UseApprovals wires the port that says who approved a release request.
// It is set once, when the composition root has built Workflow (which
// itself needs Release); until it is, every deploy is refused.
func (s *Service) UseApprovals(a Approvals) { s.approvals = a }

// SetEnvironmentApproval sets or clears (nil) an environment's approval
// requirement. ifMatch is the environment's ETag; the change bumps its
// version and publishes release.environment.policy_changed naming the
// actor, as a policy change does.
//
// Changing who must approve a release is governance, not publishing: a
// publisher who could switch the requirement off would make it
// decorative. It takes releases.publish and workflows.manage (owner and
// admin) in the project. A request already pending keeps the
// requirement it shows its approvers; its deploy must also meet the
// requirement of the moment (DeployRequest).
func (s *Service) SetEnvironmentApproval(ctx context.Context, project uuid.UUID, name string, ifMatch int, approval *domain.ApprovalPolicy) (domain.Environment, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Environment{}, err
	}
	if err := authz.RequireIn(ctx, authz.WorkflowsManage, project); err != nil {
		return domain.Environment{}, err
	}
	if !validName(name) {
		return domain.Environment{}, ErrNotFound
	}
	if approval != nil {
		a, err := domain.NewApprovalPolicy(approval.N, approval.From, approval.DistinctFromRequester)
		if err != nil {
			return domain.Environment{}, err
		}
		approval = &a
	}
	var e domain.Environment
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		var err error
		if e, err = st.Environment(ctx, project, name, true); err != nil {
			return err
		}
		if e.Version != ifMatch {
			return ErrPreconditionFailed
		}
		expected := e.Version
		changed, err := e.ChangeApproval(approval, s.now())
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateEnvironment(ctx, e, expected); err != nil {
			return err
		}
		return st.Publish(ctx, environmentEvent(domain.EventEnvironmentPolicyChanged, e, by))
	})
	return e, err
}

// ApprovalChange is what an environment update asks of its approval
// requirement: Approval replaces it, and a nil Approval switches it
// off. A nil *ApprovalChange leaves it as it is.
type ApprovalChange struct {
	Approval *domain.ApprovalPolicy
}

// ConfigureEnvironment changes an environment's eligibility policy and,
// when approval is not nil, its approval requirement — together, under
// one ifMatch and in one transaction, so a client that changes both
// never leaves one applied and the other refused. It takes
// releases.publish, and workflows.manage as well when the approval
// requirement actually changes (SetEnvironmentApproval says why); an
// update that restates the requirement unchanged is a policy edit.
// One release.environment.policy_changed is published when anything
// changed.
func (s *Service) ConfigureEnvironment(ctx context.Context, project uuid.UUID, name string, ifMatch int,
	policy domain.Policy, approval *ApprovalChange,
) (domain.Environment, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.Environment{}, err
	}
	if !validName(name) {
		return domain.Environment{}, ErrNotFound
	}
	p, err := domain.NewPolicy(policy.States, policy.IncludeOutdated)
	if err != nil {
		return domain.Environment{}, err
	}
	if approval != nil && approval.Approval != nil {
		a, err := domain.NewApprovalPolicy(approval.Approval.N, approval.Approval.From, approval.Approval.DistinctFromRequester)
		if err != nil {
			return domain.Environment{}, err
		}
		approval = &ApprovalChange{Approval: &a}
	}
	var e domain.Environment
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureDefaults(ctx, st, project); err != nil {
			return err
		}
		var err error
		if e, err = st.Environment(ctx, project, name, true); err != nil {
			return err
		}
		if e.Version != ifMatch {
			return ErrPreconditionFailed
		}
		expected := e.Version
		policyChanged, err := e.ChangePolicy(p, s.now())
		if err != nil {
			return err
		}
		var approvalChanged bool
		if approval != nil {
			if !sameApproval(e.Approval, approval.Approval) {
				if err := authz.RequireIn(ctx, authz.WorkflowsManage, project); err != nil {
					return err
				}
			}
			if approvalChanged, err = e.ChangeApproval(approval.Approval, s.now()); err != nil {
				return err
			}
		}
		if !policyChanged && !approvalChanged {
			return nil
		}
		if policyChanged && approvalChanged {
			e.Version = expected + 1 // one change, one version
		}
		if err := st.UpdateEnvironment(ctx, e, expected); err != nil {
			return err
		}
		return st.Publish(ctx, environmentEvent(domain.EventEnvironmentPolicyChanged, e, by))
	})
	return e, err
}

// sameApproval reports whether a and b are the same requirement (nil is
// none), as the domain compares them.
func sameApproval(a, b *domain.ApprovalPolicy) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// hold records a request to deploy rel into env instead of moving the
// pointer, in the caller's transaction (env locked). A pending request
// into the same environment is withdrawn by the newer one: two requests
// can never both deploy, and the newest is what its requester meant.
func (s *Service) hold(ctx context.Context, st Store, env domain.Environment, rel domain.Release, action domain.Action,
	by string, verdict domain.GateVerdict, override domain.Override,
) (domain.ReleaseRequest, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	now := s.now()
	req, err := domain.NewReleaseRequest(id, env, rel.ID, action, by, verdict, override, now)
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	old, err := st.PendingReleaseRequest(ctx, env.ProjectID, env.Name)
	switch {
	case err == nil:
		expected := old.Version
		if err := old.Withdraw(by, "replaced by request "+id.String(), now); err != nil {
			return domain.ReleaseRequest{}, err
		}
		if err := st.UpdateReleaseRequest(ctx, old, expected); err != nil {
			return domain.ReleaseRequest{}, err
		}
		if err := st.Publish(ctx, requestEvent(domain.EventRequestWithdrawn, old, nil, by)); err != nil {
			return domain.ReleaseRequest{}, err
		}
	case !isNotFound(err):
		return domain.ReleaseRequest{}, err
	}
	if err := st.InsertReleaseRequest(ctx, req); err != nil {
		return domain.ReleaseRequest{}, err
	}
	return req, st.Publish(ctx, requestEvent(domain.EventRequestCreated, req, nil, by))
}

func requestEvent(typ string, r domain.ReleaseRequest, approvers []string, by string) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateReleaseRequest, AggregateID: r.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.RequestChangedOf(r, approvers, by),
	}
}

// heldReplay answers a replayed publish whose first answer was a
// request: the same request, and still nothing moved by this call.
func (s *Service) heldReplay(ctx context.Context, rel domain.Release) error {
	var req domain.ReleaseRequest
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) (err error) {
		req, err = st.ReleaseRequestFor(ctx, rel.ProjectID, rel.ID, rel.Environment)
		return err
	})
	switch {
	case isNotFound(err):
		return nil
	case err != nil:
		return err
	}
	return &domain.HeldError{Request: req}
}

// GetReleaseRequest returns one release request: what it would deploy,
// where, who asked, what they were asked for, what the gate said and
// whether the requester forced it (and why). It takes releases.read.
func (s *Service) GetReleaseRequest(ctx context.Context, project, id uuid.UUID) (domain.ReleaseRequest, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return domain.ReleaseRequest{}, err
	}
	var r domain.ReleaseRequest
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) (err error) {
		r, err = st.ReleaseRequest(ctx, project, id, false)
		return err
	})
	return r, err
}

// ListReleaseRequests lists a project's release requests, newest first,
// optionally in one environment and one state. It takes releases.read.
func (s *Service) ListReleaseRequests(ctx context.Context, project uuid.UUID, environment string, state domain.RequestState, page pagination.Page) ([]domain.ReleaseRequest, *string, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return nil, nil, err
	}
	if environment != "" && !validName(environment) {
		return nil, nil, ErrNotFound
	}
	before, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.ReleaseRequest
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) (err error) {
		rows, err = st.ReleaseRequests(ctx, RequestFilter{
			Project: project, Environment: environment, State: state, Before: before, Limit: page.Limit(),
		})
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.ReleaseRequest) string { return r.ID.String() })
	return items, next, nil
}

// requestIn reads a request and checks perm-for-the-environment as the
// caller: approvals.decide is environment-scoped for a release request
// (RFC 0006 §4.2). The read itself takes releases.read.
func (s *Service) requestIn(ctx context.Context, project, id uuid.UUID) (domain.ReleaseRequest, string, error) {
	req, err := s.GetReleaseRequest(ctx, project, id)
	if err != nil {
		return domain.ReleaseRequest{}, "", err
	}
	if err := authz.RequireInEnvironment(ctx, authz.ApprovalsDecide, project, req.Environment); err != nil {
		return domain.ReleaseRequest{}, "", err
	}
	actor, err := authz.EventActor(ctx)
	if err != nil {
		return domain.ReleaseRequest{}, "", err
	}
	return req, actor.String(), nil
}

// DeployRequest deploys an approved release request as the caller, who
// must be one of its approvers — the last one, when a workflow runs it
// (RFC 0006 §5.1). It moves the pointer through the same path a publish
// uses, and only when:
//
//   - the caller holds approvals.decide in the request's environment;
//   - the grants on the request (Approvals) come from enough distinct
//     people other than the requester — enough for the requirement the
//     request was made under and for the environment's requirement of
//     the moment, whichever asks more — and include the caller;
//   - the publish gate, run again, passes: the release is immutable, so
//     its answer changes only if the environment's policy changed, and
//     then the new policy decides. A forced request carries its force
//     into the re-run, as it did into the first.
//
// A gate that refuses closes the request as refused, records why and
// publishes release.release_request.refused; the error is a
// *DeployRefusedError (ErrPolicyNotMet or ErrIneligible). Deploying a
// request that is already deployed changes nothing.
func (s *Service) DeployRequest(ctx context.Context, project, id uuid.UUID) (domain.ReleaseRequest, error) {
	req, by, err := s.requestIn(ctx, project, id)
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	if req.State == domain.RequestDeployed {
		return req, nil // a redelivered step
	}
	if !req.Open() {
		return domain.ReleaseRequest{}, fmt.Errorf("%w: it is %s", domain.ErrRequestClosed, req.State)
	}
	if s.approvals == nil {
		return domain.ReleaseRequest{}, ErrApprovalsUnavailable
	}
	// Read before the transaction: Workflow and Catalog run their own.
	grants, err := s.approvals.Granters(ctx, project, id)
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	gate, err := s.policyGate(ctx, project, req.Environment)
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	var (
		refused *domain.DeployRefusedError
		moved   bool
	)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		env, err := st.Environment(ctx, project, req.Environment, true)
		if err != nil {
			return err
		}
		if req, err = st.ReleaseRequest(ctx, project, id, true); err != nil {
			return err
		}
		if req.State == domain.RequestDeployed {
			return nil
		}
		approvers := domain.Approvers(grants, req.Requester)
		if err := approvalMet(req, env, approvers, by); err != nil {
			return err
		}
		rel, err := st.Release(ctx, project, req.ReleaseID)
		if err != nil {
			return err
		}
		expected := req.Version
		override, cause := redeployGate(gate, env, rel, req.Override)
		if cause != nil {
			if err := req.Refuse(by, cause.Error(), s.now()); err != nil {
				return err
			}
			if err := st.UpdateReleaseRequest(ctx, req, expected); err != nil {
				return err
			}
			refused = &domain.DeployRefusedError{Request: req, Cause: cause}
			for _, typ := range []string{domain.EventRequestApproved, domain.EventRequestRefused} {
				if err := st.Publish(ctx, requestEvent(typ, req, approvers, by)); err != nil {
					return err
				}
			}
			return nil
		}
		if err := req.Deploy(by, s.now()); err != nil {
			return err
		}
		if err := st.UpdateReleaseRequest(ctx, req, expected); err != nil {
			return err
		}
		previous := env.Current
		if err := s.move(ctx, st, &env, rel, req.Action, by, override); err != nil {
			return err
		}
		moved = previous != rel.ID
		for _, typ := range []string{domain.EventRequestApproved, domain.EventRequestDeployed} {
			if err := st.Publish(ctx, requestEvent(typ, req, approvers, by)); err != nil {
				return err
			}
		}
		if !moved {
			return nil
		}
		return st.Publish(ctx, deployedEvent(req, rel, previous, by))
	})
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	if refused != nil {
		return refused.Request, refused
	}
	if moved {
		s.syncNow(ctx, project, req.Environment)
	}
	return req, nil
}

// approvalMet is the requirement Release keeps whatever a workflow says:
// enough distinct approvers who are not the requester, for the
// requirement the request was made under and the environment's current
// one, with the deployer among them.
func approvalMet(req domain.ReleaseRequest, env domain.Environment, approvers []string, by string) error {
	need := req.Approval.N
	if env.Approval != nil {
		need = max(need, env.Approval.N)
	}
	switch {
	case !req.Open():
		return fmt.Errorf("%w: it is %s", domain.ErrRequestClosed, req.State)
	case by == req.Requester:
		return fmt.Errorf("%w: the requester cannot deploy their own request", domain.ErrApprovalNotMet)
	case !slices.Contains(approvers, by):
		return fmt.Errorf("%w: %s has not approved it", domain.ErrApprovalNotMet, by)
	case len(approvers) < need:
		return fmt.Errorf("%w: %d of %d approvals", domain.ErrApprovalNotMet, len(approvers), need)
	}
	return nil
}

// redeployGate runs the publish gate again for a request's release, as
// Promote does for a release that already exists: the environment's
// policy must cover what the release ships, and its check policy must
// pass or be forced. It returns the override to record, or why not.
func redeployGate(gate domain.PolicyGate, env domain.Environment, rel domain.Release, override domain.Override) (domain.Override, error) {
	if !env.Policy.Covers(rel.Policy) {
		return domain.Override{}, domain.Ineligible(env.Name, env.Policy, rel.Version, rel.Environment, rel.Policy)
	}
	return gate.Enforce(rel.Policy, rel.Content, rel.Stats, override)
}

// deployedEvent is the pointer event of a deployed request: the one its
// action would have published had it not been held, naming the
// approver who deployed it.
func deployedEvent(req domain.ReleaseRequest, rel domain.Release, previous uuid.UUID, by string) outbox.Event {
	if req.Action == domain.ActionPublish {
		return outbox.Event{
			Type: domain.EventPublished, AggregateType: domain.AggregateRelease, AggregateID: rel.ID.String(), Actor: outbox.Actor(by),
			Payload: domain.Published{
				ReleaseID: rel.ID.String(), ProjectID: rel.ProjectID.String(), Version: rel.Version, Environment: req.Environment,
				ParentID: optionalID(rel.Parent), ManifestDigest: rel.Digest, Messages: rel.Stats.Messages, By: by,
			},
		}
	}
	return outbox.Event{
		Type: domain.EventPromoted, AggregateType: domain.AggregateRelease, AggregateID: rel.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.PointerMoved{
			ReleaseID: rel.ID.String(), ProjectID: rel.ProjectID.String(), Version: rel.Version, Environment: req.Environment,
			PreviousReleaseID: optionalID(previous), By: by,
		},
	}
}

// DenyRequest closes a pending request as denied by the caller, who
// holds approvals.decide in its environment. No pointer moves. Denying
// a request that is already denied changes nothing.
func (s *Service) DenyRequest(ctx context.Context, project, id uuid.UUID) (domain.ReleaseRequest, error) {
	req, by, err := s.requestIn(ctx, project, id)
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if req, err = st.ReleaseRequest(ctx, project, id, true); err != nil {
			return err
		}
		if req.State == domain.RequestDenied {
			return nil // a redelivered step
		}
		expected := req.Version
		if err := req.Deny(by, s.now()); err != nil {
			return err
		}
		if err := st.UpdateReleaseRequest(ctx, req, expected); err != nil {
			return err
		}
		return st.Publish(ctx, requestEvent(domain.EventRequestDenied, req, nil, by))
	})
	return req, err
}

// WithdrawRequest takes a pending request back: its requester, or
// anyone who may publish to the project (releases.publish). No pointer
// moves.
func (s *Service) WithdrawRequest(ctx context.Context, project, id uuid.UUID, reason string) (domain.ReleaseRequest, error) {
	by, err := s.checkProject(ctx, project, authz.ReleasesPublish)
	if err != nil {
		return domain.ReleaseRequest{}, err
	}
	var req domain.ReleaseRequest
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if req, err = st.ReleaseRequest(ctx, project, id, true); err != nil {
			return err
		}
		expected := req.Version
		if err := req.Withdraw(by, reason, s.now()); err != nil {
			return err
		}
		if err := st.UpdateReleaseRequest(ctx, req, expected); err != nil {
			return err
		}
		return st.Publish(ctx, requestEvent(domain.EventRequestWithdrawn, req, nil, by))
	})
	return req, err
}
