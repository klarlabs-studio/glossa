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

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// recordingBatch is a batch subscriber that records its calls. fail
// decides each delivery's batch result; event decides HandleEvent's.
type recordingBatch struct {
	mu      sync.Mutex
	batches [][]uuid.UUID
	tenants []tenancy.ID
	events  []uuid.UUID
	fail    func(d outbox.Delivery) error
	event   func(d outbox.Delivery) error
}

func (b *recordingBatch) HandleBatch(ctx context.Context, ds []outbox.Delivery) []error {
	b.mu.Lock()
	defer b.mu.Unlock()
	tenant, _ := tenancy.FromContext(ctx)
	b.tenants = append(b.tenants, tenant)
	got := make([]uuid.UUID, len(ds))
	errs := make([]error, len(ds))
	for i, d := range ds {
		got[i] = d.EventID
		if d.TenantID != tenant {
			errs[i] = errors.New("delivery outside the batch's tenant")
		} else if b.fail != nil {
			errs[i] = b.fail(d)
		}
	}
	b.batches = append(b.batches, got)
	return errs
}

func (b *recordingBatch) HandleEvent(_ context.Context, d outbox.Delivery) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, d.EventID)
	if b.event != nil {
		return b.event(d)
	}
	return nil
}

func eventIDs(cs ...outbox.Claim) []uuid.UUID {
	out := make([]uuid.UUID, len(cs))
	for i, c := range cs {
		out[i] = c.EventID
	}
	return out
}

func settlementFor(t *testing.T, s *fakeStore, id uuid.UUID) outbox.Settlement {
	t.Helper()
	for _, st := range s.settled() {
		if st.EventID == id {
			return st
		}
	}
	t.Fatalf("no settlement for %s", id)
	return outbox.Settlement{}
}

func subscribeBatch(t *testing.T, r *outbox.Registry, name string, h outbox.BatchHandler, types ...string) {
	t.Helper()
	if err := r.SubscribeBatch(name, h, types...); err != nil {
		t.Fatal(err)
	}
}

func TestBatchSubscriberGetsEachTenantsDeliveriesInClaimOrder(t *testing.T) {
	reg := outbox.NewRegistry()
	b := &recordingBatch{}
	subscribeBatch(t, reg, "audit.record", b, "a.happened", "b.happened")
	var perEvent []uuid.UUID
	subscribe(t, reg, "a.happened", "other", func(_ context.Context, d outbox.Delivery) error {
		perEvent = append(perEvent, d.EventID)
		return nil
	})
	t1, t2 := tenancy.NewID(), tenancy.NewID()
	claim := func(typ string, tenant tenancy.ID, deliveredTo ...string) outbox.Claim {
		c := newClaim(typ, 1, deliveredTo...)
		c.TenantID = tenant
		return c
	}
	c1, c2, c3, c4, c5 := claim("a.happened", t1), claim("b.happened", t2), claim("b.happened", t1), claim("a.happened", t2), claim("a.happened", t1)
	done := claim("a.happened", t1, "audit.record") // a retry: the batch subscriber already succeeded
	order := []outbox.Claim{c1, c2, c3, c4, done, c5}
	store := &fakeStore{queue: order}

	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := [][]uuid.UUID{eventIDs(c1, c3, c5), eventIDs(c2, c4)}; !slices.EqualFunc(b.batches, want, slices.Equal) {
		t.Errorf("batches = %v, want %v (per tenant, claim order)", b.batches, want)
	}
	if !slices.Equal(b.tenants, []tenancy.ID{t1, t2}) || len(b.events) != 0 {
		t.Errorf("tenants = %v, single deliveries = %v", b.tenants, b.events)
	}
	if want := eventIDs(c1, c4, done, c5); !slices.Equal(perEvent, want) {
		t.Errorf("per-event subscriber saw %v, want %v (claim order)", perEvent, want)
	}
	got := store.settled()
	if len(got) != len(order) {
		t.Fatalf("settlements = %+v", got)
	}
	for i, c := range order {
		if got[i].EventID != c.EventID || got[i].Outcome != outbox.OutcomeDelivered {
			t.Errorf("settlement %d = %+v, want %s delivered (claim order)", i, got[i], c.EventID)
		}
	}
	for _, c := range []outbox.Claim{c1, done} {
		if s := settlementFor(t, store, c.EventID); !slices.Equal(s.DeliveredTo, []string{"audit.record", "other"}) {
			t.Errorf("%s delivered to %v", c.EventID, s.DeliveredTo)
		}
	}
}

func TestABatchFailureRedeliversOnlyTheFailedDeliveries(t *testing.T) {
	reg := outbox.NewRegistry()
	c1, c2, c3 := newClaim("a.happened", 1), newClaim("a.happened", 2), newClaim("a.happened", 1)
	c2.TenantID, c3.TenantID = c1.TenantID, c1.TenantID
	calls := 0
	b := &recordingBatch{
		fail: func(d outbox.Delivery) error {
			if d.EventID == c2.EventID {
				return errors.New("conflict")
			}
			return nil
		},
		event: func(outbox.Delivery) error {
			calls++
			if calls < 2 {
				return errors.New("still conflicting") // the inline retry gets it
			}
			return nil
		},
	}
	subscribeBatch(t, reg, "audit.record", b, "a.happened")
	store := &fakeStore{queue: []outbox.Claim{c1, c2, c3}}
	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.batches) != 1 || !slices.Equal(b.events, eventIDs(c2, c2)) {
		t.Errorf("batches %v, single deliveries %v: want only c2 redelivered, retried inline", b.batches, b.events)
	}
	for _, c := range []outbox.Claim{c1, c2, c3} {
		if s := settlementFor(t, store, c.EventID); s.Outcome != outbox.OutcomeDelivered || !slices.Equal(s.DeliveredTo, []string{"audit.record"}) {
			t.Errorf("%s settled %+v", c.EventID, s)
		}
	}
}

func TestABatchDeliveryThatStillFailsIsRescheduledAlone(t *testing.T) {
	reg := outbox.NewRegistry()
	c1, c2 := newClaim("a.happened", 1), newClaim("a.happened", 1)
	c2.TenantID = c1.TenantID
	boom := errors.New("boom")
	b := &recordingBatch{
		fail: func(d outbox.Delivery) error {
			if d.EventID == c1.EventID {
				return boom
			}
			return nil
		},
		event: func(outbox.Delivery) error { return boom },
	}
	subscribeBatch(t, reg, "audit.record", b, "a.happened")
	store := &fakeStore{queue: []outbox.Claim{c1, c2}}
	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.events) != testConfig().InlineAttempts {
		t.Errorf("c1 delivered alone %d times, want the inline attempts", len(b.events))
	}
	if s := settlementFor(t, store, c1.EventID); s.Outcome != outbox.OutcomeRetry || len(s.DeliveredTo) != 0 || s.RetryAfter != time.Second {
		t.Errorf("c1 settled %+v, want a retry", s)
	}
	if s := settlementFor(t, store, c2.EventID); s.Outcome != outbox.OutcomeDelivered {
		t.Errorf("c2 settled %+v, want delivered", s)
	}
}

func TestAPermanentBatchErrorDeadLettersWithoutRedelivery(t *testing.T) {
	reg := outbox.NewRegistry()
	b := &recordingBatch{fail: func(outbox.Delivery) error { return outbox.Permanent(errors.New("undecodable")) }}
	subscribeBatch(t, reg, "audit.record", b, "a.happened")
	store := &fakeStore{queue: []outbox.Claim{newClaim("a.happened", 1)}}
	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := onlySettlement(t, store); s.Outcome != outbox.OutcomeDead || len(b.events) != 0 {
		t.Errorf("settlement %+v after %d single deliveries, want dead and none", s, len(b.events))
	}
}

// brokenBatch panics in HandleBatch, or answers with too few results.
type brokenBatch struct {
	short  bool
	events int
}

func (p *brokenBatch) HandleBatch(context.Context, []outbox.Delivery) []error {
	if p.short {
		return nil
	}
	panic("batch exploded")
}

func (p *brokenBatch) HandleEvent(context.Context, outbox.Delivery) error {
	p.events++
	return nil
}

func TestABrokenBatchFallsBackToSingleDeliveries(t *testing.T) {
	for _, short := range []bool{false, true} {
		reg := outbox.NewRegistry()
		b := &brokenBatch{short: short}
		subscribeBatch(t, reg, "audit.record", b, "a.happened")
		c1, c2 := newClaim("a.happened", 1), newClaim("a.happened", 1)
		store := &fakeStore{queue: []outbox.Claim{c1, c2}}
		if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		if b.events != 2 {
			t.Errorf("short=%t: %d single deliveries, want 2", short, b.events)
		}
		for _, s := range store.settled() {
			if s.Outcome != outbox.OutcomeDelivered {
				t.Errorf("short=%t: settled %+v", short, s)
			}
		}
	}
}

func TestLeaseExhaustedAfterABatchReleasesKeepingWhatSucceeded(t *testing.T) {
	reg := outbox.NewRegistry()
	now := time.Now()
	b := &recordingBatch{fail: func(outbox.Delivery) error {
		now = now.Add(2 * time.Minute) // the batch used up the 1m lease
		return nil
	}}
	subscribeBatch(t, reg, "audit.record", b, "a.happened")
	ran := false
	subscribe(t, reg, "a.happened", "other", func(context.Context, outbox.Delivery) error {
		ran = true
		return nil
	})
	store := &fakeStore{queue: []outbox.Claim{newClaim("a.happened", 1, "earlier")}}
	d := newDispatcher(t, store, reg, outbox.DispatcherOptions{Now: func() time.Time { return now }})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := onlySettlement(t, store)
	if ran || s.Outcome != outbox.OutcomeRelease || !slices.Equal(s.DeliveredTo, []string{"earlier", "audit.record"}) {
		t.Errorf("settlement %+v (per-event subscriber ran: %t), want released keeping audit.record", s, ran)
	}
}

func TestShutdownBeforeABatchReleasesItsClaims(t *testing.T) {
	reg := outbox.NewRegistry()
	b := &recordingBatch{}
	subscribeBatch(t, reg, "audit.record", b, "a.happened")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeStore{queue: []outbox.Claim{newClaim("a.happened", 1, "earlier")}}
	_, _ = newDispatcher(t, store, reg, outbox.DispatcherOptions{}).ProcessBatch(ctx)
	if s := onlySettlement(t, store); s.Outcome != outbox.OutcomeRelease || !slices.Equal(s.DeliveredTo, []string{"earlier"}) || len(b.batches) != 0 {
		t.Errorf("settlement %+v after %d batches, want released untouched", s, len(b.batches))
	}
}

// batchStore settles a batch at once; broken makes it fail as a whole.
type batchStore struct {
	fakeStore
	calls  int
	broken bool
	lost   uuid.UUID
}

func (b *batchStore) SettleAll(_ context.Context, ss []outbox.Settlement) []error {
	b.calls++
	if b.broken {
		return nil
	}
	errs := make([]error, len(ss))
	for i, s := range ss {
		if s.EventID == b.lost {
			errs[i] = outbox.ErrLeaseLost
			continue
		}
		b.settlements = append(b.settlements, s)
	}
	return errs
}

func settlementCount(t *testing.T, g prometheus.Gatherer, outcome string) float64 {
	t.Helper()
	families, err := g.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "glossa_outbox_settlements_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetValue() == outcome {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func TestABatchIsSettledInOneRoundTrip(t *testing.T) {
	reg := outbox.NewRegistry()
	c1, c2, c3 := newClaim("a.happened", 1), newClaim("a.happened", 1), newClaim("a.happened", 1)
	metrics := prometheus.NewRegistry()
	store := &batchStore{fakeStore: fakeStore{queue: []outbox.Claim{c1, c2, c3}}, lost: c2.EventID}
	if _, err := newDispatcher(t, store, reg, outbox.DispatcherOptions{Registerer: metrics}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.settled(); store.calls != 1 || len(got) != 2 || got[0].EventID != c1.EventID || got[1].EventID != c3.EventID {
		t.Errorf("%d SettleAll calls, settled %+v", store.calls, got)
	}
	if n := settlementCount(t, metrics, "lease_lost"); n != 1 {
		t.Errorf("lease_lost settlements = %v, want 1", n)
	}

	broken := &batchStore{fakeStore: fakeStore{queue: []outbox.Claim{newClaim("a.happened", 1), newClaim("a.happened", 1)}}, broken: true}
	if _, err := newDispatcher(t, broken, reg, outbox.DispatcherOptions{}).ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := broken.settled(); len(got) != 2 {
		t.Errorf("after a failed SettleAll, settled one at a time: %+v", got)
	}
}

func TestSubscribeBatchNames(t *testing.T) {
	reg := outbox.NewRegistry()
	b := &recordingBatch{}
	if err := reg.SubscribeBatch("audit.record", b); !errors.Is(err, outbox.ErrInvalidSubscription) {
		t.Errorf("no types: %v", err)
	}
	if err := reg.SubscribeBatch("audit.record", b, "a.happened", "Bad"); !errors.Is(err, outbox.ErrInvalidSubscription) {
		t.Errorf("bad type: %v", err)
	}
	if err := reg.SubscribeBatch("audit.record", b, "a.happened"); err != nil {
		t.Errorf("after a failed attempt the name is free: %v", err)
	}
	if err := reg.SubscribeBatch("audit.record", b, "b.happened"); !errors.Is(err, outbox.ErrInvalidSubscription) {
		t.Errorf("twice: %v", err)
	}
	if err := reg.Subscribe("b.happened", "audit.record", b); !errors.Is(err, outbox.ErrInvalidSubscription) {
		t.Errorf("batch name reused per event: %v", err)
	}
	if err := reg.Subscribe("b.happened", "plain", b); err != nil {
		t.Fatal(err)
	}
	if err := reg.SubscribeBatch("plain", b, "c.happened"); !errors.Is(err, outbox.ErrInvalidSubscription) {
		t.Errorf("per-event name reused for a batch: %v", err)
	}
}
