package httpapi_test

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// workMemory is one tenant's assignments and approvals in memory, with
// the Postgres adapter's semantics: lists in id order after a cursor,
// filters as the queries apply them, a duplicate id refused.
type workMemory struct {
	mu          sync.Mutex
	assignments map[uuid.UUID]domain.Assignment
	approvals   map[uuid.UUID]domain.Approval
	events      []outbox.Event
}

func newWorkMemory() *workMemory {
	return &workMemory{assignments: map[uuid.UUID]domain.Assignment{}, approvals: map[uuid.UUID]domain.Approval{}}
}

func (m *workMemory) InTenant(ctx context.Context, fn func(context.Context, app.WorkStore) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &workTx{m: m, assignments: clone(m.assignments), approvals: clone(m.approvals)}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	m.assignments, m.approvals = tx.assignments, tx.approvals
	m.events = append(m.events, tx.pending...)
	return nil
}

func clone[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type workTx struct {
	m           *workMemory
	assignments map[uuid.UUID]domain.Assignment
	approvals   map[uuid.UUID]domain.Approval
	pending     []outbox.Event
}

func (s *workTx) InsertAssignment(_ context.Context, a domain.Assignment) error {
	if _, ok := s.assignments[a.ID]; ok {
		return app.ErrConflict
	}
	s.assignments[a.ID] = a
	return nil
}

func (s *workTx) LiveAssignmentsOf(_ context.Context, assignee domain.Assignee) (int, error) {
	n := 0
	for _, a := range s.assignments {
		if a.Assignee == assignee && a.State.Live() {
			n++
		}
	}
	return n, nil
}

func (s *workTx) GetAssignment(_ context.Context, id uuid.UUID) (domain.Assignment, error) {
	a, ok := s.assignments[id]
	if !ok {
		return a, app.ErrNotFound
	}
	return a, nil
}

func (s *workTx) LockAssignment(ctx context.Context, id uuid.UUID) (domain.Assignment, error) {
	return s.GetAssignment(ctx, id)
}

func (s *workTx) UpdateAssignment(_ context.Context, a domain.Assignment) error {
	if s.assignments[a.ID].Version != a.Version-1 {
		return app.ErrConflict
	}
	s.assignments[a.ID] = a
	return nil
}

func sortedIDs[V any](m map[uuid.UUID]V) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}

func (s *workTx) ListAssignments(_ context.Context, f app.AssignmentFilter) ([]domain.Assignment, error) {
	var out []domain.Assignment
	for _, id := range sortedIDs(s.assignments) {
		a := s.assignments[id]
		covers := f.Message == uuid.Nil && f.Locale == ""
		for _, u := range a.Units {
			if (f.Message == uuid.Nil || u.Message == f.Message) && (f.Locale == "" || u.Locale == f.Locale) {
				covers = true
			}
		}
		switch {
		case f.After != uuid.Nil && id.String() <= f.After.String(),
			f.Project != uuid.Nil && a.ProjectID != f.Project,
			f.Within != nil && !slices.Contains(*f.Within, a.ProjectID),
			f.Assignees != nil && !slices.Contains(f.Assignees, a.Assignee.String()),
			len(f.States) > 0 && !slices.Contains(f.States, a.State),
			!covers:
			continue
		}
		out = append(out, a)
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

func (s *workTx) CoveredUnits(context.Context, app.CoverageQuery) ([]domain.Unit, error) {
	return nil, nil
}

func (s *workTx) InsertApproval(_ context.Context, a domain.Approval) error {
	s.approvals[a.ID] = a
	return nil
}

func (s *workTx) GetApproval(_ context.Context, id uuid.UUID) (domain.Approval, error) {
	a, ok := s.approvals[id]
	if !ok {
		return a, app.ErrNotFound
	}
	a.Decisions = slices.Clone(a.Decisions)
	return a, nil
}

func (s *workTx) LockApproval(ctx context.Context, id uuid.UUID) (domain.Approval, error) {
	return s.GetApproval(ctx, id)
}

func (s *workTx) LatestApproval(ctx context.Context, project uuid.UUID, subject domain.ApprovalSubject) (domain.Approval, error) {
	ids := sortedIDs(s.approvals)
	for i := len(ids) - 1; i >= 0; i-- {
		if a := s.approvals[ids[i]]; a.ProjectID == project && a.Subject == subject {
			return s.GetApproval(ctx, a.ID)
		}
	}
	return domain.Approval{}, app.ErrNotFound
}

func (s *workTx) ListApprovals(_ context.Context, f app.ApprovalFilter) ([]domain.Approval, error) {
	var out []domain.Approval
	for _, id := range sortedIDs(s.approvals) {
		a := s.approvals[id]
		switch {
		case f.After != uuid.Nil && id.String() <= f.After.String(),
			f.Project != uuid.Nil && a.ProjectID != f.Project,
			f.Within != nil && !slices.Contains(*f.Within, a.ProjectID),
			f.Kind != "" && a.Subject.Kind != f.Kind,
			f.SubjectID != uuid.Nil && a.Subject.ID != f.SubjectID,
			f.Locale != "" && a.Subject.Locale != f.Locale,
			len(f.States) > 0 && !slices.Contains(f.States, a.State):
			continue
		}
		out = append(out, a)
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

func (s *workTx) AppendDecision(_ context.Context, id uuid.UUID, seq int, d domain.Decision) error {
	a := s.approvals[id]
	if seq != len(a.Decisions)+1 {
		return app.ErrConflict
	}
	a.Decisions = append(slices.Clone(a.Decisions), d)
	s.approvals[id] = a
	return nil
}

func (s *workTx) UpdateApproval(_ context.Context, a domain.Approval) error {
	stored := s.approvals[a.ID]
	if stored.Version != a.Version-1 {
		return app.ErrConflict
	}
	stored.State, stored.Version, stored.ClosedAt = a.State, a.Version, a.ClosedAt
	s.approvals[a.ID] = stored
	return nil
}

func (s *workTx) Publish(_ context.Context, e outbox.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	s.pending = append(s.pending, e)
	return nil
}

// directory is Identity's read side: members' affiliations, and groups
// and vendors by name.
type directory struct {
	members map[uuid.UUID]app.Affiliation
	vendors map[string]uuid.UUID
}

func (d *directory) Affiliation(_ context.Context, m uuid.UUID) (app.Affiliation, error) {
	a, ok := d.members[m]
	if !ok {
		return a, app.ErrNotFound
	}
	return a, nil
}

func (d *directory) Resolve(_ context.Context, kind domain.AssigneeKind, ref string) (uuid.UUID, error) {
	switch kind {
	case domain.AssigneeVendor:
		if id, ok := d.vendors[strings.ToLower(ref)]; ok {
			return id, nil
		}
		if id, err := uuid.Parse(ref); err == nil && slices.Contains(mapValues(d.vendors), id) {
			return id, nil
		}
	case domain.AssigneeMember:
		if id, err := uuid.Parse(ref); err == nil {
			if _, ok := d.members[id]; ok {
				return id, nil
			}
		}
	}
	return uuid.Nil, app.ErrUnknownParty
}

func mapValues(m map[string]uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// authors says who wrote each message's text.
type authors map[uuid.UUID]string

func (a authors) Author(_ context.Context, _ uuid.UUID, s domain.ApprovalSubject) (string, error) {
	return a[s.ID], nil
}
