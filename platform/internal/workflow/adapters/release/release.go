// Package release adapts Release to Workflow and Workflow's approvals to
// Release, for release requests (RFC 0006 §5.1).
//
// Requests is Workflow's port onto Release: the facts of a request
// (where, who asked, what approval it needs), and the deploy and denial
// a definition's deploy_release and deny_release carry out — through
// Release's application service as the principal on the context, so
// Release's own checks decide. Ledger is Release's port onto Workflow:
// who has granted the approval of a request, which Release counts
// against the environment's requirement itself before it moves a
// pointer. Release imports neither; this package depends on both
// (RFC 0006 §2.1, internal/workflow/architecture_test.go).
package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	releasedomain "go.klarlabs.de/glossa/platform/internal/release/domain"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Requests implements app.ReleaseRequests over Release's service.
type Requests struct{ release *releaseapp.Service }

// NewRequests returns the adapter.
func NewRequests(r *releaseapp.Service) *Requests { return &Requests{release: r} }

var _ app.ReleaseRequests = (*Requests)(nil)

// Request implements app.ReleaseRequests.
func (r *Requests) Request(ctx context.Context, project, id uuid.UUID) (app.ReleaseRequestFacts, error) {
	req, err := r.release.GetReleaseRequest(ctx, project, id)
	if errors.Is(err, releaseapp.ErrNotFound) {
		return app.ReleaseRequestFacts{}, fmt.Errorf("%w: release request %s", app.ErrUnavailable, id)
	}
	if err != nil {
		return app.ReleaseRequestFacts{}, err
	}
	from := req.Approval.From
	return app.ReleaseRequestFacts{
		Environment: req.Environment, Requester: req.Requester, State: string(req.State),
		Required: req.Approval.N, From: domain.Party{Member: from.Member, Role: from.Role, Group: from.Group},
	}, nil
}

// Deploy implements app.ReleaseRequests.
func (r *Requests) Deploy(ctx context.Context, project, id uuid.UUID) (string, error) {
	req, err := r.release.DeployRequest(ctx, project, id)
	if err != nil {
		return "", refusal(err)
	}
	return fmt.Sprintf("deployed release %s to %s", req.ReleaseID, req.Environment), nil
}

// Deny implements app.ReleaseRequests.
func (r *Requests) Deny(ctx context.Context, project, id uuid.UUID) error {
	_, err := r.release.DenyRequest(ctx, project, id)
	return refusal(err)
}

// refusal maps Release's answers to the runner's outcomes: what Release
// refuses to do leaves the instance where it was (ErrRefused), a
// request that is gone cannot be acted on (ErrUnavailable), and a
// permission refusal passes through as authz.ErrForbidden. Anything
// else is retried.
func refusal(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, releasedomain.ErrPolicyNotMet), errors.Is(err, releasedomain.ErrIneligible),
		errors.Is(err, releasedomain.ErrApprovalNotMet), errors.Is(err, releasedomain.ErrRequestClosed),
		errors.Is(err, releaseapp.ErrApprovalsUnavailable):
		return fmt.Errorf("%w: %w", app.ErrRefused, err)
	case errors.Is(err, releaseapp.ErrNotFound), errors.Is(err, releaseapp.ErrReleaseNotInProject):
		return fmt.Errorf("%w: %w", app.ErrUnavailable, err)
	}
	return err
}

// Ledger implements Release's Approvals port over Workflow's approvals.
type Ledger struct{ work *app.WorkService }

// NewLedger returns the adapter.
func NewLedger(w *app.WorkService) *Ledger { return &Ledger{work: w} }

var _ releaseapp.Approvals = (*Ledger)(nil)

// Granters implements releaseapp.Approvals: every grant on the
// request's current approval, as the principal on ctx. Release counts
// them; a denied approval answers none.
func (l *Ledger) Granters(ctx context.Context, project, request uuid.UUID) ([]string, error) {
	return l.work.Approvers(ctx, project, domain.ApprovalSubject{Kind: domain.SubjectReleaseRequest, ID: request})
}
