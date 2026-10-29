package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// ExportWorkerConfig tunes the export workers in glossa-server.
type ExportWorkerConfig struct {
	// PollInterval is how long an idle worker waits before looking again.
	PollInterval time.Duration
	// JobTimeout bounds one attempt; Lease (longer) is how long a claimed
	// job is reserved before another worker takes it over.
	JobTimeout time.Duration
	Lease      time.Duration
	// SweepInterval is how often retention deletes expired objects.
	SweepInterval time.Duration
}

func (c *ExportWorkerConfig) defaults() {
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

// ExportWorker claims export jobs across tenants and runs each in its
// tenant's scope as the background principal audit.exporter, which
// holds no permission: it reads the chain through the store, not
// through a use case. It also deletes expired exports' objects.
type ExportWorker struct {
	s       *ExportService
	claimer ExportClaimer
	cfg     ExportWorkerConfig
}

// NewExportWorker returns a worker.
func NewExportWorker(s *ExportService, claimer ExportClaimer, cfg ExportWorkerConfig) *ExportWorker {
	cfg.defaults()
	return &ExportWorker{s: s, claimer: claimer, cfg: cfg}
}

// Run works until ctx is cancelled; a job in progress finishes its
// current attempt first (bounded by JobTimeout).
func (w *ExportWorker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { w.loop(ctx) })
	wg.Go(func() { w.sweeper(ctx) })
	wg.Wait()
}

func (w *ExportWorker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.RunOnce(ctx)
		if err != nil {
			w.s.logger.ErrorContext(ctx, "audit: export worker", slog.Any("error", err))
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

func (w *ExportWorker) sweeper(ctx context.Context) {
	t := time.NewTicker(w.cfg.SweepInterval)
	defer t.Stop()
	for {
		if err := w.Sweep(ctx); err != nil && ctx.Err() == nil {
			w.s.logger.ErrorContext(ctx, "audit: export retention sweep", slog.Any("error", err))
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
func (w *ExportWorker) RunOnce(ctx context.Context) (worked bool, err error) {
	c, ok, err := w.claimer.Claim(ctx, w.cfg.Lease)
	if err != nil || !ok {
		return false, err
	}
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.JobTimeout)
	defer cancel()
	jctx = tenancy.ContextWithTenant(jctx, tenancy.ID(c.TenantID))
	bg, err := authz.Background(jctx, ExporterPrincipal)
	if err != nil {
		return true, err
	}
	return true, w.s.run(bg, c)
}

// Sweep applies retention: the objects of exports past their expiry are
// deleted from object storage; the jobs stay, as the record that the
// export was made.
func (w *ExportWorker) Sweep(ctx context.Context) error {
	const batch = 100
	for {
		expired, err := w.claimer.Expired(ctx, batch)
		if err != nil || len(expired) == 0 {
			return err
		}
		var done []uuid.UUID
	next:
		for _, e := range expired {
			for _, key := range e.Keys {
				if err := w.s.objects.Delete(ctx, key); err != nil {
					w.s.logger.WarnContext(ctx, "audit: delete an expired export object",
						slog.String("job_id", e.JobID.String()), slog.Any("error", err))
					continue next
				}
			}
			done = append(done, e.JobID)
		}
		if len(done) == 0 {
			return nil
		}
		if err := w.claimer.MarkDeleted(ctx, done); err != nil {
			return err
		}
		if len(expired) < batch {
			return nil
		}
	}
}
