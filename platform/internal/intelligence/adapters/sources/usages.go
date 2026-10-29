package sources

import (
	"context"
	"errors"

	"github.com/google/uuid"

	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
)

// ── Context ──────────────────────────────────────────────────────────

// Usages implements app.UsageContext on the Context context's service:
// a message's current usages on the default branch and the messages
// shown together with it. Its reads need catalog.read, which the job
// worker's principal holds.
type Usages struct{ svc *contextapp.Service }

// NewUsages returns the port.
func NewUsages(svc *contextapp.Service) *Usages { return &Usages{svc: svc} }

var _ app.UsageContext = (*Usages)(nil)

func contextErr(err error) error {
	if errors.Is(err, contextapp.ErrProjectNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}

// Usages implements app.UsageContext.
func (u *Usages) Usages(ctx context.Context, project, message uuid.UUID, limit int) ([]app.Usage, error) {
	vs, err := u.svc.MessageUsages(ctx, project, message, contextapp.UsageQuery{Limit: limit})
	if err != nil {
		return nil, contextErr(err)
	}
	out := make([]app.Usage, len(vs))
	for i, v := range vs {
		out[i] = app.Usage{File: v.File, Line: v.Line, Component: v.Component, Route: v.Route}
	}
	return out, nil
}

// CoLocated implements app.UsageContext.
func (u *Usages) CoLocated(ctx context.Context, project, message uuid.UUID, limit int) ([]uuid.UUID, error) {
	ids, err := u.svc.CoLocated(ctx, project, message, limit)
	return ids, contextErr(err)
}
