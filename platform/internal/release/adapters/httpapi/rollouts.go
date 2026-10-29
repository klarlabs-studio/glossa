package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Staged rollouts (RFC 0006 §5.2): start, read, advance, complete and
// abort. A rollout is addressed under its environment; one of another
// environment is not found there. Its version is its ETag.

func toRollout(r domain.Rollout) apiv1.Rollout {
	out := apiv1.Rollout{
		Id: r.ID.String(), Environment: r.Environment, ReleaseId: r.Candidate.String(), StableReleaseId: r.Stable.String(),
		Percent: r.Percent, Status: apiv1.RolloutStatus(r.Status), MaxDurationSeconds: int(r.MaxDuration / time.Second),
		ExpiresAt: r.ExpiresAt, Forced: r.Override.Forced, StartedBy: r.StartedBy, StartedAt: r.StartedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Override.Forced {
		out.ForceReason = apiconv.Ptr(r.Override.Reason)
	}
	if !r.Active() {
		out.End = apiconv.Ptr(apiv1.RolloutEnd(r.End))
		out.EndedBy = apiconv.Ptr(r.EndedBy)
		out.EndedAt = apiconv.Ptr(r.EndedAt)
	}
	return out
}

func rolloutPath(ctx context.Context, r domain.Rollout) string {
	return projectPath(ctx, r.ProjectID, "/environments/"+r.Environment+"/rollouts/"+r.ID.String())
}

// rolloutIn reads the path's project and rollout ids.
func rolloutIn(project, rollout string) (uuid.UUID, uuid.UUID, error) {
	p, err := parseID(project)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := parseID(rollout)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return p, id, nil
}

func (a *API) ListRollouts(ctx context.Context, req apiv1.ListRolloutsRequestObject) (apiv1.ListRolloutsResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	rows, next, err := a.svc.ListRollouts(ctx, project, req.Environment, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListRollouts200JSONResponse{Items: make([]apiv1.Rollout, len(rows)), NextPageToken: next}
	for i, r := range rows {
		out.Items[i] = toRollout(r)
	}
	return out, nil
}

func (a *API) StartRollout(ctx context.Context, req apiv1.StartRolloutRequestObject) (apiv1.StartRolloutResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	release, err := uuid.Parse(req.Body.ReleaseId)
	if err != nil {
		return nil, mapError(app.ErrReleaseNotInProject)
	}
	in := app.RolloutInput{Release: release, Percent: req.Body.Percent, Force: req.Body.Force != nil && *req.Body.Force}
	if req.Body.ForceReason != nil {
		in.ForceReason = *req.Body.ForceReason
	}
	if req.Body.MaxDurationSeconds != nil {
		// Zero is not "the default" on the wire: omitting it is. A zero
		// sent is a duration, and the domain refuses it as one.
		in.MaxDuration = time.Duration(*req.Body.MaxDurationSeconds) * time.Second
		if *req.Body.MaxDurationSeconds <= 0 {
			return nil, mapError(domain.ErrInvalidMaxDuration)
		}
	}
	var key string
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	ro, replayed, err := a.svc.StartRollout(ctx, project, req.Environment, in, key)
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.StartRollout201ResponseHeaders{ETag: apiconv.ETag(ro.Version), Location: apiconv.Ptr(rolloutPath(ctx, ro))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.StartRollout201JSONResponse{Body: toRollout(ro), Headers: h}, nil
}

func (a *API) GetRollout(ctx context.Context, req apiv1.GetRolloutRequestObject) (apiv1.GetRolloutResponseObject, error) {
	project, id, err := rolloutIn(req.Project, req.Rollout)
	if err != nil {
		return nil, err
	}
	ro, err := a.svc.GetRollout(ctx, project, id)
	if err == nil && ro.Environment != req.Environment {
		err = app.ErrNotFound // another environment's rollout is not here
	}
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetRollout200JSONResponse{Body: toRollout(ro), Headers: apiv1.GetRollout200ResponseHeaders{ETag: apiconv.ETag(ro.Version)}}, nil
}

func (a *API) AdvanceRollout(ctx context.Context, req apiv1.AdvanceRolloutRequestObject) (apiv1.AdvanceRolloutResponseObject, error) {
	project, id, err := rolloutIn(req.Project, req.Rollout)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	ro, err := a.svc.AdvanceRollout(ctx, project, req.Environment, id, req.Body.Percent, &ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.AdvanceRollout200JSONResponse{Body: toRollout(ro), Headers: apiv1.AdvanceRollout200ResponseHeaders{ETag: apiconv.ETag(ro.Version)}}, nil
}

func (a *API) CompleteRollout(ctx context.Context, req apiv1.CompleteRolloutRequestObject) (apiv1.CompleteRolloutResponseObject, error) {
	project, id, err := rolloutIn(req.Project, req.Rollout)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	ro, err := a.svc.CompleteRollout(ctx, project, req.Environment, id, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CompleteRollout200JSONResponse{Body: toRollout(ro), Headers: apiv1.CompleteRollout200ResponseHeaders{ETag: apiconv.ETag(ro.Version)}}, nil
}

func (a *API) AbortRollout(ctx context.Context, req apiv1.AbortRolloutRequestObject) (apiv1.AbortRolloutResponseObject, error) {
	project, id, err := rolloutIn(req.Project, req.Rollout)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	ro, err := a.svc.AbortRollout(ctx, project, req.Environment, id, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.AbortRollout200JSONResponse{Body: toRollout(ro), Headers: apiv1.AbortRollout200ResponseHeaders{ETag: apiconv.ETag(ro.Version)}}, nil
}
