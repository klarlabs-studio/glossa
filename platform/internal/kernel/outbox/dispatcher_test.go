package outbox_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// fakeStore hands out queued claims and records settlements.
type fakeStore struct {
	mu          sync.Mutex
	queue       []outbox.Claim
	settlements []outbox.Settlement
	claimErr    error
	settleErr   error
}

func (f *fakeStore) Claim(_ context.Context, limit int, _ time.Duration) ([]outbox.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	n := min(limit, len(f.queue))
	out := f.queue[:n]
	f.queue = f.queue[n:]
	return out, nil
}

func (f *fakeStore) Settle(_ context.Context, s outbox.Settlement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settlements = append(f.settlements, s)
	return f.settleErr
}

func (f *fakeStore) settled() []outbox.Settlement {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.settlements)
}

func newClaim(eventType string, attempt int, deliveredTo ...string) outbox.Claim {
	return outbox.Claim{
		Delivery: outbox.Delivery{
			EventID:       uuid.Must(uuid.NewV7()),
			TenantID:      tenancy.NewID(),
			Type:          eventType,
			AggregateType: "message",
			AggregateID:   "m1",
			Payload:       []byte(`{}`),
			Attempt:       attempt,
		},
		ClaimToken:  uuid.New(),
		DeliveredTo: deliveredTo,
	}
}

func testConfig() outbox.DispatcherConfig {
	return outbox.DispatcherConfig{
		BatchSize:      10,
		MaxAttempts:    3,
		PollInterval:   5 * time.Millisecond,
		Lease:          time.Minute,
		HandlerTimeout: time.Second,
		InlineAttempts: 3,
		InlineBackoff:  time.Millisecond,
		RetryBase:      time.Second,
		RetryMax:       time.Minute,
	}
}

func newDispatcher(t *testing.T, store outbox.Store, reg *outbox.Registry, opts outbox.DispatcherOptions) *outbox.Dispatcher {
	t.Helper()
	d, err := outbox.NewDispatcher(store, reg, testConfig(), opts)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	return d
}

func subscribe(t *testing.T, r *outbox.Registry, typ, name string, h outbox.HandlerFunc) {
	t.Helper()
	if err := r.Subscribe(typ, name, h); err != nil {
		t.Fatal(err)
	}
}

func onlySettlement(t *testing.T, s *fakeStore) outbox.Settlement {
	t.Helper()
	got := s.settled()
	if len(got) != 1 {
		t.Fatalf("got %d settlements, want 1: %+v", len(got), got)
	}
	return got[0]
}

func TestDeliversToEverySubscriberInTenantScope(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("catalog.source_revised", 1)
	var seen []string
	for _, name := range []string{"localization.mark_outdated", "quality.recheck"} {
		subscribe(t, reg, c.Type, name, func(ctx context.Context, d outbox.Delivery) error {
			tenant, ok := tenancy.FromContext(ctx)
			if !ok || tenant != c.TenantID || d.EventID != c.EventID || d.Attempt != 1 {
				t.Errorf("%s: tenant=%v ok=%t delivery=%+v", name, tenant, ok, d)
			}
			seen = append(seen, name)
			return nil
		})
	}
	store := &fakeStore{queue: []outbox.Claim{c}}

	n, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ProcessBatch = %d, %v", n, err)
	}
	s := onlySettlement(t, store)
	if s.Outcome != outbox.OutcomeDelivered || s.EventID != c.EventID || s.ClaimToken != c.ClaimToken {
		t.Errorf("settlement = %+v", s)
	}
	if !slices.Equal(s.DeliveredTo, seen) || len(seen) != 2 {
		t.Errorf("DeliveredTo = %v, handlers ran %v", s.DeliveredTo, seen)
	}
}

func TestEventWithoutSubscribersIsDelivered(t *testing.T) {
	store := &fakeStore{queue: []outbox.Claim{newClaim("nobody.cares", 1)}}
	if _, err := newDispatcher(t, store, outbox.NewRegistry(), outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := onlySettlement(t, store); s.Outcome != outbox.OutcomeDelivered {
		t.Errorf("outcome = %v", s.Outcome)
	}
}

func TestTransientFailureRetriesInlineThenReschedules(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 2)
	calls := 0
	subscribe(t, reg, c.Type, "ok", func(context.Context, outbox.Delivery) error { return nil })
	subscribe(t, reg, c.Type, "flaky", func(context.Context, outbox.Delivery) error {
		calls++
		return errors.New("provider timeout")
	})
	store := &fakeStore{queue: []outbox.Claim{c}}

	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("flaky handler called %d times, want 3 inline attempts", calls)
	}
	s := onlySettlement(t, store)
	if s.Outcome != outbox.OutcomeRetry {
		t.Fatalf("outcome = %v, want retry", s.Outcome)
	}
	if !slices.Equal(s.DeliveredTo, []string{"ok"}) {
		t.Errorf("DeliveredTo = %v, want [ok]", s.DeliveredTo)
	}
	if s.RetryAfter != 2*time.Second { // base 1s × 2^(attempt-1)
		t.Errorf("RetryAfter = %v, want 2s", s.RetryAfter)
	}
	if s.LastError == "" {
		t.Error("LastError is empty")
	}
}

func TestRetryDelayIsCapped(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 2)
	subscribe(t, reg, c.Type, "flaky", func(context.Context, outbox.Delivery) error { return errors.New("nope") })
	store := &fakeStore{queue: []outbox.Claim{c}}
	cfg := testConfig()
	cfg.MaxAttempts = 50
	cfg.RetryBase = 40 * time.Second
	d, err := outbox.NewDispatcher(store, reg, cfg, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := onlySettlement(t, store); s.RetryAfter != time.Minute {
		t.Errorf("RetryAfter = %v, want the 1m cap", s.RetryAfter)
	}
}

func TestSucceededSubscribersAreSkippedOnRetry(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 2, "done")
	subscribe(t, reg, c.Type, "done", func(context.Context, outbox.Delivery) error {
		t.Error("already-delivered subscriber ran again")
		return nil
	})
	subscribe(t, reg, c.Type, "pending", func(context.Context, outbox.Delivery) error { return nil })
	store := &fakeStore{queue: []outbox.Claim{c}}

	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := onlySettlement(t, store)
	if s.Outcome != outbox.OutcomeDelivered || !slices.Equal(s.DeliveredTo, []string{"done", "pending"}) {
		t.Errorf("settlement = %+v", s)
	}
}

func TestLastAttemptDeadLetters(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 3) // MaxAttempts is 3
	subscribe(t, reg, c.Type, "broken", func(context.Context, outbox.Delivery) error { return errors.New("still broken") })
	store := &fakeStore{queue: []outbox.Claim{c}}
	metrics := prometheus.NewRegistry()

	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{Registerer: metrics}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := onlySettlement(t, store)
	if s.Outcome != outbox.OutcomeDead {
		t.Fatalf("outcome = %v, want dead", s.Outcome)
	}
	if want := "broken: still broken"; s.LastError != want {
		t.Errorf("LastError = %q, want %q", s.LastError, want)
	}
	if n := testutil.CollectAndCount(metrics, "glossa_outbox_settlements_total"); n == 0 {
		t.Error("no settlement metric recorded")
	}
}

func TestPermanentErrorDeadLettersWithoutRetrying(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 1)
	calls := 0
	subscribe(t, reg, c.Type, "strict", func(context.Context, outbox.Delivery) error {
		calls++
		return outbox.Permanent(errors.New("unknown schema version"))
	})
	store := &fakeStore{queue: []outbox.Claim{c}}

	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("permanent failure retried: %d calls", calls)
	}
	if s := onlySettlement(t, store); s.Outcome != outbox.OutcomeDead {
		t.Errorf("outcome = %v, want dead", s.Outcome)
	}
}

func TestHandlerPanicIsAFailureNotACrash(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 1)
	subscribe(t, reg, c.Type, "buggy", func(context.Context, outbox.Delivery) error { panic("nil map") })
	store := &fakeStore{queue: []outbox.Claim{c}}

	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := onlySettlement(t, store); s.Outcome != outbox.OutcomeRetry {
		t.Errorf("outcome = %v, want retry", s.Outcome)
	}
}

func TestHandlerTimeout(t *testing.T) {
	reg := outbox.NewRegistry()
	c := newClaim("a.happened", 1)
	subscribe(t, reg, c.Type, "slow", func(ctx context.Context, _ outbox.Delivery) error {
		<-ctx.Done()
		return ctx.Err()
	})
	store := &fakeStore{queue: []outbox.Claim{c}}
	cfg := testConfig()
	cfg.HandlerTimeout = 20 * time.Millisecond
	d, err := outbox.NewDispatcher(store, reg, cfg, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("handler timeout not enforced: took %v", elapsed)
	}
	if s := onlySettlement(t, store); s.Outcome != outbox.OutcomeRetry {
		t.Errorf("outcome = %v, want retry", s.Outcome)
	}
}

func TestLeaseExhaustionReleasesUnstartedEvents(t *testing.T) {
	reg := outbox.NewRegistry()
	first, second := newClaim("a.happened", 1), newClaim("a.happened", 1)
	now := time.Now()
	clock := func() time.Time { return now }
	subscribe(t, reg, "a.happened", "slow", func(context.Context, outbox.Delivery) error {
		now = now.Add(2 * time.Minute) // past the 1m lease
		return nil
	})
	store := &fakeStore{queue: []outbox.Claim{first, second}}

	d := newDispatcher(t, store, reg, outbox.DispatcherOptions{Now: clock})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := store.settled()
	if len(got) != 2 {
		t.Fatalf("settlements = %+v", got)
	}
	if got[0].Outcome != outbox.OutcomeDelivered {
		t.Errorf("first outcome = %v, want delivered", got[0].Outcome)
	}
	if got[1].EventID != second.EventID || got[1].Outcome != outbox.OutcomeRelease {
		t.Errorf("second settlement = %+v, want release", got[1])
	}
}

func TestLostLeaseIsNotAnError(t *testing.T) {
	store := &fakeStore{queue: []outbox.Claim{newClaim("a.happened", 1)}, settleErr: outbox.ErrLeaseLost}
	n, err := newDispatcher(t, store, outbox.NewRegistry(), outbox.DispatcherOptions{}).ProcessBatch(context.Background())
	if err != nil || n != 1 {
		t.Errorf("ProcessBatch = %d, %v", n, err)
	}
}

func TestClaimErrorIsReturned(t *testing.T) {
	store := &fakeStore{claimErr: errors.New("db down")}
	if _, err := newDispatcher(t, store, outbox.NewRegistry(), outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err == nil {
		t.Error("claim error swallowed")
	}
}

func TestHandlerContinuesThePublisherTrace(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	c := newClaim("a.happened", 1)
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	c.TraceContext = map[string]string{"traceparent": "00-" + traceID + "-00f067aa0ba902b7-01"}

	reg := outbox.NewRegistry()
	var got string
	subscribe(t, reg, c.Type, "traced", func(ctx context.Context, _ outbox.Delivery) error {
		got = trace.SpanContextFromContext(ctx).TraceID().String()
		return nil
	})
	store := &fakeStore{queue: []outbox.Claim{c}}
	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{TracerProvider: tp}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != traceID {
		t.Errorf("handler trace = %s, want %s", got, traceID)
	}
}

func TestRunDrainsAndStopsOnCancel(t *testing.T) {
	reg := outbox.NewRegistry()
	var mu sync.Mutex
	handled := 0
	subscribe(t, reg, "a.happened", "count", func(context.Context, outbox.Delivery) error {
		mu.Lock()
		defer mu.Unlock()
		handled++
		return nil
	})
	var queue []outbox.Claim
	for range 25 { // more than one batch of 10
		queue = append(queue, newClaim("a.happened", 1))
	}
	store := &fakeStore{queue: queue}
	d := newDispatcher(t, store, reg, outbox.DispatcherOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for len(store.settled()) < 25 {
		select {
		case <-deadline:
			t.Fatalf("only %d of 25 settled", len(store.settled()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if handled != 25 {
		t.Errorf("handled %d, want 25", handled)
	}
}

func TestShutdownReleasesRemainingClaims(t *testing.T) {
	reg := outbox.NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	subscribe(t, reg, "a.happened", "stopper", func(context.Context, outbox.Delivery) error {
		cancel() // shutdown arrives mid-batch
		return nil
	})
	store := &fakeStore{queue: []outbox.Claim{newClaim("a.happened", 1), newClaim("a.happened", 1)}}

	_, _ = newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(ctx)
	got := store.settled()
	if len(got) != 2 || got[1].Outcome != outbox.OutcomeRelease {
		t.Errorf("settlements = %+v, want the second released", got)
	}
}

func TestNewDispatcherValidatesConfig(t *testing.T) {
	cfg := testConfig()
	cfg.Lease = cfg.HandlerTimeout
	if _, err := outbox.NewDispatcher(&fakeStore{}, outbox.NewRegistry(), cfg, outbox.DispatcherOptions{}); err == nil {
		t.Error("lease <= handler timeout accepted")
	}
	cfg = testConfig()
	cfg.BatchSize = 0
	if _, err := outbox.NewDispatcher(&fakeStore{}, outbox.NewRegistry(), cfg, outbox.DispatcherOptions{}); err == nil {
		t.Error("zero batch size accepted")
	}
}
