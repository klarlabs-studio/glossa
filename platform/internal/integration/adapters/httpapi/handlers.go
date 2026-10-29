package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
)

// ── imports ──────────────────────────────────────────────────────────

func (a *API) CreateImportJob(ctx context.Context, req apiv1.CreateImportJobRequestObject) (apiv1.CreateImportJobResponseObject, error) {
	b := req.Body
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	j, replayed, err := a.svc.CreateImport(ctx, app.ImportRequest{
		ProjectID: project, Format: string(b.Format), Mode: string(deref(b.Mode)), FileName: deref(b.FileName),
		Options: fromImportOptions(b.Options),
	}, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateImportJob201ResponseHeaders{Location: apiconv.Ptr(tenantPath(ctx, "/import-jobs/"+j.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateImportJob201JSONResponse{Body: toImportJob(ctx, j), Headers: h}, nil
}

func (a *API) ListImportJobs(ctx context.Context, req apiv1.ListImportJobsRequestObject) (apiv1.ListImportJobsResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.JobFilter{Direction: domain.Import}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	if p.State != nil {
		f.State = apiconv.Ptr(domain.State(*p.State))
	}
	jobs, next, err := a.svc.ListJobs(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListImportJobs200JSONResponse{Items: make([]apiv1.ImportJob, len(jobs)), NextPageToken: next}
	for i, j := range jobs {
		out.Items[i] = toImportJob(ctx, j)
	}
	return out, nil
}

func (a *API) GetImportJob(ctx context.Context, req apiv1.GetImportJobRequestObject) (apiv1.GetImportJobResponseObject, error) {
	id, err := pathID(req.ImportJob)
	if err != nil {
		return nil, err
	}
	j, err := a.svc.GetImport(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetImportJob200JSONResponse(toImportJob(ctx, j)), nil
}

func (a *API) UploadImportFile(ctx context.Context, req apiv1.UploadImportFileRequestObject) (apiv1.UploadImportFileResponseObject, error) {
	id, err := pathID(req.ImportJob)
	if err != nil {
		return nil, err
	}
	j, err := a.svc.UploadImport(ctx, id, req.Body)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UploadImportFile200JSONResponse(toImportJob(ctx, j)), nil
}

func (a *API) CancelImportJob(ctx context.Context, req apiv1.CancelImportJobRequestObject) (apiv1.CancelImportJobResponseObject, error) {
	id, err := pathID(req.ImportJob)
	if err != nil {
		return nil, err
	}
	j, err := a.svc.Cancel(ctx, id, domain.Import)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CancelImportJob200JSONResponse(toImportJob(ctx, j)), nil
}

func (a *API) ListImportResults(ctx context.Context, req apiv1.ListImportResultsRequestObject) (apiv1.ListImportResultsResponseObject, error) {
	id, err := pathID(req.ImportJob)
	if err != nil {
		return nil, err
	}
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	var f app.ItemFilter
	if p.Status != nil {
		f.Status = apiconv.Ptr(domain.ItemStatus(*p.Status))
	}
	if p.Kind != nil {
		f.Kind = apiconv.Ptr(domain.ItemKind(*p.Kind))
	}
	items, next, err := a.svc.ImportResults(ctx, id, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListImportResults200JSONResponse{Items: make([]apiv1.ImportResult, len(items)), NextPageToken: next}
	for i, it := range items {
		out.Items[i] = toResult(it)
	}
	return out, nil
}

// ── exports ──────────────────────────────────────────────────────────

func (a *API) CreateExportJob(ctx context.Context, req apiv1.CreateExportJobRequestObject) (apiv1.CreateExportJobResponseObject, error) {
	b := req.Body
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	j, replayed, err := a.svc.CreateExport(ctx, app.ExportRequest{
		ProjectID: project, Format: string(b.Format), Options: fromExportOptions(b.Options),
	}, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateExportJob201ResponseHeaders{Location: apiconv.Ptr(tenantPath(ctx, "/export-jobs/"+j.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateExportJob201JSONResponse{Body: toExportJob(ctx, j), Headers: h}, nil
}

func (a *API) ListExportJobs(ctx context.Context, req apiv1.ListExportJobsRequestObject) (apiv1.ListExportJobsResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.JobFilter{Direction: domain.Export}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	if p.State != nil {
		f.State = apiconv.Ptr(domain.State(*p.State))
	}
	jobs, next, err := a.svc.ListJobs(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListExportJobs200JSONResponse{Items: make([]apiv1.ExportJob, len(jobs)), NextPageToken: next}
	for i, j := range jobs {
		out.Items[i] = toExportJob(ctx, j)
	}
	return out, nil
}

func (a *API) GetExportJob(ctx context.Context, req apiv1.GetExportJobRequestObject) (apiv1.GetExportJobResponseObject, error) {
	id, err := pathID(req.ExportJob)
	if err != nil {
		return nil, err
	}
	j, err := a.svc.GetExport(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetExportJob200JSONResponse(toExportJob(ctx, j)), nil
}

func (a *API) CancelExportJob(ctx context.Context, req apiv1.CancelExportJobRequestObject) (apiv1.CancelExportJobResponseObject, error) {
	id, err := pathID(req.ExportJob)
	if err != nil {
		return nil, err
	}
	j, err := a.svc.Cancel(ctx, id, domain.Export)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CancelExportJob200JSONResponse(toExportJob(ctx, j)), nil
}

func (a *API) DownloadExportFile(ctx context.Context, req apiv1.DownloadExportFileRequestObject) (apiv1.DownloadExportFileResponseObject, error) {
	id, err := pathID(req.ExportJob)
	if err != nil {
		return nil, err
	}
	rc, j, err := a.svc.OpenExport(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.DownloadExportFile200ApplicationoctetStreamResponse{
		Body: rc, ContentLength: j.File.Size,
		Headers: apiv1.DownloadExportFile200ResponseHeaders{
			ContentDisposition: apiconv.Ptr(attachment(j.FileName)), ETag: apiconv.Ptr(etag(j.File.SHA256)),
		},
	}, nil
}
