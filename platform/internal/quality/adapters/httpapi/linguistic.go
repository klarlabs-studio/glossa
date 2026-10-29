package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Linguistic-QA jobs (RFC 0005 §3.8, §9). The one layer that needs a
// model is a job and never a check, so it has a resource of its own:
// create it, read it — which also brings a handed-over review up to
// date — and cancel one that has not finished.
//
// Nothing here can say what a linguistic finding is worth. The job's
// findings are sealed at `warning` by the domain and graded by the
// check policy, which clamps an advisory layer back to `warning`
// however strict the document is (RFC 0005 §14 decision 10). The edge
// only renders the job.

func toLinguisticJob(j domain.LinguisticJob) apiv1.LinguisticJob {
	out := apiv1.LinguisticJob{
		Id: j.ID.String(), Ref: j.Ref, State: apiv1.LinguisticJobState(j.State),
		Findings: j.Findings, SkippedSensitive: j.SkippedSensitive, Reviewed: j.Reviewed,
		CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
		Scope: apiv1.LinguisticScope{Locales: j.Scope.Locales},
	}
	if j.Scope.Namespace != "" {
		out.Scope.Namespace = apiconv.Ptr(j.Scope.Namespace)
	}
	if j.Scope.KeyPrefix != "" {
		out.Scope.KeyPrefix = apiconv.Ptr(j.Scope.KeyPrefix)
	}
	if len(j.Scope.Keys) > 0 {
		out.Scope.Keys = apiconv.Ptr(j.Scope.Keys)
	}
	if j.CheckRun != nil {
		out.CheckRun = apiconv.Ptr(j.CheckRun.String())
	}
	if j.FailureCode != "" {
		out.FailureCode = apiconv.Ptr(apiv1.LinguisticFailureCode(j.FailureCode))
	}
	if j.LastError != "" {
		out.FailureDetail = apiconv.Ptr(j.LastError)
	}
	if j.StartedAt != nil {
		out.StartedAt = apiconv.Ptr(*j.StartedAt)
	}
	if j.FinishedAt != nil {
		out.FinishedAt = apiconv.Ptr(*j.FinishedAt)
	}
	return out
}

func linguisticScope(in apiv1.LinguisticScope) domain.LinguisticScope {
	return domain.LinguisticScope{
		Locales:   in.Locales,
		Namespace: deref(in.Namespace),
		KeyPrefix: deref(in.KeyPrefix),
		Keys:      deref(in.Keys),
	}
}

// ListLinguisticJobs pages a project's jobs, newest first. It does not
// follow a running job: one page should not fan out into a poll per
// row, and a read of one job brings that job up to date.
func (a *API) ListLinguisticJobs(
	ctx context.Context, req apiv1.ListLinguisticJobsRequestObject,
) (apiv1.ListLinguisticJobsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	jobs, next, err := a.svc.ListLinguisticJobs(ctx, project, string(deref(req.Params.State)), page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListLinguisticJobs200JSONResponse{
		Items: make([]apiv1.LinguisticJob, len(jobs)), NextPageToken: next,
	}
	for i, j := range jobs {
		out.Items[i] = toLinguisticJob(j)
	}
	return out, nil
}

// CreateLinguisticJob asks for one review.
//
// A job refused by the tenant's consent, budget or `sensitive`
// namespaces is still a job: it comes back 201, `failed`, with the
// `failure_code` that says which — the same four codes Intelligence
// reports for the same refusals — because a refusal on the record is
// worth more than a 4xx nobody kept.
func (a *API) CreateLinguisticJob(
	ctx context.Context, req apiv1.CreateLinguisticJobRequestObject,
) (apiv1.CreateLinguisticJobResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	job, created, err := a.svc.RequestLinguisticReview(ctx, project, app.CreateLinguisticJob{
		Ref: req.Body.Ref, Scope: linguisticScope(req.Body.Scope),
		IdempotencyKey: deref(req.Params.IdempotencyKey),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if !created {
		return apiv1.CreateLinguisticJob200JSONResponse(toLinguisticJob(job)), nil
	}
	return apiv1.CreateLinguisticJob201JSONResponse{
		Body: toLinguisticJob(job),
		Headers: apiv1.CreateLinguisticJob201ResponseHeaders{
			Location: apiconv.Ptr(tenantPath(ctx, "/projects/"+project.String()+"/linguistic-jobs/"+job.ID.String())),
		},
	}, nil
}

// GetLinguisticJob reads one job, following a review that has been
// handed over and recording its findings once.
func (a *API) GetLinguisticJob(
	ctx context.Context, req apiv1.GetLinguisticJobRequestObject,
) (apiv1.GetLinguisticJobResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.LinguisticJob, app.ErrLinguisticJobNotFound)
	if err != nil {
		return nil, err
	}
	job, err := a.svc.GetLinguisticJob(ctx, project, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetLinguisticJob200JSONResponse(toLinguisticJob(job)), nil
}

// CancelLinguisticJob stops a job that has not finished. Findings a job
// already recorded stay: they are waived, never deleted.
func (a *API) CancelLinguisticJob(
	ctx context.Context, req apiv1.CancelLinguisticJobRequestObject,
) (apiv1.CancelLinguisticJobResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.LinguisticJob, app.ErrLinguisticJobNotFound)
	if err != nil {
		return nil, err
	}
	job, err := a.svc.CancelLinguisticJob(ctx, project, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CancelLinguisticJob200JSONResponse(toLinguisticJob(job)), nil
}
