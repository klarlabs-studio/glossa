package app_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	identitydomain "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The read and export API's use cases on in-memory ports; the
// Postgres adapters run under cmd/glossa-server's integration test.

var (
	trailTenant = tenancy.ID(uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"))
	projectA    = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000a")
	projectB    = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000b")
	trailStart  = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
)

// chain is n entries: every third tenant-level, the others alternating
// between projects A and B, one a minute from trailStart.
func chain(t *testing.T, n int) []domain.Entry {
	t.Helper()
	var head domain.Head
	out := make([]domain.Entry, 0, n)
	for i := range n {
		project := uuid.NullUUID{}
		switch i % 3 {
		case 1:
			project = uuid.NullUUID{UUID: projectA, Valid: true}
		case 2:
			project = uuid.NullUUID{UUID: projectB, Valid: true}
		}
		e, err := domain.Append(trailTenant.UUID(), head, domain.Draft{
			EventID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprint(i))), Source: domain.SourceOutbox,
			Action: "localization.translation.revised", Actor: "person:0190a1b2-0000-7000-8000-0000000000aa",
			OccurredAt: trailStart.Add(time.Duration(i) * time.Minute), AggregateType: "translation",
			AggregateID: fmt.Sprint(i), Project: project, Summary: json.RawMessage(`{"text":"string(len=9)"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
		head = e.Head()
	}
	return out
}

// memTrail is app.EntryReader over a slice.
type memTrail struct{ entries []domain.Entry }

func (m *memTrail) ListEntries(_ context.Context, f app.EntryFilter, cursor int64, limit int) ([]domain.Entry, error) {
	var out []domain.Entry
	for _, e := range m.entries {
		if f.Ascending && e.Sequence <= cursor || !f.Ascending && e.Sequence >= cursor {
			continue
		}
		if f.Projects != nil && (!e.Project.Valid || !slices.Contains(f.Projects, e.Project.UUID)) {
			continue
		}
		if f.Project.Valid && e.Project != f.Project {
			continue
		}
		out = append(out, e)
	}
	if !f.Ascending {
		slices.Reverse(out)
	}
	return out[:min(limit, len(out))], nil
}

func (m *memTrail) EntryAt(_ context.Context, seq int64) (domain.Entry, error) {
	if seq < 1 || seq > int64(len(m.entries)) {
		return domain.Entry{}, app.ErrNotFound
	}
	return m.entries[seq-1], nil
}

func (m *memTrail) ChainHead(context.Context) (domain.Head, error) {
	if len(m.entries) == 0 {
		return domain.Head{}, nil
	}
	return m.entries[len(m.entries)-1].Head(), nil
}

func (m *memTrail) OccurredSpan(_ context.Context, from, to time.Time) (app.Span, error) {
	var s app.Span
	for _, e := range m.entries {
		if e.OccurredAt.Before(from) || !e.OccurredAt.Before(to) {
			continue
		}
		if s.Inside == 0 || e.Sequence < s.First {
			s.First = e.Sequence
		}
		s.Last = max(s.Last, e.Sequence)
		s.Inside++
	}
	return s, nil
}

func (m *memTrail) Entries(_ context.Context, after int64, limit int) ([]domain.Entry, error) {
	if after >= int64(len(m.entries)) {
		return nil, nil
	}
	return m.entries[after:min(int64(len(m.entries)), after+int64(limit))], nil
}

// memJobs is app.ExportJobs and app.ExportClaimer for one tenant.
type memJobs struct {
	mu     sync.Mutex
	jobs   map[uuid.UUID]domain.ExportJob
	tokens map[uuid.UUID]uuid.UUID
	events []outbox.Event
}

func newMemJobs() *memJobs {
	return &memJobs{jobs: map[uuid.UUID]domain.ExportJob{}, tokens: map[uuid.UUID]uuid.UUID{}}
}

func (m *memJobs) Create(_ context.Context, j domain.ExportJob, e outbox.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.ID]; ok {
		return false, nil
	}
	m.jobs[j.ID] = j
	m.events = append(m.events, e)
	return true, nil
}

func (m *memJobs) Get(_ context.Context, id uuid.UUID) (domain.ExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return domain.ExportJob{}, app.ErrNotFound
	}
	return j, nil
}

func (m *memJobs) List(context.Context, *app.ExportCursor, int) ([]domain.ExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.ExportJob
	for _, j := range m.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out, nil
}

func (m *memJobs) Finish(_ context.Context, j domain.ExportJob, token uuid.UUID, e outbox.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens[j.ID] != token {
		return app.ErrLeaseLost
	}
	delete(m.tokens, j.ID)
	m.jobs[j.ID] = j
	m.events = append(m.events, e)
	return nil
}

func (m *memJobs) Retry(_ context.Context, j domain.ExportJob, token uuid.UUID, _ string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens[j.ID] != token {
		return app.ErrLeaseLost
	}
	delete(m.tokens, j.ID)
	j.State = domain.ExportQueued
	m.jobs[j.ID] = j
	return nil
}

func (m *memJobs) Claim(context.Context, time.Duration) (app.ExportClaim, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		if j.State == domain.ExportQueued {
			j.State, j.Attempts = domain.ExportRunning, j.Attempts+1
			m.jobs[id] = j
			tok := uuid.New()
			m.tokens[id] = tok
			return app.ExportClaim{JobID: id, TenantID: trailTenant.UUID(), Token: tok, Attempts: j.Attempts}, true, nil
		}
	}
	return app.ExportClaim{}, false, nil
}

func (m *memJobs) Expired(context.Context, int) ([]app.ExpiredExport, error) { return nil, nil }
func (m *memJobs) MarkDeleted(context.Context, []uuid.UUID) error            { return nil }

func auditKeys(t *testing.T) *domain.KeySet {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	k, err := domain.ParseSigningKey("audit-test", base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	ks, err := domain.NewKeySet(k, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

type exportRig struct {
	svc     *app.ExportService
	worker  *app.ExportWorker
	jobs    *memJobs
	objects *objectstore.Memory
	keys    *domain.KeySet
}

func newExportRig(t *testing.T, entries []domain.Entry, keys *domain.KeySet, opts ...app.ExportOption) exportRig {
	t.Helper()
	jobs, objects := newMemJobs(), objectstore.NewMemory()
	svc := app.NewExportService(app.ExportConfig{Enabled: true, Retention: time.Hour}, &memTrail{entries: entries},
		jobs, objects, keys, append([]app.ExportOption{app.WithExportClock(func() time.Time { return trailStart.Add(24 * time.Hour) })}, opts...)...)
	return exportRig{svc: svc, worker: app.NewExportWorker(svc, jobs, app.ExportWorkerConfig{}), jobs: jobs, objects: objects, keys: keys}
}

func owner() context.Context {
	return authztest.Member(context.Background(), trailTenant, []string{"owner"})
}

func seqRange(first, last int64) domain.ExportRangeRequest {
	return domain.ExportRangeRequest{FirstSequence: &first, LastSequence: &last}
}

// Only an owner limited to no project exports (RFC 0006 §6.2): an
// export is the tenant's chain, never one project's.
func TestOnlyAnUnscopedOwnerExportsTheTrail(t *testing.T) {
	rig := newExportRig(t, chain(t, 6), auditKeys(t))
	ctx := context.Background()
	refused := map[string]context.Context{
		"an admin":                 authztest.Member(ctx, trailTenant, []string{"admin"}),
		"a token with every scope": authztest.Token(ctx, trailTenant, "read", "write", "publish", "admin", "workflows"),
		"an owner in one project":  authztest.ScopedMember(ctx, trailTenant, []uuid.UUID{projectA}, []string{"owner"}),
		"nobody":                   tenancy.ContextWithTenant(ctx, trailTenant),
	}
	for who, c := range refused {
		if _, _, err := rig.svc.CreateExport(c, seqRange(1, 6), ""); !errors.Is(err, authz.ErrForbidden) &&
			!errors.Is(err, authz.ErrUnauthenticated) {
			t.Errorf("%s: create err = %v, want a refusal", who, err)
		}
		if _, err := rig.svc.ListExports(c, nil, 10); err == nil {
			t.Errorf("%s listed export jobs", who)
		}
	}
	j, replayed, err := rig.svc.CreateExport(owner(), seqRange(1, 6), "")
	if err != nil || replayed || j.State != domain.ExportQueued {
		t.Fatalf("the owner's export: %+v %v %v", j, replayed, err)
	}
	if len(rig.jobs.events) != 1 || rig.jobs.events[0].Type != domain.EventExportRequested ||
		rig.jobs.events[0].Actor == "" {
		t.Errorf("making the job published %+v", rig.jobs.events)
	}
}

// Without an audit key there is nothing to sign with, and no fallback.
func TestWithoutAKeyExportsAreUnavailable(t *testing.T) {
	rig := newExportRig(t, chain(t, 3), nil)
	if _, _, err := rig.svc.CreateExport(owner(), seqRange(1, 3), ""); !errors.Is(err, app.ErrExportsUnavailable) {
		t.Fatalf("err = %v, want ErrExportsUnavailable", err)
	}
	off := app.NewExportService(app.ExportConfig{}, &memTrail{entries: chain(t, 3)}, newMemJobs(), objectstore.NewMemory(), auditKeys(t))
	if _, _, err := off.CreateExport(owner(), seqRange(1, 3), ""); !errors.Is(err, app.ErrExportsUnavailable) {
		t.Fatalf("exports switched off: err = %v", err)
	}
}

func TestAnExportRangeIsChecked(t *testing.T) {
	rig := newExportRig(t, chain(t, 3), auditKeys(t))
	from, to := trailStart, trailStart.Add(32*24*time.Hour)
	first := int64(2)
	cases := map[string]struct {
		req  domain.ExportRangeRequest
		want error
	}{
		"no range":         {domain.ExportRangeRequest{}, domain.ErrInvalidRange},
		"both ranges":      {domain.ExportRangeRequest{From: &from, To: &to, FirstSequence: &first}, domain.ErrInvalidRange},
		"half a time":      {domain.ExportRangeRequest{From: &from}, domain.ErrInvalidRange},
		"over 31 days":     {domain.ExportRangeRequest{From: &from, To: &to}, domain.ErrRangeTooLong},
		"past the head":    {seqRange(2, 4), domain.ErrSequenceOutOfRange},
		"backwards":        {seqRange(3, 2), domain.ErrInvalidRange},
		"before the chain": {seqRange(0, 2), domain.ErrInvalidRange},
	}
	for name, c := range cases {
		if _, _, err := rig.svc.CreateExport(owner(), c.req, ""); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	// An open sequence range ends at the head when the job is made.
	j, _, err := rig.svc.CreateExport(owner(), domain.ExportRangeRequest{FirstSequence: &first}, "")
	if err != nil || j.Range.FirstSequence != 2 || j.Range.LastSequence != 3 {
		t.Errorf("an open range: %+v, %v", j.Range, err)
	}
}

func TestAnIdempotencyKeyMakesOneJob(t *testing.T) {
	rig := newExportRig(t, chain(t, 6), auditKeys(t))
	ada := owner()
	a, replayed, err := rig.svc.CreateExport(ada, seqRange(1, 4), "export-2026-10")
	if err != nil || replayed {
		t.Fatal(err, replayed)
	}
	b, replayed, err := rig.svc.CreateExport(ada, seqRange(1, 4), "export-2026-10")
	if err != nil || !replayed || b.ID != a.ID {
		t.Fatalf("the retry: %v %v %v (first %v)", b.ID, replayed, err, a.ID)
	}
	if _, _, err := rig.svc.CreateExport(ada, seqRange(1, 5), "export-2026-10"); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Errorf("a different range under the key: %v", err)
	}
	if len(rig.jobs.jobs) != 1 {
		t.Errorf("%d jobs, want 1", len(rig.jobs.jobs))
	}
}

func readObject(t *testing.T, svc *app.ExportService, id uuid.UUID, f app.ExportFile) []byte {
	t.Helper()
	rc, _, _, err := svc.OpenExport(owner(), id, f)
	if err != nil {
		t.Fatalf("open %s: %v", f, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The worker writes wave 4's glossa.audit/v1 export of exactly the
// range, signed with the active key: it verifies offline with the
// public key, and one flipped byte fails it.
func TestTheWorkerWritesAnExportThatVerifies(t *testing.T) {
	entries := chain(t, 12)
	rig := newExportRig(t, entries, auditKeys(t))
	j, _, err := rig.svc.CreateExport(owner(), seqRange(4, 9), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := rig.svc.OpenExport(owner(), j.ID, app.ExportEntries); !errors.Is(err, app.ErrExportNotReady) {
		t.Errorf("a queued job's file: %v, want ErrExportNotReady", err)
	}
	if worked, err := rig.worker.RunOnce(context.Background()); !worked || err != nil {
		t.Fatalf("RunOnce: %v %v", worked, err)
	}
	done, err := rig.svc.GetExport(owner(), j.ID)
	if err != nil || done.State != domain.ExportSucceeded {
		t.Fatalf("the job: %+v %v", done, err)
	}
	if done.EntryCount != 6 || done.Range.FirstSequence != 4 || done.Range.LastSequence != 9 || done.KeyID != "audit-test" ||
		done.FirstPrevHash != domain.HexHash(entries[2].Hash) || done.LastHash != domain.HexHash(entries[8].Hash) {
		t.Errorf("the job records %+v", done)
	}
	last := rig.jobs.events[len(rig.jobs.events)-1]
	if last.Type != domain.EventExportCompleted || last.Actor != authz.SystemEventActor(app.ExporterPrincipal) {
		t.Errorf("the job ended with %+v", last)
	}

	manifest := readObject(t, rig.svc, j.ID, app.ExportManifest)
	lines := readObject(t, rig.svc, j.ID, app.ExportEntries)
	if r := domain.VerifyExport(manifest, bytes.NewReader(lines), rig.keys.PublicKeys()); !r.OK || r.Verified != 6 {
		t.Fatalf("the export does not verify: %+v", r.Failure)
	}
	flipped := bytes.Clone(lines)
	flipped[len(flipped)/2] ^= 0x01
	if r := domain.VerifyExport(manifest, bytes.NewReader(flipped), rig.keys.PublicKeys()); r.OK {
		t.Error("an export with a flipped byte verified")
	}
	if r := domain.VerifyExport(manifest, bytes.NewReader(lines), auditKeys(t).PublicKeys()); r.OK {
		t.Error("the export verified with another deployment's key")
	}
}

// A time range exports the segment its entries occupy; one whose
// entries are not one unbroken segment fails for good.
func TestATimeRangeExportsItsSegment(t *testing.T) {
	entries := chain(t, 10)
	m := &countingMetrics{}
	rig := newExportRig(t, entries, auditKeys(t), app.WithExportMetrics(m))
	from, to := trailStart.Add(2*time.Minute), trailStart.Add(5*time.Minute)
	j, _, err := rig.svc.CreateExport(owner(), domain.ExportRangeRequest{From: &from, To: &to}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, _ := rig.svc.GetExport(owner(), j.ID)
	if done.State != domain.ExportSucceeded || done.Range.FirstSequence != 3 || done.Range.LastSequence != 5 {
		t.Fatalf("the time range's job: %+v", done)
	}

	// Imported history: an entry appended inside the segment that
	// occurred years before it.
	old := entries[:6]
	imported, err := domain.Append(trailTenant.UUID(), old[5].Head(), domain.Draft{
		EventID: uuid.New(), Source: domain.SourceImport, Action: domain.ActionV0TranslationChanged,
		Actor: "v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d", OccurredAt: trailStart.AddDate(-1, 0, 0),
		AggregateType: "translation", AggregateID: "v0", Summary: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := domain.Append(trailTenant.UUID(), imported.Head(), domain.Draft{
		EventID: uuid.New(), Source: domain.SourceOutbox, Action: "localization.translation.revised",
		Actor: "person:0190a1b2-0000-7000-8000-0000000000aa", OccurredAt: trailStart.Add(6 * time.Minute),
		AggregateType: "translation", AggregateID: "7", Summary: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rig = newExportRig(t, append(slices.Clone(old), imported, next), auditKeys(t), app.WithExportMetrics(m))
	to = trailStart.Add(7 * time.Minute)
	j, _, err = rig.svc.CreateExport(owner(), domain.ExportRangeRequest{From: &from, To: &to}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, _ = rig.svc.GetExport(owner(), j.ID)
	if done.State != domain.ExportFailed || done.FailureCode != domain.FailureRangeNotContiguous {
		t.Errorf("a broken segment: %+v", done)
	}
	// glossa_audit_export_jobs_total: one succeeded, one failed for good.
	if !slices.Equal(m.exports, []string{app.ExportSucceeded, app.ExportFailed}) {
		t.Errorf("the ended jobs were counted as %v", m.exports)
	}
}

// glossa_audit_export_jobs_total moves once per job that ended, by how
// it ended (RFC 0006 §10.1): a succeeded export and one that failed for
// good are each counted, a queued one is not.
func TestEndedExportJobsAreCounted(t *testing.T) {
	m := &countingMetrics{}
	entries := chain(t, 10)
	jobs, objects := newMemJobs(), objectstore.NewMemory()
	svc := app.NewExportService(app.ExportConfig{Enabled: true, Retention: time.Hour}, &memTrail{entries: entries},
		jobs, objects, auditKeys(t), app.WithExportMetrics(m), app.WithExportClock(func() time.Time { return trailStart.Add(24 * time.Hour) }))
	worker := app.NewExportWorker(svc, jobs, app.ExportWorkerConfig{})

	if _, _, err := svc.CreateExport(owner(), seqRange(2, 6), ""); err != nil {
		t.Fatal(err)
	}
	if len(m.exports) != 0 {
		t.Fatalf("a queued job counted: %v", m.exports)
	}
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.exports, []string{app.ExportSucceeded}) {
		t.Fatalf("after a succeeded job: %v", m.exports)
	}
}

func readers(t *testing.T) (*app.ReadService, []domain.Entry) {
	t.Helper()
	entries := chain(t, 9)
	return app.NewReadService(&memTrail{entries: entries}), entries
}

// audit.read is owner and admin; a project-scoped caller sees its
// projects' entries and none of the tenant-level ones; an assigned
// member is refused.
func TestTheTrailIsReadWithinTheCallersProjects(t *testing.T) {
	svc, entries := readers(t)
	ctx := context.Background()
	admin := authztest.Member(ctx, trailTenant, []string{"admin"})
	all, next, err := svc.ListEntries(admin, app.EntryFilter{}, "", 100)
	if err != nil || len(all) != len(entries) || next != "" || all[0].Sequence != 9 {
		t.Fatalf("an admin lists %d entries (next %q): %v", len(all), next, err)
	}
	page, next, err := svc.ListEntries(admin, app.EntryFilter{Ascending: true}, "", 4)
	if err != nil || len(page) != 4 || next != "4" {
		t.Fatalf("a first page: %d, next %q, %v", len(page), next, err)
	}
	if page, _, _ = svc.ListEntries(admin, app.EntryFilter{Ascending: true}, next, 4); page[0].Sequence != 5 {
		t.Errorf("the second page starts at %d", page[0].Sequence)
	}
	if _, _, err := svc.ListEntries(admin, app.EntryFilter{}, "nope", 4); !errors.Is(err, app.ErrInvalidCursor) {
		t.Errorf("a forged cursor: %v", err)
	}

	scoped := authztest.ScopedMember(ctx, trailTenant, []uuid.UUID{projectA}, []string{"admin"})
	got, _, err := svc.ListEntries(scoped, app.EntryFilter{}, "", 100)
	if err != nil || len(got) != 3 {
		t.Fatalf("a member scoped to A lists %d: %v", len(got), err)
	}
	for _, e := range got {
		if !e.Project.Valid || e.Project.UUID != projectA {
			t.Errorf("a member scoped to A sees entry %d of %v", e.Sequence, e.Project)
		}
	}
	if got, _, _ := svc.ListEntries(scoped, app.EntryFilter{Project: uuid.NullUUID{UUID: projectB, Valid: true}}, "", 100); len(got) != 0 {
		t.Errorf("filtering by a project outside the scope lists %d", len(got))
	}
	if _, err := svc.Entry(scoped, 2); err != nil {
		t.Errorf("an entry of A: %v", err)
	}
	for _, seq := range []int64{1, 3} { // tenant-level, project B
		if _, err := svc.Entry(scoped, seq); !errors.Is(err, authz.ErrNotVisible) {
			t.Errorf("entry %d for a member scoped to A: %v, want not visible", seq, err)
		}
	}

	assigned := authztest.Member(ctx, trailTenant, []string{"admin"})
	p, _ := authz.From(assigned)
	p.Visibility = identitydomain.VisibilityAssigned
	assigned = authz.WithPrincipal(assigned, p)
	refused := map[string]context.Context{
		"an assigned member":       assigned,
		"a developer":              authztest.Member(ctx, trailTenant, []string{"developer"}),
		"a token with every scope": authztest.Token(ctx, trailTenant, "read", "write", "publish", "admin", "workflows"),
	}
	for who, c := range refused {
		if _, _, err := svc.ListEntries(c, app.EntryFilter{}, "", 10); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("%s lists the trail: %v", who, err)
		}
		if _, err := svc.Entry(c, 2); err == nil {
			t.Errorf("%s reads an entry", who)
		}
	}
	if _, err := svc.Entry(admin, 99); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("an entry past the head: %v", err)
	}
}
