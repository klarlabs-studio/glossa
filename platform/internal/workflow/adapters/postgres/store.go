// Package postgres implements Workflow's persistence port on the
// kernel's unit of work, with sqlc queries over the workflow_* tables
// (migration 0040, RFC 0006 §2.3).
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres/workflowsql"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{q: workflowsql.New(tx), tx: tx})
	})
}

type store struct {
	q *workflowsql.Queries
	// tx is the transaction the queries run in, kept so a domain event
	// lands with the rows that raised it.
	tx *db.TenantTx
}

// Publish implements app.Store: the event goes in the transaction that
// made the change, so a rollback leaves no announcement of it.
func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}

var _ app.Store = (*store)(nil)

// Postgres error codes this adapter translates.
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

func storeError(err error) error {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return app.ErrNotFound
	case errors.As(err, &pg) && pg.Code == uniqueViolation:
		return app.ErrConflict
	case errors.As(err, &pg) && pg.Code == foreignKeyViolation:
		// A definition id that is not this tenant's: the composite
		// foreign key refuses it, and to this tenant it does not exist.
		return app.ErrNotFound
	}
	return err
}

func (s *store) InsertDefinition(ctx context.Context, d domain.DefinitionRecord) error {
	return storeError(s.q.InsertDefinition(ctx, workflowsql.InsertDefinitionParams{
		ID: d.ID, ProjectID: nullUUID(d.ProjectID), Name: d.Name, Subject: string(d.Subject),
		CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt,
	}))
}

func (s *store) GetDefinition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	r, err := s.q.GetDefinition(ctx, id)
	if err != nil {
		return domain.DefinitionRecord{}, storeError(err)
	}
	return definition(r), nil
}

func (s *store) LockDefinition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	r, err := s.q.GetDefinitionForUpdate(ctx, id)
	if err != nil {
		return domain.DefinitionRecord{}, storeError(err)
	}
	return definition(r), nil
}

func (s *store) ListDefinitions(ctx context.Context, project uuid.UUID) ([]domain.DefinitionRecord, error) {
	rows, err := s.q.ListDefinitions(ctx, nullUUID(project))
	if err != nil {
		return nil, err
	}
	out := make([]domain.DefinitionRecord, len(rows))
	for i, r := range rows {
		out[i] = definition(r)
	}
	return out, nil
}

func (s *store) CountLiveDefinitions(ctx context.Context) (int, error) {
	n, err := s.q.CountLiveDefinitions(ctx)
	return int(n), err
}

func (s *store) SetLatest(ctx context.Context, id uuid.UUID, n int) error {
	rows, err := s.q.SetLatestVersion(ctx, workflowsql.SetLatestVersionParams{ID: id, Latest: int32Of(n)})
	if err != nil {
		return err
	}
	if rows != 1 {
		return app.ErrConflict
	}
	return nil
}

func (s *store) MarkDeleted(ctx context.Context, d domain.DefinitionRecord) error {
	rows, err := s.q.MarkDefinitionDeleted(ctx, workflowsql.MarkDefinitionDeletedParams{ID: d.ID, DeletedAt: timestamptz(d.DeletedAt)})
	if err != nil {
		return err
	}
	if rows != 1 {
		return app.ErrNotFound
	}
	return nil
}

func (s *store) InsertVersion(ctx context.Context, v domain.Version) error {
	rows, err := s.q.InsertVersion(ctx, workflowsql.InsertVersionParams{
		ID: v.ID, DefinitionID: v.DefinitionID, Version: int32Of(v.Number), Schema: v.Schema,
		Document: v.Document, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
	})
	if err != nil {
		return storeError(err)
	}
	if rows != 1 {
		return app.ErrConflict
	}
	return nil
}

func (s *store) GetVersion(ctx context.Context, definition uuid.UUID, n int) (domain.Version, error) {
	r, err := s.q.GetVersion(ctx, workflowsql.GetVersionParams{DefinitionID: definition, Version: int32Of(n)})
	if err != nil {
		return domain.Version{}, storeError(err)
	}
	return version(r), nil
}

func (s *store) ListVersions(ctx context.Context, definition uuid.UUID) ([]domain.Version, error) {
	rows, err := s.q.ListVersions(ctx, definition)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Version, len(rows))
	for i, r := range rows {
		out[i] = version(r)
	}
	return out, nil
}

func (s *store) InsertBinding(ctx context.Context, b domain.Binding) (domain.Binding, error) {
	locales := b.Locales
	if locales == nil {
		locales = []string{}
	}
	pos, err := s.q.InsertBinding(ctx, workflowsql.InsertBindingParams{
		ID: b.ID, ProjectID: b.ProjectID, Subject: string(b.Subject), Locales: locales,
		Namespace: text(b.Namespace), DefinitionID: b.DefinitionID, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING: another binding has this selector.
		return domain.Binding{}, app.ErrConflict
	}
	if err != nil {
		return domain.Binding{}, storeError(err)
	}
	b.Position = pos.Int64
	return b, nil
}

func (s *store) GetBinding(ctx context.Context, id uuid.UUID) (domain.Binding, error) {
	r, err := s.q.GetBinding(ctx, id)
	if err != nil {
		return domain.Binding{}, storeError(err)
	}
	return binding(r), nil
}

func (s *store) ListBindings(ctx context.Context, project uuid.UUID, subject domain.SubjectKind) ([]domain.Binding, error) {
	rows, err := s.q.ListBindings(ctx, workflowsql.ListBindingsParams{ProjectID: project, Subject: text(string(subject))})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Binding, len(rows))
	for i, r := range rows {
		out[i] = binding(r)
	}
	return out, nil
}

func (s *store) DeleteBinding(ctx context.Context, id uuid.UUID) error {
	rows, err := s.q.DeleteBinding(ctx, id)
	if err != nil {
		return err
	}
	if rows == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *store) DeleteBindingsOf(ctx context.Context, definition uuid.UUID) (int, error) {
	n, err := s.q.DeleteBindingsOfDefinition(ctx, definition)
	return int(n), err
}

// ── rows ────────────────────────────────────────────────────────────

func definition(r workflowsql.WorkflowDefinition) domain.DefinitionRecord {
	d := domain.DefinitionRecord{
		ID: r.ID, Name: r.Name, Subject: domain.SubjectKind(r.Subject), Latest: int(r.Latest),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	}
	if r.ProjectID.Valid {
		d.ProjectID = r.ProjectID.UUID
	}
	if r.DeletedAt.Valid {
		t := r.DeletedAt.Time
		d.DeletedAt = &t
	}
	return d
}

func version(r workflowsql.WorkflowDefinitionVersion) domain.Version {
	return domain.Version{
		ID: r.ID, DefinitionID: r.DefinitionID, Number: int(r.Version), Schema: r.Schema,
		Document: r.Document, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	}
}

func binding(r workflowsql.WorkflowBinding) domain.Binding {
	return domain.Binding{
		ID: r.ID, ProjectID: r.ProjectID, Subject: domain.SubjectKind(r.Subject), Locales: r.Locales,
		Namespace: r.Namespace.String, DefinitionID: r.DefinitionID, Position: r.Position.Int64,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	}
}

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func nullUUID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

//nolint:gosec // version numbers are bounded by the domain and the column
func int32Of(n int) int32 { return int32(n) }
