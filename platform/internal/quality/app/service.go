package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// Service implements Quality's stored use cases: recording a check run
// with its findings, reading runs and findings back, and the waivers
// that accept a finding.
//
// Permissions (RFC 0005 §9). Reading a run, a finding or a waiver needs
// `catalog.read`: a finding is about the catalog's messages and their
// translations, and the callers that must read one — `glossa check`,
// the pull-request check, Studio and MCP's read tools — hold exactly
// that. A CI token allows only `catalog.read` and `catalog.write`
// (RFC 0004 §6.3), so any narrower scope would lock CI out of its own
// verdict.
//
// Writing — recording a run, waiving a finding, revoking a waiver —
// needs `catalog.write`, the permission that already carries the
// authority to change what the project's check concludes (it uploads
// the messages, the usages and the captures the layers grade).
// `translations.review` was the alternative and is the wrong one: it is
// locale-scoped, and the `structure`, `completeness` and `source`
// layers produce findings with no locale at all, which nobody could
// then waive.
type Service struct {
	tx      Transactor
	catalog Catalog
	metrics Metrics
	// snapshot reads a project as the layers read it, so a check can run
	// on the server (RunCheck). Nil where the deployment does not wire
	// it, and RunCheck then answers ErrNoSnapshot rather than pretending
	// a project is clean.
	snapshot Snapshot
	// scanner finds the tenants the daily sweep has work in, across
	// tenants (system scope quality.sweep). Nil where the deployment
	// does not wire it, and Sweep then says so rather than reporting a
	// sweep that visited nobody.
	scanner Scanner
	// sources are the other contexts' ports the quality summary reads
	// (RFC 0005 §8). Any of them may be nil, and that number is then
	// reported as not measured rather than as zero.
	sources SummarySources
	// summaries caches a computed summary for SummaryTTL.
	summaries *summaryCache
	// linguist is Intelligence's reviewer, which owns the model call the
	// linguistic layer needs (RFC 0005 §3.8). Nil where the deployment
	// does not wire one, and a review is then refused with
	// ErrLinguistUnavailable rather than answering an empty list, which
	// would read as a clean bill of health nobody issued.
	linguist Linguist
	// links says where the project's pull requests are, so the impact
	// preview can link to the one it would newly fail. Nil where no
	// GitHub integration is wired; the preview then names each pull
	// request by its number alone.
	links  PullRequestLinks
	tracer trace.Tracer
	logger *slog.Logger
	now    func() time.Time
}

// tracerName names Quality's spans' instrumentation scope.
const tracerName = "go.klarlabs.de/glossa/platform/internal/quality"

// Option configures a Service.
type Option func(*Service)

// WithMetrics records runs, findings and waivers (NoMetrics by default).
func WithMetrics(m Metrics) Option { return func(s *Service) { s.metrics = m } }

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithTracerProvider traces a check run across its layers (RFC 0005
// §11). Without one, nothing is traced.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *Service) {
		if tp != nil {
			s.tracer = tp.Tracer(tracerName)
		}
	}
}

// NewService returns the service.
func NewService(tx Transactor, catalog Catalog, opts ...Option) *Service {
	s := &Service{
		tx: tx, catalog: catalog, metrics: NoMetrics{}, logger: slog.New(slog.DiscardHandler),
		summaries: newSummaryCache(SummaryTTL),
		tracer:    noop.NewTracerProvider().Tracer(tracerName),
		now:       func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// span starts a child span of ctx named name, and returns it with the
// function that ends it: end(&err) records the error and finishes.
func (s *Service) span(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func(*error)) {
	ctx, sp := s.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attrs...))
	return ctx, func(err *error) {
		if err != nil && *err != nil {
			sp.RecordError(*err)
			sp.SetStatus(codes.Error, (*err).Error())
		}
		sp.End()
	}
}

// read checks `catalog.read` and that the project exists, so an unknown
// project is a 404 rather than an empty list.
func (s *Service) read(ctx context.Context, project uuid.UUID) error {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project); err != nil {
		return err
	}
	return s.catalog.Project(ctx, project)
}

// write checks `catalog.write`, that the project exists, and returns
// the acting principal.
func (s *Service) write(ctx context.Context, project uuid.UUID) (string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogWrite, project); err != nil {
		return "", err
	}
	if err := s.catalog.Project(ctx, project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// storedPolicy reads the project's check policy and publishes its
// version as `glossa_quality_policy_version{project}` (RFC 0005 §11).
//
// Every read goes through here rather than only the save, because the
// gauge is about which document is grading right now: a replica that
// has restarted and not yet seen a save would otherwise publish
// nothing, and a deployment where the number matters most — two
// versions live at once during a grace period (§4.3) — is exactly the
// one where a gap is worst.
func (s *Service) storedPolicy(ctx context.Context, project uuid.UUID) (StoredPolicy, error) {
	stored, err := s.catalog.CheckPolicy(ctx, project)
	if err != nil {
		return StoredPolicy{}, err
	}
	s.metrics.PolicyVersionRead(project, stored.Policy.Version)
	return stored, nil
}

func invalidPageToken() error {
	return problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this list issued")
}
