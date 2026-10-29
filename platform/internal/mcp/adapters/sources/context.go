package sources

import (
	"context"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalog "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// Usages adapts the Context context to tools.UsageReader. Neighbours
// needs Catalog too: Context answers with message ids, and an agent can
// do nothing with an id it cannot name.
type Usages struct {
	usages  *contextapp.Service
	catalog *catalogapp.Service
}

// NewUsages returns the adapter.
func NewUsages(u *contextapp.Service, c *catalogapp.Service) *Usages {
	return &Usages{usages: u, catalog: c}
}

var _ tools.UsageReader = (*Usages)(nil)

// contextNotFound lists the context's own not-found errors.
var contextNotFound = []error{
	contextapp.ErrNotFound, contextapp.ErrProjectNotFound, contextapp.ErrMessageNotFound,
}

// OfKey implements tools.UsageReader.
func (a *Usages) OfKey(
	ctx context.Context, project uuid.UUID, key, branch string, limit int,
) (tools.Usages, error) {
	got, err := a.usages.UsagesOfKey(ctx, project, key, contextapp.UsageQuery{Branch: branch, Limit: limit})
	if err != nil {
		return tools.Usages{}, notFound(err, contextNotFound...)
	}
	out := tools.Usages{
		MessageID: got.MessageID.String(), Key: key, Branch: branch,
		Usages: make([]tools.Usage, len(got.Usages)), Truncated: got.Truncated,
	}
	for i, u := range got.Usages {
		out.Usages[i] = tools.Usage{
			File: u.File, Line: u.Line, Column: u.Column, Component: u.Component, Route: u.Route,
			Kind: u.Kind, Branch: string(u.Branch), OnDefaultBranch: u.OnDefaultBranch,
		}
	}
	return out, nil
}

// CaptureCount implements tools.UsageReader.
func (a *Usages) CaptureCount(
	ctx context.Context, project uuid.UUID, key string, limit int,
) (int, bool, error) {
	got, err := a.usages.CapturesOfKey(ctx, project, key, contextapp.UsageQuery{Limit: limit})
	if err != nil {
		return 0, false, notFound(err, contextNotFound...)
	}
	return len(got.Captures), got.Truncated, nil
}

// Neighbours implements tools.UsageReader.
func (a *Usages) Neighbours(
	ctx context.Context, project, message uuid.UUID, limit int,
) ([]tools.Neighbour, error) {
	ids, err := a.usages.CoLocated(ctx, project, message, limit)
	if err != nil {
		return nil, notFound(err, contextNotFound...)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]catalog.MessageID, len(ids))
	for i, id := range ids {
		keys[i] = catalog.MessageID(id)
	}
	byID, err := a.catalog.MessagesByIDs(ctx, catalog.ProjectID(project), keys)
	if err != nil {
		return nil, notFound(err, catalogapp.ErrNotFound)
	}
	// Context's order is "most shared first", which is the useful one.
	out := make([]tools.Neighbour, 0, len(ids))
	for _, id := range ids {
		if m, ok := byID[catalog.MessageID(id)]; ok {
			out = append(out, tools.Neighbour{ID: id.String(), Key: string(m.Key)})
		}
	}
	return out, nil
}
