package httpapi

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// The Branches API (RFC 0004 §4.1, §9). A branch name may hold slashes,
// so every URL addresses a branch by its ID; the name travels in bodies
// and in the list's name filter.

func branchID(s string) (domain.BranchID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return domain.BranchID{}, mapError(app.ErrNotFound)
	}
	return domain.BranchID(id), nil
}

func toBranch(b domain.Branch) apiv1.Branch {
	out := apiv1.Branch{
		Id: b.ID.String(), Name: string(b.Name), State: apiv1.BranchState(b.State), PrNumber: b.PR,
		ClosedAt: b.ClosedAt, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
	if b.HeadCommit != "" {
		out.HeadCommit = apiconv.Ptr(b.HeadCommit)
	}
	if b.PreviewURL != "" {
		out.PreviewUrl = apiconv.Ptr(b.PreviewURL)
	}
	return out
}

func toKeys(ks []domain.MessageKey) []apiv1.MessageKey {
	out := make([]apiv1.MessageKey, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return out
}

func toStatus(rep app.BranchReport) apiv1.BranchStatus {
	out := apiv1.BranchStatus{
		Branch: toBranch(rep.Branch), NewKeys: toKeys(rep.NewKeys), SourceProposals: toKeys(rep.SourceProposals),
		Removed: toKeys(rep.Removed), Conflicts: make([]apiv1.KeyConflict, len(rep.Conflicts)),
		Outdated: rep.Outdated,
	}
	for i, c := range rep.Conflicts {
		names := make([]apiv1.BranchName, len(c.Branches))
		for j, n := range c.Branches {
			names[j] = string(n)
		}
		out.Conflicts[i] = apiv1.KeyConflict{Key: string(c.Key), Branches: names}
	}
	if out.Outdated == nil {
		out.Outdated = map[string]int{}
	}
	return out
}

func (a *API) ListBranches(ctx context.Context, req apiv1.ListBranchesRequestObject) (apiv1.ListBranchesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.BranchFilter{Name: str(req.Params.Name)}
	if req.Params.State != nil {
		f.State = domain.BranchState(*req.Params.State)
	}
	bs, next, err := a.svc.ListBranches(ctx, project, f, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListBranches200JSONResponse{Items: make([]apiv1.Branch, len(bs)), NextPageToken: next}
	for i, b := range bs {
		out.Items[i] = toBranch(b)
	}
	return out, nil
}

func (a *API) UpsertBranch(ctx context.Context, req apiv1.UpsertBranchRequestObject) (apiv1.UpsertBranchResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	in := app.BranchUpsert{
		Branch:     req.Body.Name,
		PushInfo:   domain.PushInfo{HeadCommit: str(req.Body.HeadCommit), PR: req.Body.PrNumber},
		PreviewURL: req.Body.PreviewUrl,
	}
	b, created, err := a.svc.UpsertBranch(ctx, project, in)
	if err != nil {
		return nil, mapError(err)
	}
	if !created {
		return apiv1.UpsertBranch200JSONResponse(toBranch(b)), nil
	}
	return apiv1.UpsertBranch201JSONResponse{
		Body: toBranch(b),
		Headers: apiv1.UpsertBranch201ResponseHeaders{
			Location: apiconv.Ptr(projectPath(ctx, project, "/branches/"+b.ID.String())),
		},
	}, nil
}

func (a *API) PushBranchMessages(ctx context.Context, req apiv1.PushBranchMessagesRequestObject) (apiv1.PushBranchMessagesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	in := app.BranchPush{
		Branch:   req.Body.Branch,
		PushInfo: domain.PushInfo{HeadCommit: str(req.Body.HeadCommit), PR: req.Body.PrNumber},
		Items:    make([]app.UpsertItem, len(req.Body.Items)),
	}
	if req.Body.Complete != nil {
		in.Complete = *req.Body.Complete
	}
	for i, it := range req.Body.Items {
		in.Items[i] = app.UpsertItem{
			Key: it.Key, Text: it.Text, Syntax: syntax(it.Syntax), Namespace: it.Namespace,
			Description: it.Description, MaxLength: it.MaxLength,
		}
	}
	rep, err := a.svc.PushBranch(ctx, project, in)
	if err != nil {
		return nil, mapError(err)
	}
	status := toStatus(rep)
	out := apiv1.PushBranchMessages200JSONResponse{
		Branch: status.Branch, NewKeys: status.NewKeys, SourceProposals: status.SourceProposals,
		Removed: status.Removed, Conflicts: status.Conflicts, Outdated: status.Outdated,
		Items: make([]apiv1.BranchItemResult, len(rep.Items)),
	}
	for i, it := range rep.Items {
		res := apiv1.BranchItemResult{Key: it.Key, Status: apiv1.BranchItemResultStatus(it.Status)}
		if it.Message != nil {
			res.Message = apiconv.Ptr(toMessage(*it.Message))
		}
		if it.Error != nil {
			res.Error = &apiv1.ItemError{Code: it.Error.Code, Detail: it.Error.Detail}
		}
		out.Items[i] = res
	}
	return out, nil
}

func (a *API) GetBranch(ctx context.Context, req apiv1.GetBranchRequestObject) (apiv1.GetBranchResponseObject, error) {
	project, b, err := a.branch(ctx, req.Project, req.Branch)
	if err != nil {
		return nil, err
	}
	rep, err := a.svc.BranchStatus(ctx, project, string(b.Name))
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetBranch200JSONResponse(toStatus(rep)), nil
}

func (a *API) ListBranchProposals(ctx context.Context, req apiv1.ListBranchProposalsRequestObject) (apiv1.ListBranchProposalsResponseObject, error) {
	project, b, err := a.branch(ctx, req.Project, req.Branch)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ps, next, err := a.svc.ListProposals(ctx, project, string(b.Name), page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListBranchProposals200JSONResponse{Items: make([]apiv1.Proposal, len(ps)), NextPageToken: next}
	for i, p := range ps {
		item := apiv1.Proposal{
			Key: string(p.Key), Kind: apiv1.ProposalKind(p.Kind), MessageId: p.MessageID.String(),
			Source: apiconv.Content(p.Source), Author: apiconv.Ptr(string(p.Author)),
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		if p.BaseRevision > 0 {
			item.BaseRevision = apiconv.Ptr(p.BaseRevision)
		}
		out.Items[i] = item
	}
	return out, nil
}

func (a *API) CloseBranch(ctx context.Context, req apiv1.CloseBranchRequestObject) (apiv1.CloseBranchResponseObject, error) {
	project, b, err := a.branch(ctx, req.Project, req.Branch)
	if err != nil {
		return nil, err
	}
	closed, err := a.svc.CloseBranch(ctx, project, string(b.Name))
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CloseBranch200JSONResponse(toBranch(closed)), nil
}

func (a *API) MergeBranch(ctx context.Context, req apiv1.MergeBranchRequestObject) (apiv1.MergeBranchResponseObject, error) {
	project, b, err := a.branch(ctx, req.Project, req.Branch)
	if err != nil {
		return nil, err
	}
	merged, err := a.svc.MergeBranch(ctx, project, string(b.Name))
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.MergeBranch200JSONResponse(toBranch(merged)), nil
}

func (a *API) SetBranchPreview(ctx context.Context, req apiv1.SetBranchPreviewRequestObject) (apiv1.SetBranchPreviewResponseObject, error) {
	project, b, err := a.branch(ctx, req.Project, req.Branch)
	if err != nil {
		return nil, err
	}
	updated, err := a.svc.ReportBranchPreview(ctx, project, string(b.Name), req.Body.Url)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.SetBranchPreview200JSONResponse(toBranch(updated)), nil
}

// branch resolves the path's project and branch ID; the use cases take
// the branch by name, which is what a push and the webhooks know.
func (a *API) branch(ctx context.Context, project, branch string) (domain.ProjectID, domain.Branch, error) {
	p, err := projectID(project)
	if err != nil {
		return domain.ProjectID{}, domain.Branch{}, err
	}
	id, err := branchID(branch)
	if err != nil {
		return domain.ProjectID{}, domain.Branch{}, err
	}
	b, err := a.svc.GetBranch(ctx, p, id)
	if err != nil {
		return domain.ProjectID{}, domain.Branch{}, mapError(err)
	}
	return p, b, nil
}
