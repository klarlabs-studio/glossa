// Command glossa-server is Glossa's control plane (RFC 0002 §3). This
// file is the composition root and nothing else: it loads config, builds
// telemetry, opens the database, optionally migrates, and runs the HTTP
// server and the outbox dispatcher until SIGINT/SIGTERM, then drains.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	integrationapp "go.klarlabs.de/glossa/platform/internal/integration/app"
	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/buildinfo"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/httpserver"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "glossa-server:", err)
		os.Exit(1)
	}
}

// run is main without process globals, so tests can drive it.
func run(ctx context.Context, args []string, lookup config.LookupFunc, stdout io.Writer) error {
	lookup, showVersion, err := parseFlags(args, lookup)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if showVersion {
		_, err := fmt.Fprintln(stdout, version())
		return err
	}
	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	logger := observability.NewLogger(stdout, cfg.LogLevel, tenantAttrs)
	logger.InfoContext(ctx, "glossa-server starting", slog.String("version", version()), slog.String("revision", buildinfo.Revision()), slog.String("config", cfg.String()))

	if cfg.Migrate != config.MigrateOff {
		if err := migrate(ctx, cfg, logger); err != nil || cfg.Migrate == config.MigrateOnly {
			return err
		}
	}
	return serve(ctx, cfg, logger, lookup)
}

// parseFlags lets -migrate override GLOSSA_MIGRATE, so the same image
// runs as a migration Job (`-migrate=only`) or a server.
func parseFlags(args []string, lookup config.LookupFunc) (config.LookupFunc, bool, error) {
	fs := flag.NewFlagSet("glossa-server", flag.ContinueOnError)
	migrateMode := fs.String("migrate", "", "off | up | only (overrides GLOSSA_MIGRATE)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return nil, false, err
	}
	if *migrateMode == "" {
		return lookup, *showVersion, nil
	}
	return func(key string) (string, bool) {
		if key == "GLOSSA_MIGRATE" {
			return *migrateMode, true
		}
		return lookup(key)
	}, *showVersion, nil
}

func migrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	m, err := dbRetry(cfg, logger).NewMigrator(ctx, cfg.MigrationDatabaseURL.Reveal(), logger)
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	if err := m.Up(ctx); err != nil {
		return err
	}
	v, dirty, err := m.Version()
	logger.InfoContext(ctx, "migrations applied", slog.Uint64("version", uint64(v)), slog.Bool("dirty", dirty))
	return err
}

// dbRetry retries the first database connection for
// GLOSSA_DB_CONNECT_TIMEOUT, so neither the server nor -migrate=only
// fails on a database that is not reachable yet (#66).
func dbRetry(cfg config.Config, logger *slog.Logger) db.Retry {
	return db.Retry{Deadline: cfg.DBConnectTimeout, Logger: logger}
}

// app is everything serve starts and stops.
type app struct {
	cfg        config.Config
	logger     *slog.Logger
	pool       *pgxpool.Pool
	server     *httpserver.Server
	dispatcher *outbox.Dispatcher
	// aiWorker runs Intelligence's translation jobs; nil when disabled.
	aiWorker *intelligenceapp.Worker
	// integrationWorker runs import and export jobs; nil when disabled.
	integrationWorker *integrationapp.Worker
	// keyIndexes is Release's key index task (see contexts).
	keyIndexes func(context.Context) (int, error)
	// purger runs the daily retention jobs; nil when disabled.
	purger *scheduler.Scheduler
	// workflowTimers raises workflow timers every minute (RFC 0006
	// §2.3); nil where the leased periodic jobs don't run.
	workflowTimers *scheduler.Scheduler
	// rolloutSweep aborts staged rollouts past their max_duration
	// (RFC 0006 §5.2); nil where the leased periodic jobs don't run.
	rolloutSweep *scheduler.Scheduler
	// branchPublisher publishes due branch environments; nil when the
	// publisher is off.
	branchPublisher *releaseapp.Publisher
	// githubInbox drains the GitHub webhook inbox; nil when the
	// deployment has no GitHub App or the inbox worker is off.
	githubInbox *integrationapp.InboxWorker
	// githubChecks renders pull requests' Glossa checks; nil when the
	// deployment has no GitHub App or the check worker is off.
	githubChecks *integrationapp.CheckWorker
	// audit is the Audit context; its backfill runs once at startup.
	audit *auditapp.Service
	// auditExports runs audit export jobs and their retention; nil
	// unless GLOSSA_AUDIT_EXPORTS_ENABLED.
	auditExports *auditapp.ExportWorker
	// identity is waited on at shutdown for the failed sign-ins it is
	// still recording in the background.
	identity   *identityapp.Service
	shutdownTP observability.ShutdownFunc
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, lookup config.LookupFunc) error {
	a, err := build(ctx, cfg, logger, lookup)
	if err != nil {
		return err
	}
	defer a.pool.Close()
	return a.run(ctx)
}

// buildOption adjusts the bounded contexts build wires before Identity
// is handed what it needs from them — a port another context provides,
// such as the authz.Coverage Workflow's assignments implement.
type buildOption func(*contexts)

func build(ctx context.Context, cfg config.Config, logger *slog.Logger, lookup config.LookupFunc, opts ...buildOption) (*app, error) {
	auditKeys, err := newAuditKeys(cfg)
	if err != nil {
		return nil, err
	}
	tp, shutdownTP, err := observability.NewTracerProvider(ctx, cfg.OTel, version())
	if err != nil {
		return nil, err
	}
	pool, err := dbRetry(cfg, logger).OpenPool(ctx, cfg.DatabaseURL.Reveal(), "glossa-server")
	if err != nil {
		return nil, err
	}
	if err := db.VerifyAppRole(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	registry := observability.NewRegistry()
	events := outbox.NewRegistry() // bounded contexts Subscribe here
	dispatcher, err := newDispatcher(cfg, logger, tp, registry, pool, events)
	if err != nil {
		pool.Close()
		return nil, err
	}
	audit, err := newAudit(pool, events, auditKeys, logger, registry)
	if err != nil {
		pool.Close()
		return nil, err
	}
	identity, identitySvc, err := newIdentity(cfg.Identity, logger, pool, audit)
	if err != nil {
		pool.Close()
		return nil, err
	}
	bounded, err := buildContexts(cfg, logger, pool, events, registry, tp, lookup, identitySvc)
	if err != nil {
		pool.Close()
		return nil, err
	}
	// Audit's HTTP edge (RFC 0006 §6.2, §7.2): the trail's entries, its
	// export jobs (on the contexts' object storage) and the v0.3 history
	// import. Audit is built before the other contexts, so it meets the
	// object store here.
	var auditExports *auditapp.ExportWorker
	bounded.auditAPI, auditExports = newAuditAPI(cfg.Audit, pool, audit, bounded.objects, logger)
	if bounded.auditKeys, err = auditKeysHandler(auditKeys); err != nil {
		pool.Close()
		return nil, err
	}
	for _, o := range opts {
		o(&bounded)
	}
	// Identity is built first, and the GitHub Actions OIDC exchange is
	// the one thing it needs from a later context: Integration's Git
	// connections say which tenant a verified repository belongs to
	// (RFC 0004 §6.3). Without a GitHub App this stays zero and the
	// exchange answers `github_not_configured`.
	identitySvc.SetGitHubOIDC(bounded.ciAuth)
	// The other thing Identity needs from a later context: which units a
	// member whose visibility is `assigned` may see (RFC 0006 §3.3),
	// from Workflow's assignments. Nil means they see nothing.
	identitySvc.SetCoverage(bounded.coverage)
	purger, err := newPurger(cfg.Purge, logger, registry, pool, bounded.purgeJobs)
	if err != nil {
		pool.Close()
		return nil, err
	}
	workflowTimers, err := newWorkflowTimers(cfg.Purge, logger, registry, pool, bounded.workflowRuntime.runner)
	if err != nil {
		pool.Close()
		return nil, err
	}
	rolloutSweep, err := newRolloutSweep(cfg.Purge, logger, registry, pool, bounded.rolloutSweeper)
	if err != nil {
		pool.Close()
		return nil, err
	}
	mcpHandler, err := newMCP(cfg.MCP, identitySvc, pool, bounded.mcpTools, audit, registry, tp, logger)
	if err != nil {
		pool.Close()
		return nil, err
	}
	server := httpserver.New(cfg.HTTP, httpserver.Deps{
		Logger:         logger,
		TracerProvider: tp,
		Registry:       registry,
		Readiness:      []httpserver.Check{{Name: "postgres", Probe: pool.Ping}},
		Routes:         apiRoutes(identity, &metaAPI{signIn: identitySvc, edgeURL: cfg.Release.EdgePublicURL}, bounded, mcpHandler),
		LargeBodies:    largeBodies(cfg.Integration),
	})
	return &app{
		cfg: cfg, logger: logger, pool: pool, server: server, dispatcher: dispatcher, aiWorker: bounded.aiWorker,
		integrationWorker: bounded.integrationWorker, keyIndexes: bounded.keyIndexes, purger: purger, workflowTimers: workflowTimers,
		rolloutSweep:    rolloutSweep,
		branchPublisher: bounded.branchPublisher, githubInbox: bounded.githubInbox,
		githubChecks: bounded.githubChecks, audit: audit, auditExports: auditExports, identity: identitySvc, shutdownTP: shutdownTP,
	}, nil
}

// newPurger wires the daily retention scheduler on the Postgres lease,
// so one replica leads each run (RFC 0004 §2.3). It returns nil when
// the purge is disabled.
func newPurger(
	cfg config.Purge, logger *slog.Logger, reg prometheus.Registerer, pool *pgxpool.Pool, jobs []scheduler.Job,
) (*scheduler.Scheduler, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	s, err := scheduler.New(scheduler.NewPostgresLease(db.NewUnitOfWork(pool)), scheduler.Config{
		Interval: cfg.Interval, Timeout: cfg.Timeout, Lease: cfg.Lease, Poll: cfg.PollInterval, Jitter: cfg.Jitter,
	}, scheduler.WithLogger(logger), scheduler.WithMetrics(scheduler.NewMetrics(reg)))
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		s.Add(j)
	}
	return s, nil
}

func newDispatcher(
	cfg config.Config, logger *slog.Logger, tp trace.TracerProvider,
	reg prometheus.Registerer, pool *pgxpool.Pool, events *outbox.Registry,
) (*outbox.Dispatcher, error) {
	store := outbox.NewPostgresStore(db.NewUnitOfWork(pool))
	return outbox.NewDispatcher(store, events, outbox.DispatcherConfig{
		BatchSize:      cfg.Outbox.BatchSize,
		MaxAttempts:    cfg.Outbox.MaxAttempts,
		PollInterval:   cfg.Outbox.PollInterval,
		Lease:          cfg.Outbox.Lease,
		HandlerTimeout: cfg.Outbox.HandlerTimeout,
	}, outbox.DispatcherOptions{Logger: logger, TracerProvider: tp, Registerer: reg})
}

// run serves until ctx is cancelled or a component fails, then shuts
// down in order: stop taking traffic and drain requests, stop the
// dispatcher (unstarted claims are released), flush traces.
func (a *app) run(ctx context.Context) error {
	ln, err := a.server.Listen()
	if err != nil {
		return err
	}
	a.logger.InfoContext(ctx, "listening", slog.String("addr", ln.Addr().String()))

	errc := make(chan error, 2)
	go func() { errc <- a.server.Serve(ln) }()
	dispatchCtx, stopDispatch := context.WithCancel(context.WithoutCancel(ctx))
	defer stopDispatch()
	dispatched := a.startDispatcher(dispatchCtx, errc)
	worked := a.startWorker(dispatchCtx)
	moved := a.startIntegrationWorker(dispatchCtx)
	exported := a.startAuditExports(dispatchCtx)
	purged := a.startPurger(dispatchCtx)
	published := a.startBranchPublisher(dispatchCtx)
	delivered := a.startGitHubInbox(dispatchCtx)
	checked := a.startGitHubChecks(dispatchCtx)
	a.startKeyIndexTask(dispatchCtx)
	a.startAuditBackfill(dispatchCtx)

	var runErr error
	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received")
	case runErr = <-errc:
		a.logger.Error("component failed; shutting down", slog.Any("error", runErr))
	}
	return errors.Join(runErr, a.shutdown(stopDispatch, dispatched, worked, moved, exported, purged, published, delivered, checked))
}

// startPurger runs the daily retention jobs until ctx ends; a run in
// progress finishes first, bounded by its timeout, and gives its lease
// back so the next replica isn't blocked.
//
// The workflow timer sweep and the rollout sweep run beside it on
// their own schedules, and are waited for with it.
func (a *app) startPurger(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, s := range []*scheduler.Scheduler{a.purger, a.workflowTimers, a.rolloutSweep} {
		if s == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Run(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

// keyIndexRetry is how long the key index task waits after a failure.
const keyIndexRetry = time.Minute

// startKeyIndexTask runs Release's key index task in the background
// until it succeeds once (every replica runs it; it is idempotent).
// Until it has, the edge reads an index object without a scope as the
// default environments, the scope those keys migrated to.
func (a *app) startKeyIndexTask(ctx context.Context) {
	if a.keyIndexes == nil {
		return
	}
	go func() {
		for {
			n, err := a.keyIndexes(ctx)
			if err == nil {
				if n > 0 {
					a.logger.InfoContext(ctx, "release: delivery key index objects rewritten", slog.Int("keys", n))
				}
				return
			}
			if ctx.Err() != nil {
				return
			}
			a.logger.WarnContext(ctx, "release: delivery key index objects not rewritten yet; retrying",
				slog.Any("error", err), slog.Duration("retry_in", keyIndexRetry))
			select {
			case <-ctx.Done():
				return
			case <-time.After(keyIndexRetry):
			}
		}
	}()
}

// startBranchPublisher publishes due branch environments until ctx
// ends. It is idempotent and safe to run on every replica: a publish is
// keyed by its request. (The proposal sweep is not here — it is a
// leased scheduler job, so one replica leads it.)
func (a *app) startBranchPublisher(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if a.branchPublisher == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = a.branchPublisher.Run(ctx)
	}()
	return done
}

// startIntegrationWorker runs the import and export workers until ctx
// ends; a job in progress finishes its current attempt first (bounded
// by its timeout).
func (a *app) startIntegrationWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if a.integrationWorker == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = a.integrationWorker.Run(ctx)
	}()
	return done
}

// startGitHubInbox drains the GitHub webhook inbox until ctx ends; a
// delivery in progress finishes its current attempt first (bounded by
// its timeout), and an unfinished one is claimed again when its lease
// runs out.
func (a *app) startGitHubInbox(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if a.githubInbox == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = a.githubInbox.Run(ctx)
	}()
	return done
}

// startGitHubChecks renders pull requests' Glossa checks until ctx
// ends; a check in progress finishes its current attempt first, and an
// unfinished one is claimed again when its lease runs out.
func (a *app) startGitHubChecks(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if a.githubChecks == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = a.githubChecks.Run(ctx)
	}()
	return done
}

// startWorker runs Intelligence's job workers until ctx ends; a job in
// progress finishes first (bounded by its timeout).
func (a *app) startWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if a.aiWorker == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = a.aiWorker.Run(ctx)
	}()
	return done
}

func (a *app) startDispatcher(ctx context.Context, errc chan<- error) <-chan struct{} {
	done := make(chan struct{})
	if !a.cfg.Outbox.Enabled {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		if err := a.dispatcher.Run(ctx); err != nil {
			errc <- err
		}
	}()
	return done
}

func (a *app) shutdown(stopDispatch context.CancelFunc,
	dispatched, worked, moved, exported, purged, published, delivered, checked <-chan struct{},
) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	var errs []error
	if err := a.server.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if a.identity != nil {
		audited := make(chan struct{})
		go func() { a.identity.WaitAudits(); close(audited) }()
		select {
		case <-audited:
		case <-ctx.Done():
			errs = append(errs, errors.New("failed sign-ins were still being audited at the shutdown timeout; those entries are lost"))
		}
	}
	stopDispatch()
	select {
	case <-dispatched:
	case <-ctx.Done():
		errs = append(errs, errors.New("outbox dispatcher did not stop before the shutdown timeout"))
	}
	select {
	case <-worked:
	case <-ctx.Done():
		errs = append(errs, errors.New("AI job workers did not stop before the shutdown timeout; their jobs are claimed again when the lease ends"))
	}
	select {
	case <-moved:
	case <-ctx.Done():
		errs = append(errs, errors.New("import/export workers did not stop before the shutdown timeout; their jobs resume from their last checkpoint when the lease ends"))
	}
	select {
	case <-exported:
	case <-ctx.Done():
		errs = append(errs, errors.New("the audit export worker did not stop before the shutdown timeout; its job is claimed again when the lease ends"))
	}
	select {
	case <-purged:
	case <-ctx.Done():
		errs = append(errs, errors.New("the retention job did not stop before the shutdown timeout; its lease ends on its own and another replica purges at the next interval"))
	}
	select {
	case <-published:
	case <-ctx.Done():
		errs = append(errs, errors.New("the branch publisher did not stop before the shutdown timeout; another replica, or its next pass, picks the due publishes up"))
	}
	select {
	case <-delivered:
	case <-ctx.Done():
		errs = append(errs, errors.New("the GitHub inbox worker did not stop before the shutdown timeout; its claimed deliveries are claimed again when the lease ends"))
	}
	select {
	case <-checked:
	case <-ctx.Done():
		errs = append(errs, errors.New("the GitHub check worker did not stop before the shutdown timeout; its claimed checks are claimed again when the lease ends"))
	}
	if err := a.shutdownTP(ctx); err != nil {
		errs = append(errs, fmt.Errorf("flush traces: %w", err))
	}
	a.logger.Info("glossa-server stopped")
	return errors.Join(errs...)
}

func tenantAttrs(ctx context.Context) []slog.Attr {
	if id, ok := tenancy.FromContext(ctx); ok {
		return []slog.Attr{slog.String("tenant_id", id.String())}
	}
	return nil
}

// version is the build's release version (see kernel/buildinfo).
func version() string { return buildinfo.Version() }
