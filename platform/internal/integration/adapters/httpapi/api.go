// Package httpapi is the Integration context's HTTP edge: import and
// export jobs of the generated /v1 strict server — create, upload,
// follow, cancel, page through results, download.
package httpapi

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// API serves Integration's operations.
type API struct {
	svc *app.Service
	// gh is the GitHub integration (RFC 0004 §6); nil when this
	// deployment has registered no GitHub App, and then every GitHub
	// operation answers github_not_configured.
	gh *app.GitHubService
}

// New returns the API. gh may be nil.
func New(svc *app.Service, gh *app.GitHubService) *API { return &API{svc: svc, gh: gh} }

// UploadPath reports whether a request is an import job's upload
// (PUT /v1/tenants/{tenant}/import-jobs/{import_job}/file), which takes
// large bodies: the composition root lifts the body limit there.
func UploadPath(method, path string) bool {
	return method == http.MethodPut && jobFile(path, "import-jobs")
}

// DownloadPath reports whether a request is an export's download
// (GET /v1/tenants/{tenant}/export-jobs/{export_job}/file), which
// streams large bodies.
func DownloadPath(method, path string) bool {
	return method == http.MethodGet && jobFile(path, "export-jobs")
}

func jobFile(path, collection string) bool {
	s := strings.Split(path, "/")
	return len(s) == 7 && s[0] == "" && s[1] == "v1" && s[2] == "tenants" && s[3] != "" &&
		s[4] == collection && s[5] != "" && s[6] == "file"
}

// pathID parses an ID in a URL; a malformed one is simply not found.
func pathID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrNotFound)
	}
	return id, nil
}

// optionalID parses an optional ID; a malformed one is invalid.
func optionalID(name string, s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil || id == uuid.Nil {
		return nil, mapError(fmt.Errorf("%w: %s is not an id this API issued", app.ErrInvalidQuery, name))
	}
	return &id, nil
}

func tenantPath(ctx context.Context, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + sub
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func idPtr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	return apiconv.Ptr(id.String())
}

func page(size *int, token *string) (pagination.Page, error) { return pagination.Parse(size, token) }

// ── converters ───────────────────────────────────────────────────────

func fromImportOptions(o *apiv1.ImportOptions) domain.Options {
	if o == nil {
		return domain.Options{}
	}
	return domain.Options{
		Locale: deref(o.Locale), Namespace: deref(o.Namespace), Syntax: string(deref(o.Syntax)),
		State: string(deref(o.State)), PluralVariable: deref(o.PluralVariable),
	}
}

func toImportOptions(o domain.Options) apiv1.ImportOptions {
	out := apiv1.ImportOptions{Locale: nonEmpty(o.Locale), Namespace: nonEmpty(o.Namespace), PluralVariable: nonEmpty(o.PluralVariable)}
	if o.Syntax != "" {
		out.Syntax = apiconv.Ptr(apiv1.Syntax(o.Syntax))
	}
	if o.State != "" {
		out.State = apiconv.Ptr(apiv1.ReviewState(o.State))
	}
	return out
}

func fromExportOptions(o *apiv1.ExportOptions) domain.Options {
	if o == nil {
		return domain.Options{}
	}
	out := domain.Options{
		Locales: deref(o.Locales), SourceLocale: deref(o.SourceLocale), Namespaces: deref(o.Namespaces),
		Layout: string(deref(o.Layout)), Syntax: string(deref(o.Syntax)),
	}
	for _, s := range deref(o.States) {
		out.States = append(out.States, string(s))
	}
	return out
}

func toExportOptions(o domain.Options) apiv1.ExportOptions {
	out := apiv1.ExportOptions{SourceLocale: nonEmpty(o.SourceLocale)}
	if len(o.Locales) > 0 {
		out.Locales = apiconv.Ptr(o.Locales)
	}
	if len(o.Namespaces) > 0 {
		out.Namespaces = apiconv.Ptr(o.Namespaces)
	}
	if len(o.States) > 0 {
		states := make([]apiv1.ReviewState, len(o.States))
		for i, s := range o.States {
			states[i] = apiv1.ReviewState(s)
		}
		out.States = &states
	}
	if o.Layout != "" {
		out.Layout = apiconv.Ptr(apiv1.ExportOptionsLayout(o.Layout))
	}
	if o.Syntax != "" {
		out.Syntax = apiconv.Ptr(apiv1.Syntax(o.Syntax))
	}
	return out
}

func toFile(f *domain.File) *apiv1.IntegrationFile {
	if f == nil {
		return nil
	}
	return &apiv1.IntegrationFile{Size: f.Size, Sha256: f.SHA256, ContentType: f.ContentType}
}

func toCounts(c domain.Counts) apiv1.ImportCounts {
	return apiv1.ImportCounts{Created: c.Created, Updated: c.Updated, Unchanged: c.Unchanged, Conflict: c.Conflict, Invalid: c.Invalid}
}

func toSummary(s domain.Summary) apiv1.ImportSummary {
	out := apiv1.ImportSummary{
		Created: s.Created, Updated: s.Updated, Unchanged: s.Unchanged, Conflict: s.Conflict, Invalid: s.Invalid,
		ByKind: map[string]apiv1.ImportCounts{},
	}
	for k, c := range s.ByKind {
		out.ByKind[string(k)] = toCounts(c)
	}
	return out
}

func toImportJob(ctx context.Context, j domain.Job) apiv1.ImportJob {
	out := apiv1.ImportJob{
		Id: j.ID.String(), ProjectId: idPtr(j.ProjectID), Kind: apiv1.IntegrationKind(j.Kind),
		Format: apiv1.IntegrationFormat(j.Format), Mode: apiv1.ImportMode(j.Mode), Options: toImportOptions(j.Options),
		State: apiv1.IntegrationJobState(j.State), FileName: j.FileName, File: toFile(j.File), ReusedJobId: idPtr(j.ReusedJobID),
		Summary: toSummary(j.Summary), TotalItems: j.TotalItems, ProcessedItems: j.ProcessedItems,
		FailureCode: nonEmpty(j.FailureCode), FailureMessage: nonEmpty(j.FailureMessage), CancelRequested: j.CancelRequested,
		Attempts: j.Attempts, CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt,
		FinishedAt: j.FinishedAt, UpdatedAt: j.UpdatedAt, ExpiresAt: j.ExpiresAt, FilesDeletedAt: j.FilesDeletedAt,
	}
	if j.State == domain.StateAwaitingUpload {
		out.UploadUrl = apiconv.Ptr(tenantPath(ctx, "/import-jobs/"+j.ID.String()+"/file"))
	}
	return out
}

func toExportJob(ctx context.Context, j domain.Job) apiv1.ExportJob {
	out := apiv1.ExportJob{
		Id: j.ID.String(), ProjectId: idPtr(j.ProjectID), Kind: apiv1.IntegrationKind(j.Kind),
		Format: apiv1.IntegrationFormat(j.Format), Options: toExportOptions(j.Options), State: apiv1.IntegrationJobState(j.State),
		FileName: j.FileName, File: toFile(j.File), Written: j.Summary.Written, FailureCode: nonEmpty(j.FailureCode),
		FailureMessage: nonEmpty(j.FailureMessage), CancelRequested: j.CancelRequested, Attempts: j.Attempts,
		CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
		UpdatedAt: j.UpdatedAt, ExpiresAt: j.ExpiresAt, FilesDeletedAt: j.FilesDeletedAt,
	}
	if j.State == domain.StateSucceeded && j.File != nil && j.FilesDeletedAt == nil {
		out.DownloadUrl = apiconv.Ptr(tenantPath(ctx, "/export-jobs/"+j.ID.String()+"/file"))
	}
	return out
}

func toResult(it domain.Item) apiv1.ImportResult {
	return apiv1.ImportResult{
		Seq: it.Seq, Kind: apiv1.ImportResultKind(it.Kind), Key: it.Key, Locale: nonEmpty(it.Locale),
		Status: apiv1.ImportResultStatus(it.Status), Code: nonEmpty(it.Code), Detail: nonEmpty(it.Detail),
		Line: nonZero(it.Line), Column: nonZero(it.Column), Ref: nonEmpty(it.Ref),
	}
}

// attachment renders a Content-Disposition for name.
func attachment(name string) string {
	if name == "" {
		name = "export"
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// etag quotes a digest as a strong entity tag.
func etag(sha string) string { return strconv.Quote(sha) }
