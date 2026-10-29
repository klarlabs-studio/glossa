package sources

import (
	"context"
	"errors"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// Branches implements app.Branches on Catalog's service: a pull request
// opens, pushes to and closes a branch through the Branches API and its
// statekit lifecycle, never around it (RFC 0004 §4.1, §6.2).
type Branches struct{ svc *catalogapp.Service }

// NewBranches returns the port.
func NewBranches(svc *catalogapp.Service) *Branches { return &Branches{svc: svc} }

var _ app.Branches = (*Branches)(nil)

// UpsertBranch implements app.Branches.
func (b *Branches) UpsertBranch(ctx context.Context, project uuid.UUID, branch, headCommit string, pr *int) error {
	_, _, err := b.svc.UpsertBranch(ctx, catalogdomain.ProjectID(project), catalogapp.BranchUpsert{
		Branch: branch, PushInfo: catalogdomain.PushInfo{HeadCommit: headCommit, PR: pr},
	})
	return branchErr(err)
}

// CloseBranch implements app.Branches.
func (b *Branches) CloseBranch(ctx context.Context, project uuid.UUID, branch string) error {
	_, err := b.svc.CloseBranch(ctx, catalogdomain.ProjectID(project), branch)
	return branchErr(err)
}

// MergeBranch implements app.Branches.
func (b *Branches) MergeBranch(ctx context.Context, project uuid.UUID, branch string) error {
	_, err := b.svc.MergeBranch(ctx, catalogdomain.ProjectID(project), branch)
	return branchErr(err)
}

// ApplicationExists implements app.Branches.
func (b *Branches) ApplicationExists(ctx context.Context, project, application uuid.UUID) (bool, error) {
	_, err := b.svc.GetApplication(ctx, catalogdomain.ProjectID(project), catalogdomain.ApplicationID(application))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, catalogapp.ErrNotFound):
		return false, nil
	}
	return false, err
}

// branchErr softens what a webhook could never fix by retrying: a
// branch already where the event would put it, a merged branch taking
// another push, a project or branch that has gone, a head ref GitHub
// allows and Catalog does not. Correctness never depends on the webhook
// — the default branch's push is what activates messages (RFC 0004
// §4.1) — so the delivery is done either way, and only a real outage is
// worth a retry.
func branchErr(err error) error {
	switch {
	case err == nil,
		errors.Is(err, catalogapp.ErrNotFound),
		errors.Is(err, catalogdomain.ErrBranchMerged),
		errors.Is(err, catalogdomain.ErrInvalidBranchName),
		errors.Is(err, catalogdomain.ErrInvalidPush):
		return nil
	}
	return err
}
