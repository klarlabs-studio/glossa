package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/audit/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// reads records the filter that reached the use case.
type reads struct {
	f       app.EntryFilter
	entries []domain.Entry
	err     error
}

func (r *reads) ListEntries(_ context.Context, f app.EntryFilter, _ string, _ int) ([]domain.Entry, string, error) {
	r.f = f
	return r.entries, "", r.err
}

func (r *reads) Entry(context.Context, int64) (domain.Entry, error) {
	if len(r.entries) == 0 {
		return domain.Entry{}, app.ErrNotFound
	}
	return r.entries[0], r.err
}

// exports answers with fixed results.
type exports struct {
	job  domain.ExportJob
	body string
	err  error
}

func (e *exports) CreateExport(context.Context, domain.ExportRangeRequest, string) (domain.ExportJob, bool, error) {
	return e.job, false, e.err
}

func (e *exports) ListExports(context.Context, *app.ExportCursor, int) ([]domain.ExportJob, error) {
	return []domain.ExportJob{e.job}, e.err
}

func (e *exports) GetExport(context.Context, uuid.UUID) (domain.ExportJob, error) {
	return e.job, e.err
}

func (e *exports) OpenExport(context.Context, uuid.UUID, app.ExportFile) (io.ReadCloser, domain.ExportJob, domain.ExportObject, error) {
	if e.err != nil {
		return nil, domain.ExportJob{}, domain.ExportObject{}, e.err
	}
	return io.NopCloser(strings.NewReader(e.body)), e.job, e.job.Entries, nil
}

func tenantCtx() context.Context {
	return tenancy.ContextWithTenant(context.Background(), tenancy.ID(uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")))
}

func TestTheEntryListParsesItsFilters(t *testing.T) {
	r := &reads{}
	api := httpapi.New(nil, httpapi.WithReads(r))
	from, to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	project := uuid.NewString()
	src := apiv1.AuditSource("direct")
	order := apiv1.ListAuditEntriesParamsOrder("asc")
	_, err := api.ListAuditEntries(tenantCtx(), apiv1.ListAuditEntriesRequestObject{Params: apiv1.ListAuditEntriesParams{
		From: &from, To: &to, FirstSequence: apiconv.Ptr(int64(2)), LastSequence: apiconv.Ptr(int64(9)),
		Actor: apiconv.Ptr("person:x"), Action: apiconv.Ptr("identity.person.signed_in"), Project: &project,
		Source: &src, Order: &order,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.f.From.Equal(from) || !r.f.To.Equal(to) || r.f.FirstSequence != 2 || r.f.LastSequence != 9 ||
		r.f.Actor != "person:x" || r.f.Action != "identity.person.signed_in" || r.f.Project.UUID.String() != project ||
		r.f.Source != domain.SourceDirect || !r.f.Ascending || r.f.Projects != nil {
		t.Errorf("the filter reached the use case as %+v", r.f)
	}

	bad := map[string]apiv1.ListAuditEntriesParams{
		"to before from":    {From: &to, To: &from},
		"last before first": {FirstSequence: apiconv.Ptr(int64(5)), LastSequence: apiconv.Ptr(int64(4))},
		"a project slug":    {Project: apiconv.Ptr("shop")},
		"an unknown source": {Source: apiconv.Ptr(apiv1.AuditSource("mail"))},
		"an unknown order":  {Order: apiconv.Ptr(apiv1.ListAuditEntriesParamsOrder("sideways"))},
	}
	for name, p := range bad {
		_, err := api.ListAuditEntries(tenantCtx(), apiv1.ListAuditEntriesRequestObject{Params: p})
		if status, c := code(t, err); status != 400 || c != "invalid_query" {
			t.Errorf("%s: %d %s", name, status, c)
		}
	}
	r.err = app.ErrInvalidCursor
	_, err = api.ListAuditEntries(tenantCtx(), apiv1.ListAuditEntriesRequestObject{})
	if status, c := code(t, err); status != 400 || c != "invalid_page_token" {
		t.Errorf("a forged cursor: %d %s", status, c)
	}
}

func TestAnEntryOutsideTheTrailIsNotFound(t *testing.T) {
	api := httpapi.New(nil, httpapi.WithReads(&reads{}))
	_, err := api.GetAuditEntry(tenantCtx(), apiv1.GetAuditEntryRequestObject{Sequence: 7})
	if status, c := code(t, err); status != 404 || c != "not_found" {
		t.Errorf("got %d %s", status, c)
	}
}

func TestExportErrorsHaveTheirCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{app.ErrExportsUnavailable, 503, "audit_export_unavailable"},
		{domain.ErrInvalidRange, 400, "invalid_range"},
		{domain.ErrRangeTooLong, 422, "range_too_long"},
		{domain.ErrSequenceOutOfRange, 422, "sequence_out_of_range"},
		{app.ErrIdempotencyReuse, 422, "idempotency_key_reused"},
	}
	for _, c := range cases {
		api := httpapi.New(nil, httpapi.WithExports(&exports{err: c.err}))
		_, err := api.CreateAuditExportJob(tenantCtx(), apiv1.CreateAuditExportJobRequestObject{
			Body: &apiv1.AuditExportJobCreate{FirstSequence: apiconv.Ptr(int64(1))},
		})
		if status, got := code(t, err); status != c.status || string(got) != c.code {
			t.Errorf("%v: %d %s, want %d %s", c.err, status, got, c.status, c.code)
		}
	}
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{app.ErrExportNotReady, 409, "export_not_ready"},
		{app.ErrExportExpired, 410, "file_expired"},
		{app.ErrStorageUnavailable, 503, "storage_unavailable"},
		{app.ErrNotFound, 404, "not_found"},
	} {
		api := httpapi.New(nil, httpapi.WithExports(&exports{err: c.err}))
		_, err := api.DownloadAuditExportEntries(tenantCtx(), apiv1.DownloadAuditExportEntriesRequestObject{AuditExportJob: uuid.NewString()})
		if status, got := code(t, err); status != c.status || string(got) != c.code {
			t.Errorf("download %v: %d %s, want %d %s", c.err, status, got, c.status, c.code)
		}
	}
	// Without the export service the routes say so.
	_, err := httpapi.New(nil).ListAuditExportJobs(tenantCtx(), apiv1.ListAuditExportJobsRequestObject{})
	if status, c := code(t, err); status != 503 || c != "audit_export_unavailable" {
		t.Errorf("no export service: %d %s", status, c)
	}
}

// A succeeded job names its files and where to download them, and a
// download is the stored bytes with their digest as the ETag.
func TestASucceededExportIsDownloadedByteForByte(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	job := domain.ExportJob{
		ID: id, State: domain.ExportSucceeded, Range: domain.ExportRange{FirstSequence: 1, LastSequence: 2}, EntryCount: 2,
		KeyID: "audit-1", Entries: domain.ExportObject{Key: "k", SHA256: strings.Repeat("a", 64), Bytes: 6},
		Manifest:  domain.ExportObject{Key: "m", SHA256: strings.Repeat("b", 64), Bytes: 2},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour), CreatedBy: "person:x",
	}
	api := httpapi.New(nil, httpapi.WithExports(&exports{job: job, body: "{}\n{}\n"}))
	got, err := api.GetAuditExportJob(tenantCtx(), apiv1.GetAuditExportJobRequestObject{AuditExportJob: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	j := got.(apiv1.GetAuditExportJob200JSONResponse)
	if j.Entries == nil || j.Entries.DownloadUrl == nil || !strings.HasSuffix(*j.Entries.DownloadUrl, id.String()+"/file") ||
		j.Manifest == nil || !strings.HasSuffix(*j.Manifest.DownloadUrl, "/manifest") || *j.EntryCount != 2 {
		b, _ := json.Marshal(j)
		t.Errorf("the job reads %s", b)
	}
	d, err := api.DownloadAuditExportEntries(tenantCtx(), apiv1.DownloadAuditExportEntriesRequestObject{AuditExportJob: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := d.VisitDownloadAuditExportEntriesResponse(w); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != "{}\n{}\n" || w.Header().Get("ETag") != `"`+strings.Repeat("a", 64)+`"` ||
		w.Header().Get("Content-Disposition") != `attachment; filename="entries.jsonl"` {
		t.Errorf("the download: %q %v", w.Body, w.Header())
	}
}
