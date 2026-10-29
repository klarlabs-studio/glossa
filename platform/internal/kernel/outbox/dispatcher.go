package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.klarlabs.de/fortify/retry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// DispatcherConfig tunes delivery. Zero values are invalid except where
// noted; config.Outbox supplies production defaults.
type DispatcherConfig struct {
	// BatchSize is the number of events claimed per round trip.
	BatchSize int
	// MaxAttempts is the number of deliveries before dead-lettering.
	MaxAttempts int
	// PollInterval is the idle wait between empty claims.
	PollInterval time.Duration
	// Lease is how long a claim stays exclusive. Events a batch hasn't
	// started by the end of its lease are released, not delivered late.
	Lease time.Duration
	// HandlerTimeout bounds one subscriber's delivery, inline retries
	// included. It must be shorter than Lease.
	HandlerTimeout time.Duration
	// InlineAttempts and InlineBackoff configure the in-process fortify
	// retry per subscriber (defaults: 3 attempts, 100ms).
	InlineAttempts int
	InlineBackoff  time.Duration
	// RetryBase and RetryMax shape the persistent backoff between
	// deliveries: RetryBase·2^(attempt−1), capped at RetryMax
	// (defaults: 5s, 15m).
	RetryBase time.Duration
	RetryMax  time.Duration
}

func (c *DispatcherConfig) applyDefaults() {
	if c.InlineAttempts == 0 {
		c.InlineAttempts = 3
	}
	if c.InlineBackoff == 0 {
		c.InlineBackoff = 100 * time.Millisecond
	}
	if c.RetryBase == 0 {
		c.RetryBase = 5 * time.Second
	}
	if c.RetryMax == 0 {
		c.RetryMax = 15 * time.Minute
	}
}

func (c DispatcherConfig) validate() error {
	var errs []error
	if c.BatchSize < 1 || c.MaxAttempts < 1 || c.InlineAttempts < 1 {
		errs = append(errs, errors.New("BatchSize, MaxAttempts and InlineAttempts must be positive"))
	}
	if c.PollInterval <= 0 || c.HandlerTimeout <= 0 {
		errs = append(errs, errors.New("PollInterval and HandlerTimeout must be positive"))
	}
	if c.Lease <= c.HandlerTimeout {
		errs = append(errs, errors.New("Lease must be longer than HandlerTimeout"))
	}
	if c.RetryMax < c.RetryBase {
		errs = append(errs, errors.New("RetryMax must be at least RetryBase"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("outbox: invalid dispatcher config: %w", errors.Join(errs...))
	}
	return nil
}

// DispatcherOptions carries optional collaborators; nil means a no-op.
type DispatcherOptions struct {
	Logger         *slog.Logger
	TracerProvider trace.TracerProvider
	Registerer     prometheus.Registerer
	// Now is the clock used for lease bookkeeping (tests).
	Now func() time.Time
}

// Dispatcher claims due events and delivers them to subscribers.
type Dispatcher struct {
	store    Store
	registry *Registry
	cfg      DispatcherConfig
	logger   *slog.Logger
	tracer   trace.Tracer
	metrics  *metrics
	now      func() time.Time
	retry    retry.Retry[struct{}]
}

// NewDispatcher validates cfg and wires the dispatcher.
func NewDispatcher(store Store, reg *Registry, cfg DispatcherConfig, opts DispatcherOptions) (*Dispatcher, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	d := &Dispatcher{store: store, registry: reg, cfg: cfg, logger: opts.Logger, now: opts.Now}
	if d.logger == nil {
		d.logger = slog.New(slog.DiscardHandler)
	}
	if d.now == nil {
		d.now = time.Now
	}
	tp := opts.TracerProvider
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	d.tracer = tp.Tracer("go.klarlabs.de/glossa/platform/internal/kernel/outbox")
	d.metrics = newMetrics(opts.Registerer)
	d.retry = retry.New[struct{}](retry.Config{
		MaxAttempts:   cfg.InlineAttempts,
		InitialDelay:  cfg.InlineBackoff,
		Multiplier:    2,
		BackoffPolicy: retry.BackoffExponential,
		Jitter:        true,
		IsRetryable:   func(err error) bool { return !IsPermanent(err) },
	})
	return d, nil
}

// Run delivers until ctx is cancelled. It polls every PollInterval when
// idle and immediately when the last batch was full. Claim errors are
// logged and retried on the next tick, so a database blip doesn't stop
// the dispatcher.
func (d *Dispatcher) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		n, err := d.ProcessBatch(ctx)
		if err != nil && ctx.Err() == nil {
			d.logger.ErrorContext(ctx, "outbox: claim failed", slog.Any("error", err))
		}
		wait := d.cfg.PollInterval
		if err == nil && n == d.cfg.BatchSize {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// ProcessBatch claims one batch and settles every claim in it. It
// returns the number of events claimed.
//
// Batch subscribers (Registry.SubscribeBatch) go first: their pending
// deliveries are grouped per subscriber and tenant, in claim order, and
// handed over together. Every other subscriber then gets each claim in
// claim order, one delivery at a time, as before. Each claim is settled
// on its own, in claim order, from what its subscribers did.
func (d *Dispatcher) ProcessBatch(ctx context.Context) (int, error) {
	leaseEnd := d.now().Add(d.cfg.Lease - d.cfg.Lease/10) // keep a margin for settling
	claims, err := d.store.Claim(ctx, d.cfg.BatchSize, d.cfg.Lease)
	if err != nil {
		d.metrics.claimErrors.Inc()
		return 0, fmt.Errorf("outbox: claim: %w", err)
	}
	runs := make([]*claimRun, len(claims))
	for i, c := range claims {
		runs[i] = &claimRun{Claim: c, delivered: slices.Clone(c.DeliveredTo)}
	}
	d.deliverBatches(ctx, runs, leaseEnd)
	for _, r := range runs {
		d.deliverEach(ctx, r, leaseEnd)
	}
	d.settleAll(ctx, runs)
	return len(claims), nil
}

// claimRun is one claim's progress through a batch.
type claimRun struct {
	Claim
	// delivered starts as the claim's DeliveredTo and gains every
	// subscriber that succeeds.
	delivered []string
	failures  []error
	// deferred is set when a subscriber was not attempted (shutdown,
	// lease exhausted): the claim is handed back, not charged.
	deferred bool
}

func (r *claimRun) pending(sub subscription) bool { return !slices.Contains(r.delivered, sub.name) }

func (r *claimRun) record(sub subscription, err error) {
	if err != nil {
		r.failures = append(r.failures, fmt.Errorf("%s: %w", sub.name, err))
		return
	}
	r.delivered = append(r.delivered, sub.name)
}

// canStart reports whether there is time left to start a delivery.
func (d *Dispatcher) canStart(ctx context.Context, leaseEnd time.Time) bool {
	return ctx.Err() == nil && d.now().Before(leaseEnd)
}

// settlementOf turns a run into its settlement: a failure retries or
// dead-letters as decide says, a deferred subscriber hands the claim
// back keeping what succeeded, and otherwise it is delivered.
func (d *Dispatcher) settlementOf(r *claimRun) Settlement {
	if len(r.failures) == 0 && r.deferred {
		return Settlement{EventID: r.EventID, ClaimToken: r.ClaimToken, Outcome: OutcomeRelease, DeliveredTo: r.delivered}
	}
	return d.decide(r.Claim, r.delivered, r.failures)
}

// batchGroup is one batch subscriber's pending deliveries for one
// tenant, in claim order.
type batchGroup struct {
	sub  subscription
	runs []*claimRun
}

// deliverBatches hands each batch subscriber its pending deliveries,
// one call per subscriber and tenant.
func (d *Dispatcher) deliverBatches(ctx context.Context, runs []*claimRun, leaseEnd time.Time) {
	type key struct {
		sub    string
		tenant tenancy.ID
	}
	groups := map[key]*batchGroup{}
	var order []key
	for _, r := range runs {
		for _, sub := range d.registry.subscribers(r.Type) {
			if sub.batch == nil || !r.pending(sub) {
				continue
			}
			k := key{sub.name, r.TenantID}
			g, ok := groups[k]
			if !ok {
				g = &batchGroup{sub: sub}
				groups[k] = g
				order = append(order, k)
			}
			g.runs = append(g.runs, r)
		}
	}
	for _, k := range order {
		g := groups[k]
		if !d.canStart(ctx, leaseEnd) {
			for _, r := range g.runs {
				r.deferred = true
			}
			continue
		}
		d.deliverBatch(ctx, g, leaseEnd)
	}
}

// deliverBatch calls one batch subscriber with a group's deliveries.
// What it did not handle is delivered again one at a time, with inline
// retries, so a delivery the batch failed is neither lost nor applied
// twice by the dispatcher: handled ones are never redelivered here, and
// the rest are retried exactly as a per-event subscriber would be.
func (d *Dispatcher) deliverBatch(ctx context.Context, g *batchGroup, leaseEnd time.Time) {
	ds := make([]Delivery, len(g.runs))
	links := make([]trace.Link, 0, len(g.runs))
	for i, r := range g.runs {
		ds[i] = r.Delivery
		pub := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier(r.TraceContext))
		if sc := trace.SpanContextFromContext(pub); sc.IsValid() {
			links = append(links, trace.Link{SpanContext: sc})
		}
	}
	tenant := g.runs[0].TenantID
	bctx, span := d.tracer.Start(tenancy.ContextWithTenant(ctx, tenant), "outbox deliver batch "+g.sub.name,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithLinks(links...),
		trace.WithAttributes(
			attribute.String("messaging.system", "glossa.outbox"),
			attribute.Int("messaging.batch.message_count", len(ds)),
			attribute.String("glossa.outbox.subscriber", g.sub.name),
			attribute.String("glossa.tenant.id", tenant.String()),
		))
	start := time.Now()
	errs := d.callBatch(bctx, g.sub.batch, ds)
	share := time.Since(start) / time.Duration(len(ds))
	var redeliver []*claimRun
	for i, r := range g.runs {
		switch err := errs[i]; {
		case err == nil, IsPermanent(err):
			d.metrics.observeHandler(r.Type, g.sub.name, err, share)
			if err != nil {
				d.logHandlerFailure(bctx, g.sub, r.Delivery, err)
			}
			r.record(g.sub, err)
		default:
			redeliver = append(redeliver, r)
		}
	}
	if len(redeliver) > 0 {
		span.SetStatus(codes.Error, fmt.Sprintf("%d of %d deliveries failed in the batch", len(redeliver), len(ds)))
		d.logger.WarnContext(bctx, "outbox: batch handler failed; delivering one at a time",
			slog.String("subscriber", g.sub.name), slog.Int("failed", len(redeliver)), slog.Int("batch", len(ds)))
	}
	span.End()
	for _, r := range redeliver {
		if !d.canStart(ctx, leaseEnd) {
			r.deferred = true
			continue
		}
		ectx, span := d.startDelivery(ctx, r.Claim)
		err := d.invoke(ectx, g.sub, r.Delivery)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
		r.record(g.sub, err)
	}
}

// callBatch calls h with the handler timeout and panic isolation, and
// always returns one result per delivery.
func (d *Dispatcher) callBatch(ctx context.Context, h BatchHandler, ds []Delivery) (errs []error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.HandlerTimeout)
	defer cancel()
	all := func(err error) []error {
		out := make([]error, len(ds))
		for i := range out {
			out[i] = err
		}
		return out
	}
	defer func() {
		if p := recover(); p != nil {
			errs = all(fmt.Errorf("batch handler panicked: %v\n%s", p, debug.Stack()))
		}
	}()
	errs = h.HandleBatch(ctx, ds)
	if len(errs) != len(ds) {
		return all(fmt.Errorf("batch handler returned %d results for %d deliveries", len(errs), len(ds)))
	}
	return errs
}

// deliverEach runs the claim's pending per-event subscribers in the
// event's tenant scope and under the publisher's trace, unless the
// dispatcher must hand the claim back.
func (d *Dispatcher) deliverEach(ctx context.Context, r *claimRun, leaseEnd time.Time) {
	var subs []subscription
	for _, sub := range d.registry.subscribers(r.Type) {
		if sub.batch == nil && r.pending(sub) {
			subs = append(subs, sub)
		}
	}
	if len(subs) == 0 {
		return
	}
	if !d.canStart(ctx, leaseEnd) {
		r.deferred = true
		return
	}
	ctx, span := d.startDelivery(ctx, r.Claim)
	defer span.End()
	before := len(r.failures)
	for _, sub := range subs {
		r.record(sub, d.invoke(ctx, sub, r.Delivery))
	}
	if len(r.failures) > before {
		span.SetStatus(codes.Error, joinErrors(r.failures[before:]))
	}
}

// startDelivery scopes ctx to c's tenant and continues the publisher's
// trace with a consumer span.
func (d *Dispatcher) startDelivery(ctx context.Context, c Claim) (context.Context, trace.Span) {
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier(c.TraceContext))
	ctx, span := d.tracer.Start(ctx, "outbox deliver "+c.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "glossa.outbox"),
			attribute.String("messaging.message.id", c.EventID.String()),
			attribute.String("glossa.event.type", c.Type),
			attribute.String("glossa.tenant.id", c.TenantID.String()),
			attribute.Int("glossa.event.attempt", c.Attempt),
		))
	return tenancy.ContextWithTenant(ctx, c.TenantID), span
}

// invoke calls one subscriber with inline retries, a timeout and panic
// isolation.
func (d *Dispatcher) invoke(ctx context.Context, sub subscription, del Delivery) error {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, d.cfg.HandlerTimeout)
	defer cancel()
	_, err := d.retry.Execute(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, callSafely(ctx, sub.handler, del)
	})
	d.metrics.observeHandler(del.Type, sub.name, err, time.Since(start))
	if err != nil {
		d.logHandlerFailure(ctx, sub, del, err)
	}
	return err
}

func (d *Dispatcher) logHandlerFailure(ctx context.Context, sub subscription, del Delivery, err error) {
	d.logger.WarnContext(ctx, "outbox: handler failed",
		slog.String("event_type", del.Type), slog.String("event_id", del.EventID.String()),
		slog.String("subscriber", sub.name), slog.Int("attempt", del.Attempt), slog.Any("error", err))
}

func callSafely(ctx context.Context, h Handler, del Delivery) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panicked: %v\n%s", p, debug.Stack())
		}
	}()
	return h.HandleEvent(ctx, del)
}

// decide turns delivery results into a settlement.
func (d *Dispatcher) decide(c Claim, delivered []string, failures []error) Settlement {
	s := Settlement{EventID: c.EventID, ClaimToken: c.ClaimToken, DeliveredTo: delivered}
	if len(failures) == 0 {
		s.Outcome = OutcomeDelivered
		return s
	}
	s.LastError = joinErrors(failures)
	if slices.ContainsFunc(failures, IsPermanent) || c.Attempt >= d.cfg.MaxAttempts {
		s.Outcome = OutcomeDead
		return s
	}
	s.Outcome = OutcomeRetry
	s.RetryAfter = retryDelay(c.Attempt, d.cfg.RetryBase, d.cfg.RetryMax)
	return s
}

func joinErrors(errs []error) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	const maxLen = 4096
	msg := strings.Join(msgs, "; ")
	if len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return msg
}

// retryDelay is base·2^(attempt−1), capped at max.
func retryDelay(attempt int, base, max time.Duration) time.Duration {
	delay := base
	for i := 1; i < attempt && delay < max; i++ {
		delay *= 2
	}
	return min(delay, max)
}

// settleAll records each run's settlement even when ctx is cancelled
// (shutdown), so finished work isn't redelivered needlessly: in one
// round trip when the store settles batches, else one at a time.
func (d *Dispatcher) settleAll(ctx context.Context, runs []*claimRun) {
	if len(runs) == 0 {
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	ss := make([]Settlement, len(runs))
	for i, r := range runs {
		ss[i] = d.settlementOf(r)
	}
	var errs []error
	if bs, ok := d.store.(BatchSettler); ok {
		errs = bs.SettleAll(sctx, ss)
	}
	if len(errs) != len(ss) {
		errs = make([]error, len(ss))
		for i, s := range ss {
			errs[i] = d.store.Settle(sctx, s)
		}
	}
	for i, r := range runs {
		d.settled(ctx, r.Claim, ss[i], errs[i])
	}
}

// settled accounts for one settlement's result.
func (d *Dispatcher) settled(ctx context.Context, c Claim, s Settlement, err error) {
	switch {
	case errors.Is(err, ErrLeaseLost):
		d.metrics.settlements.WithLabelValues("lease_lost").Inc()
		d.logger.WarnContext(ctx, "outbox: lease lost before settling",
			slog.String("event_id", c.EventID.String()), slog.String("outcome", s.Outcome.String()))
	case err != nil:
		d.metrics.settlements.WithLabelValues("error").Inc()
		d.logger.ErrorContext(ctx, "outbox: settle failed; the lease will expire and redeliver",
			slog.String("event_id", c.EventID.String()), slog.Any("error", err))
	default:
		d.metrics.settlements.WithLabelValues(s.Outcome.String()).Inc()
		if s.Outcome == OutcomeDead {
			d.logger.ErrorContext(ctx, "outbox: event dead-lettered",
				slog.String("event_id", c.EventID.String()), slog.String("event_type", c.Type),
				slog.String("tenant_id", c.TenantID.String()), slog.String("error", s.LastError))
		}
	}
}
