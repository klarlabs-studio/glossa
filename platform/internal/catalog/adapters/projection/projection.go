// Package projection adapts Localization's application service to
// Catalog's MessageProjection port: a bulk upsert updates Localization's
// projection of the messages it changed in its own transaction, without
// Catalog touching Localization's tables.
package projection

import (
	"context"
	"slices"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	locapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

// Port implements app.MessageProjection.
type Port struct{ svc *locapp.Service }

// New returns the port.
func New(svc *locapp.Service) *Port { return &Port{svc: svc} }

var _ app.MessageProjection = (*Port)(nil)

// MessagesChanged implements app.MessageProjection, in key order (the
// order every bulk writer locks in).
func (p *Port) MessagesChanged(ctx context.Context, ms []domain.Message) error {
	states := make([]locapp.MessageState, len(ms))
	for i, m := range ms {
		states[i] = locapp.MessageState{
			MessageID: m.ID.UUID(), ProjectID: m.ProjectID.UUID(), Key: string(m.Key), Namespace: string(m.Namespace),
			State: string(m.State), SourceRevision: m.Revision, Version: m.Version,
		}
	}
	slices.SortFunc(states, func(a, b locapp.MessageState) int { return strings.Compare(a.Key, b.Key) })
	return p.svc.ProjectMessages(ctx, states)
}
