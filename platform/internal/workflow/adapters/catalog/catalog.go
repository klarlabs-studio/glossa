// Package catalog adapts Catalog's application service to Workflow's
// Catalog port: that a project exists, and which message a key names
// (RFC 0002 §4, contexts reach each other through application ports).
// Every call is an ordinary authorized Catalog use case, so Workflow
// learns nothing its caller couldn't read through the API.
package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
)

// Port implements app.Catalog on Catalog's service.
type Port struct{ svc *catalogapp.Service }

// New returns the port.
func New(svc *catalogapp.Service) *Port { return &Port{svc: svc} }

var _ app.Catalog = (*Port)(nil)

// Project implements app.Catalog.
func (p *Port) Project(ctx context.Context, project uuid.UUID) error {
	_, err := p.svc.GetProject(ctx, catalogdomain.ProjectID(project))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}

// MessageID implements app.Catalog with Catalog's key lookup. Messages
// of every state resolve: an instance of an obsolete message still
// belongs to it. A malformed key is simply not found.
func (p *Port) MessageID(ctx context.Context, project uuid.UUID, key string) (uuid.UUID, error) {
	found, err := p.svc.MessagesByKeys(ctx, catalogdomain.ProjectID(project), []string{key})
	if errors.Is(err, catalogapp.ErrNotFound) {
		return uuid.Nil, app.ErrProjectNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	m, ok := found[key]
	if !ok {
		return uuid.Nil, app.ErrNotFound
	}
	return m.ID.UUID(), nil
}
