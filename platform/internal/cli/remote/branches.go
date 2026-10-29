package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Branch types as the Branches API serves them (RFC 0004 §4.1),
// re-exported so commands don't import the generated package.
type (
	Branch           = apiclient.Branch
	BranchStatus     = apiclient.BranchStatus
	BranchPushResult = apiclient.BranchPushResult
	BranchItemResult = apiclient.BranchItemResult
)

// MaxBranchItems is the server's limit for one branch push. A branch
// push is the branch's whole catalog, so it is never split: splitting it
// would make `complete` a lie.
const MaxBranchItems = 10000

// BranchPushInput is what CI pushes from a branch.
type BranchPushInput struct {
	Branch     string
	PR         *int
	HeadCommit string
	// Complete says the items are the branch's whole catalog.
	Complete bool
	Items    []MessageUpsertItem
}

// PushBranch pushes a branch's messages and returns its status report
// (glossa push --branch).
func (c *Client) PushBranch(ctx context.Context, s Scope, in BranchPushInput) (BranchPushResult, error) {
	body := apiclient.BranchPush{Branch: in.Branch, Items: in.Items, PrNumber: in.PR, Complete: &in.Complete}
	if in.HeadCommit != "" {
		body.HeadCommit = &in.HeadCommit
	}
	r, err := c.api.PushBranchMessagesWithResponse(ctx, s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/branch-pushes", s.Tenant, s.Project)); err != nil {
		return BranchPushResult{}, err
	}
	return *r.JSON200, nil
}

// Branch finds a branch by name; ok is false when the project has none
// of that name (branch names hold slashes, so URLs use the ID).
func (c *Client) Branch(ctx context.Context, s Scope, name string) (Branch, bool, error) {
	size := 1
	r, err := c.api.ListBranchesWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListBranchesParams{Name: &name, PageSize: &size})
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/branches", s.Tenant, s.Project)); err != nil {
		return Branch{}, false, err
	}
	if len(r.JSON200.Items) == 0 {
		return Branch{}, false, nil
	}
	return r.JSON200.Items[0], true, nil
}

// BranchStatus reads a branch's status report by its ID.
func (c *Client) BranchStatus(ctx context.Context, s Scope, id string) (BranchStatus, error) {
	r, err := c.api.GetBranchWithResponse(ctx, s.Tenant, s.Project, id)
	if err := check(r, err, http.MethodGet, c.branchPath(s, id, "")); err != nil {
		return BranchStatus{}, err
	}
	return *r.JSON200, nil
}

// CloseBranch closes an unmerged branch by its ID.
func (c *Client) CloseBranch(ctx context.Context, s Scope, id string) (Branch, error) {
	r, err := c.api.CloseBranchWithResponse(ctx, s.Tenant, s.Project, id)
	if err := check(r, err, http.MethodPost, c.branchPath(s, id, "/closure")); err != nil {
		return Branch{}, err
	}
	return *r.JSON200, nil
}

// UpsertBranch creates a branch, or records CI's head commit, pull
// request and preview URL for it.
func (c *Client) UpsertBranch(ctx context.Context, s Scope, name string, pr *int, headCommit string, previewURL *string) (Branch, error) {
	body := apiclient.UpsertBranch{Name: name, PrNumber: pr, PreviewUrl: previewURL}
	if headCommit != "" {
		body.HeadCommit = &headCommit
	}
	r, err := c.api.UpsertBranchWithResponse(ctx, s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/branches", s.Tenant, s.Project)); err != nil {
		return Branch{}, err
	}
	if r.JSON201 != nil {
		return *r.JSON201, nil
	}
	return *r.JSON200, nil
}

// SetBranchPreview records where CI deployed a branch's preview
// (glossa preview register --url).
func (c *Client) SetBranchPreview(ctx context.Context, s Scope, id, url string) (Branch, error) {
	r, err := c.api.SetBranchPreviewWithResponse(ctx, s.Tenant, s.Project, id, apiclient.BranchPreview{Url: url})
	if err := check(r, err, http.MethodPut, c.branchPath(s, id, "/preview")); err != nil {
		return Branch{}, err
	}
	return *r.JSON200, nil
}

func (c *Client) branchPath(s Scope, id, sub string) string {
	return c.path("/v1/tenants/%s/projects/%s/branches/%s", s.Tenant, s.Project, id) + sub
}
