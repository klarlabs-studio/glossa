package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// WorkerConfig tunes the job workers that run inside glossa-server.
type WorkerConfig struct {
	// Workers is the number of jobs this process runs at once.
	Workers int
	// PollInterval is how long an idle worker waits before looking again.
	PollInterval time.Duration
	// JobTimeout bounds one attempt of a job; Lease (longer) is how long
	// a claimed job is reserved before another worker takes it over.
	JobTimeout time.Duration
	Lease      time.Duration
	// SweepInterval is how often retention deletes expired files.
	SweepInterval time.Duration
}

func (c *WorkerConfig) defaults() {
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 30 * time.Minute
	}
	if c.Lease <= c.JobTimeout {
		c.Lease = c.JobTimeout + 5*time.Minute
	}
	if c.SweepInterval <= 0 {
		c.SweepInterval = 10 * time.Minute
	}
}

// Worker claims import and export jobs across tenants and runs each in
// its tenant's scope as the background principal integration.worker,
// within the access its requester had. It also sweeps expired files.
type Worker struct {
	s       *Service
	claimer Claimer
	cfg     WorkerConfig
}

// NewWorker returns a worker pool.
func NewWorker(s *Service, claimer Claimer, cfg WorkerConfig) *Worker {
	cfg.defaults()
	return &Worker{s: s, claimer: claimer, cfg: cfg}
}

// Run works until ctx is cancelled; a job in progress finishes its
// current attempt first (bounded by JobTimeout).
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for range w.cfg.Workers {
		wg.Go(func() { w.loop(ctx) })
	}
	wg.Go(func() { w.sweeper(ctx) })
	wg.Wait()
	return nil
}

func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.RunOnce(ctx)
		if err != nil {
			w.s.logger.ErrorContext(ctx, "integration: job worker", slog.Any("error", err))
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.cfg.PollInterval):
		}
	}
}

func (w *Worker) sweeper(ctx context.Context) {
	t := time.NewTicker(w.cfg.SweepInterval)
	defer t.Stop()
	for {
		if err := w.Sweep(ctx); err != nil && ctx.Err() == nil {
			w.s.logger.ErrorContext(ctx, "integration: retention sweep", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce claims one due job and runs it; worked is false when none was
// due. Tests drive the queue with it.
func (w *Worker) RunOnce(ctx context.Context) (worked bool, err error) {
	c, ok, err := w.claimer.Claim(ctx, w.cfg.Lease)
	if err != nil || !ok {
		return false, err
	}
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.JobTimeout)
	defer cancel()
	jctx = tenancy.ContextWithTenant(jctx, tenancy.ID(c.TenantID))
	bg, err := authz.Background(jctx, principalWorker,
		authz.CatalogRead, authz.CatalogWrite, authz.TranslationsRead, authz.TranslationsWrite, authz.TranslationsReview,
		authz.KnowledgeRead, authz.KnowledgeWrite)
	if err != nil {
		return true, err
	}
	return true, w.s.runJob(bg, c)
}

// Sweep applies retention: imports still waiting for their file after
// the upload window fail, and files past their job's expiry are deleted
// from object storage (the jobs and their results stay).
func (w *Worker) Sweep(ctx context.Context) error {
	if _, err := w.claimer.ExpireUploads(ctx, w.s.cfg.UploadWindow); err != nil {
		return err
	}
	for {
		files, err := w.claimer.ExpiredFiles(ctx, 100)
		if err != nil || len(files) == 0 {
			return err
		}
		var done []uuid.UUID
		for _, f := range files {
			if f.Key != "" {
				if err := w.s.objects.Delete(ctx, f.Key); err != nil {
					w.s.logger.WarnContext(ctx, "integration: delete expired file", slog.String("job_id", f.JobID.String()), slog.Any("error", err))
					continue
				}
			}
			done = append(done, f.JobID)
		}
		if len(done) == 0 {
			return nil
		}
		if err := w.claimer.MarkFilesDeleted(ctx, done); err != nil {
			return err
		}
		if len(files) < 100 {
			return nil
		}
	}
}

// errCancelled stops a running job whose cancellation was requested.
var errCancelled = errors.New("integration: cancelled")

// permanentError fails a job for good with a code (a malformed file, a
// catalog the format can't express); anything else is retried.
type permanentError struct {
	code    string
	message string
}

func (e *permanentError) Error() string { return e.code + ": " + e.message }

func permanent(code, format string, args ...any) error {
	return &permanentError{code: code, message: fmt.Sprintf(format, args...)}
}

// runJob executes a claimed job and records how it ended.
func (s *Service) runJob(ctx context.Context, c Claim) error {
	var j domain.Job
	if err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		j, err = st.Job(ctx, c.JobID)
		return err
	}); err != nil {
		return err
	}
	if j.StartedAt == nil {
		now := s.now()
		j.StartedAt = &now
	}
	j.State, j.Attempts = domain.StateRunning, c.Attempts
	var err error
	if c.Attempts > j.MaxAttempts {
		err = permanent(domain.FailureInternal, "the job was claimed %d times; its workers kept stopping before it finished", c.Attempts)
	} else if j.Direction == domain.Import {
		err = s.runImport(ctx, c, &j)
	} else {
		err = s.runExport(ctx, c, &j)
	}
	var perm *permanentError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrLeaseLost):
		s.logger.WarnContext(ctx, "integration: job lease lost; another worker has it", slog.String("job_id", j.ID.String()))
		return nil
	case errors.Is(err, errCancelled):
		return s.finish(ctx, c, &j, domain.StateCancelled, "", "")
	case errors.As(err, &perm):
		return s.finish(ctx, c, &j, domain.StateFailed, perm.code, perm.message)
	case c.Attempts < j.MaxAttempts:
		s.logger.WarnContext(ctx, "integration: job failed; retrying", slog.String("job_id", j.ID.String()), slog.Any("error", err))
		return s.retry(ctx, c, &j, err)
	}
	return s.finish(ctx, c, &j, domain.StateFailed, domain.FailureInternal,
		fmt.Sprintf("gave up after %d attempts: %v", c.Attempts, err))
}

// checkpoint records a batch's results and progress under the claim,
// and stops the job if its cancellation was requested.
func (s *Service) checkpoint(ctx context.Context, c Claim, j *domain.Job, items []domain.Item) error {
	j.UpdatedAt = s.now()
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, err := st.LockClaimedJob(ctx, c.JobID, c.Token)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			if err := st.PutItems(ctx, j.ID, items); err != nil {
				return err
			}
		}
		j.CancelRequested = cur.CancelRequested
		return st.SaveJob(ctx, *j)
	})
	if err == nil && j.CancelRequested {
		return errCancelled
	}
	return err
}

// finish ends a job under its claim and publishes its completion.
func (s *Service) finish(ctx context.Context, c Claim, j *domain.Job, state domain.State, code, message string) error {
	by, err := authz.EventActor(ctx) // the worker's background principal
	if err != nil {
		return err
	}
	now := s.now()
	j.State, j.FailureCode, j.FailureMessage, j.FinishedAt, j.UpdatedAt = state, code, truncate(message, 4000), &now, now
	if state == domain.StateSucceeded && j.TotalItems < j.ProcessedItems {
		j.TotalItems = j.ProcessedItems
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.LockClaimedJob(ctx, c.JobID, c.Token); err != nil {
			return err
		}
		if err := st.SaveJob(ctx, *j); err != nil {
			return err
		}
		return st.Publish(ctx, completedEvent(*j, by))
	})
	if errors.Is(err, ErrLeaseLost) {
		return nil
	}
	return err
}

// retry queues the job again with backoff; its progress is kept, so the
// next attempt resumes after the last checkpoint.
func (s *Service) retry(ctx context.Context, c Claim, j *domain.Job, cause error) error {
	j.FailureMessage, j.UpdatedAt = truncate(cause.Error(), 4000), s.now()
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.LockClaimedJob(ctx, c.JobID, c.Token); err != nil {
			return err
		}
		return st.RetryJob(ctx, *j, domain.Backoff(c.Attempts))
	})
	if errors.Is(err, ErrLeaseLost) {
		return nil
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
