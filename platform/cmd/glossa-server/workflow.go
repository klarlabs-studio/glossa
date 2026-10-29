package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	workflowcatalog "go.klarlabs.de/glossa/platform/internal/workflow/adapters/catalog"
	workflowidentity "go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	workflowmetrics "go.klarlabs.de/glossa/platform/internal/workflow/adapters/metrics"
	workflowpg "go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	workflowrelease "go.klarlabs.de/glossa/platform/internal/workflow/adapters/release"
	workflowsources "go.klarlabs.de/glossa/platform/internal/workflow/adapters/sources"
	workflowapp "go.klarlabs.de/glossa/platform/internal/workflow/app"
)

// workflowServices are the Workflow context's runtime (RFC 0006 §2.5,
// §3): the instance runner, the instance store the Workflow API reads,
// and assignments and approvals.
type workflowServices struct {
	runner    *workflowapp.Runner
	instances *workflowpg.Instances
	work      *workflowapp.WorkService
	// coverage is assignments as the read port every context's read
	// path filters an `assigned` member through (authz.Coverage).
	coverage *workflowapp.Coverage
}

// workflowSources are the contexts the runner reads and acts through.
type workflowSources struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	quality      *qualityapp.Service
	intelligence *intelligenceapp.Service
	identity     *identityapp.Service
	release      *releaseapp.Service
}

// newWorkflow builds Workflow and subscribes its instance runner to the
// vocabulary events (RFC 0006 §2.4–§2.5). The runner acts on the other
// contexts only through their application services, as the actor whose
// event moved an instance, so each service's own permission check
// decides.
func newWorkflow(
	uow *db.UnitOfWork, events *outbox.Registry, definitions *workflowapp.Service, src workflowSources, logger *slog.Logger,
	reg prometheus.Registerer,
) (workflowServices, error) {
	instances := workflowpg.NewInstances(uow)
	// RFC 0006 §10.1: transitions, instances, live assignments and
	// approval decisions, on the server's registry.
	metrics := workflowmetrics.New(reg)
	// Assignments and approvals (RFC 0006 §3.1–§3.2). Who a member is
	// comes from Identity's tenant store, joined inside the caller's
	// transaction (reads only: no TOTP secret is ever opened there, so
	// the store needs no cipher); four-eyes reads the author of the text
	// through Localization.
	workTx := workflowpg.NewWorkTransactor(uow)
	directory := workflowidentity.NewDirectory(identitypg.NewTransactor(uow, nil))
	//
	// Release requests (RFC 0006 §5.1) are a subject too: Release says
	// where a request is and who asked (approvals.decide is checked in
	// its environment, four-eyes against its requester), and the runner
	// deploys or denies it through Release's service. Release in turn
	// asks Workflow who approved, and checks the requirement itself
	// before it moves a pointer; until UseApprovals it deploys nothing.
	var requests *workflowrelease.Requests
	workOpts := []workflowapp.WorkOption{
		workflowapp.WithWorkMetrics(metrics),
		// Per-vendor quality numbers (RFC 0006 §3.4), computed on read
		// from Catalog, Localization and Quality as the caller.
		workflowapp.WithQualityFacts(workflowsources.NewQualityFacts(src.catalog, src.localization, src.quality)),
	}
	if src.release != nil {
		requests = workflowrelease.NewRequests(src.release)
		workOpts = append(workOpts, workflowapp.WithReleaseRequests(requests))
	}
	// A request made through the API names its units by message key,
	// resolved through Catalog as the caller.
	work := workflowapp.NewWorkService(workTx, directory,
		workflowsources.NewAuthors(src.catalog, src.localization),
		append(workOpts, workflowapp.WithWorkCatalog(workflowcatalog.New(src.catalog)))...)
	if src.release != nil {
		src.release.UseApprovals(workflowrelease.NewLedger(work))
	}
	// The same assignments, read as coverage: which units a member with
	// visibility `assigned` may see (RFC 0006 §3.3).
	coverage := workflowapp.NewCoverage(workTx, directory, nil)
	deps := workflowapp.RunnerDeps{
		Tx: instances, Definitions: definitions, Timers: instances, Retention: instances, Logger: logger,
		Metrics: metrics, Workload: instances,
		Translations: workflowsources.NewTranslations(src.catalog, src.localization),
		Findings:     workflowsources.NewFindings(src.quality),
		Suggestions:  workflowsources.NewSuggestions(src.intelligence),
		// assign and request_approval join the step's transaction.
		Assignments: work,
	}
	if requests != nil {
		deps.Releases = requests
	}
	if src.identity != nil {
		deps.Actors = workflowidentity.NewActors(src.identity)
	}
	runner := workflowapp.NewRunner(deps)
	if err := runner.Subscribe(events); err != nil {
		return workflowServices{}, err
	}
	return workflowServices{runner: runner, instances: instances, work: work, coverage: coverage}, nil
}

// The timer sweep's cadence (RFC 0006 §2.3). Due periods are written in
// minutes at the finest, so a minute is as late as a timer is raised.
const (
	workflowTimerInterval = time.Minute
	workflowTimerTimeout  = 45 * time.Second
	workflowTimerLease    = 55 * time.Second
	workflowTimerPoll     = 15 * time.Second
)

// workflowRetentionJob is the daily workflow.retention job, one of the
// leased Purge jobs (RFC 0006 §2.5, §13 wave 6): it deletes the
// instances that finished more than keep ago, with their transition
// logs, and never a running one.
func workflowRetentionJob(runner *workflowapp.Runner, keep time.Duration, logger *slog.Logger) scheduler.Job {
	return scheduler.Job{Name: "workflow.retention", Run: func(ctx context.Context) error {
		n, err := runner.SweepRetention(ctx, keep)
		if n > 0 {
			logger.InfoContext(ctx, "workflow: finished instances past their retention deleted",
				slog.Int("instances", n), slog.Duration("retention", keep))
		}
		return err
	}}
}

// newWorkflowTimers schedules the timer sweep on the Postgres lease so
// one replica raises each timer. It runs where the leased periodic jobs
// run (GLOSSA_PURGE_ENABLED), and nil means it does not run here.
func newWorkflowTimers(
	cfg config.Purge, logger *slog.Logger, reg prometheus.Registerer, pool *pgxpool.Pool, runner *workflowapp.Runner,
) (*scheduler.Scheduler, error) {
	if !cfg.Enabled || runner == nil {
		return nil, nil
	}
	s, err := scheduler.New(scheduler.NewPostgresLease(db.NewUnitOfWork(pool)), scheduler.Config{
		Interval: workflowTimerInterval, Timeout: workflowTimerTimeout, Lease: workflowTimerLease,
		Poll: workflowTimerPoll, Jitter: cfg.Jitter,
	}, scheduler.WithLogger(logger), scheduler.WithMetrics(scheduler.NewMetrics(reg)))
	if err != nil {
		return nil, err
	}
	s.Add(scheduler.Job{Name: "workflow.timers", Run: func(ctx context.Context) error {
		n, err := runner.SweepTimers(ctx)
		if n > 0 {
			logger.InfoContext(ctx, "workflow: timers raised", slog.Int("timers", n))
		}
		// The §10.1 gauges are counted on the same lease, so one replica
		// reports them; a failed count leaves them as they were.
		if cerr := runner.CountWork(ctx); cerr != nil {
			logger.WarnContext(ctx, "workflow: instances and assignments not counted", slog.Any("error", cerr))
		}
		return err
	}})
	return s, nil
}
