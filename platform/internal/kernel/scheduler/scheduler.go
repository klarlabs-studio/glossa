// Package scheduler runs periodic jobs in glossa-server, once per
// deployment rather than once per replica: a job takes a named lease
// before it runs, the way a worker claims a job, so a rolling upgrade
// or three replicas still purge once (RFC 0004 §2.3, "a purge job runs
// daily").
//
// The lease also carries when the job last finished, so due-ness
// survives restarts: a replica that starts a minute after a purge does
// not purge again.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"time"
)

// Lease is a deployment-wide mutual exclusion for a named periodic job.
type Lease interface {
	// Acquire takes name's lease for holder until now+ttl, when nothing
	// holds it and the job's last run finished at or before now−interval.
	// ok is false when another holder has it or the job isn't due yet.
	Acquire(ctx context.Context, name, holder string, interval, ttl time.Duration) (ok bool, err error)
	// Release ends holder's lease on name and records the run, so the
	// next one is due an interval later. It is a no-op for a lease
	// holder no longer holds.
	Release(ctx context.Context, name, holder string) error
}

// Job is one periodic task. Name is its lease name and its metric
// label: a lowercase dotted identifier such as "context.purge".
type Job struct {
	Name string
	Run  func(context.Context) error
}

// Config tunes the scheduler. Every field is required.
type Config struct {
	// Interval is how often a job runs across the whole deployment.
	Interval time.Duration
	// Timeout bounds one run; it must fit inside Interval.
	Timeout time.Duration
	// Lease is how long a run reserves its job. It must exceed Timeout,
	// so a replica that dies mid-run doesn't block the next one for
	// longer than the lease.
	Lease time.Duration
	// Poll is how often this replica asks whether a job is due. It must
	// not exceed Interval, or a job would be missed.
	Poll time.Duration
	// Jitter spreads each poll by up to this fraction of Poll, so
	// replicas started together don't all ask at the same instant
	// (0: none, 1: up to a full extra poll).
	Jitter float64
}

func (c Config) validate() error {
	var errs []error
	if c.Interval <= 0 || c.Timeout <= 0 || c.Poll <= 0 {
		errs = append(errs, errors.New("Interval, Timeout and Poll must be positive"))
	}
	if c.Lease <= c.Timeout {
		errs = append(errs, errors.New("Lease must be longer than Timeout"))
	}
	if c.Timeout > c.Interval {
		errs = append(errs, errors.New("Timeout must fit inside Interval"))
	}
	if c.Poll > c.Interval {
		errs = append(errs, errors.New("Poll must not exceed Interval"))
	}
	if c.Jitter < 0 || c.Jitter > 1 {
		errs = append(errs, errors.New("Jitter must be between 0 and 1"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("scheduler: invalid config: %w", errors.Join(errs...))
	}
	return nil
}

// Metrics records runs (RFC 0004 §11).
type Metrics interface {
	// Ran reports one completed run of job: how long it took and
	// whether it failed.
	Ran(job string, d time.Duration, err error)
	// Skipped reports a poll where the job was not due, or another
	// replica held its lease.
	Skipped(job string)
}

// NoMetrics records nothing.
type NoMetrics struct{}

// Ran implements Metrics.
func (NoMetrics) Ran(string, time.Duration, error) {}

// Skipped implements Metrics.
func (NoMetrics) Skipped(string) {}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithLogger sets the logger (discarding by default).
func WithLogger(l *slog.Logger) Option { return func(s *Scheduler) { s.logger = l } }

// WithMetrics records runs.
func WithMetrics(m Metrics) Option { return func(s *Scheduler) { s.metrics = m } }

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Scheduler) { s.now = now } }

// WithHolder names this replica in the lease (tests, and deployments
// that want a readable holder). The default is host/pid.
func WithHolder(holder string) Option { return func(s *Scheduler) { s.holder = holder } }

// Scheduler runs its jobs on Config's interval, one deployment-wide run
// at a time per job.
type Scheduler struct {
	lease   Lease
	cfg     Config
	jobs    []Job
	holder  string
	logger  *slog.Logger
	metrics Metrics
	now     func() time.Time
}

// leaseName is what a job may be called: the same shape as a database
// system scope, because the name is a lease row's primary key.
var leaseName = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,62}$`)

// New validates cfg and returns a scheduler with no jobs yet.
func New(lease Lease, cfg Config, opts ...Option) (*Scheduler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Scheduler{
		lease: lease, cfg: cfg, holder: defaultHolder(),
		logger: slog.New(slog.DiscardHandler), metrics: NoMetrics{}, now: func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func defaultHolder() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return host + "/" + strconv.Itoa(os.Getpid())
}

// Add registers a job. Call it before Run.
func (s *Scheduler) Add(j Job) { s.jobs = append(s.jobs, j) }

// Jobs reports how many jobs are registered.
func (s *Scheduler) Jobs() int { return len(s.jobs) }

// Run polls until ctx is cancelled. A run in progress finishes first,
// bounded by Timeout. It never returns an error: a job's failure is
// logged, counted and retried at the next interval, because a purge
// that can't reach storage must not take the server down.
func (s *Scheduler) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "scheduler: periodic job", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.wait()):
		}
	}
	return nil
}

// wait is one poll interval plus up to Jitter of it.
func (s *Scheduler) wait() time.Duration {
	if s.cfg.Jitter == 0 {
		return s.cfg.Poll
	}
	return s.cfg.Poll + time.Duration(rand.Float64()*s.cfg.Jitter*float64(s.cfg.Poll)) //nolint:gosec // spreading polls, not a secret
}

// RunOnce runs every job that is due and that this replica takes the
// lease for, and reports how many it ran. A job's failure doesn't stop
// the others; their errors are joined. Tests and operators drive a run
// with it.
func (s *Scheduler) RunOnce(ctx context.Context) (int, error) {
	var (
		ran  int
		errs []error
	)
	for _, j := range s.jobs {
		worked, err := s.runJob(ctx, j)
		if worked {
			ran++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return ran, errors.Join(errs...)
}

// runJob takes j's lease and runs it, then releases the lease whatever
// happened: a failed run is retried at the next interval, not hammered.
func (s *Scheduler) runJob(ctx context.Context, j Job) (bool, error) {
	if !leaseName.MatchString(j.Name) {
		return false, fmt.Errorf("scheduler: invalid job name %q", j.Name)
	}
	ok, err := s.lease.Acquire(ctx, j.Name, s.holder, s.cfg.Interval, s.cfg.Lease)
	if err != nil {
		return false, fmt.Errorf("scheduler: take the lease for %s: %w", j.Name, err)
	}
	if !ok {
		s.metrics.Skipped(j.Name)
		return false, nil
	}
	started := s.now()
	runErr := s.invoke(ctx, j)
	s.metrics.Ran(j.Name, s.now().Sub(started), runErr)
	// The release runs even when ctx is done, so the lease isn't held
	// until it expires after a shutdown mid-run.
	relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.lease.Release(relCtx, j.Name, s.holder); err != nil {
		return true, errors.Join(runErr, fmt.Errorf("scheduler: release the lease for %s: %w", j.Name, err))
	}
	if runErr != nil {
		return true, fmt.Errorf("scheduler: %s: %w", j.Name, runErr)
	}
	s.logger.InfoContext(ctx, "scheduler: job ran", slog.String("job", j.Name),
		slog.Duration("took", s.now().Sub(started)))
	return true, nil
}

// releaseTimeout bounds giving the lease back during a shutdown.
const releaseTimeout = 10 * time.Second

// invoke runs j under Timeout, turning a panic into an error so one
// job can't take the server down.
func (s *Scheduler) invoke(ctx context.Context, j Job) (err error) {
	// The run finishes even when shutdown starts; Timeout bounds it.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(ctx, "scheduler: job panicked", slog.String("job", j.Name),
				slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
			err = fmt.Errorf("scheduler: job %s panicked: %v", j.Name, r)
		}
	}()
	return j.Run(runCtx)
}
