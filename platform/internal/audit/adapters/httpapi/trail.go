package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The read and export API (RFC 0006 §6.2, wave 5). Identity's Guard has
// authenticated the caller and resolved the tenant; the use cases check
// audit.read and audit.export themselves.

// Reads is the read side of the trail (app.ReadService).
type Reads interface {
	ListEntries(ctx context.Context, f app.EntryFilter, cursor string, limit int) ([]domain.Entry, string, error)
	Entry(ctx context.Context, sequence int64) (domain.Entry, error)
}

// Exports is the export jobs (app.ExportService).
type Exports interface {
	CreateExport(ctx context.Context, req domain.ExportRangeRequest, idemKey string) (domain.ExportJob, bool, error)
	ListExports(ctx context.Context, before *app.ExportCursor, limit int) ([]domain.ExportJob, error)
	GetExport(ctx context.Context, id uuid.UUID) (domain.ExportJob, error)
	OpenExport(ctx context.Context, id uuid.UUID, file app.ExportFile) (io.ReadCloser, domain.ExportJob, domain.ExportObject, error)
}

// Problem codes of the read and export API. A code here is part of the
// contract: never rename one.
const (
	codeInvalidQuery          problem.Code = "invalid_query"
	codeInvalidRange          problem.Code = "invalid_range"
	codeRangeTooLong          problem.Code = "range_too_long"
	codeSequenceOutOfRange    problem.Code = "sequence_out_of_range"
	codeExportUnavailable     problem.Code = "audit_export_unavailable"
	codeIdempotencyKeyReused  problem.Code = "idempotency_key_reused"
	codeInvalidIdempotencyKey problem.Code = "invalid_idempotency_key"
	codeExportNotReady        problem.Code = "export_not_ready"
	codeFileExpired           problem.Code = "file_expired"
	codeStorageUnavailable    problem.Code = "storage_unavailable"
	codeInvalidEntry          problem.Code = "invalid_entry"
)

// mapError turns the context's errors into problems; anything else —
// authorization included — goes on to Identity's error writer.
func mapError(err error) error {
	m := []struct {
		err    error
		status int
		code   problem.Code
	}{
		{app.ErrNotFound, http.StatusNotFound, problem.CodeNotFound},
		{app.ErrInvalidCursor, http.StatusBadRequest, "invalid_page_token"},
		{domain.ErrInvalidRange, http.StatusBadRequest, codeInvalidRange},
		{domain.ErrRangeTooLong, http.StatusUnprocessableEntity, codeRangeTooLong},
		{domain.ErrSequenceOutOfRange, http.StatusUnprocessableEntity, codeSequenceOutOfRange},
		{app.ErrExportsUnavailable, http.StatusServiceUnavailable, codeExportUnavailable},
		{app.ErrIdempotencyReuse, http.StatusUnprocessableEntity, codeIdempotencyKeyReused},
		{idempotency.ErrInvalidKey, http.StatusBadRequest, codeInvalidIdempotencyKey},
		{app.ErrExportNotReady, http.StatusConflict, codeExportNotReady},
		{app.ErrExportExpired, http.StatusGone, codeFileExpired},
		{app.ErrStorageUnavailable, http.StatusServiceUnavailable, codeStorageUnavailable},
		{app.ErrTooManyV0Entries, http.StatusBadRequest, problem.CodeInvalidRequest},
		{domain.ErrInvalidEntry, http.StatusUnprocessableEntity, codeInvalidEntry},
	}
	for _, p := range m {
		if errors.Is(err, p.err) {
			detail := err.Error()
			if p.err == app.ErrNotFound {
				detail = "no such resource"
			}
			return problem.New(p.status, p.code, detail)
		}
	}
	return err
}

func badQuery(detail string) error {
	return problem.New(http.StatusBadRequest, codeInvalidQuery, detail)
}

// ListAuditEntries lists the tenant's entries.
func (a *API) ListAuditEntries(ctx context.Context, req apiv1.ListAuditEntriesRequestObject) (apiv1.ListAuditEntriesResponseObject, error) {
	if a.reads == nil {
		return nil, mapError(app.ErrNotFound)
	}
	p := req.Params
	pg, err := pagination.Parse(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f, err := entryFilter(p)
	if err != nil {
		return nil, err
	}
	entries, next, err := a.reads.ListEntries(ctx, f, pg.After, pg.Size)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListAuditEntries200JSONResponse{Items: make([]apiv1.AuditEntry, 0, len(entries)), NextPageToken: pagination.Token(next)}
	for _, e := range entries {
		out.Items = append(out.Items, toEntry(e))
	}
	return out, nil
}

func entryFilter(p apiv1.ListAuditEntriesParams) (app.EntryFilter, error) {
	f := app.EntryFilter{
		Actor: deref(p.Actor), Action: deref(p.Action), AggregateType: deref(p.AggregateType), AggregateID: deref(p.AggregateId),
		FirstSequence: deref(p.FirstSequence), LastSequence: deref(p.LastSequence),
	}
	if p.From != nil {
		f.From = domain.Instant(*p.From)
	}
	if p.To != nil {
		f.To = domain.Instant(*p.To)
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		return f, badQuery("from must be before to")
	}
	if f.FirstSequence < 0 || f.LastSequence < 0 || (f.LastSequence > 0 && f.LastSequence < f.FirstSequence) {
		return f, badQuery("first_sequence and last_sequence are sequences, first at most last")
	}
	if p.Source != nil {
		f.Source = domain.Source(*p.Source)
		if !f.Source.Valid() {
			return f, badQuery("source is outbox, direct or import")
		}
	}
	if p.Project != nil {
		id, err := uuid.Parse(*p.Project)
		if err != nil || id == uuid.Nil {
			return f, badQuery("project is not a project id")
		}
		f.Project = uuid.NullUUID{UUID: id, Valid: true}
	}
	if p.Order != nil {
		switch string(*p.Order) {
		case "asc":
			f.Ascending = true
		case "desc":
		default:
			return f, badQuery("order is asc or desc")
		}
	}
	return f, nil
}

// GetAuditEntry reads one entry.
func (a *API) GetAuditEntry(ctx context.Context, req apiv1.GetAuditEntryRequestObject) (apiv1.GetAuditEntryResponseObject, error) {
	if a.reads == nil {
		return nil, mapError(app.ErrNotFound)
	}
	e, err := a.reads.Entry(ctx, req.Sequence)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAuditEntry200JSONResponse(toEntry(e)), nil
}

func toEntry(e domain.Entry) apiv1.AuditEntry {
	out := apiv1.AuditEntry{
		Sequence: e.Sequence, EventId: e.EventID.String(), Source: apiv1.AuditSource(e.Source), Action: e.Action,
		Actor: e.Actor, OccurredAt: e.OccurredAt.UTC(), AggregateType: e.AggregateType, AggregateId: e.AggregateID,
		Locale: nonEmpty(e.Locale), RequestId: nonEmpty(e.RequestID), TraceId: nonEmpty(e.TraceID),
		PrevHash: domain.HexHash(e.PrevHash), Hash: domain.HexHash(e.Hash), Summary: map[string]any{},
	}
	if e.Project.Valid {
		out.ProjectId = ptr(e.Project.UUID.String())
	}
	_ = json.Unmarshal(e.Summary, &out.Summary)
	return out
}

// CreateAuditExportJob queues an export.
func (a *API) CreateAuditExportJob(ctx context.Context, req apiv1.CreateAuditExportJobRequestObject) (apiv1.CreateAuditExportJobResponseObject, error) {
	if a.exports == nil {
		return nil, mapError(app.ErrExportsUnavailable)
	}
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is an export range")
	}
	b := req.Body
	j, replayed, err := a.exports.CreateExport(ctx, domain.ExportRangeRequest{
		From: b.From, To: b.To, FirstSequence: b.FirstSequence, LastSequence: b.LastSequence,
	}, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateAuditExportJob201ResponseHeaders{Location: ptr(jobPath(ctx, j.ID))}
	if replayed {
		h.IdempotentReplayed = ptr("true")
	}
	return apiv1.CreateAuditExportJob201JSONResponse{Body: toJob(ctx, j), Headers: h}, nil
}

// ListAuditExportJobs lists export jobs, newest first.
func (a *API) ListAuditExportJobs(ctx context.Context, req apiv1.ListAuditExportJobsRequestObject) (apiv1.ListAuditExportJobsResponseObject, error) {
	if a.exports == nil {
		return nil, mapError(app.ErrExportsUnavailable)
	}
	pg, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var before *app.ExportCursor
	if pg.After != "" {
		if before, err = parseJobCursor(pg.After); err != nil {
			return nil, problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this API issued")
		}
	}
	jobs, err := a.exports.ListExports(ctx, before, pg.Limit())
	if err != nil {
		return nil, mapError(err)
	}
	jobs, next := pagination.Trim(jobs, pg, func(j domain.ExportJob) string {
		return j.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + j.ID.String()
	})
	out := apiv1.ListAuditExportJobs200JSONResponse{Items: make([]apiv1.AuditExportJob, 0, len(jobs)), NextPageToken: next}
	for _, j := range jobs {
		out.Items = append(out.Items, toJob(ctx, j))
	}
	return out, nil
}

func parseJobCursor(s string) (*app.ExportCursor, error) {
	at, id, ok := strings.Cut(s, "|")
	if !ok {
		return nil, app.ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &app.ExportCursor{CreatedAt: t, ID: u}, nil
}

// GetAuditExportJob reads one export job.
func (a *API) GetAuditExportJob(ctx context.Context, req apiv1.GetAuditExportJobRequestObject) (apiv1.GetAuditExportJobResponseObject, error) {
	if a.exports == nil {
		return nil, mapError(app.ErrExportsUnavailable)
	}
	id, err := jobID(req.AuditExportJob)
	if err != nil {
		return nil, err
	}
	j, err := a.exports.GetExport(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetAuditExportJob200JSONResponse(toJob(ctx, j)), nil
}

// DownloadAuditExportEntries streams entries.jsonl.
func (a *API) DownloadAuditExportEntries(ctx context.Context, req apiv1.DownloadAuditExportEntriesRequestObject) (apiv1.DownloadAuditExportEntriesResponseObject, error) {
	d, err := a.download(ctx, req.AuditExportJob, app.ExportEntries)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// DownloadAuditExportManifest streams manifest.json.
func (a *API) DownloadAuditExportManifest(ctx context.Context, req apiv1.DownloadAuditExportManifestRequestObject) (apiv1.DownloadAuditExportManifestResponseObject, error) {
	d, err := a.download(ctx, req.AuditExportJob, app.ExportManifest)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (a *API) download(ctx context.Context, rawID string, file app.ExportFile) (download, error) {
	if a.exports == nil {
		return download{}, mapError(app.ErrExportsUnavailable)
	}
	id, err := jobID(rawID)
	if err != nil {
		return download{}, err
	}
	rc, _, obj, err := a.exports.OpenExport(ctx, id, file)
	if err != nil {
		return download{}, mapError(err)
	}
	contentType := "application/jsonl"
	if file == app.ExportManifest {
		contentType = "application/json"
	}
	return download{body: rc, name: string(file), contentType: contentType, size: obj.Bytes, sha256: obj.SHA256}, nil
}

// download answers 200 with an export's object, byte for byte: the
// manifest's signature and the lines' digest cover exactly those bytes,
// so nothing may re-encode them on the way out (release/httpapi's
// rawJSON is the same rule for release manifests).
type download struct {
	body        io.ReadCloser
	name        string
	contentType string
	size        int64
	sha256      string
}

func (d download) write(w http.ResponseWriter) error {
	defer d.body.Close()
	w.Header().Set("Content-Type", d.contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.name))
	w.Header().Set("Content-Length", strconv.FormatInt(d.size, 10))
	if d.sha256 != "" {
		w.Header().Set("ETag", `"`+d.sha256+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, d.body)
	return err
}

// VisitDownloadAuditExportEntriesResponse implements apiv1.DownloadAuditExportEntriesResponseObject.
func (d download) VisitDownloadAuditExportEntriesResponse(w http.ResponseWriter) error {
	return d.write(w)
}

// VisitDownloadAuditExportManifestResponse implements apiv1.DownloadAuditExportManifestResponseObject.
func (d download) VisitDownloadAuditExportManifestResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func jobID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrNotFound)
	}
	return id, nil
}

func jobPath(ctx context.Context, id uuid.UUID) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + "/audit-export-jobs/" + id.String()
}

func toJob(ctx context.Context, j domain.ExportJob) apiv1.AuditExportJob {
	out := apiv1.AuditExportJob{
		Id: j.ID.String(), State: apiv1.AuditExportJobState(j.State), Attempts: j.Attempts, CreatedBy: j.CreatedBy,
		CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, ExpiresAt: j.ExpiresAt, StartedAt: j.StartedAt,
		FinishedAt: j.FinishedAt, FilesDeletedAt: j.FilesDeletedAt,
		FailureCode: nonEmpty(j.FailureCode), FailureMessage: nonEmpty(j.FailureMessage),
	}
	if j.Range.ByTime() {
		out.From, out.To = ptr(j.Range.From), ptr(j.Range.To)
	}
	if !j.Range.ByTime() || j.State == domain.ExportSucceeded {
		out.FirstSequence, out.LastSequence = ptr(j.Range.FirstSequence), ptr(j.Range.LastSequence)
	}
	if j.State == domain.ExportSucceeded {
		out.EntryCount, out.KeyId = ptr(j.EntryCount), nonEmpty(j.KeyID)
		out.FirstPrevHash, out.LastHash = nonEmpty(j.FirstPrevHash), nonEmpty(j.LastHash)
		base := jobPath(ctx, j.ID)
		out.Entries = file(domain.EntriesFile, j.Entries, base+"/file", j.Kept())
		out.Manifest = file(domain.ManifestFile, j.Manifest, base+"/manifest", j.Kept())
	}
	return out
}

func file(name string, o domain.ExportObject, url string, kept bool) *apiv1.AuditExportFile {
	f := &apiv1.AuditExportFile{Path: name, Sha256: o.SHA256, Bytes: o.Bytes}
	if kept {
		f.DownloadUrl = ptr(url)
	}
	return f
}

func ptr[T any](v T) *T { return &v }

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
