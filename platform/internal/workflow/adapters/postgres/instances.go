package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres/workflowsql"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Instances implements the instance runner's persistence
// (app.InstanceTransactor), the read side the Workflow API serves
// (app.InstanceQueries), and the timer sweep's cross-tenant scan
// (app.TimerScanner), over migration 0043's tables.
type Instances struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
	// retention is the retention sweep's system scope: the tenant, the
	// status and when an instance finished (migration 0056).
	retention db.SystemScope
}

// NewInstances returns the instance store on uow.
func NewInstances(uow *db.UnitOfWork) *Instances {
	return &Instances{uow: uow, scope: db.NewSystemScope("workflow.timers"), retention: db.NewSystemScope("workflow.retention")}
}

var (
	_ app.InstanceTransactor = (*Instances)(nil)
	_ app.InstanceQueries    = (*Instances)(nil)
	_ app.TimerScanner       = (*Instances)(nil)
	_ app.RetentionScanner   = (*Instances)(nil)
)

// TenantsWithExpiredInstances implements app.RetentionScanner in the
// system scope workflow.retention, which reads the tenant, the status
// and finished_at (migrations 0043 and 0056) and nothing else.
func (s *Instances) TenantsWithExpiredInstances(ctx context.Context, cutoff time.Time, limit int) ([]tenancy.ID, error) {
	var out []tenancy.ID
	err := s.uow.InSystemTx(ctx, s.retention, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := workflowsql.New(tx).ListTenantsWithExpiredInstances(ctx, workflowsql.ListTenantsWithExpiredInstancesParams{
			Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true}, MaxRows: int32Of(limit),
		})
		for _, r := range rows {
			out = append(out, tenancy.ID(r))
		}
		return err
	})
	return out, err
}

// InTenant implements app.InstanceTransactor.
func (s *Instances) InTenant(ctx context.Context, fn func(context.Context, app.InstanceStore) error) error {
	return s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &instanceStore{q: workflowsql.New(tx), tx: tx})
	})
}

type instanceStore struct {
	q  *workflowsql.Queries
	tx *db.TenantTx
}

var _ app.InstanceStore = (*instanceStore)(nil)

func (s *instanceStore) LockActive(ctx context.Context, ref app.SubjectRef) ([]domain.Instance, error) {
	rows, err := s.q.LockActiveInstancesOfSubject(ctx, workflowsql.LockActiveInstancesOfSubjectParams{
		SubjectKind: string(ref.Kind), SubjectID: ref.ID, Locale: ref.Locale,
	})
	return instances(rows), err
}

func (s *instanceStore) LockActiveInProject(ctx context.Context, project uuid.UUID, kind domain.SubjectKind, limit int) ([]domain.Instance, error) {
	rows, err := s.q.LockActiveInstancesOfProject(ctx, workflowsql.LockActiveInstancesOfProjectParams{
		ProjectID: project, SubjectKind: string(kind), MaxRows: int32Of(limit),
	})
	return instances(rows), err
}

func (s *instanceStore) LockInstance(ctx context.Context, id uuid.UUID) (domain.Instance, error) {
	r, err := s.q.LockInstance(ctx, id)
	if err != nil {
		return domain.Instance{}, storeError(err)
	}
	return instance(r), nil
}

func (s *instanceStore) InsertInstance(ctx context.Context, i domain.Instance) (bool, error) {
	n, err := s.q.InsertInstance(ctx, workflowsql.InsertInstanceParams{
		ID: i.ID, ProjectID: i.Project, DefinitionID: i.Definition, Version: int32Of(i.Version),
		SubjectKind: string(i.Kind), SubjectID: i.SubjectID, Locale: i.Locale, CreatedAt: i.CreatedAt,
	})
	if err != nil {
		return false, storeError(err)
	}
	return n == 1, nil
}

func (s *instanceStore) SaveInstance(ctx context.Context, i domain.Instance, snapshot []byte) error {
	p := workflowsql.UpdateInstanceParams{
		ID: i.ID, State: i.State, Snapshot: snapshot, Status: i.Status, UpdatedAt: i.UpdatedAt,
		FinishedAt: timestamptz(i.FinishedAt),
	}
	if t := i.Timer; t != nil && (t.DueAt != nil || t.OverdueAt != nil) {
		p.DueAt, p.OverdueAt, p.TimerState = timestamptz(t.DueAt), timestamptz(t.OverdueAt), pgtype.Text{String: t.State, Valid: true}
	}
	n, err := s.q.UpdateInstance(ctx, p)
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return app.ErrNotFound
	}
	return nil
}

func (s *instanceStore) Snapshot(ctx context.Context, id uuid.UUID) ([]byte, error) {
	r, err := s.q.GetInstance(ctx, id)
	if err != nil {
		return nil, storeError(err)
	}
	return r.Snapshot, nil
}

func (s *instanceStore) HasTransition(ctx context.Context, instance, event uuid.UUID) (bool, error) {
	return s.q.HasTransition(ctx, workflowsql.HasTransitionParams{InstanceID: instance, OutboxEventID: event})
}

func (s *instanceStore) AppendTransition(ctx context.Context, instance uuid.UUID, t app.Transition) error {
	guards, err := json.Marshal(guardRows(t.Guards))
	if err != nil {
		return err
	}
	actions, err := json.Marshal(actionRows(t.Actions))
	if err != nil {
		return err
	}
	return storeError(s.q.AppendTransition(ctx, workflowsql.AppendTransitionParams{
		InstanceID: instance, FromState: t.From, Event: string(t.Event), ToState: t.To, Outcome: t.Outcome,
		Guards: guards, Actions: actions, Actor: t.Actor.String(), OutboxEventID: t.EventID, At: t.At,
	}))
}

func (s *instanceStore) Version(ctx context.Context, definition uuid.UUID, n int) (domain.Version, error) {
	r, err := s.q.GetVersion(ctx, workflowsql.GetVersionParams{DefinitionID: definition, Version: int32Of(n)})
	if err != nil {
		return domain.Version{}, storeError(err)
	}
	return version(r), nil
}

func (s *instanceStore) LockDueTimers(ctx context.Context, now time.Time, limit int) ([]domain.Instance, error) {
	rows, err := s.q.LockDueTimers(ctx, workflowsql.LockDueTimersParams{
		Now: pgtype.Timestamptz{Time: now, Valid: true}, MaxRows: int32Of(limit),
	})
	return instances(rows), err
}

func (s *instanceStore) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}

func (s *instanceStore) Seeded(ctx context.Context) (bool, error) { return s.q.HasSeed(ctx) }

func (s *instanceStore) HasTenantDefinition(ctx context.Context, name string) (bool, error) {
	return s.q.LiveDefinitionNamed(ctx, name)
}

func (s *instanceStore) TenantDefinition(ctx context.Context, name string) (domain.DefinitionRecord, error) {
	r, err := s.q.LiveTenantDefinition(ctx, name)
	if err != nil {
		return domain.DefinitionRecord{}, storeError(err)
	}
	return definition(r), nil
}

func (s *instanceStore) InsertDefinition(ctx context.Context, rec domain.DefinitionRecord, v domain.Version) error {
	ds := &store{q: s.q}
	if err := ds.InsertDefinition(ctx, rec); err != nil {
		return err
	}
	return ds.InsertVersion(ctx, v)
}

func (s *instanceStore) LatestVersion(ctx context.Context, definition uuid.UUID) (int, error) {
	r, err := s.q.GetDefinition(ctx, definition)
	if err != nil {
		return 0, storeError(err)
	}
	return int(r.Latest), nil
}

func (s *instanceStore) RebaseInstance(ctx context.Context, i domain.Instance, snapshot []byte) error {
	n, err := s.q.RebaseInstance(ctx, workflowsql.RebaseInstanceParams{
		ID: i.ID, Version: int32Of(i.Version), Snapshot: snapshot, UpdatedAt: i.UpdatedAt,
	})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return app.ErrNotFound
	}
	return nil
}

func (s *instanceStore) DeleteFinished(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	n, err := s.q.DeleteFinishedInstances(ctx, workflowsql.DeleteFinishedInstancesParams{
		Cutoff: cutoff, MaxRows: int32Of(limit),
	})
	return int(n), storeError(err)
}

func (s *instanceStore) SeedDefault(ctx context.Context, rec *domain.DefinitionRecord, v *domain.Version, at time.Time) (bool, error) {
	var definition uuid.NullUUID
	if rec != nil && v != nil {
		ds := &store{q: s.q}
		if err := ds.InsertDefinition(ctx, *rec); err != nil {
			return false, err
		}
		if err := ds.InsertVersion(ctx, *v); err != nil {
			return false, err
		}
		definition = nullUUID(rec.ID)
	}
	n, err := s.q.InsertSeed(ctx, workflowsql.InsertSeedParams{DefinitionID: definition, SeededAt: at})
	if err != nil {
		return false, storeError(err)
	}
	if n == 0 {
		// A concurrent seeding won; roll ours back.
		return false, fmt.Errorf("%w: the tenant's default is already seeded", app.ErrConflict)
	}
	return true, nil
}

// ── read side (app.InstanceQueries) ─────────────────────────────────

// Page sizes for ListInstances.
const (
	defaultInstancePage = 50
	maxInstancePage     = 200
)

// ListInstances implements app.InstanceQueries. The cursor is the last
// instance id of the previous page.
func (s *Instances) ListInstances(ctx context.Context, f app.InstanceFilter) ([]app.InstanceView, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultInstancePage
	}
	limit = min(limit, maxInstancePage)
	var after uuid.NullUUID
	if f.After != "" {
		id, err := uuid.Parse(f.After)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %q", app.ErrInvalidCursor, f.After)
		}
		after = nullUUID(id)
	}
	var rows []workflowsql.WorkflowInstance
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		var err error
		rows, err = workflowsql.New(tx).ListInstances(ctx, workflowsql.ListInstancesParams{
			ProjectID: nullUUID(f.Project), DefinitionID: nullUUID(f.Definition), Status: text(f.Status),
			Locale: text(f.Locale), SubjectID: nullUUID(f.SubjectID), After: after, MaxRows: int32Of(limit + 1),
		})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].ID.String()
	}
	out := make([]app.InstanceView, len(rows))
	for i, r := range rows {
		out[i] = instanceView(r)
	}
	return out, next, nil
}

// GetInstance implements app.InstanceQueries.
func (s *Instances) GetInstance(ctx context.Context, id uuid.UUID) (app.InstanceView, error) {
	var out app.InstanceView
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		r, err := workflowsql.New(tx).GetInstance(ctx, id)
		if err != nil {
			return storeError(err)
		}
		out = instanceView(r)
		return nil
	})
	return out, err
}

// ListTransitions implements app.InstanceQueries: the instance's log,
// oldest first. An instance this tenant does not have is ErrNotFound.
func (s *Instances) ListTransitions(ctx context.Context, instance uuid.UUID) ([]app.TransitionView, error) {
	var out []app.TransitionView
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		q := workflowsql.New(tx)
		if _, err := q.GetInstance(ctx, instance); err != nil {
			return storeError(err)
		}
		rows, err := q.ListTransitions(ctx, instance)
		if err != nil {
			return err
		}
		out = make([]app.TransitionView, 0, len(rows))
		for _, r := range rows {
			v, err := transitionView(r)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// TenantsWithDueTimers implements app.TimerScanner in the system scope
// workflow.timers, which migration 0043 opens to the tenant and timer
// columns of workflow_instances and nothing else.
func (s *Instances) TenantsWithDueTimers(ctx context.Context, now time.Time, limit int) ([]tenancy.ID, error) {
	var out []tenancy.ID
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := workflowsql.New(tx).ListTenantsWithDueTimers(ctx, workflowsql.ListTenantsWithDueTimersParams{
			Now: pgtype.Timestamptz{Time: now, Valid: true}, MaxRows: int32Of(limit),
		})
		for _, r := range rows {
			out = append(out, tenancy.ID(r))
		}
		return err
	})
	return out, err
}

var _ app.WorkloadScanner = (*Instances)(nil)

// InstancesByStatus implements app.WorkloadScanner in the system scope
// workflow.timers: the status column migration 0043 grants, counted.
func (s *Instances) InstancesByStatus(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := workflowsql.New(tx).CountInstancesByStatus(ctx)
		for _, r := range rows {
			out[r.Status] = int(r.Instances)
		}
		return err
	})
	return out, err
}

// LiveAssignments implements app.WorkloadScanner in the system scope
// workflow.timers: the state and due date migration 0055 grants,
// counted.
func (s *Instances) LiveAssignments(ctx context.Context, now time.Time) (overdue, onTime int, err error) {
	err = s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		r, err := workflowsql.New(tx).CountLiveAssignments(ctx, pgtype.Timestamptz{Time: now, Valid: true})
		overdue, onTime = int(r.Overdue), int(r.OnTime)
		return err
	})
	return overdue, onTime, err
}

// ── rows ────────────────────────────────────────────────────────────

func instances(rows []workflowsql.WorkflowInstance) []domain.Instance {
	out := make([]domain.Instance, len(rows))
	for i, r := range rows {
		out[i] = instance(r)
	}
	return out
}

func instance(r workflowsql.WorkflowInstance) domain.Instance {
	i := domain.Instance{
		ID: r.ID, Project: r.ProjectID, Definition: r.DefinitionID, Version: int(r.Version),
		Kind: domain.SubjectKind(r.SubjectKind), SubjectID: r.SubjectID, Locale: r.Locale,
		State: r.State, Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		FinishedAt: timePtr(r.FinishedAt),
	}
	if r.TimerState.Valid {
		i.Timer = &domain.Timer{State: r.TimerState.String, DueAt: timePtr(r.DueAt), OverdueAt: timePtr(r.OverdueAt)}
	}
	return i
}

func instanceView(r workflowsql.WorkflowInstance) app.InstanceView {
	return app.InstanceView{
		ID: r.ID, Project: r.ProjectID, Definition: r.DefinitionID, Version: int(r.Version),
		Kind: domain.SubjectKind(r.SubjectKind), SubjectID: r.SubjectID, Locale: r.Locale,
		State: r.State, Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

type guardRow struct {
	Guard  string `json:"guard"`
	Passed bool   `json:"passed"`
}

type actionRow struct {
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

func guardRows(gs []app.GuardOutcome) []guardRow {
	out := make([]guardRow, len(gs))
	for i, g := range gs {
		out[i] = guardRow(g)
	}
	return out
}

func actionRows(as []app.ActionOutcome) []actionRow {
	out := make([]actionRow, len(as))
	for i, a := range as {
		out[i] = actionRow(a)
	}
	return out
}

func transitionView(r workflowsql.WorkflowTransition) (app.TransitionView, error) {
	var (
		gs []guardRow
		as []actionRow
	)
	if err := json.Unmarshal(r.Guards, &gs); err != nil {
		return app.TransitionView{}, fmt.Errorf("workflow: stored guards: %w", err)
	}
	if err := json.Unmarshal(r.Actions, &as); err != nil {
		return app.TransitionView{}, fmt.Errorf("workflow: stored actions: %w", err)
	}
	v := app.TransitionView{
		Seq: r.Seq, From: r.FromState, Event: r.Event, To: r.ToState, Outcome: r.Outcome,
		Guards: make([]app.GuardOutcome, len(gs)), Actions: make([]app.ActionOutcome, len(as)),
		Actor: r.Actor, OutboxEventID: r.OutboxEventID, At: r.At,
	}
	for i, g := range gs {
		v.Guards[i] = app.GuardOutcome(g)
	}
	for i, a := range as {
		v.Actions[i] = app.ActionOutcome(a)
	}
	return v, nil
}
