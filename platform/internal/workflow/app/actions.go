package app

import (
	"context"
	"errors"
	"fmt"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Action outcomes, as the transition log records them.
const (
	ActionDone    = "done"
	ActionRefused = "refused"
	ActionFailed  = "failed"
)

// loaded is a subject as one transition sees it: the snapshot guards
// read, and the message key the actions address it by.
type loaded struct {
	Subject domain.Subject
	Key     string
}

// loadSubject reads the read-only snapshot guards look at (§2.4), once
// per transition, through the owning contexts' ports as Workflow's
// reader.
func (r *Runner) loadSubject(reader context.Context, s SubjectRef) (loaded, error) {
	out := loaded{Subject: domain.Subject{Kind: s.Kind, Locale: s.Locale}}
	switch s.Kind {
	case domain.SubjectTranslation:
		if err := r.loadUnit(reader, s, &out); err != nil {
			return loaded{}, err
		}
	case domain.SubjectReleaseRequest:
		if err := r.loadRequest(reader, s, &out); err != nil {
			return loaded{}, err
		}
	}
	if r.d.Assignments != nil {
		approvers, err := r.d.Assignments.Approvers(reader, s.Project, domain.ApprovalSubject{Kind: s.Kind, ID: s.ID, Locale: s.Locale})
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return loaded{}, fmt.Errorf("workflow: approvals of %s: %w", s.ID, err)
		}
		out.Subject.Approvers = approvers
	}
	return out, nil
}

// loadRequest reads a release request's facts: its requester is the
// author four-eyes counts against, and its approval requirement is what
// request_approval_as_required asks for and approvals_as_required
// counts to.
func (r *Runner) loadRequest(reader context.Context, s SubjectRef, out *loaded) error {
	if r.d.Releases == nil {
		return nil
	}
	f, err := r.d.Releases.Request(reader, s.Project, s.ID)
	switch {
	case errors.Is(err, ErrUnavailable):
		return nil // the request is gone: guards see nothing
	case err != nil:
		return fmt.Errorf("workflow: release request %s: %w", s.ID, err)
	}
	sub := &out.Subject
	sub.Author, sub.Required, sub.RequiredFrom = f.Requester, f.Required, f.From
	return nil
}

func (r *Runner) loadUnit(reader context.Context, s SubjectRef, out *loaded) error {
	if r.d.Translations != nil {
		f, err := r.d.Translations.Unit(reader, s.Project, s.ID, s.Locale)
		switch {
		case errors.Is(err, ErrUnavailable), errors.Is(err, ErrUnknownParty), errors.Is(err, ErrUnsupportedSubject),
			errors.Is(err, domain.ErrInvalidAssignment), errors.Is(err, domain.ErrInvalidApproval):
			// The message is gone: guards see an empty unit.
		case err != nil:
			return fmt.Errorf("workflow: translation unit %s/%s: %w", s.ID, s.Locale, err)
		default:
			out.Key = f.Key
			sub := &out.Subject
			sub.Namespace, sub.ReviewState, sub.Origin, sub.Author, sub.SourceRevision =
				f.Namespace, f.ReviewState, f.Origin, f.Author, f.SourceRevision
		}
	}
	if r.d.Findings != nil && out.Key != "" {
		fs, err := r.d.Findings.Open(reader, s.Project, s.ID, s.Locale, out.Key)
		if err != nil {
			return fmt.Errorf("workflow: findings of %s/%s: %w", s.ID, s.Locale, err)
		}
		out.Subject.Findings = fs
	}
	if r.d.Suggestions != nil {
		band, tm, err := r.d.Suggestions.Latest(reader, s.Project, s.ID, s.Locale)
		if err != nil {
			return fmt.Errorf("workflow: suggestions of %s/%s: %w", s.ID, s.Locale, err)
		}
		out.Subject.Band, out.Subject.TMMatch = band, tm
	}
	return nil
}

// run carries out a transition's effects in order, each through its
// owning context's port as the acting principal, so that context's own
// permission check decides (§2.5). The first refusal stops the run: the
// transition is then refused and the instance stays where it was.
// Anything else that fails is returned, and the whole step is retried
// by the outbox — every port call is idempotent on a key derived from
// the event, the instance and the action.
//
// Actions into Localization, Quality and Intelligence commit in their
// owning context's transaction, not in the step's — those services
// open their own and never join another context's — so a redelivery
// may repeat one, and each is idempotent (a review state already set
// is done; a fill carries an idempotency key; a check stores nothing).
// They are called on outer, the handler's context outside the step's
// transaction. Assignments and approval requests are Workflow's own:
// they join the step's transaction (txCtx) and commit with the
// transition or not at all.
func (r *Runner) run(
	txCtx, outer context.Context, act acting, effects []domain.Effect, inst domain.Instance, subject loaded, ev Event, to string,
) (outcomes []ActionOutcome, timer *domain.Timer, refused bool, err error) {
	for _, e := range effects {
		o := ActionOutcome{Action: e.Name}
		var (
			detail string
			due    domain.Duration
			err    error
		)
		switch {
		case !act.ok && e.Use != "notify" && decision(e):
			o.Outcome, o.Detail = ActionRefused, act.why
			return append(outcomes, o), nil, true, nil
		case !act.ok && e.Use != "notify":
			detail, due, err = r.asWorkflow(outer, txCtx, e, inst, subject, ev, act.why)
		default:
			detail, due, err = r.execute(act.in(outer), act.in(txCtx), e, inst, subject, ev)
			if refusedForPermission(err) && !decision(e) {
				detail, due, err = r.asWorkflow(outer, txCtx, e, inst, subject, ev, err.Error())
			}
		}
		switch {
		case err == nil:
			o.Outcome, o.Detail = ActionDone, detail
			if !due.IsZero() {
				timer = domain.NewTimer(to, r.now(), due)
			}
		case errors.Is(err, ErrRefused), errors.Is(err, authz.ErrForbidden), errors.Is(err, authz.ErrUnauthenticated):
			o.Outcome, o.Detail = ActionRefused, err.Error()
			return append(outcomes, o), nil, true, nil
		case errors.Is(err, ErrUnavailable), errors.Is(err, ErrUnknownParty), errors.Is(err, ErrUnsupportedSubject),
			errors.Is(err, domain.ErrInvalidAssignment), errors.Is(err, domain.ErrInvalidApproval),
			// An assignee at the limit of open assignments (§9.6): the
			// instance stays where it was, as for any work it could not
			// ask for, rather than the outbox retrying until a slot
			// frees.
			errors.Is(err, ErrLimit):
			o.Outcome, o.Detail = ActionFailed, err.Error()
			return append(outcomes, o), nil, true, nil
		default:
			return nil, nil, false, fmt.Errorf("workflow: action %s (%s): %w", e.Name, e.Use, err)
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, timer, false, nil
}

// execute carries out one effect. It returns a detail for the log and,
// for an assignment or approval request with a due period, the period.
//
// ctx is the actor outside any transaction, for the other contexts;
// txCtx is the actor on the step's transaction, for assignments.
func (r *Runner) execute(
	ctx, txCtx context.Context, e domain.Effect, inst domain.Instance, subject loaded, ev Event,
) (string, domain.Duration, error) {
	s := refOf(inst)
	key := fmt.Sprintf("workflow:%s:%s:%s", ev.ID, inst.ID, e.Name)
	switch p := e.Params.(type) {
	case domain.SetReviewState:
		if r.d.Translations == nil {
			return "", domain.Duration{}, unwired("Localization")
		}
		return "", domain.Duration{}, r.d.Translations.Review(ctx, s.Project, s.ID, s.Locale, p.State)
	case domain.RunCheck:
		if r.d.Findings == nil {
			return "", domain.Duration{}, unwired("Quality")
		}
		detail, err := r.d.Findings.Run(ctx, s.Project, p.Layers)
		return detail, domain.Duration{}, err
	case domain.RequestFill:
		if r.d.Suggestions == nil {
			return "", domain.Duration{}, unwired("Intelligence")
		}
		if subject.Key == "" {
			return "", domain.Duration{}, fmt.Errorf("%w: the message no longer exists", ErrUnavailable)
		}
		detail, err := r.d.Suggestions.Fill(ctx, s.Project, subject.Key, s.Locale, key)
		return detail, domain.Duration{}, err
	case domain.Assign:
		if r.d.Assignments == nil {
			return "", domain.Duration{}, unwired("assignments")
		}
		a, err := r.d.Assignments.AssignForInstance(txCtx, WorkflowAssign{
			InstanceID: inst.ID, ProjectID: s.Project, Subject: s.Kind,
			Units: []domain.Unit{{Message: s.ID, Locale: s.Locale}}, Params: p,
		})
		if err != nil {
			return "", domain.Duration{}, err
		}
		return "assignment " + a.ID.String(), p.Due, nil
	case domain.RequestApproval:
		if r.d.Assignments == nil {
			return "", domain.Duration{}, unwired("approvals")
		}
		a, err := r.d.Assignments.RequestApprovalForInstance(txCtx, WorkflowApproval{
			InstanceID: inst.ID, ProjectID: s.Project,
			Subject: domain.ApprovalSubject{Kind: s.Kind, ID: s.ID, Locale: s.Locale}, Params: p,
		})
		if err != nil {
			return "", domain.Duration{}, err
		}
		return "approval " + a.ID.String(), p.Due, nil
	case domain.RequestApprovalAsRequired:
		if r.d.Assignments == nil {
			return "", domain.Duration{}, unwired("approvals")
		}
		if subject.Subject.Required < 1 {
			return "", domain.Duration{}, fmt.Errorf("%w: the subject requires no approval", ErrUnavailable)
		}
		a, err := r.d.Assignments.RequestApprovalForInstance(txCtx, WorkflowApproval{
			InstanceID: inst.ID, ProjectID: s.Project,
			Subject: domain.ApprovalSubject{Kind: s.Kind, ID: s.ID, Locale: s.Locale},
			Params:  domain.RequestApproval{N: subject.Subject.Required, From: subject.Subject.RequiredFrom, Due: p.Due},
		})
		if err != nil {
			return "", domain.Duration{}, err
		}
		return "approval " + a.ID.String(), p.Due, nil
	case domain.DeployRelease:
		if r.d.Releases == nil {
			return "", domain.Duration{}, unwired("Release")
		}
		detail, err := r.d.Releases.Deploy(ctx, s.Project, s.ID)
		return detail, domain.Duration{}, err
	case domain.DenyRelease:
		if r.d.Releases == nil {
			return "", domain.Duration{}, unwired("Release")
		}
		return "", domain.Duration{}, r.d.Releases.Deny(ctx, s.Project, s.ID)
	case domain.Notify:
		// In-app notifications have no store yet and mail is not
		// configured (§2.4: email only when mail is configured): the
		// log entry is the notification.
		return "recorded; no notification channel is configured", domain.Duration{}, nil
	}
	return "", domain.Duration{}, fmt.Errorf("%w: %s is not an action the runner knows", ErrUnavailable, e.Use)
}

func unwired(what string) error {
	return fmt.Errorf("%w: %s is not wired in this deployment", ErrUnavailable, what)
}

// decision reports whether e is a decision: approving or rejecting
// text, or deploying or denying a release request (§5.1). Those run as
// the actor and only as the actor (§2.5) — a workflow cannot create a
// new way to approve text or to move production. Anything else decides
// nothing and may fall back to Workflow's own principal.
//
// It lists the decisions rather than the actions that may fall back,
// so a primitive added later is a decision until someone says it isn't
// — TestEveryPrimitiveIsClassified makes them say.
func decision(e domain.Effect) bool {
	switch p := e.Params.(type) {
	case domain.SetReviewState:
		return p.State == "approved" || p.State == "rejected"
	case domain.DeployRelease, domain.DenyRelease:
		return true
	}
	return !mayFallBack[e.Use]
}

// mayFallBack are the action primitives that decide nothing.
// set_review_state is here for needs_review and draft; decision()
// catches approved and rejected before this is read.
var mayFallBack = map[string]bool{
	"set_review_state": true, "request_approval": true, "request_approval_as_required": true,
	"assign": true, "run_check": true, "request_fill": true, "notify": true,
}

func refusedForPermission(err error) bool {
	return errors.Is(err, ErrRefused) || errors.Is(err, authz.ErrForbidden) || errors.Is(err, authz.ErrUnauthenticated)
}

// asWorkflow runs an action that decides nothing — sending back to
// review, asking for an approval, assigning work, running a check — as
// Workflow's own principal, because the actor whose event moved the
// instance cannot (RFC 0006 §2.5, amended in wave 3).
//
// It exists for the source change a CI token pushes: GitHub OIDC's push
// path holds only catalog permissions and resolves to no principal, so
// without it the default definition's re-review never happened and an
// outdated translation shipped as approved. Workflow's principal holds
// catalog.read, translations.read and translations.write and never
// review, and decisions never come here, so nothing it does can approve
// or reject text. The detail records that it ran and why.
func (r *Runner) asWorkflow(
	outer, txCtx context.Context, e domain.Effect, inst domain.Instance, subject loaded, ev Event, why string,
) (string, domain.Duration, error) {
	bg, err := authz.Background(outer, PrincipalRunner, runnerPermissions...)
	if err != nil {
		return "", domain.Duration{}, err
	}
	p, _ := authz.From(bg)
	detail, due, err := r.execute(bg, authz.WithPrincipal(txCtx, p), e, inst, subject, ev)
	if err != nil {
		return detail, due, err
	}
	note := "as " + PrincipalRunner + " (" + why + ")"
	if detail != "" {
		note = detail + "; " + note
	}
	return note, due, nil
}
