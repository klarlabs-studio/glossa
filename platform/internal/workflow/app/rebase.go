package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Rebase (RFC 0006 §2.3): a running instance stays on the version it
// started with until a workflow manager moves it, explicitly, to a newer
// version of the same definition. The state is kept by name; a version
// that lacks it, or in which it is final, refuses the rebase, and the
// instance stays where it was.
//
// What a rebase does not do is decided here and documented in the
// API: it runs no entry action again (the assignments and approvals the
// state asked for stand, and nothing is asked twice), and it leaves a
// pending timer's due date alone. A rebase changes what happens next,
// never what already happened. If the new version's state asks for
// something the old one did not, the instance gets it on the next state
// it enters, as every instance on that version does.

// TransitionRebase is the event a rebase is recorded under in an
// instance's transition log. It is not a vocabulary event: no chart can
// react to it.
const TransitionRebase domain.EventName = "rebase"

// actionRebase is the one "action" a rebase's log entry records, so the
// log says which versions it moved between.
const actionRebase = "rebase"

// ErrRebaseVersion is a rebase onto a version the definition does not
// have.
var ErrRebaseVersion = errors.New("workflow: the definition has no such version")

// RebaseInput is a rebase request.
type RebaseInput struct {
	Project  uuid.UUID
	Instance uuid.UUID
	// IfVersion is the version the caller saw the instance run on (its
	// ETag): a rebase that another overtook is ErrConflict.
	IfVersion int
	// Version is the version to move to; 0 is the definition's latest.
	Version int
}

// Rebase moves one of project's running instances to a newer version of
// its definition, as the caller, who needs workflows.manage in the
// project. It appends the rebase to the instance's transition log and
// publishes workflow.instance.rebased in the same transaction.
func (r *Runner) Rebase(ctx context.Context, in RebaseInput) (InstanceView, error) {
	if in.Project == uuid.Nil {
		return InstanceView{}, ErrProjectNotFound
	}
	if err := authz.RequireIn(ctx, PermWorkflowsManage, in.Project); err != nil {
		return InstanceView{}, err
	}
	actor, err := authz.EventActor(ctx)
	if err != nil {
		return InstanceView{}, err
	}
	var out domain.Instance
	err = r.d.Tx.InTenant(ctx, func(ctx context.Context, st InstanceStore) error {
		inst, err := st.LockInstance(ctx, in.Instance)
		if err != nil {
			return err
		}
		if inst.Project != in.Project {
			return ErrNotFound
		}
		if inst.Version != in.IfVersion {
			return fmt.Errorf("%w: the instance runs on version %d, not %d", ErrConflict, inst.Version, in.IfVersion)
		}
		target, def, err := r.rebaseTarget(ctx, st, inst, in.Version)
		if err != nil {
			return err
		}
		rebased, snapshot, err := inst.Rebase(def, target, r.now())
		if err != nil {
			return err
		}
		if err := st.RebaseInstance(ctx, rebased, snapshot); err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if err := st.Publish(ctx, outbox.Event{
			ID: id, Type: domain.EventInstanceRebased, AggregateType: AggregateInstance, AggregateID: inst.ID.String(),
			Actor: actor, Payload: domain.InstanceRebasedOf(inst, target),
		}); err != nil {
			return err
		}
		out = rebased
		return st.AppendTransition(ctx, inst.ID, Transition{
			From: inst.State, Event: TransitionRebase, To: inst.State, Outcome: TransitionApplied,
			Guards: []GuardOutcome{},
			Actions: []ActionOutcome{{Action: actionRebase, Outcome: ActionDone,
				Detail: fmt.Sprintf("version %d → %d", inst.Version, target)}},
			Actor: actor, EventID: id, At: rebased.UpdatedAt,
		})
	})
	if err != nil {
		return InstanceView{}, err
	}
	return viewOf(out), nil
}

// rebaseTarget resolves the version to rebase onto (the latest for 0)
// and compiles it.
func (r *Runner) rebaseTarget(ctx context.Context, st InstanceStore, inst domain.Instance, n int) (int, *domain.Definition, error) {
	if n == 0 {
		latest, err := st.LatestVersion(ctx, inst.Definition)
		if err != nil {
			return 0, nil, err
		}
		n = latest
	}
	if n <= inst.Version {
		// Before loading: an older version exists, and is still refused.
		return 0, nil, fmt.Errorf("%w: it runs on version %d, and %d is not newer", domain.ErrRebaseNotNewer, inst.Version, n)
	}
	at := inst
	at.Version = n
	def, err := r.definition(ctx, st, at)
	if errors.Is(err, ErrNotFound) {
		return 0, nil, fmt.Errorf("%w: version %d", ErrRebaseVersion, n)
	}
	return n, def, err
}

func viewOf(i domain.Instance) InstanceView {
	return InstanceView{ID: i.ID, Project: i.Project, Definition: i.Definition, Version: i.Version, Kind: i.Kind,
		SubjectID: i.SubjectID, Locale: i.Locale, State: i.State, Status: i.Status,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt}
}
