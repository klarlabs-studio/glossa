package sources

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalog "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// Catalog adapts Catalog's application service — and, for the `unused`
// filter, Context's — to tools.Catalog.
type Catalog struct {
	catalog *catalogapp.Service
	// usages answers `unused`, which is Context's question (RFC 0004
	// §2.2), not Catalog's. Nil leaves the filter refused rather than
	// silently ignored.
	usages *contextapp.Service
}

// NewCatalog returns the adapter. usages may be nil.
func NewCatalog(c *catalogapp.Service, usages *contextapp.Service) *Catalog {
	return &Catalog{catalog: c, usages: usages}
}

var _ tools.Catalog = (*Catalog)(nil)

// SearchMessages implements tools.Catalog.
func (a *Catalog) SearchMessages(
	ctx context.Context, project uuid.UUID, f tools.CatalogFilter, cursor string, limit int,
) ([]tools.MessageSummary, string, error) {
	p, err := page(cursor, limit)
	if err != nil {
		return nil, "", err
	}
	if f.Unused {
		return a.unused(ctx, project, f, p)
	}
	rows, token, err := a.catalog.ListMessages(ctx, catalog.ProjectID(project), catalogapp.MessageQuery{
		Namespace: f.Namespace, State: f.State, KeyPrefix: f.KeyPrefix,
		MissingIn: f.MissingIn, OutdatedIn: f.OutdatedIn,
	}, p)
	if err != nil {
		return nil, "", notFound(err, catalogapp.ErrNotFound)
	}
	out := make([]tools.MessageSummary, len(rows))
	for i, m := range rows {
		out[i] = summaryOf(m)
	}
	return out, next(token), nil
}

// unused answers the `unused` filter. Context knows which messages no
// current build refers to and answers in key order, so the page is cut
// from that list and the messages are then loaded by key — one read of
// each context, whatever the page size.
func (a *Catalog) unused(
	ctx context.Context, project uuid.UUID, f tools.CatalogFilter, p pagination.Page,
) ([]tools.MessageSummary, string, error) {
	if a.usages == nil {
		return nil, "", tools.ErrNotFound
	}
	un, err := a.usages.UnusedMessages(ctx, project, "")
	if err != nil {
		return nil, "", notFound(err, contextapp.ErrNotFound, contextapp.ErrProjectNotFound)
	}
	var keys []string
	for _, ref := range un.Messages {
		if ref.Key <= p.After || !strings.HasPrefix(ref.Key, f.KeyPrefix) {
			continue
		}
		keys = append(keys, ref.Key)
		if len(keys) == p.Limit() {
			break
		}
	}
	keys, token := pagination.Trim(keys, p, func(k string) string { return k })
	byKey, err := a.catalog.MessagesByKeys(ctx, catalog.ProjectID(project), keys)
	if err != nil {
		return nil, "", notFound(err, catalogapp.ErrNotFound)
	}
	out := make([]tools.MessageSummary, 0, len(keys))
	for _, k := range keys {
		if m, ok := byKey[k]; ok {
			out = append(out, summaryOf(m))
		}
	}
	return out, next(token), nil
}

// Message implements tools.Catalog.
func (a *Catalog) Message(ctx context.Context, project uuid.UUID, key string) (tools.MessageDetail, error) {
	m, err := a.catalog.GetMessage(ctx, catalog.ProjectID(project), key)
	if err != nil {
		return tools.MessageDetail{}, notFound(err, catalogapp.ErrNotFound)
	}
	d := tools.MessageDetail{MessageSummary: summaryOf(m), Description: m.Description, MaxLength: m.MaxLength}
	for _, arg := range m.Source.Arguments {
		d.Arguments = append(d.Arguments, arg.Name)
	}
	return d, nil
}

func summaryOf(m catalog.Message) tools.MessageSummary {
	return tools.MessageSummary{
		ID: uuid.UUID(m.ID).String(), Key: string(m.Key), Namespace: string(m.Namespace),
		State: string(m.State), Source: m.Source.Text, Syntax: string(m.Source.Syntax), Revision: m.Revision,
	}
}

var _ tools.CatalogWriter = (*Catalog)(nil)

// UpsertMessage implements tools.CatalogWriter. It is Catalog's own
// bulk upsert with one item — the use case `glossa push` and the REST
// API call — so an agent's message is written by exactly the code that
// writes CI's, including its idempotency and its base-revision check
// (RFC 0005 §7.1).
func (a *Catalog) UpsertMessage(
	ctx context.Context, project uuid.UUID, in tools.MessageUpsert,
) (tools.MessageWritten, error) {
	results, err := a.catalog.UpsertMessages(ctx, catalog.ProjectID(project), []catalogapp.UpsertItem{{
		Key: in.Key, Namespace: in.Namespace, Description: in.Description, MaxLength: in.MaxLength,
		Text: in.Text, Syntax: in.Syntax, BaseRevision: in.BaseRevision,
	}})
	if err != nil {
		return tools.MessageWritten{}, notFound(err, catalogapp.ErrNotFound)
	}
	if len(results) != 1 {
		return tools.MessageWritten{}, fmt.Errorf("mcp: an upsert of one message answered %d results", len(results))
	}
	res := results[0]
	// An item fails on its own rather than failing the batch, so a
	// one-item batch's failure is this call's error. The code is
	// Catalog's own (invalid_key, invalid_source,
	// source_revision_conflict …), which is what an agent needs to know
	// whether to fix its text or to re-read the message.
	if res.Error != nil {
		return tools.MessageWritten{}, &app.InvalidArgumentError{
			Argument: res.Error.Code, Reason: res.Error.Detail,
		}
	}
	if res.Message == nil {
		return tools.MessageWritten{}, fmt.Errorf("mcp: the upsert of %q reported %s and no message", res.Key, res.Status)
	}
	return tools.MessageWritten{MessageSummary: summaryOf(*res.Message), Status: string(res.Status)}, nil
}
