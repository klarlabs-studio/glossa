package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	auditapi "go.klarlabs.de/glossa/platform/internal/audit/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/coverage"
	catalogapi "go.klarlabs.de/glossa/platform/internal/catalog/adapters/httpapi"
	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/projection"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	contextcatalog "go.klarlabs.de/glossa/platform/internal/context/adapters/catalog"
	contextapi "go.klarlabs.de/glossa/platform/internal/context/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/context/adapters/imaging"
	contextmetrics "go.klarlabs.de/glossa/platform/internal/context/adapters/metrics"
	contextpg "go.klarlabs.de/glossa/platform/internal/context/adapters/postgres"
	contextquality "go.klarlabs.de/glossa/platform/internal/context/adapters/quality"
	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	contextdomain "go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/ghoidc"
	identitymetrics "go.klarlabs.de/glossa/platform/internal/identity/adapters/metrics"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	identitysources "go.klarlabs.de/glossa/platform/internal/identity/adapters/sources"
	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
	integrationapi "go.klarlabs.de/glossa/platform/internal/integration/adapters/httpapi"
	integrationmetrics "go.klarlabs.de/glossa/platform/internal/integration/adapters/metrics"
	integrationpg "go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres"
	integrationsources "go.klarlabs.de/glossa/platform/internal/integration/adapters/sources"
	integrationapp "go.klarlabs.de/glossa/platform/internal/integration/app"
	intelligenceapi "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/httpapi"
	intelligencemetrics "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/metrics"
	intelligencepg "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/postgres"
	intelligenceproviders "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/providers"
	intelligencesources "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/sources"
	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/httpserver"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/configured"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/ratelimit"
	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler"
	"go.klarlabs.de/glossa/platform/internal/kernel/sealing"
	knowledgeapi "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/httpapi"
	knowledgepg "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/postgres"
	knowledgesources "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/sources"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationapi "go.klarlabs.de/glossa/platform/internal/localization/adapters/httpapi"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	mcpsources "go.klarlabs.de/glossa/platform/internal/mcp/adapters/sources"
	mcpapp "go.klarlabs.de/glossa/platform/internal/mcp/app"
	mcptools "go.klarlabs.de/glossa/platform/internal/mcp/tools"
	previewapi "go.klarlabs.de/glossa/platform/internal/preview/adapters/httpapi"
	previewlimit "go.klarlabs.de/glossa/platform/internal/preview/adapters/ratelimit"
	previewapp "go.klarlabs.de/glossa/platform/internal/preview/app"
	qualitycatalog "go.klarlabs.de/glossa/platform/internal/quality/adapters/catalog"
	qualityapi "go.klarlabs.de/glossa/platform/internal/quality/adapters/httpapi"
	qualityintelligence "go.klarlabs.de/glossa/platform/internal/quality/adapters/intelligence"
	qualitymetrics "go.klarlabs.de/glossa/platform/internal/quality/adapters/metrics"
	qualitypg "go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres"
	qualitypullrequests "go.klarlabs.de/glossa/platform/internal/quality/adapters/pullrequests"
	qualityreview "go.klarlabs.de/glossa/platform/internal/quality/adapters/review"
	qualitysnapshot "go.klarlabs.de/glossa/platform/internal/quality/adapters/snapshot"
	qualitystyle "go.klarlabs.de/glossa/platform/internal/quality/adapters/style"
	qualitysummary "go.klarlabs.de/glossa/platform/internal/quality/adapters/summary"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	releaseapi "go.klarlabs.de/glossa/platform/internal/release/adapters/httpapi"
	releasemetrics "go.klarlabs.de/glossa/platform/internal/release/adapters/metrics"
	releasepg "go.klarlabs.de/glossa/platform/internal/release/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/sources"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	releasedomain "go.klarlabs.de/glossa/platform/internal/release/domain"
	workflowcatalog "go.klarlabs.de/glossa/platform/internal/workflow/adapters/catalog"
	workflowapi "go.klarlabs.de/glossa/platform/internal/workflow/adapters/httpapi"
	workflowidentity "go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	workflowpg "go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	workflowapp "go.klarlabs.de/glossa/platform/internal/workflow/app"
	workflowdomain "go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// contexts are the bounded contexts besides Identity, wired to each
// other only through their application ports: Localization reads the
// catalog through catalogport, Catalog asks Localization for
// translation coverage through coverage, Release and Knowledge read
// both through their sources adapters, and Intelligence reads (and
// writes translations) through its own.
type contexts struct {
	catalogAPI      *catalogapi.API
	localizationAPI *localizationapi.API
	releaseAPI      *releaseapi.API
	knowledgeAPI    *knowledgeapi.API
	intelligenceAPI *intelligenceapi.API
	// previewAPI is the stateless message preview (no database).
	previewAPI *previewapi.API
	// aiWorker runs Intelligence's jobs; nil when disabled.
	aiWorker *intelligenceapp.Worker
	// integrationAPI serves import and export jobs; integrationWorker
	// runs them (nil when disabled).
	integrationAPI    *integrationapi.API
	integrationWorker *integrationapp.Worker
	// usageContext is the Context context (RFC 0004): where messages
	// appear. contextAPI serves its uploads and reads; Intelligence's
	// message_context reads it through its own port.
	usageContext *contextapp.Service
	contextAPI   *contextapi.API
	// quality is the Quality context (RFC 0005): check runs, their
	// immutable findings and the waivers that accept one. qualityAPI
	// serves its reads and waiver writes.
	quality    *qualityapp.Service
	qualityAPI *qualityapi.API
	// workflow is the Workflow context (RFC 0006 §2): definitions as
	// data, their versions and bindings. workflowAPI serves them and the
	// read side of instances.
	workflow    *workflowapp.Service
	workflowAPI *workflowapi.API
	// auditAPI is Audit's HTTP edge (RFC 0006 §6): the v0.3 history
	// import. Audit is built before the other contexts (Identity records
	// through it), so main sets this once both exist.
	auditAPI *auditapi.API
	// objects is the deployment's object storage, which Audit's export
	// jobs write to (Audit is built before the contexts that open it).
	objects objectstore.StreamStore
	// auditKeys serves /.well-known/glossa-audit-keys.json beside /v1,
	// outside the contract and the Guard (RFC 0006 §6.2).
	auditKeys http.HandlerFunc
	// keyIndexes is Release's key index task: it rewrites the index
	// objects of keys written before their current format (migration
	// 0015 gave existing keys a scope).
	keyIndexes func(context.Context) (int, error)
	// purgeJobs are the daily retention jobs (RFC 0004 §2.3): Context's
	// builds, captures and images, and Catalog's proposal sweep. The
	// scheduler leases each so only one replica runs it.
	purgeJobs []scheduler.Job
	// branchPublisher publishes branch environments whose debounced
	// request is due (RFC 0004 §4). A publish is keyed by its request,
	// so every replica may run it; nil when
	// GLOSSA_BRANCH_PUBLISHER_ENABLED is off.
	branchPublisher *releaseapp.Publisher
	// rolloutSweeper aborts staged rollouts past their max_duration
	// (RFC 0006 §5.2); the leased scheduler of newRolloutSweep runs it.
	rolloutSweeper *releaseapp.RolloutSweeper
	// githubInbox drains the GitHub webhook inbox (RFC 0004 §6.2); nil
	// when this deployment configures no GitHub App, or when
	// GLOSSA_GITHUB_INBOX_ENABLED is off. The install flow and the
	// endpoint still work without the worker — deliveries pile up
	// instead of being processed.
	githubInbox *integrationapp.InboxWorker
	// githubChecks renders pull requests' Glossa checks and writes them
	// to GitHub (RFC 0004 §6.4); nil without a GitHub App, or when
	// GLOSSA_GITHUB_CHECKS_ENABLED is off. The webhook still queues the
	// checks without it — they wait rather than being lost.
	githubChecks *integrationapp.CheckWorker
	// mcpTools are the MCP tools this deployment can serve (RFC 0005
	// §7.3), each a thin call into one of the contexts above. They are
	// built here because this is where those application services are;
	// newMCP registers them when the endpoint is enabled and drops them
	// when it is not.
	mcpTools []mcpapp.Tool
	// ciAuth is what Identity needs to exchange a GitHub Actions ID
	// token for a CI token (RFC 0004 §6.3): one process-wide verifier
	// and Integration's Git connections. Zero when this deployment has
	// no GitHub App, and the exchange then answers
	// `github_not_configured`.
	ciAuth identityapp.GitHubOIDC
	// workflowRuntime is the rest of Workflow: the instance runner
	// subscribed to the vocabulary events, the instance store the API
	// reads, and assignments and approvals.
	workflowRuntime workflowServices
	// coverage is Workflow's assignments as the read port assignment-
	// scoped visibility filters through (authz.Coverage, RFC 0006 §3.3).
	// Were it ever nil, Identity would hand every `assigned` member a
	// principal without one and authz would show them nothing: the
	// restriction fails closed, never open.
	coverage authz.Coverage
}

// newPurgeJobs builds the daily retention jobs over the two contexts that
// have something to purge. Each logs what it did; a failure is the
// scheduler's to count and retry at the next interval.
func newPurgeJobs(usages *contextapp.Service, catalog *catalogapp.Service, quality *qualityapp.Service,
	inbox *integrationapp.InboxWorker, checks *integrationapp.CheckWorker, logger *slog.Logger,
) []scheduler.Job {
	jobs := []scheduler.Job{
		{Name: "context.purge", Run: func(ctx context.Context) error {
			purged, err := usages.Purge(ctx)
			for _, p := range purged {
				logger.InfoContext(ctx, "context: retention purged a project",
					slog.String("tenant_id", p.Tenant.String()), slog.String("project_id", p.Project.String()),
					slog.Int("builds", len(p.Builds)), slog.Int("captures", p.Captures),
					slog.Int("images", p.ImagesDeleted))
			}
			return err
		}},
		// Quality's housekeeping (RFC 0005 §2.2, §2.3): the waivers whose
		// date has passed, recorded rather than deleted, and the check
		// runs past their 90 days, deleted with their findings — except
		// a ref's newest, which every dashboard reads. The trend
		// survives them in the findings-by-day rollup.
		{Name: "quality.sweep", Run: func(ctx context.Context) error {
			swept, err := quality.Sweep(ctx)
			if swept.WaiversExpired > 0 || swept.RunsDeleted > 0 {
				logger.InfoContext(ctx, "quality: sweep", slog.Int("tenants", swept.Tenants),
					slog.Int("waivers_expired", swept.WaiversExpired), slog.Int("runs_deleted", swept.RunsDeleted))
			}
			return err
		}},
		{Name: "catalog.proposals", Run: func(ctx context.Context) error {
			n, err := catalog.SweepAllProposals(ctx)
			if n > 0 {
				logger.InfoContext(ctx, "catalog: proposals of closed branches obsoleted", slog.Int("messages", n))
			}
			return err
		}},
	}
	// The webhook inbox's periodic half: delivery IDs are kept for the
	// replay window and then dropped, along with install intents nobody
	// finished (RFC 0004 §6.2). The worker beside it is a queue worker,
	// because a delivery is handled within seconds of arriving; this one
	// is leased, because one replica a day is enough.
	if inbox != nil {
		jobs = append(jobs, scheduler.Job{Name: "integration.github.sweep", Run: inbox.Sweep})
	}
	// The check's thirty-minute wait (RFC 0004 §6.4). It only makes the
	// checks past their deadline due again; the worker beside it decides
	// what they conclude, so a check is completed in one place. Leased,
	// because one replica asking is enough.
	if checks != nil {
		jobs = append(jobs, scheduler.Job{Name: "integration.github.check_timeout", Run: checks.Sweep})
	}
	return jobs
}

// Context's upload limits (RFC 0004 §10): a usages document is read
// within contextUploadTimeout, and a tenant uploads at most 10 a minute
// in bursts of up to 60 (a CI run uploads a few per application).
const contextUploadTimeout = 2 * time.Minute

// assignmentBodyTimeout bounds reading and answering a createAssignment
// whose body may run to MaxAssignmentBodyBytes.
const assignmentBodyTimeout = time.Minute

// captureUploadTimeout bounds reading a capture upload: up to
// contextdomain.MaxCaptureUploadBytes (200 MB) of images from CI.
const captureUploadTimeout = 10 * time.Minute

func contextUploadLimit() ratelimit.Config {
	return ratelimit.Config{Rate: 10, Interval: time.Minute, Burst: 60}
}

// contextDeps are what the contexts need beyond the database.
type contextDeps struct {
	objects objectstore.StreamStore
	signer  *releasedomain.Signer
	logger  *slog.Logger
	// sealKey seals tenants' provider keys (derived from the auth
	// secret).
	sealKey    []byte
	registerer prometheus.Registerer
	// tracer traces CI uploads end to end (RFC 0004 §11).
	tracer      trace.TracerProvider
	ai          config.Intelligence
	integration config.Integration
	purge       config.Purge
	branches    config.Branches
	context     config.Context
	github      config.GitHub
	workflow    config.Workflow
	// studioURL is where the pull request's sticky comment links to the
	// branch (GLOSSA_STUDIO_URL); edgeURL is where a branch
	// environment's manifest is served (GLOSSA_EDGE_PUBLIC_URL, empty
	// when the deployment does not announce its edge).
	studioURL string
	edgeURL   string
	// identity resolves the actor whose event moved a workflow instance
	// to the grant its actions run with (RFC 0006 §2.5); nil in tests
	// that build the contexts alone, and the actions of a person's or a
	// token's event are then refused.
	identity *identityapp.Service
	// lookup reads the environment for the GitHub App's own
	// configuration (RFC 0004 §14.1: platform configuration, not tenant
	// data, in one Kubernetes Secret).
	lookup config.LookupFunc
}

// buildContexts opens object storage and the signer, then the contexts.
func buildContexts(
	cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, events *outbox.Registry,
	reg prometheus.Registerer, tp trace.TracerProvider, lookup config.LookupFunc, identity *identityapp.Service,
) (contexts, error) {
	objects, err := configured.Open(cfg.Storage)
	if err != nil {
		return contexts{}, err
	}
	signer, err := newSigner(cfg, logger)
	if err != nil {
		return contexts{}, err
	}
	sealKey, err := deriveKey(cfg.Identity.AuthKey(), "secret-seal")
	if err != nil {
		return contexts{}, err
	}
	c, err := newContexts(pool, events, contextDeps{
		objects: objects, signer: signer, logger: logger, sealKey: sealKey, registerer: reg, ai: cfg.Intelligence,
		integration: cfg.Integration, purge: cfg.Purge, branches: cfg.Branches, context: cfg.Context, tracer: tp,
		github: cfg.GitHub, workflow: cfg.Workflow, studioURL: cfg.Identity.StudioURL, edgeURL: cfg.Release.EdgePublicURL, lookup: lookup,
		identity: identity,
	})
	c.objects = objects
	return c, err
}

// newContexts builds Catalog, Localization and Release and subscribes
// them to each other's events.
func newContexts(pool *pgxpool.Pool, events *outbox.Registry, deps contextDeps) (contexts, error) {
	uow := db.NewUnitOfWork(pool)
	catalog := catalogapp.New(catalogpg.NewTransactor(uow), catalogapp.WithScanner(catalogpg.NewScanner(uow)))
	localization := localizationapp.New(localizationpg.NewTransactor(uow), catalogport.New(catalog),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	translationPort := coverage.New(localization)
	catalog.SetCoverage(translationPort)
	catalog.SetImpact(translationPort)
	// The locales a check policy's require_complete may name are
	// validated through a port of their own, not through translationPort:
	// the check belongs to the catalog.write that is saving the policy,
	// so it must not also demand translations.read (a CI token holds
	// catalog.read and catalog.write, and policy-as-code is a CI job).
	// coverage.PolicyLocales says what that path may not be used for.
	catalog.SetLocales(coverage.NewPolicyLocales(localization))
	catalog.SetProjection(projection.New(localization))
	if err := localization.Subscribe(events); err != nil {
		return contexts{}, err
	}
	knowledge := knowledgeapp.New(knowledgepg.NewTransactor(uow), knowledgesources.NewTranslations(localization, catalog),
		knowledgesources.NewProjects(catalog), knowledgeapp.WithMessages(knowledgesources.NewMessages(catalog)), knowledgeapp.WithLogger(deps.logger))
	if err := knowledge.Subscribe(events); err != nil {
		return contexts{}, err
	}
	release := releaseapp.New(releasepg.NewTransactor(uow), sources.New(catalog, localization), deps.objects, deps.signer,
		releaseapp.WithLogger(deps.logger))
	if err := release.Subscribe(events); err != nil {
		return contexts{}, err
	}
	// Quality is built before Context, because a capture upload hands it
	// the visual findings the upload carries (RFC 0005 §5.1) and a
	// constructor argument says that dependency once, where a setter
	// would leave it possible to forget.
	quality := qualityapp.NewService(qualitypg.NewTransactor(uow), qualitycatalog.New(catalog),
		qualityapp.WithLogger(deps.logger), qualityapp.WithMetrics(qualitymetrics.New(deps.registerer)),
		qualityapp.WithTracerProvider(deps.tracer),
		// The daily sweep's cross-tenant half: which tenants hold an
		// expired waiver or a check run past its 90 days (RFC 0005 §2.2,
		// §2.3). It reads two timestamps and a tenant id, nothing else.
		qualityapp.WithScanner(qualitypg.NewScanner(uow)),
		// The server-side check reads the project through Catalog's and
		// Localization's own services, so it sees exactly what its
		// caller could read through the API (RFC 0005 §2.2).
		// The server-side check also reads the effective style guides,
		// because the style layer grades their mechanical fields and has
		// nothing to say without them (RFC 0005 §3.2). They come through
		// Intelligence's adapter onto Knowledge, which already resolves
		// the tenant → project → locale → namespace stack: a second
		// resolution would be a second answer.
		qualityapp.WithSnapshot(qualitysnapshot.New(catalog, localization,
			qualitysnapshot.WithStyles(qualitystyle.New(intelligencesources.NewKnowledge(knowledge))))))
	usageContext := contextapp.New(contextpg.NewTransactor(uow), contextcatalog.New(catalog),
		contextapp.WithSweeper(contextpg.NewSweeper(uow)), contextapp.WithLogger(deps.logger),
		contextapp.WithLimiter(ratelimit.New(contextUploadLimit())), contextapp.WithMetrics(contextmetrics.New(deps.registerer)),
		contextapp.WithImages(deps.objects, imaging.New("", imaging.DefaultDecodeBudget)),
		contextapp.WithDeleteBatch(deps.purge.BatchSize), contextapp.WithStorageQuota(deps.context.StorageQuotaBytes),
		contextapp.WithTracerProvider(deps.tracer), contextapp.WithFindings(contextquality.New(quality)))
	if err := usageContext.Subscribe(events); err != nil {
		return contexts{}, err
	}
	intelligence, err := newIntelligence(uow, catalog, localization, release, knowledge, usageContext, deps)
	if err != nil {
		return contexts{}, err
	}
	if err := intelligence.Subscribe(events); err != nil {
		return contexts{}, err
	}
	aiAPI, err := intelligenceapi.New(intelligence)
	if err != nil {
		return contexts{}, err
	}
	// Workflow learns about projects and message keys only through
	// Catalog's service (migration 0040 has no foreign key to them).
	workflowCatalog := workflowcatalog.New(catalog)
	workflow := workflowapp.New(workflowpg.NewTransactor(uow), workflowidentity.Permissions{},
		workflowapp.WithCatalog(workflowCatalog))
	// The instance runner steps instances on the vocabulary events; its
	// store is what the API's instance reads query.
	wf, err := newWorkflow(uow, events, workflow, workflowSources{
		catalog: catalog, localization: localization, quality: quality, intelligence: intelligence, identity: deps.identity,
		release: release,
	}, deps.logger, deps.registerer)
	if err != nil {
		return contexts{}, err
	}
	c := contexts{
		workflow: workflow, workflowAPI: workflowapi.New(workflow, wf.instances, workflowCatalog, wf.work).WithRebase(wf.runner), workflowRuntime: wf,
		coverage:   wf.coverage,
		catalogAPI: catalogapi.New(catalog), localizationAPI: localizationapi.New(localization),
		releaseAPI: releaseapi.New(release), knowledgeAPI: knowledgeapi.New(knowledge), intelligenceAPI: aiAPI,
		previewAPI:   previewapi.New(previewapp.New(previewlimit.New(previewlimit.Default()))),
		usageContext: usageContext, contextAPI: contextapi.New(usageContext),
		quality: quality, qualityAPI: qualityapi.New(quality),
	}
	scanner := releasepg.NewScanner(uow)
	c.keyIndexes = func(ctx context.Context) (int, error) { return release.RewriteKeyIndexes(ctx, scanner) }
	c.rolloutSweeper = releaseapp.NewRolloutSweeper(release, scanner, releasemetrics.New(deps.registerer))
	if deps.branches.PublisherEnabled {
		c.branchPublisher = releaseapp.NewPublisher(release, scanner, deps.branches.PublishInterval)
	}
	if deps.ai.WorkersEnabled {
		c.aiWorker = intelligenceapp.NewWorker(intelligence, intelligencepg.NewClaimer(uow), intelligenceapp.WorkerConfig{
			Workers: deps.ai.Workers, PollInterval: deps.ai.PollInterval, Lease: deps.ai.Lease, JobTimeout: deps.ai.JobTimeout,
		})
	}
	integration := integrationapp.New(integrationapp.Deps{
		Tx: integrationpg.NewTransactor(uow), Catalog: integrationsources.NewCatalog(catalog),
		Localization: integrationsources.NewLocalization(localization, catalog), Knowledge: integrationsources.NewKnowledge(knowledge),
		Objects: deps.objects, Logger: deps.logger,
		Config: integrationapp.Config{MaxUploadBytes: deps.integration.MaxUploadBytes, Retention: deps.integration.Retention},
	})
	if err := integration.Subscribe(events); err != nil {
		return contexts{}, err
	}
	if deps.integration.WorkersEnabled {
		c.integrationWorker = integrationapp.NewWorker(integration, integrationpg.NewClaimer(uow), integrationapp.WorkerConfig{
			Workers: deps.integration.Workers, PollInterval: deps.integration.PollInterval,
			Lease: deps.integration.Lease, JobTimeout: deps.integration.JobTimeout,
		})
	}
	gh, err := newGitHub(uow, checkSources{
		catalog: catalog, localization: localization, knowledge: knowledge,
		usages: usageContext, release: release, quality: quality,
	}, deps)
	if err != nil {
		return contexts{}, err
	}
	c.integrationAPI = integrationapi.New(integration, gh)
	if gh != nil {
		// The impact preview links to the pull request a policy would
		// newly fail (RFC 0005 §4.3); only Integration knows where it is.
		quality.SetPullRequestLinks(qualitypullrequests.New(gh))
		if err := gh.SubscribeChecks(events); err != nil {
			return contexts{}, err
		}
		if c.ciAuth, err = newCIAuth(gh, deps); err != nil {
			return contexts{}, err
		}
	}
	if gh != nil && deps.github.InboxEnabled {
		c.githubInbox = integrationapp.NewInboxWorker(gh, integrationapp.InboxConfig{
			Workers: deps.github.InboxWorkers, PollInterval: deps.github.PollInterval,
			Timeout: deps.github.HandlerTimeout, Lease: deps.github.Lease,
			DepthInterval: deps.github.DepthInterval,
		})
	}
	if gh != nil && deps.github.ChecksEnabled {
		c.githubChecks = integrationapp.NewCheckWorker(gh, integrationapp.CheckConfig{
			Workers: deps.github.CheckWorkers, PollInterval: deps.github.CheckPollInterval,
			Timeout: deps.github.CheckTimeout, Lease: deps.github.CheckLease,
			DepthInterval: deps.github.CheckDepthInterval,
		})
	}
	c.purgeJobs = newPurgeJobs(usageContext, catalog, quality, c.githubInbox, c.githubChecks, deps.logger)
	// Workflow's retention (RFC 0006 §2.5, wave 6): finished instances and
	// their transition logs past GLOSSA_WORKFLOW_INSTANCE_RETENTION.
	c.purgeJobs = append(c.purgeJobs, workflowRetentionJob(wf.runner, deps.workflow.InstanceRetention, deps.logger))
	// The quality summary's four other sources (RFC 0005 §8). Quality is
	// built before three of them, so this direction is wired here; it
	// only reads, and each call is an authorized use case of the service
	// that owns the number, so the summary shows exactly what its caller
	// could have read endpoint by endpoint.
	quality.SetSummarySources(qualityapp.SummarySources{
		Coverage:    qualitysummary.NewTranslations(localization),
		Suggestions: qualitysummary.NewSuggestions(intelligence),
		Usage:       qualitysummary.NewUsages(usageContext),
		Deployments: qualitysummary.NewPublishes(release),
		Checks:      qualitysummary.NewPullRequestChecks(integration),
	})
	// The linguistic layer is the one layer that needs a model, so it is
	// a job and its model call is Intelligence's (RFC 0005 §3.8). This
	// port carries the preflight — the tenant's sending consent, the
	// existing monthly AI budget and the project's `sensitive`
	// namespaces, each read from the context that owns it — and the
	// review itself sits behind the Reviewer below.
	//
	// That Reviewer is the seam between the job and the layer: the job
	// asks for a scope, the layer reviews one translation, and the
	// batcher expands the first into the second. It reads the catalog
	// through Catalog, the translations through Localization and the
	// terms and style through Knowledge — each an authorized use case of
	// the service that owns the data, so a review sees exactly what its
	// caller could have read through the API. New answering nil (no
	// Intelligence service) is still `linguistic_unavailable`: "no model
	// looked" is not "a model looked and found nothing".
	reviewer, err := qualityreview.New(qualityreview.Deps{
		Catalog:      qualityreview.NewCatalog(catalog),
		Localization: qualityreview.NewLocalization(localization),
		// Intelligence's own adapter onto Knowledge: one implementation
		// of "the terms and the style guide for this pair", shared with
		// the translation agent rather than written a second time.
		Knowledge:    intelligencesources.NewKnowledge(knowledge),
		Intelligence: qualityreview.NewIntelligence(intelligence),
	}, qualityreview.WithLogger(deps.logger))
	if err != nil {
		return contexts{}, err
	}
	quality.SetLinguist(qualityintelligence.New(intelligence, reviewer))
	// One adapter per context, wired to both the read and the write
	// ports it satisfies: MCP is a second façade on these services, so a
	// tool and the endpoint beside it call the same use case.
	mcpCatalog := mcpsources.NewCatalog(catalog, usageContext)
	mcpTranslations := mcpsources.NewTranslations(localization)
	mcpQuality := mcpsources.NewQuality(quality)
	c.mcpTools = mcptools.All(mcptools.Sources{
		Catalog:      mcpCatalog,
		Translations: mcpTranslations,
		Usages:       mcpsources.NewUsages(usageContext, catalog),
		Knowledge:    mcpsources.NewKnowledge(knowledge),
		Quality:      mcpQuality,
		Checks:       mcpQuality,
		Delivery:     mcpsources.NewDelivery(release),
		Messages:     mcpCatalog,
		Proposals:    mcpTranslations,
		Locales:      mcpTranslations,
		Translator:   mcpsources.NewIntelligence(intelligence),
		Releases:     mcpsources.NewReleases(release),
		Workflow:     mcpsources.NewWorkflow(workflow, wf.work, workflowapp.NewInstances(wf.instances, workflowCatalog), workflowCatalog),
		ReleaseReads: mcpsources.NewReleaseReads(release),
	})
	return c, nil
}

// checkSources are the application services the Glossa PR check reads.
type checkSources struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	knowledge    *knowledgeapp.Service
	usages       *contextapp.Service
	release      *releaseapp.Service
	// quality holds the check runs CI records, which is what the pull
	// request renders (RFC 0005 §12.3).
	quality *qualityapp.Service
}

// newGitHub wires the GitHub integration (RFC 0004 §6) when the
// deployment configures a GitHub App: the App's ID, key, webhook secret
// and OAuth credentials come from the environment, as one Kubernetes
// Secret in the platform's namespace (§14.1).
//
// With none of it set it returns nil, nil: the endpoints then answer
// `github_not_configured`, no webhook is accepted, no worker runs, and
// nothing else in the server changes. A partial configuration is an
// error rather than a silent half-integration, because a deployment
// that meant to enable GitHub should hear about the missing half.
func newGitHub(uow *db.UnitOfWork, src checkSources, deps contextDeps) (*integrationapp.GitHubService, error) {
	cfg, enabled, err := github.LoadConfig(deps.lookup)
	if err != nil {
		return nil, err
	}
	if !enabled {
		deps.logger.Info("GLOSSA_GITHUB_APP_ID is unset: the GitHub integration is off; its endpoints answer github_not_configured")
		return nil, nil //nolint:nilnil // no App is a valid configuration
	}
	client, err := github.New(cfg, github.Options{
		Logger: deps.logger, OnCall: integrationmetrics.NewGitHub(deps.registerer).OnCall,
	})
	if err != nil {
		return nil, err
	}
	hooks, err := github.NewWebhooks(cfg.WebhookSecret)
	if err != nil {
		return nil, err
	}
	deps.logger.Info("the GitHub integration is on", slog.Any("github", cfg))
	return integrationapp.NewGitHubService(integrationapp.GitHubDeps{
		Tx:           integrationpg.NewGitHubTransactor(uow),
		Inbox:        integrationpg.NewInbox(uow),
		Repositories: integrationpg.NewRepositories(uow),
		GitHub:       client,
		Verifier:     hooks,
		Events:       hooks,
		Branches:     integrationsources.NewBranches(src.catalog),
		Checks:       integrationpg.NewChecks(uow),
		Sources: integrationsources.NewChecks(integrationsources.ChecksDeps{
			Catalog: src.catalog, Localization: src.localization, Knowledge: src.knowledge,
			Usages: src.usages, Release: src.release, Quality: src.quality, EdgeURL: deps.edgeURL,
		}),
		StudioURL:      deps.studioURL,
		Metrics:        integrationmetrics.NewWebhooks(deps.registerer),
		CheckMetrics:   integrationmetrics.NewChecks(deps.registerer),
		TracerProvider: deps.tracer,
		Logger:         deps.logger,
	})
}

// newCIAuth builds the GitHub Actions OIDC exchange Identity serves
// (RFC 0004 §6.3): one verifier for the process, over auth-go's cached
// JWKS, and Integration's Git connections as the repository directory.
//
// It is only called when a GitHub App is configured, because a
// deployment without one has no Git connections to match a
// `repository_id` against: the exchange then stays off and answers
// `github_not_configured`, and CI uses a stored API token instead.
func newCIAuth(gh *integrationapp.GitHubService, deps contextDeps) (identityapp.GitHubOIDC, error) {
	cfg, _, err := github.LoadConfig(deps.lookup)
	if err != nil {
		return identityapp.GitHubOIDC{}, err
	}
	verifier, err := ghoidc.New(ghoidc.Config{
		Issuer: cfg.OIDCIssuer, Audience: cfg.OIDCAudience, MaxStale: cfg.OIDCMaxStale,
	})
	if err != nil {
		return identityapp.GitHubOIDC{}, err
	}
	deps.logger.Info("CI can authenticate with GitHub Actions OIDC",
		slog.String("issuer", verifier.Issuer()), slog.String("audience", verifier.Audience()))
	return identityapp.GitHubOIDC{
		Verifier:     verifier,
		Repositories: identitysources.NewGitRepositories(gh),
		Metrics:      identitymetrics.NewCI(deps.registerer),
	}, nil
}

// largeBodies lets import uploads and export downloads stream files
// larger and longer than the API's default body limit and timeouts,
// usage uploads reach the 20 MB of a usages document, and capture
// uploads the 200 MB of a manifest with its images.
func largeBodies(cfg config.Integration) func(*http.Request) (httpserver.BodyPolicy, bool) {
	return func(r *http.Request) (httpserver.BodyPolicy, bool) {
		switch {
		case contextapi.UploadPath(r.Method, r.URL.Path):
			return httpserver.BodyPolicy{MaxBytes: contextdomain.MaxUploadBytes, Timeout: contextUploadTimeout}, true
		case contextapi.CaptureUploadPath(r.Method, r.URL.Path):
			return httpserver.BodyPolicy{MaxBytes: contextdomain.MaxCaptureUploadBytes, Timeout: captureUploadTimeout}, true
		case integrationapi.UploadPath(r.Method, r.URL.Path):
			// The service enforces GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES
			// while it streams, with its own problem code.
			return httpserver.BodyPolicy{Timeout: cfg.UploadTimeout}, true
		case workflowapi.CreateAssignmentPath(r.Method, r.URL.Path):
			return httpserver.BodyPolicy{MaxBytes: workflowdomain.MaxAssignmentBodyBytes, Timeout: assignmentBodyTimeout}, true
		case integrationapi.DownloadPath(r.Method, r.URL.Path):
			return httpserver.BodyPolicy{MaxBytes: 1, Timeout: cfg.UploadTimeout}, true
		}
		return httpserver.BodyPolicy{}, false
	}
}

// newIntelligence wires the Intelligence context: its Postgres store,
// the other contexts' services through its sources adapters, providers
// built from tenants' configuration over an SSRF-safe client, sealed
// keys and Prometheus metrics.
func newIntelligence(uow *db.UnitOfWork, catalog *catalogapp.Service, localization *localizationapp.Service,
	release *releaseapp.Service, knowledge *knowledgeapp.Service, usages *contextapp.Service, deps contextDeps,
) (*intelligenceapp.Service, error) {
	sealer, err := sealing.New(deps.sealKey)
	if err != nil {
		return nil, err
	}
	return intelligenceapp.NewService(intelligenceapp.Deps{
		Tx:           intelligencepg.NewTransactor(uow),
		Catalog:      intelligencesources.NewCatalog(catalog),
		Localization: intelligencesources.NewLocalization(localization, catalog),
		Environments: intelligencesources.NewEnvironments(release),
		Knowledge:    intelligencesources.NewKnowledge(knowledge),
		Usages:       intelligencesources.NewUsages(usages),
		Providers: intelligenceproviders.New(intelligenceproviders.Config{
			AllowPrivate: deps.ai.AllowPrivateEndpoints, Concurrency: deps.ai.ProviderConcurrency, Logger: deps.logger,
		}),
		Sealer:                sealer,
		Metrics:               intelligencemetrics.New(deps.registerer),
		Logger:                deps.logger,
		AllowPrivateEndpoints: deps.ai.AllowPrivateEndpoints,
	})
}

// newSigner builds the manifest signer from GLOSSA_RELEASE_SIGNING_KEYS
// and _RETIRED_KEYS. Without configured keys it derives one from the
// auth secret, for development: rotating that secret then rotates the
// signing key, which breaks runtimes pinned to it.
func newSigner(cfg config.Config, logger *slog.Logger) (*releasedomain.Signer, error) {
	var active []releasedomain.SigningKey
	for _, kv := range config.KeyList(cfg.Release.SigningKeys.Reveal()) {
		k, err := releasedomain.ParseSigningKey(kv[0], kv[1])
		if err != nil {
			return nil, fmt.Errorf("GLOSSA_RELEASE_SIGNING_KEYS: %w", err)
		}
		active = append(active, k)
	}
	if len(active) == 0 {
		seed, err := deriveKey(cfg.Identity.AuthKey(), "release-signing")
		if err != nil {
			return nil, err
		}
		pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
		id := "derived-" + delivery.Digest(pub)[:12]
		k, err := releasedomain.ParseSigningKey(id, base64.StdEncoding.EncodeToString(seed))
		if err != nil {
			return nil, err
		}
		active = append(active, k)
		logger.Warn("GLOSSA_RELEASE_SIGNING_KEYS is unset: manifests are signed with a key derived from GLOSSA_AUTH_SECRET; configure a dedicated key before runtimes pin it",
			slog.String("key_id", id))
	}
	var retired []releasedomain.PublicKey
	for _, kv := range config.KeyList(cfg.Release.RetiredKeys) {
		k, err := releasedomain.ParsePublicKey(kv[0], kv[1])
		if err != nil {
			return nil, fmt.Errorf("GLOSSA_RELEASE_RETIRED_KEYS: %w", err)
		}
		retired = append(retired, k)
	}
	return releasedomain.NewSigner(active, retired)
}
