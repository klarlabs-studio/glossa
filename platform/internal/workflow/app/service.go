package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The permissions RFC 0006 §4.2 adds for workflows are Identity's, and
// this service checks them by Identity's names. They were string copies
// here while the two slices were built in parallel; a second spelling of
// a permission is how a rename in one place silently stops matching in
// the other, so the copies are gone.
const (
	PermWorkflowsRead   = authz.WorkflowsRead
	PermWorkflowsManage = authz.WorkflowsManage
)

// Service implements Workflow's definition and binding use cases.
//
// Events. Every write publishes its domain event (domain/events.go) in
// the transaction that made it, naming the principal as its actor —
// the same principal created_by records.
type Service struct {
	tx          Transactor
	permissions Permissions
	catalog     Catalog
	now         func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithClock sets the time source.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithCatalog sets the port projects are checked through. Without one,
// a project id is taken as given: rows are keyed by it, and another
// tenant's project still finds nothing under row-level security.
func WithCatalog(c Catalog) Option { return func(s *Service) { s.catalog = c } }

// New returns a Service. permissions may be nil, and then an
// actor_has_permission guard is checked for its shape only.
func New(tx Transactor, permissions Permissions, opts ...Option) *Service {
	s := &Service{tx: tx, permissions: permissions, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Saved is a stored version and what lint had to say about it.
type Saved struct {
	Definition domain.DefinitionRecord
	Version    domain.Version
	// Findings are statekit's informational notes; anything worse
	// refused the save.
	Findings []domain.Finding
}

// NewDefinition is a definition to create.
type NewDefinition struct {
	// ProjectID scopes it to one project; uuid.Nil makes it the
	// tenant's.
	ProjectID uuid.UUID
	Document  []byte
}

// CreateDefinition compiles and lints doc and stores it as version 1 of
// a new definition. A document that does not compile is refused with
// *domain.InvalidError (errors.Is ErrInvalidWorkflow) and nothing is
// stored.
func (s *Service) CreateDefinition(ctx context.Context, in NewDefinition) (Saved, error) {
	actor, err := s.authorize(ctx, PermWorkflowsManage)
	if err != nil {
		return Saved{}, err
	}
	if err := s.createScope(ctx, in.ProjectID); err != nil {
		return Saved{}, err
	}
	d, err := s.compile(in.Document)
	if err != nil {
		return Saved{}, err
	}
	now := s.now().UTC()
	rec := domain.DefinitionRecord{ID: uuid.New(), ProjectID: in.ProjectID, Name: d.Name, Subject: d.Subject,
		Latest: 1, CreatedBy: actor, CreatedAt: now}
	v := domain.FirstVersion(rec, d, actor, now)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		n, err := st.CountLiveDefinitions(ctx)
		if err != nil {
			return err
		}
		if n >= domain.MaxActiveDefinitions {
			return fmt.Errorf("%w: at most %d live definitions per tenant", ErrLimit, domain.MaxActiveDefinitions)
		}
		if err := st.InsertDefinition(ctx, rec); err != nil {
			return err
		}
		if err := st.InsertVersion(ctx, v); err != nil {
			return err
		}
		return publishSaved(ctx, st, rec, v)
	})
	if err != nil {
		return Saved{}, err
	}
	return Saved{Definition: rec, Version: v, Findings: d.Findings}, nil
}

// SaveVersion compiles and lints doc and appends it as the next version
// of definition. ifLatest is the version the author edited: if another
// save landed since, the save is ErrConflict rather than a silent
// overwrite of their change. Running instances stay on their version.
func (s *Service) SaveVersion(ctx context.Context, definition uuid.UUID, ifLatest int, doc []byte) (Saved, error) {
	actor, err := s.authorize(ctx, PermWorkflowsManage)
	if err != nil {
		return Saved{}, err
	}
	d, err := s.compile(doc)
	if err != nil {
		return Saved{}, err
	}
	var out Saved
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rec, err := st.LockDefinition(ctx, definition)
		if err != nil {
			return err
		}
		if err := writeScope(ctx, rec.ProjectID); err != nil {
			return err
		}
		if rec.Latest != ifLatest {
			return fmt.Errorf("%w: version %d is the latest, not %d", ErrConflict, rec.Latest, ifLatest)
		}
		v, err := domain.NextVersion(rec, d, actor, s.now().UTC())
		if err != nil {
			return err
		}
		if err := st.InsertVersion(ctx, v); err != nil {
			return err
		}
		if err := st.SetLatest(ctx, rec.ID, v.Number); err != nil {
			return err
		}
		rec.Latest = v.Number
		out = Saved{Definition: rec, Version: v, Findings: d.Findings}
		return publishSaved(ctx, st, rec, v)
	})
	return out, err
}

func publishSaved(ctx context.Context, st Store, rec domain.DefinitionRecord, v domain.Version) error {
	actor, err := authz.EventActor(ctx)
	if err != nil {
		return err
	}
	return st.Publish(ctx, outbox.Event{
		Type: domain.EventDefinitionSaved, AggregateType: domain.AggregateDefinition,
		AggregateID: rec.ID.String(), Actor: actor, Payload: domain.DefinitionSavedOf(rec, v),
	})
}

func publishBinding(ctx context.Context, st Store, b domain.Binding, change string) error {
	actor, err := authz.EventActor(ctx)
	if err != nil {
		return err
	}
	return st.Publish(ctx, outbox.Event{
		Type: domain.EventBindingChanged, AggregateType: domain.AggregateBinding,
		AggregateID: b.ID.String(), Actor: actor, Payload: domain.BindingChangedOf(b, change),
	})
}

// Lint compiles doc without saving it: what `glossa workflow lint` and
// the editor ask before a save.
func (s *Service) Lint(ctx context.Context, doc []byte) ([]domain.Finding, error) {
	if _, err := s.authorize(ctx, PermWorkflowsRead); err != nil {
		return nil, err
	}
	d, err := s.compile(doc)
	if err != nil {
		return nil, err
	}
	return d.Findings, nil
}

// compile is lint-on-save, plus the one check the domain cannot make
// alone: that a permission a guard names exists.
func (s *Service) compile(doc []byte) (*domain.Definition, error) {
	d, err := domain.Compile(doc)
	if err != nil {
		return nil, err
	}
	if s.permissions == nil {
		return d, nil
	}
	for name, g := range d.Guards {
		if p, ok := g.Params.(domain.ActorHasPermission); ok && !s.permissions.Known(p.Permission) {
			return nil, &domain.InvalidError{Findings: []domain.Finding{{
				Rule: domain.RuleParams, Severity: domain.SeverityError, Path: "guards." + name,
				Message: fmt.Sprintf("%v: %q", ErrUnknownPermission, p.Permission),
			}}}
		}
	}
	return d, nil
}

// Definition reads a definition.
func (s *Service) Definition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	var out domain.DefinitionRecord
	err := s.read(ctx, func(ctx context.Context, st Store) (err error) {
		if out, err = st.GetDefinition(ctx, id); err != nil {
			return err
		}
		return readScope(ctx, out.ProjectID)
	})
	return out, err
}

// Definitions lists the live definitions project may bind (the
// tenant's and its own), or every live one for uuid.Nil.
func (s *Service) Definitions(ctx context.Context, project uuid.UUID) ([]domain.DefinitionRecord, error) {
	var out []domain.DefinitionRecord
	err := s.readProject(ctx, project, func(ctx context.Context, st Store) (err error) {
		out, err = st.ListDefinitions(ctx, project)
		return err
	})
	return out, err
}

// Version reads one version of a definition.
func (s *Service) Version(ctx context.Context, definition uuid.UUID, n int) (domain.Version, error) {
	var out domain.Version
	err := s.read(ctx, func(ctx context.Context, st Store) (err error) {
		rec, err := st.GetDefinition(ctx, definition)
		if err != nil {
			return err
		}
		if err := readScope(ctx, rec.ProjectID); err != nil {
			return err
		}
		out, err = st.GetVersion(ctx, definition, n)
		return err
	})
	return out, err
}

// Versions lists a definition's versions, newest first.
func (s *Service) Versions(ctx context.Context, definition uuid.UUID) ([]domain.Version, error) {
	var out []domain.Version
	err := s.read(ctx, func(ctx context.Context, st Store) error {
		rec, err := st.GetDefinition(ctx, definition)
		if err != nil {
			return err
		}
		if err := readScope(ctx, rec.ProjectID); err != nil {
			return err
		}
		out, err = st.ListVersions(ctx, definition)
		return err
	})
	return out, err
}

// DeleteDefinition deletes a definition and its bindings. Its versions
// stay, so whatever ran on them stays explainable. Deleting the default
// review definition is allowed and means "no workflow" (§2.3).
func (s *Service) DeleteDefinition(ctx context.Context, id uuid.UUID) error {
	if _, err := s.authorize(ctx, PermWorkflowsManage); err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rec, err := st.LockDefinition(ctx, id)
		if err != nil {
			return err
		}
		if err := writeScope(ctx, rec.ProjectID); err != nil {
			return err
		}
		removed, err := st.DeleteBindingsOf(ctx, id)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		rec.DeletedAt = &now
		if err := st.MarkDeleted(ctx, rec); err != nil {
			return err
		}
		actor, err := authz.EventActor(ctx)
		if err != nil {
			return err
		}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventDefinitionDeleted, AggregateType: domain.AggregateDefinition,
			AggregateID: rec.ID.String(), Actor: actor, Payload: domain.DefinitionDeletedOf(rec, removed),
		})
	})
}

// NewBinding is a binding to create.
type NewBinding struct {
	ProjectID    uuid.UUID
	Locales      []string
	Namespace    string
	DefinitionID uuid.UUID
}

// Bind binds a live definition to a project, optionally narrowed to
// some locales and a namespace. A selector another binding already has
// is ErrConflict: the earlier one could never apply again.
func (s *Service) Bind(ctx context.Context, in NewBinding) (domain.Binding, error) {
	actor, err := s.authorize(ctx, PermWorkflowsManage)
	if err != nil {
		return domain.Binding{}, err
	}
	if err := s.project(ctx, in.ProjectID); err != nil {
		return domain.Binding{}, err
	}
	var out domain.Binding
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rec, err := st.GetDefinition(ctx, in.DefinitionID)
		if err != nil {
			return err
		}
		if rec.DeletedAt != nil {
			return fmt.Errorf("%w: definition %s is deleted", ErrNotFound, rec.ID)
		}
		if rec.ProjectID != uuid.Nil && rec.ProjectID != in.ProjectID {
			return ErrOutOfScope
		}
		b, err := domain.NewBinding(in.ProjectID, rec.Subject, in.Locales, in.Namespace, rec.ID)
		if err != nil {
			return err
		}
		b.ID, b.CreatedBy, b.CreatedAt = uuid.New(), actor, s.now().UTC()
		if out, err = st.InsertBinding(ctx, b); err != nil {
			return err
		}
		return publishBinding(ctx, st, out, domain.BindingCreated)
	})
	return out, err
}

// Unbind removes one of project's bindings. A binding of another
// project is not found: the project in the path is part of its address.
func (s *Service) Unbind(ctx context.Context, project, id uuid.UUID) error {
	if _, err := s.authorize(ctx, PermWorkflowsManage); err != nil {
		return err
	}
	if err := s.project(ctx, project); err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		b, err := st.GetBinding(ctx, id)
		if err != nil {
			return err
		}
		if b.ProjectID != project {
			return ErrNotFound
		}
		if err := st.DeleteBinding(ctx, id); err != nil {
			return err
		}
		return publishBinding(ctx, st, b, domain.BindingDeleted)
	})
}

// Bindings lists a project's bindings in creation order.
func (s *Service) Bindings(ctx context.Context, project uuid.UUID) ([]domain.Binding, error) {
	var out []domain.Binding
	err := s.readProject(ctx, project, func(ctx context.Context, st Store) (err error) {
		out, err = st.ListBindings(ctx, project, "")
		return err
	})
	return out, err
}

// Resolution is the definition version a subject runs under, and the
// binding that chose it.
type Resolution struct {
	Binding domain.Binding
	Version domain.Version
}

// Resolve answers which definition version a new instance for t would
// start on: the binding domain.Resolve picks, at its definition's latest
// version. false means no binding applies, which is M4's behaviour — no
// instance, nothing changes (§2.2).
func (s *Service) Resolve(ctx context.Context, t domain.Target) (Resolution, bool, error) {
	var (
		out   Resolution
		found bool
	)
	err := s.readProject(ctx, t.ProjectID, func(ctx context.Context, st Store) error {
		bs, err := st.ListBindings(ctx, t.ProjectID, t.Subject)
		if err != nil {
			return err
		}
		b, ok := domain.Resolve(bs, t)
		if !ok {
			return nil
		}
		rec, err := st.GetDefinition(ctx, b.DefinitionID)
		if err != nil {
			return err
		}
		v, err := st.GetVersion(ctx, rec.ID, rec.Latest)
		if err != nil {
			return err
		}
		out, found = Resolution{Binding: b, Version: v}, true
		return nil
	})
	return out, found, err
}

func (s *Service) read(ctx context.Context, fn func(context.Context, Store) error) error {
	if _, err := s.authorize(ctx, PermWorkflowsRead); err != nil {
		return err
	}
	return s.tx.InTenant(ctx, fn)
}

// readProject is read for something addressed under project; uuid.Nil
// (the tenant's own scope) checks no project.
func (s *Service) readProject(ctx context.Context, project uuid.UUID, fn func(context.Context, Store) error) error {
	if _, err := s.authorize(ctx, PermWorkflowsRead); err != nil {
		return err
	}
	if project != uuid.Nil {
		if err := s.project(ctx, project); err != nil {
			return err
		}
	}
	return s.tx.InTenant(ctx, fn)
}

// project checks project is one the caller may address and is this
// tenant's, through Catalog. Scope comes first, so a project outside the
// caller's scope answers exactly as one that doesn't exist (RFC 0006
// §4.1) without asking Catalog anything.
func (s *Service) project(ctx context.Context, project uuid.UUID) error {
	if project == uuid.Nil {
		return ErrProjectNotFound
	}
	if err := authz.InProject(ctx, project); err != nil {
		return err
	}
	if s.catalog == nil {
		return nil
	}
	return s.catalog.Project(ctx, project)
}

// createScope is writeScope for a definition that doesn't exist yet: the
// project it names is also checked against Catalog, because nothing has
// loaded it. It runs before the transaction, as s.project must.
func (s *Service) createScope(ctx context.Context, project uuid.UUID) error {
	if project == uuid.Nil {
		return authz.RequireUnscoped(ctx, PermWorkflowsManage)
	}
	return s.project(ctx, project)
}

// readScope answers a project's definitions outside the caller's
// project scope as not found (RFC 0006 §4.1); the tenant's own
// (uuid.Nil) are everyone's to read. It checks a row already loaded, so
// it asks only authz, never Catalog, and is safe inside a transaction.
func readScope(ctx context.Context, project uuid.UUID) error {
	if project == uuid.Nil {
		return nil
	}
	return authz.InProject(ctx, project)
}

// writeScope is readScope for a change: the tenant's definitions apply
// to every project, so nobody limited to some changes them.
func writeScope(ctx context.Context, project uuid.UUID) error {
	if project == uuid.Nil {
		return authz.RequireUnscoped(ctx, PermWorkflowsManage)
	}
	return authz.InProject(ctx, project)
}

// authorize checks perm and returns who is acting, as stored in
// created_by.
func (s *Service) authorize(ctx context.Context, perm authz.Permission) (string, error) {
	if err := authz.Require(ctx, perm); err != nil {
		return "", err
	}
	p, err := authz.Authenticated(ctx)
	if err != nil {
		return "", err
	}
	return p.Actor.String(), nil
}

// IsInvalid reports whether err is a refused definition or binding —
// what the API answers with 422.
func IsInvalid(err error) bool {
	return errors.Is(err, domain.ErrInvalidWorkflow) || errors.Is(err, domain.ErrInvalidBinding) ||
		errors.Is(err, domain.ErrRenamed) || errors.Is(err, domain.ErrSubjectChanged)
}
