package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// ImportRequest asks for an import job.
type ImportRequest struct {
	// ProjectID is required for catalog formats; TMX and TBX import
	// tenant-wide without one.
	ProjectID *uuid.UUID
	Format    string
	Mode      string
	FileName  string
	Options   domain.Options
}

// CreateImport creates an import job waiting for its file. What the
// requester may do is snapshotted onto the job: catalog formats need
// integration.import (translations of the locales in its scope) or
// integration.manage (also source messages); TMX and TBX, overwrite
// mode and JSON source catalogs need integration.manage. A repeated
// idemKey returns the first request's job with replayed set.
func (s *Service) CreateImport(ctx context.Context, in ImportRequest, idemKey string) (j domain.Job, replayed bool, err error) {
	by, err := actorOf(ctx)
	if err != nil {
		return domain.Job{}, false, err
	}
	f, err := domain.ParseFormat(in.Format)
	if err != nil {
		return domain.Job{}, false, err
	}
	mode, err := domain.ParseMode(in.Mode)
	if err != nil {
		return domain.Job{}, false, err
	}
	opts, err := in.Options.NormalizeImport(f)
	if err != nil {
		return domain.Job{}, false, err
	}
	if f.Kind() == domain.KindCatalog && in.ProjectID == nil {
		return domain.Job{}, false, domain.ErrProjectRequired
	}
	if err := newJobScope(ctx, in.ProjectID); err != nil {
		return domain.Job{}, false, err
	}
	access, err := importAccess(ctx, f, mode)
	if err != nil {
		return domain.Job{}, false, err
	}
	if in.ProjectID != nil {
		p, err := s.catalog.Project(ctx, *in.ProjectID)
		if err != nil {
			return domain.Job{}, false, err
		}
		if f == domain.FormatJSON && (opts.Locale == "" || opts.Locale == p.SourceLocale.String()) && !access.Manage {
			return domain.Job{}, false, &authz.DeniedError{Permission: authz.IntegrationManage}
		}
		if err := s.checkImportLocale(ctx, p, f, opts.Locale); err != nil {
			return domain.Job{}, false, err
		}
	}
	id, err := newJobID(ctx, "import.create", by, idemKey)
	if err != nil {
		return domain.Job{}, false, err
	}
	now := s.now()
	j = domain.Job{
		ID: id, Direction: domain.Import, ProjectID: in.ProjectID, Kind: f.Kind(), Format: f, Mode: mode, Options: opts,
		Access: access, State: domain.StateAwaitingUpload, FileName: domain.CleanFileName(in.FileName),
		MaxAttempts: domain.MaxAttempts, AvailableAt: now, CreatedBy: by, CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(s.cfg.Retention),
	}
	return s.insertJob(ctx, j)
}

// checkImportLocale refuses a locale option that isn't the project's.
func (s *Service) checkImportLocale(ctx context.Context, p ProjectInfo, f domain.Format, locale string) error {
	if locale == "" || f.Kind() != domain.KindCatalog || locale == p.SourceLocale.String() && f == domain.FormatJSON {
		return nil
	}
	targets, err := s.localization.Locales(ctx, p.ID)
	if err != nil {
		return err
	}
	return domain.CheckImportLocale(f, locale, p.SourceLocale, targets)
}

// KnowledgeFormat is the format of a tenant-wide translation memory
// (TMX) or termbase (TBX) file.
func KnowledgeFormat(k domain.Kind) (domain.Format, error) {
	switch k {
	case domain.KindTM:
		return domain.FormatTMX, nil
	case domain.KindTermbase:
		return domain.FormatTBX, nil
	}
	return "", fmt.Errorf("%w: %q is not a translation memory or termbase", domain.ErrInvalidFormat, k)
}

// CreateKnowledgeImport creates an import into the workspace's own
// translation memory (TMX) or termbase (TBX): tenant-wide, never a
// project's. It needs integration.manage and knowledge.write for the
// whole tenant, like every TMX and TBX import.
func (s *Service) CreateKnowledgeImport(ctx context.Context, k domain.Kind, mode, fileName, idemKey string) (domain.Job, bool, error) {
	f, err := KnowledgeFormat(k)
	if err != nil {
		return domain.Job{}, false, err
	}
	return s.CreateImport(ctx, ImportRequest{Format: string(f), Mode: mode, FileName: fileName}, idemKey)
}

// CreateKnowledgeExport exports the workspace's whole translation
// memory (TMX) or termbase (TBX): every unit or concept the tenant
// holds, its own and every project's.
func (s *Service) CreateKnowledgeExport(ctx context.Context, k domain.Kind, opts domain.Options, idemKey string) (domain.Job, bool, error) {
	f, err := KnowledgeFormat(k)
	if err != nil {
		return domain.Job{}, false, err
	}
	return s.CreateExport(ctx, ExportRequest{Format: string(f), Options: opts}, idemKey)
}

// importAccess snapshots what the caller may import.
func importAccess(ctx context.Context, f domain.Format, mode domain.Mode) (domain.Access, error) {
	by, err := actorOf(ctx)
	if err != nil {
		return domain.Access{}, err
	}
	a := domain.Access{Actor: by}
	if f.Kind() == domain.KindCatalog {
		imp, err := authz.ScopeOf(ctx, authz.IntegrationImport)
		if err != nil {
			return domain.Access{}, err
		}
		write, _ := authz.ScopeOf(ctx, authz.TranslationsWrite)
		a.Import = domain.Intersect(imp, write)
		a.Review, _ = authz.ScopeOf(ctx, authz.TranslationsReview)
		a.Manage = authz.Require(ctx, authz.IntegrationManage) == nil && authz.Require(ctx, authz.CatalogWrite) == nil
		if !a.Import.Granted && !a.Manage {
			return domain.Access{}, &authz.DeniedError{Permission: authz.IntegrationImport}
		}
	} else {
		if err := authz.Require(ctx, authz.IntegrationManage); err != nil {
			return domain.Access{}, err
		}
		if err := authz.Require(ctx, authz.KnowledgeWrite); err != nil {
			return domain.Access{}, err
		}
		a.Manage = true
	}
	if mode == domain.ModeOverwrite && !a.Manage {
		return domain.Access{}, &authz.DeniedError{Permission: authz.IntegrationManage}
	}
	return a, nil
}

// insertJob stores a new job, or returns the one an earlier request
// with the same Idempotency-Key created.
func (s *Service) insertJob(ctx context.Context, j domain.Job) (domain.Job, bool, error) {
	replayed := false
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertJob(ctx, j)
		if err != nil || inserted {
			return err
		}
		first, err := st.Job(ctx, j.ID)
		if err != nil {
			return err
		}
		if first.Direction != j.Direction || first.Format != j.Format || first.Mode != j.Mode ||
			!sameProject(first.ProjectID, j.ProjectID) || first.CreatedBy != j.CreatedBy {
			return ErrIdempotencyReuse
		}
		j, replayed = first, true
		return nil
	})
	return j, replayed, err
}

func sameProject(a, b *uuid.UUID) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// uploadLimit fails a reader past the upload limit.
type uploadLimit struct {
	r    io.Reader
	left int64
}

var errUploadTooLarge = errors.New("integration: upload limit")

func (l *uploadLimit) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, errUploadTooLarge
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n, errUploadTooLarge
	}
	return n, err
}

// UploadImport streams an import's file to object storage — its
// requester only, once — and queues the job. A file the tenant already
// imported successfully with the same options (the same fingerprint)
// isn't applied again: the job succeeds at once with the earlier job's
// result (reused_job_id). Dry runs are always run.
func (s *Service) UploadImport(ctx context.Context, id uuid.UUID, body io.Reader) (domain.Job, error) {
	j, err := s.getJob(ctx, id, domain.Import)
	if err != nil {
		return domain.Job{}, err
	}
	if err := requireActor(ctx, j); err != nil {
		return domain.Job{}, err
	}
	if j.State != domain.StateAwaitingUpload {
		return domain.Job{}, domain.ErrUploadNotExpected
	}
	key := fileKey(tenantOf(ctx), id, "upload-"+uuid.Must(uuid.NewV7()).String())
	h := sha256.New()
	in := &trackingReader{r: &uploadLimit{r: body, left: s.cfg.MaxUploadBytes}}
	n, err := s.objects.PutStream(ctx, key, io.TeeReader(in, h), "application/octet-stream")
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, errUploadTooLarge) || errors.As(err, &tooLarge):
		return domain.Job{}, ErrUploadTooLarge
	case err != nil && in.err != nil:
		return domain.Job{}, fmt.Errorf("%w: %v", ErrUploadInterrupted, in.err)
	case err != nil && ctx.Err() == nil:
		return domain.Job{}, fmt.Errorf("%w: %v", ErrStorage, err)
	case err != nil:
		return domain.Job{}, err
	case n == 0:
		_ = s.objects.Delete(ctx, key)
		return domain.Job{}, ErrEmptyUpload
	}
	sum := hex.EncodeToString(h.Sum(nil))
	project := ""
	if j.ProjectID != nil {
		project = j.ProjectID.String()
	}
	fingerprint := domain.Fingerprint(sum, project, j.Format, j.Mode, j.Options)
	by, err := authz.EventActor(ctx)
	if err != nil {
		return domain.Job{}, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, err := st.LockJob(ctx, id)
		if err != nil {
			return err
		}
		if cur.State != domain.StateAwaitingUpload {
			return domain.ErrUploadNotExpected
		}
		now := s.now()
		cur.File = &domain.File{Key: key, Size: n, SHA256: sum, ContentType: j.Format.ContentType()}
		cur.Fingerprint, cur.State, cur.AvailableAt, cur.UpdatedAt = fingerprint, domain.StateQueued, now, now
		if cur.Mode != domain.ModeDryRun {
			prev, err := st.ReusableJob(ctx, fingerprint, id)
			switch {
			case err == nil:
				cur.State, cur.ReusedJobID, cur.Summary = domain.StateSucceeded, &prev.ID, prev.Summary
				cur.TotalItems, cur.ProcessedItems, cur.FinishedAt = prev.TotalItems, prev.ProcessedItems, &now
			case !errors.Is(err, ErrNotFound):
				return err
			}
		}
		if err := st.SaveJob(ctx, cur); err != nil {
			return err
		}
		j = cur
		if cur.State == domain.StateSucceeded {
			return st.Publish(ctx, completedEvent(cur, by))
		}
		return nil
	})
	if err != nil {
		_ = s.objects.Delete(context.WithoutCancel(ctx), key)
		return domain.Job{}, err
	}
	return j, nil
}

// getJob reads a job of one direction for someone with
// integration.read.
func (s *Service) getJob(ctx context.Context, id uuid.UUID, dir domain.Direction) (domain.Job, error) {
	var j domain.Job
	err := authz.RequireRow(ctx, authz.IntegrationRead, func() (uuid.UUID, error) {
		err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			var err error
			j, err = st.Job(ctx, id)
			return err
		})
		if err == nil && j.Direction != dir {
			err = ErrNotFound
		}
		if err == nil && jobScope(ctx, j.ProjectID) != nil {
			err = ErrNotFound
		}
		if err != nil {
			return uuid.Nil, err
		}
		if j.ProjectID == nil {
			// A tenant-wide job: jobScope has hidden it from a caller
			// limited to some projects, and uuid.Nil is in the scope of
			// every caller who is not.
			return uuid.Nil, nil
		}
		return *j.ProjectID, nil
	})
	if err != nil {
		return domain.Job{}, err
	}
	return j, nil
}

// newJobScope is jobScope for a job being made: a tenant-wide one is
// refused (403), not hidden, to a caller limited to some projects — it
// would read or write every project's knowledge (RFC 0006 §4.1).
func newJobScope(ctx context.Context, project *uuid.UUID) error {
	if project != nil {
		return authz.InProject(ctx, *project)
	}
	return authz.RequireUnscoped(ctx, authz.IntegrationRead)
}

// jobScope checks a job's project against the caller's project scope
// (RFC 0006 §4.1): a project's job is the caller's to see or make only
// inside it, and a tenant-wide one — whose file holds every project's
// translation memory or termbase — only for a caller limited to none.
func jobScope(ctx context.Context, project *uuid.UUID) error {
	if project != nil {
		return authz.InProject(ctx, *project)
	}
	p, err := authz.Authenticated(ctx)
	if err != nil {
		return err
	}
	if !p.Projects.All() {
		return authz.ErrNotVisible
	}
	return nil
}

// GetImport returns an import job. Needs integration.read.
func (s *Service) GetImport(ctx context.Context, id uuid.UUID) (domain.Job, error) {
	return s.getJob(ctx, id, domain.Import)
}

// GetExport returns an export job. Needs integration.read.
func (s *Service) GetExport(ctx context.Context, id uuid.UUID) (domain.Job, error) {
	return s.getJob(ctx, id, domain.Export)
}

// ListJobs lists imports or exports, newest first. Needs
// integration.read.
func (s *Service) ListJobs(ctx context.Context, f JobFilter, page pagination.Page) ([]domain.Job, *string, error) {
	scope, err := authz.Projects(ctx, authz.IntegrationRead)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = scope.IDs()
	before, err := parseJobCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Job
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.Jobs(ctx, f, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(j domain.Job) string {
		return j.CreatedAt.Format(time.RFC3339Nano) + "|" + j.ID.String()
	})
	return items, next, nil
}

func parseJobCursor(s string) (*JobCursor, error) {
	if s == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(s, "|")
	t, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil {
		return nil, invalidPageToken()
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, invalidPageToken()
	}
	return &JobCursor{CreatedAt: t, ID: u}, nil
}

// Cancel stops an import or export: waiting and queued jobs at once, a
// running one after its current batch (what was applied stays applied).
// The requester, or someone with integration.manage, may cancel.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID, dir domain.Direction) (domain.Job, error) {
	j, err := s.getJob(ctx, id, dir)
	if err != nil {
		return domain.Job{}, err
	}
	if err := requireActor(ctx, j); err != nil && authz.Require(ctx, authz.IntegrationManage) != nil {
		return domain.Job{}, err
	}
	by, err := authz.EventActor(ctx) // the canceller, who may not be the requester
	if err != nil {
		return domain.Job{}, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, err := st.LockJob(ctx, id)
		if err != nil {
			return err
		}
		if dir == domain.Export && cur.State == domain.StateRunning {
			return domain.ErrNotCancellable // an export is written in one go
		}
		changed, err := cur.Cancel(s.now())
		if err != nil || !changed {
			j = cur
			return err
		}
		if err := st.SaveJob(ctx, cur); err != nil {
			return err
		}
		j = cur
		if cur.State == domain.StateCancelled {
			return st.Publish(ctx, completedEvent(cur, by))
		}
		return nil
	})
	return j, err
}

// ImportResults lists an import's per-item results in file order; an
// import that reused an earlier one's result lists that job's. Needs
// integration.read.
func (s *Service) ImportResults(ctx context.Context, id uuid.UUID, f ItemFilter, page pagination.Page) ([]domain.Item, *string, error) {
	j, err := s.getJob(ctx, id, domain.Import)
	if err != nil {
		return nil, nil, err
	}
	after := -1
	if page.After != "" {
		if after, err = strconv.Atoi(page.After); err != nil || after < 0 {
			return nil, nil, invalidPageToken()
		}
	}
	source := j.ID
	if j.ReusedJobID != nil {
		source = *j.ReusedJobID
	}
	var rows []domain.Item
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.Items(ctx, source, f, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(it domain.Item) string { return strconv.Itoa(it.Seq) })
	return items, next, nil
}

// ExportRequest asks for an export job.
type ExportRequest struct {
	// ProjectID is required for catalogs; TMX and TBX export the
	// tenant's whole memory or termbase without one.
	ProjectID *uuid.UUID
	Format    string
	Options   domain.Options
}

// CreateExport queues an export. Needs integration.read, plus
// catalog.read and translations.read (catalogs) or knowledge.read (TM,
// termbases). Catalog locales must be the project's.
func (s *Service) CreateExport(ctx context.Context, in ExportRequest, idemKey string) (domain.Job, bool, error) {
	by, err := actorOf(ctx)
	if err != nil {
		return domain.Job{}, false, err
	}
	f, err := domain.ParseFormat(in.Format)
	if err != nil {
		return domain.Job{}, false, err
	}
	opts, err := in.Options.NormalizeExport(f)
	if err != nil {
		return domain.Job{}, false, err
	}
	perms := []authz.Permission{authz.IntegrationRead, authz.KnowledgeRead}
	if f.Kind() == domain.KindCatalog {
		perms = []authz.Permission{authz.IntegrationRead, authz.CatalogRead, authz.TranslationsRead}
		if in.ProjectID == nil {
			return domain.Job{}, false, domain.ErrProjectRequired
		}
	}
	for _, p := range perms {
		if err := authz.Require(ctx, p); err != nil {
			return domain.Job{}, false, err
		}
	}
	if err := newJobScope(ctx, in.ProjectID); err != nil {
		return domain.Job{}, false, err
	}
	if in.ProjectID != nil {
		p, err := s.catalog.Project(ctx, *in.ProjectID)
		if err != nil {
			return domain.Job{}, false, err
		}
		if f.Kind() == domain.KindCatalog {
			if err := s.checkExportLocales(ctx, p, f, opts.Locales); err != nil {
				return domain.Job{}, false, err
			}
		}
	}
	id, err := newJobID(ctx, "export.create", by, idemKey)
	if err != nil {
		return domain.Job{}, false, err
	}
	now := s.now()
	j := domain.Job{
		ID: id, Direction: domain.Export, ProjectID: in.ProjectID, Kind: f.Kind(), Format: f, Options: opts,
		Access: domain.Access{Actor: by}, State: domain.StateQueued, MaxAttempts: domain.MaxAttempts, AvailableAt: now,
		CreatedBy: by, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(s.cfg.Retention),
	}
	return s.insertJob(ctx, j)
}

// checkExportLocales refuses locales the project doesn't have (XLIFF
// targets can't be the source locale).
func (s *Service) checkExportLocales(ctx context.Context, p ProjectInfo, f domain.Format, locales []string) error {
	if len(locales) == 0 {
		return nil
	}
	targets, err := s.localization.Locales(ctx, p.ID)
	if err != nil {
		return err
	}
	for _, l := range locales {
		isTarget := slices.ContainsFunc(targets, func(t bcp47.Tag) bool { return t.String() == l })
		switch {
		case isTarget:
		case l == p.SourceLocale.String() && f == domain.FormatXLIFF:
			return fmt.Errorf("%w: %s is the source locale; an XLIFF export without locales carries the source", domain.ErrInvalidOptions, l)
		case l != p.SourceLocale.String():
			return fmt.Errorf("%w: %s", ErrLocaleNotFound, l)
		}
	}
	return nil
}

// OpenExport opens a finished export's file for download. Needs
// integration.read.
func (s *Service) OpenExport(ctx context.Context, id uuid.UUID) (io.ReadCloser, domain.Job, error) {
	j, err := s.getJob(ctx, id, domain.Export)
	if err != nil {
		return nil, domain.Job{}, err
	}
	switch {
	case j.State != domain.StateSucceeded || j.File == nil:
		return nil, j, domain.ErrNotReady
	case j.FilesDeletedAt != nil:
		return nil, j, domain.ErrFileExpired
	}
	rc, err := s.objects.Open(ctx, j.File.Key)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return nil, j, domain.ErrFileExpired
	case err != nil:
		return nil, j, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return rc, j, nil
}
