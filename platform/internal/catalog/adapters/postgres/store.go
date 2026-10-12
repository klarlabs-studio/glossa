// Package postgres implements Catalog's persistence port on the
// kernel's unit of work, with sqlc queries over the catalog_* tables.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres/catalogsql"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{tx: tx, q: catalogsql.New(tx)})
	})
}

type store struct {
	tx *db.TenantTx
	q  *catalogsql.Queries
}

var _ app.Store = (*store)(nil)

// Constraint names from migration 0003.
var uniqueErrors = map[string]error{
	"catalog_projects_tenant_id_slug_key":      app.ErrSlugTaken,
	"catalog_applications_project_id_slug_key": app.ErrSlugTaken,
	"catalog_messages_project_id_key_key":      app.ErrKeyTaken,
	// Two requests with one idempotency key race for the same ID.
	"catalog_projects_pkey":     app.ErrIdempotencyBusy,
	"catalog_applications_pkey": app.ErrIdempotencyBusy,
	"catalog_messages_pkey":     app.ErrIdempotencyBusy,
}

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			if mapped, ok := uniqueErrors[pgErr.ConstraintName]; ok {
				return mapped
			}
		case "23503": // a parent vanished under us (e.g. a deleted project)
			return app.ErrNotFound
		}
	}
	return err
}

func int32Of(n int) int32 { return int32(n) } //nolint:gosec // versions, revisions and page sizes stay small

// ── projects ────────────────────────────────────────────────────────

func (s *store) InsertProject(ctx context.Context, p domain.Project, by domain.Author) (bool, error) {
	settings, err := json.Marshal(p.Settings)
	if err != nil {
		return false, err
	}
	n, err := s.q.InsertProject(ctx, catalogsql.InsertProjectParams{
		ID: p.ID.UUID(), Slug: string(p.Slug), Name: p.Name, SourceLocale: p.SourceLocale.String(),
		Settings: settings, Version: int32Of(p.Version), CreatedBy: string(by), CreatedAt: p.CreatedAt,
	})
	return n == 1, storeError(err)
}

func project(row catalogsql.CatalogProject) (domain.Project, error) {
	source, err := bcp47.Parse(row.SourceLocale)
	if err != nil {
		return domain.Project{}, fmt.Errorf("catalog: stored source locale of project %s: %w", row.ID, err)
	}
	settings := domain.DefaultSettings()
	if err := json.Unmarshal(row.Settings, &settings); err != nil {
		return domain.Project{}, fmt.Errorf("catalog: stored settings of project %s: %w", row.ID, err)
	}
	return domain.Project{
		ID: domain.ProjectID(row.ID), TenantID: tenancy.ID(row.TenantID), Slug: domain.Slug(row.Slug),
		Name: row.Name, SourceLocale: source, Settings: settings, Version: int(row.Version),
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func (s *store) Project(ctx context.Context, id domain.ProjectID) (domain.Project, error) {
	row, err := s.q.GetProject(ctx, id.UUID())
	if err != nil {
		return domain.Project{}, storeError(err)
	}
	return project(row)
}

func (s *store) LockProject(ctx context.Context, id domain.ProjectID) (domain.Project, error) {
	row, err := s.q.LockProject(ctx, id.UUID())
	if err != nil {
		return domain.Project{}, storeError(err)
	}
	return project(row)
}

func (s *store) Projects(ctx context.Context, after domain.ProjectID, only []uuid.UUID, limit int) ([]domain.Project, error) {
	params := catalogsql.ListProjectsParams{After: after.UUID(), MaxRows: int32Of(limit)}
	if only != nil {
		params.FilterIds, params.Ids = true, only
	}
	rows, err := s.q.ListProjects(ctx, params)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Project, 0, len(rows))
	for _, r := range rows {
		p, err := project(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *store) UpdateProject(ctx context.Context, p domain.Project, expected int) error {
	settings, err := json.Marshal(p.Settings)
	if err != nil {
		return err
	}
	n, err := s.q.UpdateProject(ctx, catalogsql.UpdateProjectParams{
		ID: p.ID.UUID(), Slug: string(p.Slug), Name: p.Name, Settings: settings,
		Version: int32Of(p.Version), UpdatedAt: p.UpdatedAt, ExpectedVersion: int32Of(expected),
	})
	return affected(n, err)
}

func (s *store) DeleteProject(ctx context.Context, id domain.ProjectID) error {
	n, err := s.q.DeleteProject(ctx, id.UUID())
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// affected turns a version-guarded update's row count into an error.
func affected(n int64, err error) error {
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

// ── applications ────────────────────────────────────────────────────

func (s *store) InsertApplication(ctx context.Context, a domain.Application, by domain.Author) (bool, error) {
	n, err := s.q.InsertApplication(ctx, catalogsql.InsertApplicationParams{
		ID: a.ID.UUID(), ProjectID: a.ProjectID.UUID(), Slug: string(a.Slug), Name: a.Name,
		Platform: string(a.Platform), Version: int32Of(a.Version), CreatedBy: string(by), CreatedAt: a.CreatedAt,
	})
	return n == 1, storeError(err)
}

func application(r catalogsql.CatalogApplication) domain.Application {
	return domain.Application{
		ID: domain.ApplicationID(r.ID), ProjectID: domain.ProjectID(r.ProjectID), Slug: domain.Slug(r.Slug),
		Name: r.Name, Platform: domain.Platform(r.Platform), Version: int(r.Version),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func (s *store) Application(ctx context.Context, project domain.ProjectID, id domain.ApplicationID) (domain.Application, error) {
	row, err := s.q.GetApplication(ctx, catalogsql.GetApplicationParams{ProjectID: project.UUID(), ID: id.UUID()})
	return application(row), storeError(err)
}

func (s *store) LockApplication(ctx context.Context, project domain.ProjectID, id domain.ApplicationID) (domain.Application, error) {
	row, err := s.q.LockApplication(ctx, catalogsql.LockApplicationParams{ProjectID: project.UUID(), ID: id.UUID()})
	return application(row), storeError(err)
}

func (s *store) Applications(ctx context.Context, project domain.ProjectID, after domain.ApplicationID, limit int) ([]domain.Application, error) {
	rows, err := s.q.ListApplications(ctx, catalogsql.ListApplicationsParams{
		ProjectID: project.UUID(), After: after.UUID(), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Application, len(rows))
	for i, r := range rows {
		out[i] = application(r)
	}
	return out, nil
}

func (s *store) UpdateApplication(ctx context.Context, a domain.Application, expected int) error {
	n, err := s.q.UpdateApplication(ctx, catalogsql.UpdateApplicationParams{
		ID: a.ID.UUID(), Slug: string(a.Slug), Name: a.Name, Platform: string(a.Platform),
		Version: int32Of(a.Version), UpdatedAt: a.UpdatedAt, ExpectedVersion: int32Of(expected),
	})
	return affected(n, err)
}

func (s *store) DeleteApplication(ctx context.Context, project domain.ProjectID, id domain.ApplicationID) error {
	n, err := s.q.DeleteApplication(ctx, catalogsql.DeleteApplicationParams{ProjectID: project.UUID(), ID: id.UUID()})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ── messages ────────────────────────────────────────────────────────

func maxLength(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32Of(*v), Valid: true}
}

func (s *store) InsertMessage(ctx context.Context, m domain.Message, first domain.SourceRevision, by domain.Author) (bool, error) {
	n, err := s.q.InsertMessage(ctx, catalogsql.InsertMessageParams{
		ID: m.ID.UUID(), ProjectID: m.ProjectID.UUID(), Key: string(m.Key), Namespace: string(m.Namespace),
		Description: m.Description, MaxLength: maxLength(m.MaxLength), State: string(m.State),
		SourceSyntax: string(m.Source.Syntax), SourceText: m.Source.Text, SourceModel: m.Source.ModelJSON(),
		Arguments: m.Source.ArgumentsJSON(), Markup: m.Source.MarkupJSON(),
		SourceRevision: int32Of(m.Revision), Version: int32Of(m.Version), CreatedBy: string(by),
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	})
	if err != nil || n == 0 {
		return false, storeError(err)
	}
	return true, s.AppendSourceRevision(ctx, first)
}

func message(r catalogsql.CatalogMessage) (domain.Message, error) {
	src, err := mfcontent.Restore(mfcontent.Syntax(r.SourceSyntax), r.SourceText, r.SourceModel)
	if err != nil {
		return domain.Message{}, fmt.Errorf("catalog: stored source of message %s: %w", r.ID, err)
	}
	var limit *int
	if r.MaxLength.Valid {
		v := int(r.MaxLength.Int32)
		limit = &v
	}
	return domain.Message{
		ID: domain.MessageID(r.ID), ProjectID: domain.ProjectID(r.ProjectID), Key: domain.MessageKey(r.Key),
		Details: domain.Details{Namespace: domain.Namespace(r.Namespace), Description: r.Description, MaxLength: limit},
		State:   domain.MessageState(r.State), Source: src, Revision: int(r.SourceRevision), Version: int(r.Version),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

func messages(rows []catalogsql.CatalogMessage) ([]domain.Message, error) {
	out := make([]domain.Message, 0, len(rows))
	for _, r := range rows {
		m, err := message(r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *store) MessageByKey(ctx context.Context, project domain.ProjectID, key domain.MessageKey) (domain.Message, error) {
	row, err := s.q.GetMessageByKey(ctx, catalogsql.GetMessageByKeyParams{ProjectID: project.UUID(), Key: string(key)})
	if err != nil {
		return domain.Message{}, storeError(err)
	}
	return message(row)
}

func (s *store) MessageByID(ctx context.Context, project domain.ProjectID, id domain.MessageID) (domain.Message, error) {
	row, err := s.q.GetMessage(ctx, catalogsql.GetMessageParams{ProjectID: project.UUID(), ID: id.UUID()})
	if err != nil {
		return domain.Message{}, storeError(err)
	}
	return message(row)
}

func (s *store) LockMessageByKey(ctx context.Context, project domain.ProjectID, key domain.MessageKey) (domain.Message, error) {
	row, err := s.q.LockMessageByKey(ctx, catalogsql.LockMessageByKeyParams{ProjectID: project.UUID(), Key: string(key)})
	if err != nil {
		return domain.Message{}, storeError(err)
	}
	return message(row)
}

func keyStrings(keys []domain.MessageKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = string(k)
	}
	return out
}

func byKey(rows []catalogsql.CatalogMessage) (map[domain.MessageKey]domain.Message, error) {
	ms, err := messages(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.MessageKey]domain.Message, len(ms))
	for _, m := range ms {
		out[m.Key] = m
	}
	return out, nil
}

func (s *store) LockMessagesByKeys(ctx context.Context, project domain.ProjectID, keys []domain.MessageKey) (map[domain.MessageKey]domain.Message, error) {
	rows, err := s.q.LockMessagesByKeys(ctx, catalogsql.LockMessagesByKeysParams{ProjectID: project.UUID(), Keys: keyStrings(keys)})
	if err != nil {
		return nil, storeError(err)
	}
	return byKey(rows)
}

func (s *store) MessagesByKeys(ctx context.Context, project domain.ProjectID, keys []domain.MessageKey) (map[domain.MessageKey]domain.Message, error) {
	rows, err := s.q.GetMessagesByKeys(ctx, catalogsql.GetMessagesByKeysParams{ProjectID: project.UUID(), Keys: keyStrings(keys)})
	if err != nil {
		return nil, storeError(err)
	}
	return byKey(rows)
}

func (s *store) MessagesByIDs(ctx context.Context, project domain.ProjectID, ids []domain.MessageID) (map[domain.MessageID]domain.Message, error) {
	uuids := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		uuids[i] = id.UUID()
	}
	rows, err := s.q.GetMessagesByIDs(ctx, catalogsql.GetMessagesByIDsParams{ProjectID: project.UUID(), Ids: uuids})
	if err != nil {
		return nil, storeError(err)
	}
	ms, err := messages(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.MessageID]domain.Message, len(ms))
	for _, m := range ms {
		out[m.ID] = m
	}
	return out, nil
}

func text(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *p, Valid: true}
}

// LikePattern turns a key prefix into a LIKE pattern, escaping the
// wildcards _ and % (both are legal in keys).
func LikePattern(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `_`, `\_`, `%`, `\%`).Replace(prefix) + "%"
}

func (s *store) Messages(ctx context.Context, project domain.ProjectID, f app.MessageFilter, after domain.MessageKey, limit int) ([]domain.Message, error) {
	params := catalogsql.ListMessagesParams{ProjectID: project.UUID(), After: string(after), MaxRows: int32Of(limit)}
	if f.Namespace != nil {
		params.Namespace = pgtype.Text{String: string(*f.Namespace), Valid: true}
	}
	if f.State != nil {
		params.State = pgtype.Text{String: string(*f.State), Valid: true}
	}
	if f.KeyPrefix != "" {
		pattern := LikePattern(f.KeyPrefix)
		params.KeyLike = text(&pattern)
	}
	if f.IDs != nil {
		params.FilterIds = true
		params.Ids = make([]uuid.UUID, len(f.IDs))
		for i, id := range f.IDs {
			params.Ids[i] = id.UUID()
		}
	}
	rows, err := s.q.ListMessages(ctx, params)
	if err != nil {
		return nil, storeError(err)
	}
	return messages(rows)
}

func (s *store) Namespaces(ctx context.Context, project domain.ProjectID, after domain.Namespace, limit int) ([]app.NamespaceSummary, error) {
	rows, err := s.q.ListNamespaces(ctx, catalogsql.ListNamespacesParams{
		ProjectID: project.UUID(), After: string(after), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.NamespaceSummary, len(rows))
	for i, r := range rows {
		out[i] = app.NamespaceSummary{Name: domain.Namespace(r.Namespace), Active: int(r.Active), Obsolete: int(r.Obsolete)}
	}
	return out, nil
}

func (s *store) ActiveMessages(ctx context.Context, project domain.ProjectID) ([]domain.Message, error) {
	rows, err := s.q.ListActiveMessages(ctx, project.UUID())
	if err != nil {
		return nil, storeError(err)
	}
	return messages(rows)
}

func (s *store) UpdateMessage(ctx context.Context, m domain.Message, expected int) error {
	n, err := s.q.UpdateMessage(ctx, catalogsql.UpdateMessageParams{
		ID: m.ID.UUID(), Key: string(m.Key), Namespace: string(m.Namespace), Description: m.Description,
		MaxLength: maxLength(m.MaxLength), State: string(m.State), SourceSyntax: string(m.Source.Syntax),
		SourceText: m.Source.Text, SourceModel: m.Source.ModelJSON(), Arguments: m.Source.ArgumentsJSON(),
		Markup: m.Source.MarkupJSON(), SourceRevision: int32Of(m.Revision), Version: int32Of(m.Version),
		UpdatedAt: m.UpdatedAt, ExpectedVersion: int32Of(expected),
	})
	return affected(n, err)
}

func (s *store) AppendSourceRevision(ctx context.Context, r domain.SourceRevision) error {
	return storeError(s.q.InsertSourceRevision(ctx, catalogsql.InsertSourceRevisionParams{
		MessageID: r.MessageID.UUID(), Revision: int32Of(r.Number), Syntax: string(r.Content.Syntax),
		Text: r.Content.Text, Model: r.Content.ModelJSON(), Author: string(r.Author), CreatedAt: r.CreatedAt,
	}))
}

func sourceRevision(r catalogsql.CatalogSourceRevision) (domain.SourceRevision, error) {
	c, err := mfcontent.Restore(mfcontent.Syntax(r.Syntax), r.Text, r.Model)
	if err != nil {
		return domain.SourceRevision{}, fmt.Errorf("catalog: stored revision %d of %s: %w", r.Revision, r.MessageID, err)
	}
	return domain.SourceRevision{
		MessageID: domain.MessageID(r.MessageID), Number: int(r.Revision), Content: c,
		Author: domain.Author(r.Author), CreatedAt: r.CreatedAt.UTC(),
	}, nil
}

func (s *store) SourceRevisions(ctx context.Context, id domain.MessageID, before, limit int) ([]domain.SourceRevision, error) {
	rows, err := s.q.ListSourceRevisions(ctx, catalogsql.ListSourceRevisionsParams{
		MessageID: id.UUID(), Before: int32Of(before), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.SourceRevision, 0, len(rows))
	for _, r := range rows {
		rev, err := sourceRevision(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, nil
}

func (s *store) SourceRevision(ctx context.Context, id domain.MessageID, n int) (domain.SourceRevision, error) {
	row, err := s.q.GetSourceRevision(ctx, catalogsql.GetSourceRevisionParams{MessageID: id.UUID(), Revision: int32Of(n)})
	if err != nil {
		return domain.SourceRevision{}, storeError(err)
	}
	return sourceRevision(row)
}

func (s *store) SourceRevisionsOf(ctx context.Context, project domain.ProjectID, refs []app.SourceRef) (map[app.SourceRef]domain.SourceRevision, error) {
	p := catalogsql.GetSourceRevisionsOfParams{
		ProjectID: project.UUID(), MessageIds: make([]uuid.UUID, len(refs)), Revisions: make([]int32, len(refs)),
	}
	for i, r := range refs {
		p.MessageIds[i], p.Revisions[i] = r.Message.UUID(), int32Of(r.Revision)
	}
	rows, err := s.q.GetSourceRevisionsOf(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make(map[app.SourceRef]domain.SourceRevision, len(rows))
	for _, r := range rows {
		rev, err := sourceRevision(r)
		if err != nil {
			return nil, err
		}
		out[app.SourceRef{Message: rev.MessageID, Revision: rev.Number}] = rev
	}
	return out, nil
}

func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}
