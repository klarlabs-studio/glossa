package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// The workspace's own translation memory and termbase files: TMX and
// TBX imports and exports of the tenant, never a project's. The jobs
// they create are ordinary ones, followed, uploaded to, cancelled and
// downloaded through import-jobs and export-jobs.

func (a *API) createKnowledgeImport(ctx context.Context, k domain.Kind, b *apiv1.KnowledgeImportJobRequest, idemKey *string) (apiv1.ImportJob, apiv1.CreateImportJob201ResponseHeaders, error) {
	var req apiv1.KnowledgeImportJobRequest
	if b != nil {
		req = *b
	}
	j, replayed, err := a.svc.CreateKnowledgeImport(ctx, k, string(deref(req.Mode)), deref(req.FileName), deref(idemKey))
	if err != nil {
		return apiv1.ImportJob{}, apiv1.CreateImportJob201ResponseHeaders{}, mapError(err)
	}
	h := apiv1.CreateImportJob201ResponseHeaders{Location: apiconv.Ptr(tenantPath(ctx, "/import-jobs/"+j.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return toImportJob(ctx, j), h, nil
}

func (a *API) createKnowledgeExport(ctx context.Context, k domain.Kind, b *apiv1.KnowledgeExportJobRequest, idemKey *string) (apiv1.ExportJob, apiv1.CreateExportJob201ResponseHeaders, error) {
	var opts *apiv1.ExportOptions
	if b != nil {
		opts = b.Options
	}
	j, replayed, err := a.svc.CreateKnowledgeExport(ctx, k, fromExportOptions(opts), deref(idemKey))
	if err != nil {
		return apiv1.ExportJob{}, apiv1.CreateExportJob201ResponseHeaders{}, mapError(err)
	}
	h := apiv1.CreateExportJob201ResponseHeaders{Location: apiconv.Ptr(tenantPath(ctx, "/export-jobs/"+j.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return toExportJob(ctx, j), h, nil
}

// tenantJobs lists the tenant-wide jobs of one kind and direction.
func (a *API) tenantJobs(ctx context.Context, dir domain.Direction, k domain.Kind, size *int, token *string, state *apiv1.IntegrationJobState) ([]domain.Job, *string, error) {
	pg, err := pagination.Parse(size, token)
	if err != nil {
		return nil, nil, err
	}
	f := app.JobFilter{Direction: dir, TenantWide: true, Kind: &k}
	if state != nil {
		f.State = apiconv.Ptr(domain.State(*state))
	}
	jobs, next, err := a.svc.ListJobs(ctx, f, pg)
	if err != nil {
		return nil, nil, mapError(err)
	}
	return jobs, next, nil
}

func (a *API) importJobList(ctx context.Context, jobs []domain.Job, next *string) apiv1.ImportJobList {
	out := apiv1.ImportJobList{Items: make([]apiv1.ImportJob, len(jobs)), NextPageToken: next}
	for i, j := range jobs {
		out.Items[i] = toImportJob(ctx, j)
	}
	return out
}

func (a *API) exportJobList(ctx context.Context, jobs []domain.Job, next *string) apiv1.ExportJobList {
	out := apiv1.ExportJobList{Items: make([]apiv1.ExportJob, len(jobs)), NextPageToken: next}
	for i, j := range jobs {
		out.Items[i] = toExportJob(ctx, j)
	}
	return out
}

// ── translation memory ───────────────────────────────────────────────

func (a *API) CreateTMImportJob(ctx context.Context, req apiv1.CreateTMImportJobRequestObject) (apiv1.CreateTMImportJobResponseObject, error) {
	j, h, err := a.createKnowledgeImport(ctx, domain.KindTM, req.Body, req.Params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return apiv1.CreateTMImportJob201JSONResponse{Body: j, Headers: apiv1.CreateTMImportJob201ResponseHeaders(h)}, nil
}

func (a *API) ListTMImportJobs(ctx context.Context, req apiv1.ListTMImportJobsRequestObject) (apiv1.ListTMImportJobsResponseObject, error) {
	p := req.Params
	jobs, next, err := a.tenantJobs(ctx, domain.Import, domain.KindTM, p.PageSize, p.PageToken, p.State)
	if err != nil {
		return nil, err
	}
	return apiv1.ListTMImportJobs200JSONResponse(a.importJobList(ctx, jobs, next)), nil
}

func (a *API) CreateTMExportJob(ctx context.Context, req apiv1.CreateTMExportJobRequestObject) (apiv1.CreateTMExportJobResponseObject, error) {
	j, h, err := a.createKnowledgeExport(ctx, domain.KindTM, req.Body, req.Params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return apiv1.CreateTMExportJob201JSONResponse{Body: j, Headers: apiv1.CreateTMExportJob201ResponseHeaders(h)}, nil
}

func (a *API) ListTMExportJobs(ctx context.Context, req apiv1.ListTMExportJobsRequestObject) (apiv1.ListTMExportJobsResponseObject, error) {
	p := req.Params
	jobs, next, err := a.tenantJobs(ctx, domain.Export, domain.KindTM, p.PageSize, p.PageToken, p.State)
	if err != nil {
		return nil, err
	}
	return apiv1.ListTMExportJobs200JSONResponse(a.exportJobList(ctx, jobs, next)), nil
}

// ── termbase ─────────────────────────────────────────────────────────

func (a *API) CreateTermbaseImportJob(ctx context.Context, req apiv1.CreateTermbaseImportJobRequestObject) (apiv1.CreateTermbaseImportJobResponseObject, error) {
	j, h, err := a.createKnowledgeImport(ctx, domain.KindTermbase, req.Body, req.Params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return apiv1.CreateTermbaseImportJob201JSONResponse{Body: j, Headers: apiv1.CreateTermbaseImportJob201ResponseHeaders(h)}, nil
}

func (a *API) ListTermbaseImportJobs(ctx context.Context, req apiv1.ListTermbaseImportJobsRequestObject) (apiv1.ListTermbaseImportJobsResponseObject, error) {
	p := req.Params
	jobs, next, err := a.tenantJobs(ctx, domain.Import, domain.KindTermbase, p.PageSize, p.PageToken, p.State)
	if err != nil {
		return nil, err
	}
	return apiv1.ListTermbaseImportJobs200JSONResponse(a.importJobList(ctx, jobs, next)), nil
}

func (a *API) CreateTermbaseExportJob(ctx context.Context, req apiv1.CreateTermbaseExportJobRequestObject) (apiv1.CreateTermbaseExportJobResponseObject, error) {
	j, h, err := a.createKnowledgeExport(ctx, domain.KindTermbase, req.Body, req.Params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return apiv1.CreateTermbaseExportJob201JSONResponse{Body: j, Headers: apiv1.CreateTermbaseExportJob201ResponseHeaders(h)}, nil
}

func (a *API) ListTermbaseExportJobs(ctx context.Context, req apiv1.ListTermbaseExportJobsRequestObject) (apiv1.ListTermbaseExportJobsResponseObject, error) {
	p := req.Params
	jobs, next, err := a.tenantJobs(ctx, domain.Export, domain.KindTermbase, p.PageSize, p.PageToken, p.State)
	if err != nil {
		return nil, err
	}
	return apiv1.ListTermbaseExportJobs200JSONResponse(a.exportJobList(ctx, jobs, next)), nil
}
