// Package catalog adapts Catalog's application service to
// Localization's SourceCatalog port: the only way Localization reads
// projects and source messages (RFC 0002 §4, contexts reach each other
// through application ports).
package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
)

// Port implements app.SourceCatalog on Catalog's service.
type Port struct{ svc *catalogapp.Service }

// New returns the port.
func New(svc *catalogapp.Service) *Port { return &Port{svc: svc} }

var _ app.SourceCatalog = (*Port)(nil)

// notFound maps Catalog's not-found to Localization's.
func notFound(err error) error {
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.ErrNotFound
	}
	return err
}

// Project implements app.SourceCatalog.
func (p *Port) Project(ctx context.Context, id uuid.UUID) (app.ProjectInfo, error) {
	pr, err := p.svc.GetProject(ctx, catalogdomain.ProjectID(id))
	if err != nil {
		return app.ProjectInfo{}, notFound(err)
	}
	return app.ProjectInfo{
		ID: id, SourceLocale: pr.SourceLocale, DefaultSyntax: pr.Settings.DefaultSyntax,
		ReviewRequired: pr.Settings.ReviewRequired,
	}, nil
}

func message(m catalogdomain.Message) app.SourceMessage {
	return app.SourceMessage{
		ID: m.ID.UUID(), ProjectID: m.ProjectID.UUID(), Key: string(m.Key), Namespace: string(m.Namespace),
		State: string(m.State), Revision: m.Revision, Version: m.Version, Content: m.Source, MaxLength: m.MaxLength,
	}
}

// Message implements app.SourceCatalog.
func (p *Port) Message(ctx context.Context, project uuid.UUID, key string) (app.SourceMessage, error) {
	m, err := p.svc.GetMessage(ctx, catalogdomain.ProjectID(project), key)
	if err != nil {
		return app.SourceMessage{}, notFound(err)
	}
	return message(m), nil
}

// MessagesByKeys implements app.SourceCatalog.
func (p *Port) MessagesByKeys(ctx context.Context, project uuid.UUID, keys []string) (map[string]app.SourceMessage, error) {
	found, err := p.svc.MessagesByKeys(ctx, catalogdomain.ProjectID(project), keys)
	if err != nil {
		return nil, notFound(err)
	}
	out := make(map[string]app.SourceMessage, len(found))
	for k, m := range found {
		out[k] = message(m)
	}
	return out, nil
}

// SourcesAt implements app.SourceCatalog.
func (p *Port) SourcesAt(ctx context.Context, project uuid.UUID, refs []app.SourceRef) (map[app.SourceRef]mfcontent.Content, error) {
	want := make([]catalogapp.SourceRef, len(refs))
	for i, r := range refs {
		want[i] = catalogapp.SourceRef{Message: catalogdomain.MessageID(r.Message), Revision: r.Revision}
	}
	found, err := p.svc.SourceRevisionsOf(ctx, catalogdomain.ProjectID(project), want)
	if err != nil {
		return nil, notFound(err)
	}
	out := make(map[app.SourceRef]mfcontent.Content, len(found))
	for r, rev := range found {
		out[app.SourceRef{Message: r.Message.UUID(), Revision: r.Revision}] = rev.Content
	}
	return out, nil
}

// SourceAt implements app.SourceCatalog.
func (p *Port) SourceAt(ctx context.Context, project, id uuid.UUID, n int) (mfcontent.Content, error) {
	rev, err := p.svc.SourceRevisionOf(ctx, catalogdomain.ProjectID(project), catalogdomain.MessageID(id), n)
	if err != nil {
		return mfcontent.Content{}, notFound(err)
	}
	return rev.Content, nil
}
