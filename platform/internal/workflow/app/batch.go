package app

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// handleDeliveries is handleDelivery for a delivered batch (#89). Each
// event is still handled on its own, in the order delivered, each step
// in its own transaction, so one event's failure touches no other's;
// what the batch shares is the lookups every event of a push or an
// import repeats — who the actor is, and the project's bindings — read
// once per batch instead of once per event.
func (r *Runner) handleDeliveries(ctx context.Context, ds []outbox.Delivery) []error {
	ctx = context.WithValue(ctx, memoKey{}, &batchMemo{
		principals: map[outbox.Actor]principalLookup{},
		bindings:   map[uuid.UUID][]domain.Binding{},
	})
	errs := make([]error, len(ds))
	for i, d := range ds {
		if err := ctx.Err(); err != nil {
			errs[i] = err // out of time: the dispatcher delivers the rest alone
			continue
		}
		errs[i] = r.handleDelivery(ctx, d)
	}
	return errs
}

// batchMemo holds one batch's lookups. A batch is handled by one
// goroutine; the mutex only guards against a handler that fans out.
type batchMemo struct {
	mu         sync.Mutex
	principals map[outbox.Actor]principalLookup
	bindings   map[uuid.UUID][]domain.Binding
}

type principalLookup struct {
	p  authz.Principal
	ok bool
}

type memoKey struct{}

func memoOf(ctx context.Context) *batchMemo {
	m, _ := ctx.Value(memoKey{}).(*batchMemo)
	return m
}

// principal resolves actor, once per batch.
func (r *Runner) principal(ctx context.Context, actor outbox.Actor) (authz.Principal, bool, error) {
	m := memoOf(ctx)
	if m != nil {
		m.mu.Lock()
		l, found := m.principals[actor]
		m.mu.Unlock()
		if found {
			return l.p, l.ok, nil
		}
	}
	p, ok, err := r.d.Actors.Principal(ctx, actor)
	if err == nil && m != nil {
		m.mu.Lock()
		m.principals[actor] = principalLookup{p: p, ok: ok}
		m.mu.Unlock()
	}
	return p, ok, err
}

// bindings reads a project's bindings, once per batch.
func (r *Runner) bindings(ctx context.Context, project uuid.UUID) ([]domain.Binding, error) {
	m := memoOf(ctx)
	if m != nil {
		m.mu.Lock()
		bs, found := m.bindings[project]
		m.mu.Unlock()
		if found {
			return bs, nil
		}
	}
	bs, err := r.d.Definitions.Bindings(ctx, project)
	if err == nil && m != nil {
		m.mu.Lock()
		m.bindings[project] = bs
		m.mu.Unlock()
	}
	return bs, err
}
