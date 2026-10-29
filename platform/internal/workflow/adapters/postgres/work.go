package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres/worksql"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// WorkTransactor implements app.WorkTransactor: assignments and
// approvals (migration 0044) in the tenant transaction ctx is already
// in — the instance runner's step, a read path filtering by coverage —
// or in a new one.
type WorkTransactor struct{ uow *db.UnitOfWork }

// NewWorkTransactor returns a WorkTransactor on uow.
func NewWorkTransactor(uow *db.UnitOfWork) *WorkTransactor { return &WorkTransactor{uow: uow} }

// InTenant implements app.WorkTransactor.
func (t *WorkTransactor) InTenant(ctx context.Context, fn func(context.Context, app.WorkStore) error) error {
	run := func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &workStore{tx: tx, q: worksql.New(tx)})
	}
	err := t.uow.InCurrentTenantTx(ctx, run)
	if errors.Is(err, db.ErrNoTx) {
		return t.uow.InTenantTx(ctx, run)
	}
	return err
}

type workStore struct {
	tx *db.TenantTx
	q  *worksql.Queries
}

var _ app.WorkStore = (*workStore)(nil)

// ── assignments ─────────────────────────────────────────────────────

func (s *workStore) LiveAssignmentsOf(ctx context.Context, assignee domain.Assignee) (int, error) {
	if err := s.q.LockAssignee(ctx, assignee.String()); err != nil {
		return 0, err
	}
	n, err := s.q.CountLiveAssignmentsOf(ctx, assignee.String())
	return int(n), err
}

func (s *workStore) InsertAssignment(ctx context.Context, a domain.Assignment) error {
	err := s.q.InsertAssignment(ctx, worksql.InsertAssignmentParams{
		ID: a.ID, ProjectID: a.ProjectID, InstanceID: nullUUID(a.InstanceID), Assignee: a.Assignee.String(),
		Permission: a.Permission, DueAt: timestamptz(a.DueAt), State: string(a.State), Version: int32Of(a.Version),
		CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt,
	})
	if err != nil {
		return storeError(err)
	}
	ids := make([]uuid.UUID, len(a.Units))
	locales := make([]string, len(a.Units))
	for i, u := range a.Units {
		ids[i], locales[i] = u.Message, u.Locale
	}
	return storeError(s.q.InsertAssignmentUnits(ctx, worksql.InsertAssignmentUnitsParams{
		AssignmentID: a.ID, MessageIds: ids, Locales: locales,
	}))
}

func (s *workStore) GetAssignment(ctx context.Context, id uuid.UUID) (domain.Assignment, error) {
	r, err := s.q.GetAssignment(ctx, id)
	if err != nil {
		return domain.Assignment{}, storeError(err)
	}
	return s.withUnits(ctx, r)
}

func (s *workStore) LockAssignment(ctx context.Context, id uuid.UUID) (domain.Assignment, error) {
	r, err := s.q.LockAssignment(ctx, id)
	if err != nil {
		return domain.Assignment{}, storeError(err)
	}
	return s.withUnits(ctx, r)
}

func (s *workStore) withUnits(ctx context.Context, r worksql.WorkflowAssignment) (domain.Assignment, error) {
	as, err := s.assignments(ctx, []worksql.WorkflowAssignment{r})
	if err != nil {
		return domain.Assignment{}, err
	}
	return as[0], nil
}

// assignments loads rows' units in one query.
func (s *workStore) assignments(ctx context.Context, rows []worksql.WorkflowAssignment) ([]domain.Assignment, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	units, err := s.q.AssignmentUnits(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID][]domain.Unit{}
	for _, u := range units {
		byID[u.AssignmentID] = append(byID[u.AssignmentID], domain.Unit{Message: u.MessageID, Locale: u.Locale})
	}
	out := make([]domain.Assignment, len(rows))
	for i, r := range rows {
		a, err := assignment(r)
		if err != nil {
			return nil, err
		}
		a.Units = byID[r.ID]
		out[i] = a
	}
	return out, nil
}

func (s *workStore) UpdateAssignment(ctx context.Context, a domain.Assignment) error {
	n, err := s.q.UpdateAssignment(ctx, worksql.UpdateAssignmentParams{
		ID: a.ID, State: string(a.State), Version: int32Of(a.Version), UpdatedAt: a.UpdatedAt,
		ClosedBy: text(a.ClosedBy), ClosedAt: timestamptz(a.ClosedAt), Reason: a.Reason,
	})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return app.ErrConflict
	}
	return nil
}

// maxPage bounds a list of assignments.
const maxPage = 200

func (s *workStore) ListAssignments(ctx context.Context, f app.AssignmentFilter) ([]domain.Assignment, error) {
	limit := f.Limit
	if limit <= 0 || limit > maxPage {
		limit = maxPage
	}
	states := make([]string, len(f.States))
	for i, st := range f.States {
		states[i] = string(st)
	}
	rows, err := s.q.ListAssignments(ctx, worksql.ListAssignmentsParams{
		After: f.After, ProjectID: f.Project, States: states,
		ByAssignee: f.Assignees != nil, Assignees: emptyIfNil(f.Assignees), MaxRows: int32Of(limit),
		ByProjects: f.Within != nil, Projects: within(f.Within),
		ByUnit: f.Message != uuid.Nil || f.Locale != "", UnitMessage: f.Message, UnitLocale: f.Locale,
	})
	if err != nil {
		return nil, err
	}
	return s.assignments(ctx, rows)
}

func (s *workStore) CoveredUnits(ctx context.Context, q app.CoverageQuery) ([]domain.Unit, error) {
	p := worksql.CoveredUnitsParams{
		ProjectID: q.Project, Assignees: emptyIfNil(q.Assignees),
		DoneSince: pgtype.Timestamptz{Time: q.DoneSince, Valid: true},
	}
	if q.Unit != nil {
		p.OneUnit, p.MessageID, p.Locale = true, q.Unit.Message, q.Unit.Locale
	}
	rows, err := s.q.CoveredUnits(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Unit, len(rows))
	for i, r := range rows {
		out[i] = domain.Unit{Message: r.MessageID, Locale: r.Locale}
	}
	return out, nil
}

// ── approvals ───────────────────────────────────────────────────────

func (s *workStore) InsertApproval(ctx context.Context, a domain.Approval) error {
	return storeError(s.q.InsertApproval(ctx, worksql.InsertApprovalParams{
		ID: a.ID, ProjectID: a.ProjectID, InstanceID: nullUUID(a.InstanceID), SubjectKind: string(a.Subject.Kind),
		SubjectID: a.Subject.ID, Locale: a.Subject.Locale, Required: int32Of(a.Required), Eligible: a.Eligible.String(),
		DistinctFromAuthor: a.DistinctFromAuthor, DueAt: timestamptz(a.DueAt), State: string(a.State),
		Version: int32Of(a.Version), CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt,
	}))
}

func (s *workStore) GetApproval(ctx context.Context, id uuid.UUID) (domain.Approval, error) {
	r, err := s.q.GetApproval(ctx, id)
	if err != nil {
		return domain.Approval{}, storeError(err)
	}
	return s.withDecisions(ctx, r)
}

func (s *workStore) LockApproval(ctx context.Context, id uuid.UUID) (domain.Approval, error) {
	r, err := s.q.LockApproval(ctx, id)
	if err != nil {
		return domain.Approval{}, storeError(err)
	}
	return s.withDecisions(ctx, r)
}

func (s *workStore) LatestApproval(ctx context.Context, project uuid.UUID, subject domain.ApprovalSubject) (domain.Approval, error) {
	r, err := s.q.LatestApproval(ctx, worksql.LatestApprovalParams{
		ProjectID: project, SubjectKind: string(subject.Kind), SubjectID: subject.ID, Locale: subject.Locale,
	})
	if err != nil {
		return domain.Approval{}, storeError(err)
	}
	return s.withDecisions(ctx, r)
}

func (s *workStore) ListApprovals(ctx context.Context, f app.ApprovalFilter) ([]domain.Approval, error) {
	limit := f.Limit
	if limit <= 0 || limit > maxPage {
		limit = maxPage
	}
	states := make([]string, len(f.States))
	for i, st := range f.States {
		states[i] = string(st)
	}
	rows, err := s.q.ListApprovals(ctx, worksql.ListApprovalsParams{
		After: f.After, ProjectID: f.Project, SubjectKind: string(f.Kind), SubjectID: f.SubjectID, Locale: f.Locale,
		States: states, ByProjects: f.Within != nil, Projects: within(f.Within), MaxRows: int32Of(limit),
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	// Every page's decisions in one query.
	ds, err := s.q.DecisionsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID][]domain.Decision{}
	for _, d := range ds {
		byID[d.ApprovalID] = append(byID[d.ApprovalID], decision(d))
	}
	out := make([]domain.Approval, len(rows))
	for i, r := range rows {
		a, err := approval(r)
		if err != nil {
			return nil, err
		}
		a.Decisions = byID[r.ID]
		out[i] = a
	}
	return out, nil
}

func decision(d worksql.WorkflowApprovalDecision) domain.Decision {
	return domain.Decision{Principal: d.Principal, Verdict: domain.Verdict(d.Verdict), Reason: d.Reason, At: d.DecidedAt.UTC()}
}

func (s *workStore) withDecisions(ctx context.Context, r worksql.WorkflowApproval) (domain.Approval, error) {
	a, err := approval(r)
	if err != nil {
		return domain.Approval{}, err
	}
	ds, err := s.q.ApprovalDecisions(ctx, r.ID)
	if err != nil {
		return domain.Approval{}, err
	}
	for _, d := range ds {
		a.Decisions = append(a.Decisions, decision(d))
	}
	return a, nil
}

func (s *workStore) AppendDecision(ctx context.Context, approval uuid.UUID, seq int, d domain.Decision) error {
	return storeError(s.q.InsertDecision(ctx, worksql.InsertDecisionParams{
		ApprovalID: approval, Seq: int32Of(seq), Principal: d.Principal, Verdict: string(d.Verdict),
		Reason: d.Reason, DecidedAt: d.At,
	}))
}

func (s *workStore) UpdateApproval(ctx context.Context, a domain.Approval) error {
	n, err := s.q.UpdateApproval(ctx, worksql.UpdateApprovalParams{
		ID: a.ID, State: string(a.State), Version: int32Of(a.Version), ClosedAt: timestamptz(a.ClosedAt),
	})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return app.ErrConflict
	}
	return nil
}

func (s *workStore) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}

// ── rows ────────────────────────────────────────────────────────────

func assignment(r worksql.WorkflowAssignment) (domain.Assignment, error) {
	to, err := domain.ParseAssignee(r.Assignee)
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("workflow: stored assignment %s: %w", r.ID, err)
	}
	return domain.Assignment{
		ID: r.ID, ProjectID: r.ProjectID, InstanceID: r.InstanceID.UUID, Assignee: to, Permission: r.Permission,
		DueAt: timePtr(r.DueAt), State: domain.AssignmentState(r.State), Version: int(r.Version),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
		ClosedBy: r.ClosedBy.String, ClosedAt: timePtr(r.ClosedAt), Reason: r.Reason,
	}, nil
}

func approval(r worksql.WorkflowApproval) (domain.Approval, error) {
	eligible, err := domain.ParseAssignee(r.Eligible)
	if err != nil {
		return domain.Approval{}, fmt.Errorf("workflow: stored approval %s: %w", r.ID, err)
	}
	return domain.Approval{
		ID: r.ID, ProjectID: r.ProjectID, InstanceID: r.InstanceID.UUID,
		Subject:  domain.ApprovalSubject{Kind: domain.SubjectKind(r.SubjectKind), ID: r.SubjectID, Locale: r.Locale},
		Required: int(r.Required), Eligible: eligible, DistinctFromAuthor: r.DistinctFromAuthor,
		DueAt: timePtr(r.DueAt), State: domain.ApprovalState(r.State), Version: int(r.Version),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), ClosedAt: timePtr(r.ClosedAt),
	}, nil
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func emptyIfNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// within is the project set a scoped list is cut to; an empty, non-nil
// set lists nothing.
func within(p *[]uuid.UUID) []uuid.UUID {
	if p == nil || *p == nil {
		return []uuid.UUID{}
	}
	return *p
}
