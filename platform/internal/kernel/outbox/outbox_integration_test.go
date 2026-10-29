//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy/tenantpg"
)

var env *dbtest.Env

func TestMain(m *testing.M) {
	var err error
	env, err = dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	env.Close()
	os.Exit(code)
}

type fixture struct {
	uow    *db.UnitOfWork
	store  *outbox.PostgresStore
	tenant tenancy.ID
	ctx    context.Context // scoped to tenant
}

func setup(t *testing.T) fixture {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	tn, _ := tenancy.NewTenant(tenancy.KindOrganization, "acme", "Acme")
	ctx := tenancy.ContextWithTenant(context.Background(), tn.ID)
	err := uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tenantpg.Insert(ctx, tx, tn)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{uow: uow, store: outbox.NewPostgresStore(uow), tenant: tn.ID, ctx: ctx}
}

func (f fixture) publish(t *testing.T, ctx context.Context, n int) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	err := f.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		for i := range n {
			id, err := outbox.Publish(ctx, tx, outbox.Event{
				Type: "catalog.source_revised", AggregateType: "message",
				AggregateID: fmt.Sprintf("m%d", i), Actor: personActor, Payload: map[string]int{"revision": i},
			})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return ids
}

func (f fixture) dispatcher(t *testing.T, reg *outbox.Registry, mutate func(*outbox.DispatcherConfig)) *outbox.Dispatcher {
	t.Helper()
	cfg := outbox.DispatcherConfig{
		BatchSize: 10, MaxAttempts: 3, PollInterval: 10 * time.Millisecond,
		Lease: time.Minute, HandlerTimeout: 5 * time.Second,
		InlineAttempts: 2, InlineBackoff: time.Millisecond,
		RetryBase: time.Millisecond, RetryMax: 10 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := outbox.NewDispatcher(f.store, reg, cfg, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type eventState struct {
	status      string
	attempts    int
	deliveredTo []string
	lastError   *string
}

func state(t *testing.T, id uuid.UUID) eventState {
	t.Helper()
	var s eventState
	err := env.Super.QueryRow(context.Background(),
		"SELECT status, attempts, delivered_to, last_error FROM outbox_events WHERE id = $1", id,
	).Scan(&s.status, &s.attempts, &s.deliveredTo, &s.lastError)
	if err != nil {
		t.Fatalf("load event %s: %v", id, err)
	}
	return s
}

func TestPublishRolledBackPublishesNothing(t *testing.T) {
	f := setup(t)
	rollback := errors.New("business rule violated")
	err := f.uow.InTenantTx(f.ctx, func(ctx context.Context, tx *db.TenantTx) error {
		if _, err := outbox.Publish(ctx, tx, outbox.Event{
			Type: "catalog.source_revised", AggregateType: "message", AggregateID: "m1", Actor: personActor,
		}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("err = %v", err)
	}

	reg := outbox.NewRegistry()
	_ = reg.Subscribe("catalog.source_revised", "never", outbox.HandlerFunc(func(context.Context, outbox.Delivery) error {
		t.Error("handler ran for a rolled-back event")
		return nil
	}))
	n, err := f.dispatcher(t, reg, nil).ProcessBatch(context.Background())
	if err != nil || n != 0 {
		t.Errorf("ProcessBatch = %d, %v; want 0 events", n, err)
	}
}

func TestPublishValidatesEvent(t *testing.T) {
	f := setup(t)
	err := f.uow.InTenantTx(f.ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := outbox.Publish(ctx, tx, outbox.Event{Type: "x.y"})
		return err
	})
	if !errors.Is(err, outbox.ErrInvalidEvent) {
		t.Errorf("err = %v, want ErrInvalidEvent", err)
	}
}

// PublishAll stores every event in one statement, in order, with the
// same columns Publish fills; one invalid event stores none.
func TestPublishAllStoresEveryEventInOrder(t *testing.T) {
	f := setup(t)
	events := make([]outbox.Event, 3)
	for i := range events {
		events[i] = outbox.Event{
			Type: "catalog.source_revised", AggregateType: "message",
			AggregateID: fmt.Sprintf("m%d", i), Actor: personActor, Payload: map[string]int{"revision": i},
		}
	}
	var ids []uuid.UUID
	err := f.uow.InTenantTx(f.ctx, func(ctx context.Context, tx *db.TenantTx) (err error) {
		ids, err = outbox.PublishAll(ctx, tx, events)
		return err
	})
	if err != nil || len(ids) != 3 {
		t.Fatalf("PublishAll = %v, %v", ids, err)
	}
	rows, err := env.Super.Query(context.Background(),
		"SELECT id, tenant_id, aggregate_id, actor, payload->>'revision', status FROM outbox_events ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var (
			id, tenant             uuid.UUID
			agg, actor, rev, state string
		)
		if err := rows.Scan(&id, &tenant, &agg, &actor, &rev, &state); err != nil {
			t.Fatal(err)
		}
		if id != ids[i] || tenant != f.tenant.UUID() || agg != fmt.Sprintf("m%d", i) || actor != string(personActor) ||
			rev != fmt.Sprint(i) || state != "pending" {
			t.Errorf("row %d = %s %s %s %s %s %s", i, id, tenant, agg, actor, rev, state)
		}
		i++
	}
	if i != 3 {
		t.Fatalf("stored %d events, want 3", i)
	}

	events[1].Actor = ""
	err = f.uow.InTenantTx(f.ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := outbox.PublishAll(ctx, tx, events)
		return err
	})
	if !errors.Is(err, outbox.ErrInvalidEvent) {
		t.Errorf("PublishAll with an invalid event: err = %v, want ErrInvalidEvent", err)
	}
}

// An event without an actor is refused by Publish, and the database
// refuses it too: the column has no default, so a writer that bypassed
// Publish could not file it under "unknown" either.
func TestAnEventWithoutAnActorIsNeverStored(t *testing.T) {
	f := setup(t)
	err := f.uow.InTenantTx(f.ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := outbox.Publish(ctx, tx, outbox.Event{Type: "catalog.source_revised", AggregateType: "message", AggregateID: "m1"})
		return err
	})
	if !errors.Is(err, outbox.ErrInvalidEvent) {
		t.Fatalf("Publish without an actor: err = %v, want ErrInvalidEvent", err)
	}
	_, err = env.Super.Exec(context.Background(), `INSERT INTO outbox_events
		(id, tenant_id, event_type, aggregate_type, aggregate_id, payload) VALUES ($1, $2, 'a.b', 'b', '1', '{}')`,
		uuid.New(), f.tenant.UUID())
	if err == nil {
		t.Error("the database stored an event with no actor")
	}
	_, err = env.Super.Exec(context.Background(), `INSERT INTO outbox_events
		(id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload) VALUES ($1, $2, 'a.b', 'b', '1', 'someone', '{}')`,
		uuid.New(), f.tenant.UUID())
	if err == nil {
		t.Error("the database stored an actor that is not kind:uuid")
	}
}

// An event recorded before events named their actors (migration 0042
// gave those rows "unknown") is still delivered, and its handler sees
// ActorUnknown — never someone it could be attributed to.
func TestAnEventFromBeforeActorsIsReadAsUnknown(t *testing.T) {
	f := setup(t)
	id := uuid.New()
	if _, err := env.Super.Exec(context.Background(), `INSERT INTO outbox_events
		(id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload) VALUES ($1, $2, 'catalog.source_revised', 'message', 'm1', 'unknown', '{}')`,
		id, f.tenant.UUID()); err != nil {
		t.Fatal(err)
	}
	reg := outbox.NewRegistry()
	var saw outbox.Actor
	_ = reg.Subscribe("catalog.source_revised", "audit.probe", outbox.HandlerFunc(func(_ context.Context, d outbox.Delivery) error {
		saw = d.Actor
		return nil
	}))
	if _, err := f.dispatcher(t, reg, nil).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saw != outbox.ActorUnknown {
		t.Errorf("handler saw actor %q, want %q", saw, outbox.ActorUnknown)
	}
	if s := state(t, id); s.status != "delivered" {
		t.Errorf("event state = %+v, want delivered", s)
	}
}

func TestCommittedEventIsDeliveredOnceInTenantScope(t *testing.T) {
	f := setup(t)
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	pubCtx, span := tp.Tracer("test").Start(f.ctx, "api write")
	ids := f.publish(t, pubCtx, 1)
	span.End()

	reg := outbox.NewRegistry()
	var calls int
	var sawSlug tenancy.Slug
	var sawTrace trace.TraceID
	var sawActor outbox.Actor
	var payload struct{ Revision int }
	_ = reg.Subscribe("catalog.source_revised", "localization.mark_outdated",
		outbox.HandlerFunc(func(ctx context.Context, d outbox.Delivery) error {
			calls++
			sawActor = d.Actor
			sawTrace = trace.SpanContextFromContext(ctx).TraceID()
			if err := d.Decode(&payload); err != nil {
				return err
			}
			// The handler's context is tenant-scoped: it can open its own
			// unit of work and RLS admits the event's tenant.
			return f.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
				tn, err := tenantpg.Current(ctx, tx)
				sawSlug = tn.Slug
				return err
			})
		}))
	d := f.dispatcher(t, reg, nil)

	for range 3 {
		if _, err := d.ProcessBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("handler called %d times, want 1", calls)
	}
	if sawSlug != "acme" {
		t.Errorf("handler's tenant slug = %q, want acme", sawSlug)
	}
	if sawTrace != span.SpanContext().TraceID() {
		t.Errorf("handler trace %s, want publisher's %s", sawTrace, span.SpanContext().TraceID())
	}
	if payload.Revision != 0 {
		t.Errorf("payload = %+v", payload)
	}
	if sawActor != personActor {
		t.Errorf("handler saw actor %q, want the publisher's %q", sawActor, personActor)
	}
	s := state(t, ids[0])
	if s.status != "delivered" || s.attempts != 1 || len(s.deliveredTo) != 1 {
		t.Errorf("event state = %+v", s)
	}
}

func TestFailingHandlerRetriesThenDeadLetters(t *testing.T) {
	f := setup(t)
	ids := f.publish(t, f.ctx, 1)

	reg := outbox.NewRegistry()
	var okCalls, badCalls int
	_ = reg.Subscribe("catalog.source_revised", "ok", outbox.HandlerFunc(func(context.Context, outbox.Delivery) error {
		okCalls++
		return nil
	}))
	_ = reg.Subscribe("catalog.source_revised", "bad", outbox.HandlerFunc(func(context.Context, outbox.Delivery) error {
		badCalls++
		return errors.New("provider unavailable")
	}))
	d := f.dispatcher(t, reg, nil)

	deadline := time.Now().Add(10 * time.Second)
	for state(t, ids[0]).status == "pending" {
		if time.Now().After(deadline) {
			t.Fatalf("event still pending: %+v", state(t, ids[0]))
		}
		if _, err := d.ProcessBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond) // let the backoff elapse
	}

	s := state(t, ids[0])
	if s.status != "dead" || s.attempts != 3 {
		t.Errorf("event state = %+v, want dead after 3 attempts", s)
	}
	if s.lastError == nil || *s.lastError != "bad: provider unavailable" {
		t.Errorf("last_error = %v", s.lastError)
	}
	if okCalls != 1 {
		t.Errorf("succeeded subscriber ran %d times, want 1", okCalls)
	}
	if badCalls != 3*2 { // MaxAttempts × InlineAttempts
		t.Errorf("failing subscriber ran %d times, want 6", badCalls)
	}
}

func TestConcurrentDispatchersShareTheWork(t *testing.T) {
	f := setup(t)
	ids := f.publish(t, f.ctx, 60)

	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	reg := outbox.NewRegistry()
	_ = reg.Subscribe("catalog.source_revised", "count", outbox.HandlerFunc(func(_ context.Context, d outbox.Delivery) error {
		mu.Lock()
		defer mu.Unlock()
		seen[d.EventID]++
		return nil
	}))

	var wg sync.WaitGroup
	for range 3 {
		d := f.dispatcher(t, reg, nil)
		wg.Go(func() {
			for {
				n, err := d.ProcessBatch(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for _, id := range ids {
		if seen[id] != 1 {
			t.Errorf("event %s delivered %d times, want 1", id, seen[id])
		}
	}
}

func TestExpiredLeaseFencesTheOldHolder(t *testing.T) {
	f := setup(t)
	f.publish(t, f.ctx, 1)
	ctx := context.Background()

	first, err := f.store.Claim(ctx, 1, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	// Simulate the lease running out while the first holder is stuck.
	if _, err := env.Super.Exec(ctx, "UPDATE outbox_events SET available_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	second, err := f.store.Claim(ctx, 1, time.Minute)
	if err != nil || len(second) != 1 || second[0].Attempt != 2 {
		t.Fatalf("second claim = %+v, %v", second, err)
	}

	stale := outbox.Settlement{EventID: first[0].EventID, ClaimToken: first[0].ClaimToken, Outcome: outbox.OutcomeDelivered}
	if err := f.store.Settle(ctx, stale); !errors.Is(err, outbox.ErrLeaseLost) {
		t.Errorf("stale settle err = %v, want ErrLeaseLost", err)
	}
	fresh := outbox.Settlement{EventID: second[0].EventID, ClaimToken: second[0].ClaimToken, Outcome: outbox.OutcomeDelivered}
	if err := f.store.Settle(ctx, fresh); err != nil {
		t.Errorf("current holder's settle: %v", err)
	}
}

// SettleAll settles a batch in one transaction, each settlement still
// fenced by its own claim token (#89).
func TestSettleAllFencesEachSettlement(t *testing.T) {
	f := setup(t)
	ids := f.publish(t, f.ctx, 3)
	ctx := context.Background()
	claims, err := f.store.Claim(ctx, 3, time.Minute)
	if err != nil || len(claims) != 3 {
		t.Fatalf("claim = %v, %v", claims, err)
	}
	ss := make([]outbox.Settlement, len(claims))
	for i, c := range claims {
		ss[i] = outbox.Settlement{EventID: c.EventID, ClaimToken: c.ClaimToken, Outcome: outbox.OutcomeDelivered, DeliveredTo: []string{"x"}}
	}
	ss[1].ClaimToken = uuid.New() // a lease another dispatcher took over
	ss[2].Outcome, ss[2].RetryAfter, ss[2].LastError = outbox.OutcomeRetry, time.Minute, "boom"
	errs := f.store.SettleAll(ctx, ss)
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], outbox.ErrLeaseLost) || errs[2] != nil {
		t.Fatalf("SettleAll = %v, want only the second lease lost", errs)
	}
	want := map[uuid.UUID]string{ss[0].EventID: "delivered", ss[1].EventID: "pending", ss[2].EventID: "pending"}
	for _, id := range ids {
		if s := state(t, id); s.status != want[id] {
			t.Errorf("%s: %+v, want %s", id, s, want[id])
		}
	}
	if s := state(t, ss[2].EventID); s.lastError == nil || *s.lastError != "boom" {
		t.Errorf("retry not recorded: %+v", s)
	}
}

func TestReleaseDoesNotChargeAnAttempt(t *testing.T) {
	f := setup(t)
	ids := f.publish(t, f.ctx, 1)
	ctx := context.Background()

	claims, err := f.store.Claim(ctx, 1, time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim = %v, %v", claims, err)
	}
	rel := outbox.Settlement{EventID: claims[0].EventID, ClaimToken: claims[0].ClaimToken, Outcome: outbox.OutcomeRelease}
	if err := f.store.Settle(ctx, rel); err != nil {
		t.Fatal(err)
	}
	if s := state(t, ids[0]); s.status != "pending" || s.attempts != 0 {
		t.Errorf("after release: %+v", s)
	}
	again, err := f.store.Claim(ctx, 1, time.Minute)
	if err != nil || len(again) != 1 {
		t.Fatalf("released event not claimable again: %v, %v", again, err)
	}

	// A dispatcher that delivered to some subscribers before it had to
	// stop keeps them on release; they are not run again (#89).
	rel = outbox.Settlement{EventID: again[0].EventID, ClaimToken: again[0].ClaimToken, Outcome: outbox.OutcomeRelease,
		DeliveredTo: []string{"audit.record"}}
	if err := f.store.Settle(ctx, rel); err != nil {
		t.Fatal(err)
	}
	if s := state(t, ids[0]); s.status != "pending" || s.attempts != 0 || !slices.Equal(s.deliveredTo, []string{"audit.record"}) {
		t.Errorf("after a partial release: %+v", s)
	}
}
