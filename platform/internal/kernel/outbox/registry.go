package outbox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// ErrInvalidSubscription is returned by Subscribe for a bad name or a
// duplicate subscriber.
var ErrInvalidSubscription = errors.New("outbox: invalid subscription")

// Handler handles one delivery. See the package doc for the contract:
// idempotent on EventID, tenant-scoped context, at-least-once.
type Handler interface {
	HandleEvent(ctx context.Context, d Delivery) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, d Delivery) error

// HandleEvent calls f.
func (f HandlerFunc) HandleEvent(ctx context.Context, d Delivery) error { return f(ctx, d) }

// BatchHandler is a Handler that can also take a run of deliveries at
// once (#89), so a 500-item push costs it a few transactions instead
// of 500. Register it with [Registry.SubscribeBatch].
//
// HandleBatch gets deliveries of one tenant (the context's), in the
// order the dispatcher claimed them, possibly of several of the
// subscribed types. It returns one error per delivery, in the same
// order: nil means that delivery is handled and committed. A non-nil
// error means it was not; the dispatcher then delivers it again through
// HandleEvent, with the usual inline retries — unless the error is
// [Permanent], which dead-letters it as HandleEvent's would. A handler
// that fails as a whole (its transaction rolled back) returns the error
// for every delivery. The delivery contract of the package doc holds
// for each delivery as for HandleEvent: at least once, idempotent on
// EventID.
type BatchHandler interface {
	Handler
	HandleBatch(ctx context.Context, ds []Delivery) []error
}

// BatchHandlerFuncs adapts a pair of functions to BatchHandler.
type BatchHandlerFuncs struct {
	Event HandlerFunc
	Batch func(ctx context.Context, ds []Delivery) []error
}

// HandleEvent calls h.Event.
func (h BatchHandlerFuncs) HandleEvent(ctx context.Context, d Delivery) error { return h.Event(ctx, d) }

// HandleBatch calls h.Batch.
func (h BatchHandlerFuncs) HandleBatch(ctx context.Context, ds []Delivery) []error {
	return h.Batch(ctx, ds)
}

type subscription struct {
	name    string
	handler Handler
	// batch is set for a subscriber registered with SubscribeBatch.
	batch BatchHandler
}

// Registry maps event types to their subscribers.
type Registry struct {
	mu   sync.RWMutex
	subs map[string][]subscription
	// batched records, per subscriber name, whether it is a batch
	// subscriber, so one name is never both: the dispatcher groups a
	// batch subscriber's deliveries by name.
	batched map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{subs: map[string][]subscription{}, batched: map[string]bool{}}
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Subscribe registers h for eventType under a subscriber name, such as
// "localization.mark_outdated". The name is persisted with each event
// to record which subscribers already succeeded, so keep it stable
// across releases; renaming it makes pending events run it again.
func (r *Registry) Subscribe(eventType, subscriber string, h Handler) error {
	if h == nil {
		return fmt.Errorf("%w: type %q, subscriber %q", ErrInvalidSubscription, eventType, subscriber)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.batched[subscriber] {
		return fmt.Errorf("%w: %s is a batch subscriber", ErrInvalidSubscription, subscriber)
	}
	if err := r.add(eventType, subscription{name: subscriber, handler: h}); err != nil {
		return err
	}
	r.batched[subscriber] = false
	return nil
}

// SubscribeBatch registers h for every one of eventTypes under one
// subscriber name, as Subscribe would, and has the dispatcher hand it a
// claimed batch's deliveries together (see [BatchHandler]). A batch
// subscriber is registered once, with all its types.
func (r *Registry) SubscribeBatch(subscriber string, h BatchHandler, eventTypes ...string) error {
	if h == nil || len(eventTypes) == 0 {
		return fmt.Errorf("%w: batch subscriber %q", ErrInvalidSubscription, subscriber)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.batched[subscriber]; ok {
		return fmt.Errorf("%w: %s already subscribed", ErrInvalidSubscription, subscriber)
	}
	for i, typ := range eventTypes {
		if err := r.add(typ, subscription{name: subscriber, handler: h, batch: h}); err != nil {
			for _, done := range eventTypes[:i] { // all or nothing
				subs := r.subs[done]
				r.subs[done] = subs[:len(subs)-1]
			}
			return err
		}
	}
	r.batched[subscriber] = true
	return nil
}

// add appends s to eventType's subscribers; the caller holds the lock.
func (r *Registry) add(eventType string, s subscription) error {
	if !namePattern.MatchString(eventType) || !namePattern.MatchString(s.name) {
		return fmt.Errorf("%w: type %q, subscriber %q", ErrInvalidSubscription, eventType, s.name)
	}
	for _, have := range r.subs[eventType] {
		if have.name == s.name {
			return fmt.Errorf("%w: %s already subscribed to %s", ErrInvalidSubscription, s.name, eventType)
		}
	}
	r.subs[eventType] = append(r.subs[eventType], s)
	return nil
}

func (r *Registry) subscribers(eventType string) []subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]subscription(nil), r.subs[eventType]...)
}
