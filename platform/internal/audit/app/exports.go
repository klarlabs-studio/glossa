package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The export jobs' errors a caller can act on. Each has its own problem
// code at the HTTP edge.
var (
	// ErrExportsUnavailable: this deployment has no audit key, or audit
	// exports are switched off (GLOSSA_AUDIT_EXPORTS_ENABLED). There is
	// no key to fall back to (RFC 0006 §6.2): evidence signed by a key
	// nobody chose is not evidence.
	ErrExportsUnavailable = errors.New("audit: this deployment does not export the audit trail")
	// ErrIdempotencyReuse: the Idempotency-Key made a different export.
	ErrIdempotencyReuse = errors.New("audit: Idempotency-Key reused for a different export")
	// ErrExportNotReady: the job has not succeeded (yet).
	ErrExportNotReady = errors.New("audit: the export has not succeeded")
	// ErrExportExpired: retention deleted the export's objects.
	ErrExportExpired = errors.New("audit: the export's files were deleted by retention")
	// ErrStorageUnavailable: object storage did not answer.
	ErrStorageUnavailable = errors.New("audit: object storage is unavailable")
)

// ExporterPrincipal names the background process that writes exports;
// its actor ends every job in the trail (audit.export.completed).
const ExporterPrincipal = "audit.exporter"

// ExportConfig configures the export jobs.
type ExportConfig struct {
	// Enabled is GLOSSA_AUDIT_EXPORTS_ENABLED.
	Enabled bool
	// Retention is how long an export's objects are kept.
	Retention time.Duration
}

// ExportService is the audit export jobs (RFC 0006 §6.2, wave 5): it
// makes them, shows them and serves their objects; ExportWorker runs
// them. Every use case needs audit.export — only `owner` holds it by
// default, no API token scope grants it — by a principal limited to no
// project (authz.RequireUnscoped): an export is a segment of the
// tenant's one chain, never one project's, because a project's entries
// are not a chain on their own and could not be verified.
type ExportService struct {
	cfg     ExportConfig
	entries EntryReader
	jobs    ExportJobs
	objects Objects
	keys    *domain.KeySet
	logger  *slog.Logger
	metrics Metrics
	now     func() time.Time
}

// ExportOption configures the export service.
type ExportOption func(*ExportService)

// WithExportLogger sets the logger. It logs ids, counts and codes.
func WithExportLogger(l *slog.Logger) ExportOption {
	return func(s *ExportService) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithExportMetrics records each export job that ends (ExportSucceeded
// or ExportFailed) once its end is stored. A job that is retried, or
// whose lease was lost to another worker, is not counted: it has not
// ended, or the worker that ends it counts it.
func WithExportMetrics(m Metrics) ExportOption {
	return func(s *ExportService) { s.metrics = m }
}

// WithExportClock sets the clock (tests).
func WithExportClock(now func() time.Time) ExportOption {
	return func(s *ExportService) { s.now = now }
}

// NewExportService returns the export jobs. keys nil means no audit key
// is configured: every new export is refused with ErrExportsUnavailable.
func NewExportService(cfg ExportConfig, entries EntryReader, jobs ExportJobs, objects Objects, keys *domain.KeySet,
	opts ...ExportOption,
) *ExportService {
	if cfg.Retention <= 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	s := &ExportService{cfg: cfg, entries: entries, jobs: jobs, objects: objects, keys: keys,
		logger: slog.New(slog.DiscardHandler), now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateExport queues an export of a range of ctx's tenant's chain. A
// sequence range without an end ends at the chain's head now, so the
// job exports exactly the entries that existed when it was made. With
// an Idempotency-Key, a retry answers the job the first request made
// (replayed); a different range under the same key is
// ErrIdempotencyReuse. Making the job publishes audit.export.requested
// as the caller, in the same transaction.
func (s *ExportService) CreateExport(ctx context.Context, req domain.ExportRangeRequest, idemKey string) (domain.ExportJob, bool, error) {
	if err := authz.RequireUnscoped(ctx, authz.AuditExport); err != nil {
		return domain.ExportJob{}, false, err
	}
	if !s.cfg.Enabled || s.keys == nil {
		return domain.ExportJob{}, false, ErrExportsUnavailable
	}
	by, err := authz.EventActor(ctx)
	if err != nil {
		return domain.ExportJob{}, false, err
	}
	head, err := s.entries.ChainHead(ctx)
	if err != nil {
		return domain.ExportJob{}, false, err
	}
	now := domain.Instant(s.now())
	rng, err := req.Resolve(now, head.Sequence)
	if err != nil {
		return domain.ExportJob{}, false, err
	}
	id, err := exportJobID(ctx, string(by), idemKey)
	if err != nil {
		return domain.ExportJob{}, false, err
	}
	j := domain.ExportJob{
		ID: id, State: domain.ExportQueued, Range: rng, MaxAttempts: domain.ExportMaxAttempts,
		CreatedBy: string(by), CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(s.cfg.Retention),
	}
	inserted, err := s.jobs.Create(ctx, j, exportEvent(domain.EventExportRequested, j, by))
	if err != nil {
		return domain.ExportJob{}, false, err
	}
	if inserted {
		s.logger.InfoContext(ctx, "audit: export requested", slog.String("job_id", id.String()))
		return j, false, nil
	}
	first, err := s.jobs.Get(ctx, id)
	if err != nil {
		return domain.ExportJob{}, false, err
	}
	if first.CreatedBy != j.CreatedBy || !sameRequest(first.Range, req) {
		return domain.ExportJob{}, false, ErrIdempotencyReuse
	}
	return first, true, nil
}

// sameRequest reports whether a replayed request asks for the range the
// first one made. A time range's end may have been cut to the first
// request's now, and an open sequence range ended at the head then.
func sameRequest(made domain.ExportRange, req domain.ExportRangeRequest) bool {
	if made.ByTime() {
		return req.From != nil && req.To != nil && domain.Instant(*req.From).Equal(made.From) &&
			!domain.Instant(*req.To).Before(made.To)
	}
	return req.FirstSequence != nil && *req.FirstSequence == made.FirstSequence &&
		(req.LastSequence == nil || *req.LastSequence == made.LastSequence)
}

// exportJobID derives a job's id from its Idempotency-Key, or a new
// time-ordered id without one.
func exportJobID(ctx context.Context, by, key string) (uuid.UUID, error) {
	if key == "" {
		return uuid.Must(uuid.NewV7()), nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, err
	}
	tenant, _ := tenancy.FromContext(ctx)
	return idempotency.ID("audit.export.create", tenant.String(), by, key), nil
}

func exportEvent(typ string, j domain.ExportJob, by outbox.Actor) outbox.Event {
	return outbox.Event{Type: typ, AggregateType: domain.AggregateExportJob, AggregateID: j.ID.String(), Actor: by, Payload: j.Event()}
}

// ListExports lists ctx's tenant's export jobs, newest first.
func (s *ExportService) ListExports(ctx context.Context, before *ExportCursor, limit int) ([]domain.ExportJob, error) {
	if err := authz.RequireUnscoped(ctx, authz.AuditExport); err != nil {
		return nil, err
	}
	return s.jobs.List(ctx, before, limit)
}

// GetExport reads one export job.
func (s *ExportService) GetExport(ctx context.Context, id uuid.UUID) (domain.ExportJob, error) {
	if err := authz.RequireUnscoped(ctx, authz.AuditExport); err != nil {
		return domain.ExportJob{}, err
	}
	return s.jobs.Get(ctx, id)
}

// ExportFile names one of an export's two objects.
type ExportFile string

// The two files of a glossa.audit/v1 export.
const (
	ExportEntries  ExportFile = domain.EntriesFile
	ExportManifest ExportFile = domain.ManifestFile
)

// OpenExport opens one of a succeeded job's objects, to stream it out
// byte for byte; the caller closes it.
func (s *ExportService) OpenExport(ctx context.Context, id uuid.UUID, file ExportFile) (io.ReadCloser, domain.ExportJob, domain.ExportObject, error) {
	if err := authz.RequireUnscoped(ctx, authz.AuditExport); err != nil {
		return nil, domain.ExportJob{}, domain.ExportObject{}, err
	}
	j, err := s.jobs.Get(ctx, id)
	if err != nil {
		return nil, domain.ExportJob{}, domain.ExportObject{}, err
	}
	switch {
	case j.State != domain.ExportSucceeded:
		return nil, j, domain.ExportObject{}, ErrExportNotReady
	case j.FilesDeletedAt != nil:
		return nil, j, domain.ExportObject{}, ErrExportExpired
	}
	obj := j.Entries
	if file == ExportManifest {
		obj = j.Manifest
	}
	rc, err := s.objects.Open(ctx, obj.Key)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return nil, j, obj, ErrExportExpired
	case err != nil:
		s.logger.ErrorContext(ctx, "audit: open export object", slog.String("job_id", id.String()), slog.Any("error", err))
		return nil, j, obj, ErrStorageUnavailable
	}
	return rc, j, obj, nil
}

// errPermanent fails a job for good with a code; anything else retries.
type errPermanent struct{ code, message string }

func (e *errPermanent) Error() string { return e.code + ": " + e.message }

// pageSize is how many entries the writer reads at a time.
const pageSize = 1000

// run executes one claimed job in its tenant's scope (ctx carries the
// tenant and the exporter's principal).
func (s *ExportService) run(ctx context.Context, c ExportClaim) error {
	j, err := s.jobs.Get(ctx, c.JobID)
	if err != nil {
		return err
	}
	now := s.now()
	if j.StartedAt == nil {
		j.StartedAt = &now
	}
	j.State, j.Attempts = domain.ExportRunning, c.Attempts
	switch {
	case s.keys == nil:
		err = &errPermanent{domain.FailureInternal, "this deployment has no audit key to sign the export with"}
	case c.Attempts > j.MaxAttempts:
		err = &errPermanent{domain.FailureInternal, fmt.Sprintf("claimed %d times; its workers kept stopping before it finished", c.Attempts)}
	default:
		err = s.write(ctx, &j)
	}
	var perm *errPermanent
	switch {
	case err == nil:
		return s.finish(ctx, c, j, domain.ExportSucceeded, "", "")
	case errors.Is(err, ErrLeaseLost):
		return nil
	case errors.As(err, &perm):
		return s.finish(ctx, c, j, domain.ExportFailed, perm.code, perm.message)
	case c.Attempts < j.MaxAttempts:
		s.logger.WarnContext(ctx, "audit: export failed; retrying", slog.String("job_id", j.ID.String()), slog.Any("error", err))
		err = s.jobs.Retry(ctx, j, c.Token, truncate(err.Error()), domain.ExportBackoff(c.Attempts))
		if errors.Is(err, ErrLeaseLost) {
			return nil
		}
		return err
	}
	return s.finish(ctx, c, j, domain.ExportFailed, domain.FailureInternal,
		fmt.Sprintf("gave up after %d attempts: %v", c.Attempts, err))
}

// write resolves the job's range to a chain segment and writes its two
// objects through the glossa.audit/v1 writer, which refuses anything
// that is not the next link of the chain: what it signs, verifies.
func (s *ExportService) write(ctx context.Context, j *domain.ExportJob) error {
	opts := domain.ExportOptions{CreatedAt: s.now()}
	first, last := j.Range.FirstSequence, j.Range.LastSequence
	if j.Range.ByTime() {
		span, err := s.entries.OccurredSpan(ctx, j.Range.From, j.Range.To)
		if err != nil {
			return err
		}
		if !span.Contiguous() {
			return &errPermanent{domain.FailureRangeNotContiguous, fmt.Sprintf(
				"the entries of the range occupy sequences %d–%d, and %d of those occurred outside it; export them by sequence",
				span.First, span.Last, span.Last-span.First+1-span.Inside)}
		}
		opts.From, opts.To = j.Range.From, j.Range.To
		first, last = span.First, span.Last
		if span.Inside == 0 { // nothing happened: an empty export at the head
			head, err := s.entries.ChainHead(ctx)
			if err != nil {
				return err
			}
			first, last = head.Sequence+1, head.Sequence
		}
	}
	from := domain.Head{}
	if first > 1 {
		prev, err := s.entries.EntryAt(ctx, first-1)
		if err != nil {
			return fmt.Errorf("audit: read the entry before the range: %w", err)
		}
		from = prev.Head()
	}
	tenant, _ := tenancy.FromContext(ctx)
	entriesKey := domain.ExportObjectKey(tenant.UUID(), j.ID, domain.EntriesFile)
	x, err := s.writeEntries(ctx, entriesKey, tenant.UUID(), from, first, last, opts)
	if err != nil {
		return err
	}
	manifest, m, err := x.Finish(s.keys.Active())
	if err != nil {
		return &errPermanent{domain.FailureInternal, err.Error()}
	}
	manifestKey := domain.ExportObjectKey(tenant.UUID(), j.ID, domain.ManifestFile)
	if _, err := s.objects.PutStream(ctx, manifestKey, bytes.NewReader(manifest), "application/json"); err != nil {
		return fmt.Errorf("audit: store the manifest: %w", err)
	}
	sum := sha256.Sum256(manifest)
	j.Range.FirstSequence, j.Range.LastSequence = m.Range.FirstSequence, m.Range.LastSequence
	j.EntryCount, j.FirstPrevHash, j.LastHash, j.KeyID = m.EntryCount, m.Range.FirstPrevHash, m.Range.LastHash, m.KeyID
	j.Entries = domain.ExportObject{Key: entriesKey, SHA256: m.Entries.SHA256, Bytes: m.Entries.Bytes}
	j.Manifest = domain.ExportObject{Key: manifestKey, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(manifest))}
	return nil
}

// writeEntries streams entries first…last into object storage through
// the export writer, in constant memory, and returns the writer, ready
// to sign: its manifest pins the bytes it wrote.
func (s *ExportService) writeEntries(ctx context.Context, key string, tenant uuid.UUID, from domain.Head, first, last int64,
	opts domain.ExportOptions,
) (*domain.ExportWriter, error) {
	pr, pw := io.Pipe()
	x, err := domain.NewExportWriter(pw, tenant, from, opts)
	if err != nil {
		return nil, &errPermanent{domain.FailureInternal, err.Error()}
	}
	produced := make(chan error, 1)
	go func() {
		err := s.produce(ctx, x, first, last)
		_ = pw.CloseWithError(err)
		produced <- err
	}()
	_, putErr := s.objects.PutStream(ctx, key, pr, "application/jsonl")
	_ = pr.CloseWithError(putErr) // unblocks the producer if storage gave up first
	err = <-produced
	switch {
	case putErr != nil:
		return nil, fmt.Errorf("audit: store the entries: %w", putErr)
	case errors.Is(err, domain.ErrInvalidExport) || errors.Is(err, domain.ErrInvalidEntry):
		return nil, &errPermanent{domain.FailureInternal, err.Error()}
	case err != nil:
		return nil, err
	}
	return x, nil
}

// produce feeds entries first…last to x, a page at a time.
func (s *ExportService) produce(ctx context.Context, x *domain.ExportWriter, first, last int64) error {
	for after := first - 1; after < last; {
		page, err := s.entries.Entries(ctx, after, int(min(pageSize, last-after)))
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return fmt.Errorf("%w: the chain ends at %d, before %d", domain.ErrInvalidExport, after, last)
		}
		for _, e := range page {
			if e.Sequence > last {
				return nil
			}
			if err := x.Write(e); err != nil {
				return err
			}
			after = e.Sequence
		}
	}
	return nil
}

// finish ends a claimed job and publishes audit.export.completed as the
// exporter.
func (s *ExportService) finish(ctx context.Context, c ExportClaim, j domain.ExportJob, state domain.ExportState, code, message string) error {
	now := s.now()
	j.State, j.FailureCode, j.FailureMessage, j.FinishedAt, j.UpdatedAt = state, code, truncate(message), &now, now
	err := s.jobs.Finish(ctx, j, c.Token, exportEvent(domain.EventExportCompleted, j, authz.SystemEventActor(ExporterPrincipal)))
	if errors.Is(err, ErrLeaseLost) {
		return nil
	}
	if err == nil {
		if s.metrics != nil {
			s.metrics.ExportJob(exportOutcome(state))
		}
		s.logger.InfoContext(ctx, "audit: export finished", slog.String("job_id", j.ID.String()),
			slog.String("state", string(state)), slog.String("failure_code", code), slog.Int64("entries", j.EntryCount))
	}
	return err
}

func exportOutcome(state domain.ExportState) string {
	if state == domain.ExportSucceeded {
		return ExportSucceeded
	}
	return ExportFailed
}

func truncate(s string) string {
	const n = 4000
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
