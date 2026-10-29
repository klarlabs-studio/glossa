package app

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// autoTranslateBatch handles a delivered batch of the auto-translate
// triggers (#89). Each event is handled on its own, in the order
// delivered, as handle would; what the batch shares is the project's
// settings, read once per batch instead of once per event — a 500-item
// push into a project without auto-translate costs one read, not 500.
type autoTranslateBatch struct {
	byType map[string]outbox.HandlerFunc
}

// HandleEvent implements outbox.Handler.
func (b autoTranslateBatch) HandleEvent(ctx context.Context, d outbox.Delivery) error {
	h, ok := b.byType[d.Type]
	if !ok {
		return outbox.Permanent(errUnsubscribed(d.Type))
	}
	return h(ctx, d)
}

// HandleBatch implements outbox.BatchHandler.
func (b autoTranslateBatch) HandleBatch(ctx context.Context, ds []outbox.Delivery) []error {
	ctx = context.WithValue(ctx, settingsMemoKey{}, &settingsMemo{byProject: map[uuid.UUID]domain.ProjectSettings{}})
	errs := make([]error, len(ds))
	for i, d := range ds {
		if err := ctx.Err(); err != nil {
			errs[i] = err // out of time: the dispatcher delivers the rest alone
			continue
		}
		errs[i] = b.HandleEvent(ctx, d)
	}
	return errs
}

type errUnsubscribed string

func (e errUnsubscribed) Error() string { return "intelligence: not subscribed to " + string(e) }

type settingsMemo struct {
	mu        sync.Mutex
	byProject map[uuid.UUID]domain.ProjectSettings
}

type settingsMemoKey struct{}

// triggerSettings is projectSettings, read once per delivered batch.
func (s *Service) triggerSettings(ctx context.Context, project uuid.UUID) (domain.ProjectSettings, error) {
	m, _ := ctx.Value(settingsMemoKey{}).(*settingsMemo)
	if m != nil {
		m.mu.Lock()
		ps, found := m.byProject[project]
		m.mu.Unlock()
		if found {
			return ps, nil
		}
	}
	ps, err := s.projectSettings(ctx, project)
	if err == nil && m != nil {
		m.mu.Lock()
		m.byProject[project] = ps
		m.mu.Unlock()
	}
	return ps, err
}
