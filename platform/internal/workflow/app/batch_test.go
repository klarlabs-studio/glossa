package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// queueStore is an outbox store that hands out its queue once and
// records the settlements.
type queueStore struct {
	queue   []outbox.Claim
	settled []outbox.Settlement
}

func (q *queueStore) Claim(context.Context, int, time.Duration) ([]outbox.Claim, error) {
	out := q.queue
	q.queue = nil
	return out, nil
}

func (q *queueStore) Settle(_ context.Context, s outbox.Settlement) error {
	q.settled = append(q.settled, s)
	return nil
}

// A push or an import delivers many of one actor's events on one
// project together (#89): the runner handles each on its own, but asks
// who the actor is and what the project binds once per batch.
func TestABatchLooksTheActorAndBindingsUpOnce(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	translator := w.person([]string{"translator"}, "de")
	reg := outbox.NewRegistry()
	if err := w.runner.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	store := &queueStore{}
	const n = 5
	for range n {
		payload, _ := json.Marshal(app.UnitPayload{ProjectID: w.project.String(), MessageID: uuid.NewString(), Locale: "de"})
		store.queue = append(store.queue, outbox.Claim{Delivery: outbox.Delivery{
			EventID: uuid.New(), TenantID: w.tenant, Type: "localization.translation.revised",
			AggregateType: "translation", AggregateID: "t", Actor: translator, Payload: payload, Attempt: 1,
		}, ClaimToken: uuid.New()})
	}
	d, err := outbox.NewDispatcher(store, reg, outbox.DispatcherConfig{
		BatchSize: n, MaxAttempts: 3, PollInterval: time.Millisecond, Lease: time.Minute, HandlerTimeout: 10 * time.Second,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range store.settled {
		if s.Outcome != outbox.OutcomeDelivered {
			t.Errorf("settled %+v", s)
		}
	}
	if len(w.store.instances) != n {
		t.Errorf("%d instances, want one per event", len(w.store.instances))
	}
	for _, i := range w.store.instances {
		if i.State != "awaiting_review" || i.Status != domain.StatusActive {
			t.Errorf("instance %+v", i)
		}
	}
	if w.actors.reads != 1 || w.bindings.reads != 1 {
		t.Errorf("actor read %d times, bindings %d times, want once each", w.actors.reads, w.bindings.reads)
	}
}
