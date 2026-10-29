package httpapi_test

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// memory is one tenant's Workflow tables in memory, with the semantics
// the Postgres adapter has (unique names per scope, unique selectors,
// positions in creation order). Events are committed with the unit of
// work that published them, and dropped with one that failed.
type memory struct {
	mu       sync.Mutex
	defs     map[uuid.UUID]domain.DefinitionRecord
	versions map[uuid.UUID][]domain.Version
	bindings []domain.Binding
	position int64
	events   []outbox.Event
}

func newMemory() *memory {
	return &memory{defs: map[uuid.UUID]domain.DefinitionRecord{}, versions: map[uuid.UUID][]domain.Version{}}
}

func (m *memory) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &memStore{m: m}
	if err := fn(ctx, st); err != nil {
		return err
	}
	m.events = append(m.events, st.pending...)
	return nil
}

func (m *memory) published() []outbox.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.events)
}

type memStore struct {
	m       *memory
	pending []outbox.Event
}

func (s *memStore) InsertDefinition(_ context.Context, d domain.DefinitionRecord) error {
	for _, o := range s.m.defs {
		if o.DeletedAt == nil && o.Name == d.Name && o.ProjectID == d.ProjectID {
			return app.ErrConflict
		}
	}
	s.m.defs[d.ID] = d
	return nil
}

func (s *memStore) GetDefinition(_ context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	d, ok := s.m.defs[id]
	if !ok {
		return domain.DefinitionRecord{}, app.ErrNotFound
	}
	return d, nil
}

func (s *memStore) LockDefinition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	d, err := s.GetDefinition(ctx, id)
	if err == nil && d.DeletedAt != nil {
		return domain.DefinitionRecord{}, app.ErrNotFound
	}
	return d, err
}

func (s *memStore) ListDefinitions(_ context.Context, project uuid.UUID) ([]domain.DefinitionRecord, error) {
	var out []domain.DefinitionRecord
	for _, d := range s.m.defs {
		if d.DeletedAt == nil && (project == uuid.Nil || d.ProjectID == uuid.Nil || d.ProjectID == project) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *memStore) CountLiveDefinitions(ctx context.Context) (int, error) {
	ds, err := s.ListDefinitions(ctx, uuid.Nil)
	return len(ds), err
}

func (s *memStore) SetLatest(_ context.Context, id uuid.UUID, n int) error {
	d := s.m.defs[id]
	if d.Latest != n-1 {
		return app.ErrConflict
	}
	d.Latest = n
	s.m.defs[id] = d
	return nil
}

func (s *memStore) MarkDeleted(_ context.Context, d domain.DefinitionRecord) error {
	s.m.defs[d.ID] = d
	return nil
}

func (s *memStore) InsertVersion(_ context.Context, v domain.Version) error {
	for _, o := range s.m.versions[v.DefinitionID] {
		if o.Number == v.Number {
			return app.ErrConflict
		}
	}
	s.m.versions[v.DefinitionID] = append(s.m.versions[v.DefinitionID], v)
	return nil
}

func (s *memStore) GetVersion(_ context.Context, definition uuid.UUID, n int) (domain.Version, error) {
	for _, v := range s.m.versions[definition] {
		if v.Number == n {
			return v, nil
		}
	}
	return domain.Version{}, app.ErrNotFound
}

func (s *memStore) ListVersions(_ context.Context, definition uuid.UUID) ([]domain.Version, error) {
	out := slices.Clone(s.m.versions[definition])
	sort.Slice(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

func (s *memStore) InsertBinding(_ context.Context, b domain.Binding) (domain.Binding, error) {
	for _, o := range s.m.bindings {
		if o.ProjectID == b.ProjectID && o.Subject == b.Subject && o.Namespace == b.Namespace &&
			strings.Join(o.Locales, ",") == strings.Join(b.Locales, ",") {
			return domain.Binding{}, app.ErrConflict
		}
	}
	s.m.position++
	b.Position = s.m.position
	s.m.bindings = append(s.m.bindings, b)
	return b, nil
}

func (s *memStore) GetBinding(_ context.Context, id uuid.UUID) (domain.Binding, error) {
	for _, b := range s.m.bindings {
		if b.ID == id {
			return b, nil
		}
	}
	return domain.Binding{}, app.ErrNotFound
}

func (s *memStore) ListBindings(_ context.Context, project uuid.UUID, subject domain.SubjectKind) ([]domain.Binding, error) {
	var out []domain.Binding
	for _, b := range s.m.bindings {
		if b.ProjectID == project && (subject == "" || b.Subject == subject) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *memStore) DeleteBinding(_ context.Context, id uuid.UUID) error {
	n := len(s.m.bindings)
	s.m.bindings = slices.DeleteFunc(s.m.bindings, func(b domain.Binding) bool { return b.ID == id })
	if len(s.m.bindings) == n {
		return app.ErrNotFound
	}
	return nil
}

func (s *memStore) DeleteBindingsOf(_ context.Context, definition uuid.UUID) (int, error) {
	n := len(s.m.bindings)
	s.m.bindings = slices.DeleteFunc(s.m.bindings, func(b domain.Binding) bool { return b.DefinitionID == definition })
	return n - len(s.m.bindings), nil
}

func (s *memStore) Publish(_ context.Context, e outbox.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	s.pending = append(s.pending, e)
	return nil
}

// catalog knows some projects and, in each, some message keys.
type catalog struct {
	projects map[uuid.UUID]map[string]uuid.UUID
}

func (c catalog) Project(_ context.Context, project uuid.UUID) error {
	if _, ok := c.projects[project]; !ok {
		return app.ErrProjectNotFound
	}
	return nil
}

func (c catalog) MessageID(_ context.Context, project uuid.UUID, key string) (uuid.UUID, error) {
	keys, ok := c.projects[project]
	if !ok {
		return uuid.Nil, app.ErrProjectNotFound
	}
	id, ok := keys[key]
	if !ok {
		return uuid.Nil, app.ErrNotFound
	}
	return id, nil
}

// instances is an instance store holding a fixed set, recording the
// last filter it was asked.
type instances struct {
	items       []app.InstanceView
	transitions map[uuid.UUID][]app.TransitionView
	last        app.InstanceFilter
	next        string
}

func (q *instances) ListInstances(_ context.Context, f app.InstanceFilter) ([]app.InstanceView, string, error) {
	q.last = f
	var out []app.InstanceView
	for _, v := range q.items {
		if v.Project == f.Project && (f.SubjectID == uuid.Nil || v.SubjectID == f.SubjectID) {
			out = append(out, v)
		}
	}
	return out, q.next, nil
}

func (q *instances) GetInstance(_ context.Context, id uuid.UUID) (app.InstanceView, error) {
	for _, v := range q.items {
		if v.ID == id {
			return v, nil
		}
	}
	return app.InstanceView{}, app.ErrNotFound
}

func (q *instances) ListTransitions(_ context.Context, instance uuid.UUID) ([]app.TransitionView, error) {
	return q.transitions[instance], nil
}
