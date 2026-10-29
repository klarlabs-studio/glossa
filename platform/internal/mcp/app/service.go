// Package app is the MCP context's application service: it opens a
// session on a tenant API token, decides which toolset that session
// gets, and runs one tool call — authorized, audited, measured and
// traced.
//
// It is deliberately transport-free. Nothing here imports an MCP SDK;
// the transport adapter translates a JSON-RPC call into Call and a
// Result back. RFC 0005 §7.1 asks for exactly that discipline on the
// other side too: every tool is a thin call into another context's
// application port, never a second implementation of a rule.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// ErrRateLimited means the tenant called too many tools too fast. A
// runaway agent hits the same wall a runaway script does
// (RFC 0005 §7.4).
var ErrRateLimited = errors.New("mcp: too many tool calls; slow down")

// Limiter decides whether a tenant may call a tool now: Allow consumes
// one call from key's budget.
type Limiter interface {
	Allow(ctx context.Context, key string) bool
}

// tracerName names MCP's spans' instrumentation scope.
const tracerName = "go.klarlabs.de/glossa/platform/internal/mcp"

// Service opens sessions and runs tool calls.
type Service struct {
	auth    Authenticator
	tools   *Registry
	audits  []Audit
	metrics Metrics
	limiter Limiter
	tracer  trace.Tracer
	logger  *slog.Logger
	now     func() time.Time
	newID   func() uuid.UUID
}

// Option configures the service.
type Option func(*Service)

// WithAudit writes every tool call to a. Given more than once, each is
// written in turn — the MCP ledger, then the tenant's audit trail
// (RFC 0006 §6.1). Without one nothing is recorded, which is a
// configuration only tests should have.
func WithAudit(a Audit) Option {
	return func(s *Service) {
		if a != nil {
			s.audits = append(s.audits, a)
		}
	}
}

// WithMetrics records the RFC 0005 §11 series.
func WithMetrics(m Metrics) Option {
	return func(s *Service) {
		if m != nil {
			s.metrics = m
		}
	}
}

// WithLimiter rate-limits tool calls per tenant.
func WithLimiter(l Limiter) Option {
	return func(s *Service) {
		if l != nil {
			s.limiter = l
		}
	}
}

// WithTracerProvider traces one span per tool call, joined to the REST
// operation it wraps (RFC 0005 §11).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *Service) {
		if tp != nil {
			s.tracer = tp.Tracer(tracerName)
		}
	}
}

// WithLogger sets the logger. MCP logs token ids and tool names, never
// message text (RFC 0005 §11).
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithIDs replaces the id source (tests).
func WithIDs(next func() uuid.UUID) Option {
	return func(s *Service) {
		if next != nil {
			s.newID = next
		}
	}
}

// WithTools registers tools beyond the capability probe. Wave 1 ships
// none: this slice is the transport, the auth, the session model and
// the audit, and a tool added here inherits all four.
func WithTools(tools ...Tool) Option {
	return func(s *Service) {
		for _, t := range tools {
			if err := s.tools.Add(t); err != nil {
				panic(err) // a duplicate or nameless tool is a wiring bug
			}
		}
	}
}

// New returns the service. The capability probe is always registered;
// everything else is a deployment's choice.
func New(auth Authenticator, opts ...Option) (*Service, error) {
	if auth == nil {
		return nil, errors.New("mcp: an authenticator is required")
	}
	s := &Service{
		auth: auth, tools: &Registry{}, metrics: NoMetrics{},
		tracer: noop.NewTracerProvider().Tracer(tracerName),
		logger: slog.New(slog.DiscardHandler),
		now:    func() time.Time { return time.Now().UTC() },
		newID:  func() uuid.UUID { return uuid.Must(uuid.NewV7()) },
	}
	if err := s.tools.Add(whoAmI(s.tools)); err != nil {
		return nil, err
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Tools returns the tools a session with this toolset may call, for the
// transport's tools/list.
func (s *Service) Tools(ts domain.Toolset) []Tool { return s.tools.For(ts) }

// Authenticate resolves a bearer credential to the caller a session
// would act as. It is the first of the two locks on a write and the
// only lock on a read: no credential but a tenant API token gets this
// far (RFC 0005 §7.2).
func (s *Service) Authenticate(ctx context.Context, bearer string) (Caller, error) {
	return s.auth.Authenticate(ctx, bearer)
}

// AllowToolset reports whether caller may open the toolset it asked
// for: it is the *first* of the two locks. A read session needs nothing
// beyond a token, because every scope implies read; any other toolset
// needs the scope that toolset mirrors — `write` for the write tools,
// `publish` for the release tools. The two are orthogonal, so a write
// token cannot open a publish session however much it can change.
func (s *Service) AllowToolset(caller Caller, want domain.Toolset) error {
	if want == domain.ToolsetRead {
		return nil
	}
	if slices.Contains(caller.Scopes, want.Scope()) {
		return nil
	}
	if want == domain.ToolsetPublish {
		return domain.ErrPublishNotGranted
	}
	return domain.ErrWriteNotGranted
}

// Open binds a session to the caller's tenant. The tenant is the
// token's own and nothing else is offered, so no tool can take one.
// Call AllowToolset first: Open trusts its arguments.
func (s *Service) Open(transport string, caller Caller, ts domain.Toolset) Session {
	sess := Session{ID: s.newID().String(), Transport: transport, Caller: caller, Toolset: ts}
	s.metrics.SessionOpened(transport)
	s.logger.Info("mcp: session opened",
		slog.String("session", sess.ID), slog.String("transport", transport),
		slog.String("tenant", caller.Tenant.String()), slog.String("actor", caller.Principal.Actor.String()),
		slog.String("toolset", ts.String()))
	return sess
}

// Call runs one tool in sess and records it. The audit row and the
// metric are written whatever the outcome — a refusal is exactly the
// thing an operator wants to see — and the row carries the arguments'
// shape, never their content.
func (s *Service) Call(ctx context.Context, sess Session, name string, args json.RawMessage) (Result, error) {
	started := s.now()
	tool, found := s.tools.Lookup(name)

	ctx, span := s.tracer.Start(ctx, "mcp.tool_call", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("mcp.tool", name),
			attribute.String("mcp.toolset", sess.Toolset.String()),
			attribute.String("mcp.session", sess.ID),
			attribute.String("glossa.tenant", sess.Tenant.String()),
		))
	defer span.End()

	res, err := s.run(ctx, sess, tool, found, args)
	outcome := outcomeOf(err)
	span.SetAttributes(attribute.String("mcp.outcome", outcome.String()))
	if err != nil {
		span.SetStatus(codes.Error, outcome.String())
		span.RecordError(err)
	}

	s.metrics.ToolCalled(name, sess.Toolset, outcome)
	s.record(ctx, sess, tool, name, outcome, args, res.Affected, s.now().Sub(started))
	return res, err
}

// run does the call itself: the session's toolset, the tenant's rate
// limit, the tool's permission, then the handler.
func (s *Service) run(
	ctx context.Context, sess Session, tool Tool, found bool, args json.RawMessage,
) (Result, error) {
	if !found {
		return Result{}, domain.ErrToolNotFound
	}
	// The write gate. The scope was checked when the session opened; this
	// is the other lock, and it is why a `write` token still cannot write
	// through a session that did not ask to.
	if !sess.Toolset.Includes(tool.Toolset) {
		return Result{}, fmt.Errorf("%w: %s needs a %s session", domain.ErrToolNotInSession, tool.Name, tool.Toolset)
	}
	ctx = sess.Context(ctx)
	if s.limiter != nil && !s.limiter.Allow(ctx, "tenant:"+sess.Tenant.String()) {
		return Result{}, ErrRateLimited
	}
	if tool.Permission != "" {
		if err := authz.Require(ctx, tool.Permission); err != nil {
			return Result{}, err
		}
	}
	res, err := tool.Handler(ctx, sess, args)
	// Whatever a source did not translate itself, a project outside the
	// token's scope answers as one that does not exist (RFC 0006 §4.1).
	if errors.Is(err, authz.ErrNotVisible) {
		err = domain.ErrNotFound
	}
	return res, err
}

// record writes the ledger row. A ledger that fails must not swallow a
// good answer, so the failure is logged and the call still returns —
// but it is logged at error level, because a silent gap in an audit
// trail is worse than a noisy one.
func (s *Service) record(
	ctx context.Context, sess Session, tool Tool, name string,
	outcome domain.Outcome, args json.RawMessage, affected []string, took time.Duration,
) {
	entry := AuditEntry{
		ID:        s.newID(),
		Session:   sess.ID,
		Actor:     sess.Actor(),
		Token:     sess.Token,
		Toolset:   sess.Toolset,
		Tool:      name,
		Outcome:   outcome,
		Arguments: domain.Shape(args, tool.Selectors),
		Affected:  affected,
		Duration:  took,
		At:        s.now(),
	}
	// The ledger is the tenant's own table, so the write runs in the
	// session's scope even when the call was refused before it got there.
	for _, a := range s.audits {
		if err := a.Record(sess.Context(ctx), entry); err != nil {
			s.logger.ErrorContext(ctx, "mcp: the tool call was not audited",
				slog.String("session", sess.ID), slog.String("tool", name),
				slog.String("actor", sess.Actor().String()), slog.Any("error", err))
		}
	}
}

// outcomeOf maps an error to the closed outcome vocabulary.
func outcomeOf(err error) domain.Outcome {
	switch {
	case err == nil:
		return domain.OutcomeOK
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, domain.ErrToolNotInSession),
		errors.Is(err, authz.ErrUnauthenticated), errors.Is(err, ErrRateLimited):
		return domain.OutcomeDenied
	case errors.Is(err, domain.ErrToolNotFound), errors.Is(err, domain.ErrNotFound):
		// A not-found is an argument that does not resolve, not a
		// failure of the server: an agent that asks for a message that
		// isn't there has made an invalid call, and the operator wants
		// those counted apart from real errors.
		return domain.OutcomeInvalid
	}
	var invalid *InvalidArgumentError
	if errors.As(err, &invalid) {
		return domain.OutcomeInvalid
	}
	return domain.OutcomeError
}

// InvalidArgumentError is what a tool returns when its arguments do not
// make sense. It names the argument, never its value.
type InvalidArgumentError struct {
	Argument string
	Reason   string
}

func (e *InvalidArgumentError) Error() string {
	return fmt.Sprintf("mcp: argument %q is invalid: %s", e.Argument, e.Reason)
}
