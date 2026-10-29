package remote

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Integration types the CLI reads (RFC 0003 §5–§6), re-exported so
// commands don't import the generated package.
type (
	ImportJob         = apiclient.ImportJob
	ImportJobRequest  = apiclient.ImportJobRequest
	ImportOptions     = apiclient.ImportOptions
	ImportResult      = apiclient.ImportResult
	ImportSummary     = apiclient.ImportSummary
	ImportCounts      = apiclient.ImportCounts
	ImportMode        = apiclient.ImportMode
	ExportJob         = apiclient.ExportJob
	ExportJobRequest  = apiclient.ExportJobRequest
	ExportOptions     = apiclient.ExportOptions
	IntegrationFile   = apiclient.IntegrationFile
	IntegrationFormat = apiclient.IntegrationFormat
	ExportLayout      = apiclient.ExportOptionsLayout
)

// IntegrationJobFilter narrows ImportJobs and ExportJobs.
type IntegrationJobFilter struct {
	// Project is a project ID; empty lists every job of the tenant,
	// tenant-wide TMX and TBX jobs included.
	Project string
	State   string
}

// CreateImportJob creates an import job waiting for its file. The
// Idempotency-Key makes a retried request create it once.
func (c *Client) CreateImportJob(ctx context.Context, tenant string, body ImportJobRequest, idempotencyKey string) (ImportJob, error) {
	r, err := c.api.CreateImportJobWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateImportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/import-jobs")); err != nil {
		return ImportJob{}, err
	}
	return *r.JSON201, nil
}

// ImportJob reads an import job.
func (c *Client) ImportJob(ctx context.Context, tenant, id string) (ImportJob, error) {
	r, err := c.api.GetImportJobWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/import-jobs/%s", id)); err != nil {
		return ImportJob{}, err
	}
	return *r.JSON200, nil
}

// ImportJobs lists import jobs, newest first, at most limit (0: all).
func (c *Client) ImportJobs(ctx context.Context, tenant string, f IntegrationJobFilter, limit int) ([]ImportJob, error) {
	params := apiclient.ListImportJobsParams{Project: optional(f.Project)}
	if f.State != "" {
		st := apiclient.IntegrationJobState(f.State)
		params.State = &st
	}
	return limited(limit, func(size int, tok *string) ([]ImportJob, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListImportJobsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/import-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// CancelImportJob cancels an import job (a running one stops after its
// current batch).
func (c *Client) CancelImportJob(ctx context.Context, tenant, id string) (ImportJob, error) {
	r, err := c.api.CancelImportJobWithResponse(idempotent(ctx), tenant, id)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/import-jobs/%s/cancellation", id)); err != nil {
		return ImportJob{}, err
	}
	return *r.JSON200, nil
}

// ImportResults lists an import's results in file order, only those
// with status when it isn't empty.
func (c *Client) ImportResults(ctx context.Context, tenant, id, status string) ([]ImportResult, error) {
	params := apiclient.ListImportResultsParams{}
	if status != "" {
		st := apiclient.ImportResultStatus(status)
		params.Status = &st
	}
	return limited(0, func(size int, tok *string) ([]ImportResult, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListImportResultsWithResponse(ctx, tenant, id, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/import-jobs/%s/results", id)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// UploadImportFile PUTs size bytes of body to the job's upload URL
// (upload_url; relative to the server). It isn't retried: the body is
// a stream, and the server takes an upload once.
func (c *Client) UploadImportFile(ctx context.Context, uploadURL string, body io.Reader, size int64) (ImportJob, error) {
	req, err := c.transferRequest(ctx, http.MethodPut, uploadURL, body)
	if err != nil {
		return ImportJob{}, err
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.transfer.Do(req)
	if err != nil {
		return ImportJob{}, &APIError{Method: req.Method, URL: req.URL.String(), Err: err}
	}
	r, err := apiclient.ParseUploadImportFileResponse(resp)
	if err := check(r, err, req.Method, req.URL.String()); err != nil {
		return ImportJob{}, err
	}
	return *r.JSON200, nil
}

// CreateExportJob creates an export job; the server queues it at once.
func (c *Client) CreateExportJob(ctx context.Context, tenant string, body ExportJobRequest, idempotencyKey string) (ExportJob, error) {
	r, err := c.api.CreateExportJobWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateExportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/export-jobs")); err != nil {
		return ExportJob{}, err
	}
	return *r.JSON201, nil
}

// ExportJob reads an export job.
func (c *Client) ExportJob(ctx context.Context, tenant, id string) (ExportJob, error) {
	r, err := c.api.GetExportJobWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/export-jobs/%s", id)); err != nil {
		return ExportJob{}, err
	}
	return *r.JSON200, nil
}

// ExportJobs lists export jobs, newest first, at most limit (0: all).
func (c *Client) ExportJobs(ctx context.Context, tenant string, f IntegrationJobFilter, limit int) ([]ExportJob, error) {
	params := apiclient.ListExportJobsParams{Project: optional(f.Project)}
	if f.State != "" {
		st := apiclient.IntegrationJobState(f.State)
		params.State = &st
	}
	return limited(limit, func(size int, tok *string) ([]ExportJob, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListExportJobsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/export-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// The workspace's own translation memory (TMX) and termbase (TBX):
// tenant-wide jobs on routes of their own, never a project's.

// KnowledgeImportJobRequest is a tenant-wide TMX or TBX import.
type KnowledgeImportJobRequest = apiclient.KnowledgeImportJobRequest

// CreateKnowledgeImportJob creates a tenant-wide TMX (format tmx) or
// TBX (tbx) import waiting for its file (tm-import-jobs,
// termbase-import-jobs).
func (c *Client) CreateKnowledgeImportJob(ctx context.Context, tenant, format string, body KnowledgeImportJobRequest, idempotencyKey string) (ImportJob, error) {
	switch format {
	case "tmx":
		r, err := c.api.CreateTMImportJobWithResponse(idempotent(ctx), tenant,
			&apiclient.CreateTMImportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
		if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/tm-import-jobs")); err != nil {
			return ImportJob{}, err
		}
		return *r.JSON201, nil
	case "tbx":
		r, err := c.api.CreateTermbaseImportJobWithResponse(idempotent(ctx), tenant,
			&apiclient.CreateTermbaseImportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
		if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/termbase-import-jobs")); err != nil {
			return ImportJob{}, err
		}
		return *r.JSON201, nil
	}
	return ImportJob{}, fmt.Errorf("remote: %q is not a translation memory or termbase format", format)
}

// CreateKnowledgeExportJob queues a tenant-wide TMX or TBX export
// (tm-export-jobs, termbase-export-jobs).
func (c *Client) CreateKnowledgeExportJob(ctx context.Context, tenant, format string, opts *ExportOptions, idempotencyKey string) (ExportJob, error) {
	body := apiclient.KnowledgeExportJobRequest{Options: opts}
	switch format {
	case "tmx":
		r, err := c.api.CreateTMExportJobWithResponse(idempotent(ctx), tenant,
			&apiclient.CreateTMExportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
		if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/tm-export-jobs")); err != nil {
			return ExportJob{}, err
		}
		return *r.JSON201, nil
	case "tbx":
		r, err := c.api.CreateTermbaseExportJobWithResponse(idempotent(ctx), tenant,
			&apiclient.CreateTermbaseExportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
		if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/termbase-export-jobs")); err != nil {
			return ExportJob{}, err
		}
		return *r.JSON201, nil
	}
	return ExportJob{}, fmt.Errorf("remote: %q is not a translation memory or termbase format", format)
}

// WorkspaceImportJobs lists the tenant-wide TMX and TBX imports, newest
// first, at most limit of each (0: all).
func (c *Client) WorkspaceImportJobs(ctx context.Context, tenant, state string, limit int) ([]ImportJob, error) {
	st := jobState(state)
	tm, err := limited(limit, func(size int, tok *string) ([]ImportJob, *string, error) {
		r, err := c.api.ListTMImportJobsWithResponse(ctx, tenant, &apiclient.ListTMImportJobsParams{PageSize: &size, PageToken: tok, State: st})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/tm-import-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
	if err != nil {
		return nil, err
	}
	tb, err := limited(limit, func(size int, tok *string) ([]ImportJob, *string, error) {
		r, err := c.api.ListTermbaseImportJobsWithResponse(ctx, tenant, &apiclient.ListTermbaseImportJobsParams{PageSize: &size, PageToken: tok, State: st})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/termbase-import-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
	return append(tm, tb...), err
}

// WorkspaceExportJobs lists the tenant-wide TMX and TBX exports, newest
// first, at most limit of each (0: all).
func (c *Client) WorkspaceExportJobs(ctx context.Context, tenant, state string, limit int) ([]ExportJob, error) {
	st := jobState(state)
	tm, err := limited(limit, func(size int, tok *string) ([]ExportJob, *string, error) {
		r, err := c.api.ListTMExportJobsWithResponse(ctx, tenant, &apiclient.ListTMExportJobsParams{PageSize: &size, PageToken: tok, State: st})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/tm-export-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
	if err != nil {
		return nil, err
	}
	tb, err := limited(limit, func(size int, tok *string) ([]ExportJob, *string, error) {
		r, err := c.api.ListTermbaseExportJobsWithResponse(ctx, tenant, &apiclient.ListTermbaseExportJobsParams{PageSize: &size, PageToken: tok, State: st})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/termbase-export-jobs")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
	return append(tm, tb...), err
}

func jobState(s string) *apiclient.IntegrationJobState {
	if s == "" {
		return nil
	}
	st := apiclient.IntegrationJobState(s)
	return &st
}

// CancelExportJob cancels an export job.
func (c *Client) CancelExportJob(ctx context.Context, tenant, id string) (ExportJob, error) {
	r, err := c.api.CancelExportJobWithResponse(idempotent(ctx), tenant, id)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/export-jobs/%s/cancellation", id)); err != nil {
		return ExportJob{}, err
	}
	return *r.JSON200, nil
}

// Download is an export's file being read.
type Download struct {
	Body io.ReadCloser
	// ETag is the file's SHA-256 (hex) from the ETag header, unquoted;
	// empty when the server sent none.
	ETag string
	// Size is the Content-Length, -1 when unknown.
	Size int64
}

// DownloadExportFile GETs the export's file from its download URL
// (download_url; relative to the server). The caller closes Body.
func (c *Client) DownloadExportFile(ctx context.Context, downloadURL string) (Download, error) {
	req, err := c.transferRequest(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return Download{}, err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return Download{}, &APIError{Method: req.Method, URL: req.URL.String(), Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return Download{}, problem(resp.StatusCode, body, req.Method, req.URL.String())
	}
	return Download{Body: resp.Body, ETag: unquoteETag(resp.Header.Get("ETag")), Size: resp.ContentLength}, nil
}

// unquoteETag strips a (weak) entity tag's quotes.
func unquoteETag(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "W/")
	return strings.Trim(v, `"`)
}

// transferRequest builds a file transfer to ref, a URL the API handed
// out (upload_url, download_url): relative ones resolve against the
// server. The API token goes only to the server's own origin, never to
// another host a URL might name (a pre-signed storage URL).
func (c *Client) transferRequest(ctx context.Context, method, ref string, body io.Reader) (*http.Request, error) {
	base, err := url.Parse(c.server + "/")
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(ref, "/v1/") {
		// The API's own paths keep a server URL's path prefix (a proxy).
		ref = strings.TrimSuffix(base.Path, "/") + ref
	}
	u, err := base.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("remote: invalid transfer URL %q: %w", ref, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if u.Scheme == base.Scheme && u.Host == base.Host {
		if err := c.editor(ctx, req); err != nil {
			return nil, err
		}
	} else if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return req, nil
}
