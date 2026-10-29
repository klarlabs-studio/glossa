package postgres

import (
	"context"
	"fmt"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres/catalogsql"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// The bulk writes of a message upsert (#77): one statement per table,
// each row an element of parallel arrays.

// noMaxLength stands for a NULL max_length in the bulk statements'
// int arrays; the column admits only 1..100000.
const noMaxLength = 0

func maxLengthElem(v *int) int32 {
	if v == nil {
		return noMaxLength
	}
	return int32Of(*v)
}

// InsertMessages implements app.Store.
func (s *store) InsertMessages(ctx context.Context, ms []app.MessageInsert) error {
	if len(ms) == 0 {
		return nil
	}
	var p catalogsql.InsertMessagesParams
	firsts := make([]domain.SourceRevision, len(ms))
	for i, n := range ms {
		m := n.Message
		p.Ids = append(p.Ids, m.ID.UUID())
		p.ProjectIds = append(p.ProjectIds, m.ProjectID.UUID())
		p.Keys = append(p.Keys, string(m.Key))
		p.Namespaces = append(p.Namespaces, string(m.Namespace))
		p.Descriptions = append(p.Descriptions, m.Description)
		p.MaxLengths = append(p.MaxLengths, maxLengthElem(m.MaxLength))
		p.States = append(p.States, string(m.State))
		p.SourceSyntaxes = append(p.SourceSyntaxes, string(m.Source.Syntax))
		p.SourceTexts = append(p.SourceTexts, m.Source.Text)
		p.SourceModels = append(p.SourceModels, m.Source.ModelJSON())
		p.Arguments = append(p.Arguments, m.Source.ArgumentsJSON())
		p.Markups = append(p.Markups, m.Source.MarkupJSON())
		p.SourceRevisions = append(p.SourceRevisions, int32Of(m.Revision))
		p.Versions = append(p.Versions, int32Of(m.Version))
		p.CreatedBys = append(p.CreatedBys, string(n.By))
		p.CreatedAts = append(p.CreatedAts, m.CreatedAt)
		p.UpdatedAts = append(p.UpdatedAts, m.UpdatedAt)
		firsts[i] = n.First
	}
	n, err := s.q.InsertMessages(ctx, p)
	if err != nil {
		return storeError(err)
	}
	if int(n) != len(ms) {
		// ON CONFLICT (id) skipped a row: another request holds its ID.
		return fmt.Errorf("%w: %d of %d messages already exist", app.ErrIdempotencyBusy, len(ms)-int(n), len(ms))
	}
	return s.AppendSourceRevisions(ctx, firsts)
}

// UpdateMessages implements app.Store.
func (s *store) UpdateMessages(ctx context.Context, ms []app.MessageUpdate) error {
	if len(ms) == 0 {
		return nil
	}
	var p catalogsql.UpdateMessagesParams
	for _, u := range ms {
		m := u.Message
		p.Ids = append(p.Ids, m.ID.UUID())
		p.Keys = append(p.Keys, string(m.Key))
		p.Namespaces = append(p.Namespaces, string(m.Namespace))
		p.Descriptions = append(p.Descriptions, m.Description)
		p.MaxLengths = append(p.MaxLengths, maxLengthElem(m.MaxLength))
		p.States = append(p.States, string(m.State))
		p.SourceSyntaxes = append(p.SourceSyntaxes, string(m.Source.Syntax))
		p.SourceTexts = append(p.SourceTexts, m.Source.Text)
		p.SourceModels = append(p.SourceModels, m.Source.ModelJSON())
		p.Arguments = append(p.Arguments, m.Source.ArgumentsJSON())
		p.Markups = append(p.Markups, m.Source.MarkupJSON())
		p.SourceRevisions = append(p.SourceRevisions, int32Of(m.Revision))
		p.Versions = append(p.Versions, int32Of(m.Version))
		p.UpdatedAts = append(p.UpdatedAts, m.UpdatedAt)
		p.ExpectedVersions = append(p.ExpectedVersions, int32Of(u.Expected))
	}
	n, err := s.q.UpdateMessages(ctx, p)
	if err != nil {
		return storeError(err)
	}
	if int(n) != len(ms) {
		return app.ErrStaleVersion
	}
	return nil
}

// AppendSourceRevisions implements app.Store.
func (s *store) AppendSourceRevisions(ctx context.Context, rs []domain.SourceRevision) error {
	if len(rs) == 0 {
		return nil
	}
	var p catalogsql.InsertSourceRevisionsParams
	for _, r := range rs {
		p.MessageIds = append(p.MessageIds, r.MessageID.UUID())
		p.Revisions = append(p.Revisions, int32Of(r.Number))
		p.Syntaxes = append(p.Syntaxes, string(r.Content.Syntax))
		p.Texts = append(p.Texts, r.Content.Text)
		p.Models = append(p.Models, r.Content.ModelJSON())
		p.Authors = append(p.Authors, string(r.Author))
		p.CreatedAts = append(p.CreatedAts, r.CreatedAt)
	}
	return storeError(s.q.InsertSourceRevisions(ctx, p))
}

// PublishAll implements app.Store.
func (s *store) PublishAll(ctx context.Context, es []outbox.Event) error {
	_, err := outbox.PublishAll(ctx, s.tx, es)
	return err
}
