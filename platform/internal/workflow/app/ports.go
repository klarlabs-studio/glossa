// Package app is Workflow's application layer for definitions and
// bindings (RFC 0006 §2.3): saving a definition as an immutable version
// after it compiles and lints, binding definitions to projects, and
// resolving which definition a subject runs under.
//
// The instance runner and the HTTP API are wave 2 (RFC 0006 §13); they
// call this service.
package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

var (
	// ErrNotFound is a definition, version or binding this tenant does
	// not have.
	ErrNotFound = errors.New("workflow: not found")
	// ErrConflict is a save that lost to a concurrent one, a name
	// already taken in its scope, or a binding whose selector another
	// binding already has.
	ErrConflict = errors.New("workflow: conflict")
	// ErrLimit is a count limit of RFC 0006 §9.6 reached: a tenant at
	// its limit of live definitions, or an assignee at its limit of open
	// assignments.
	ErrLimit = errors.New("workflow: limit reached")
	// ErrOutOfScope is a project-scoped definition bound to another
	// project.
	ErrOutOfScope = errors.New("workflow: the definition belongs to another project")
	// ErrUnknownPermission is an actor_has_permission guard naming a
	// permission Identity does not have.
	ErrUnknownPermission = errors.New("workflow: unknown permission")
	// ErrProjectNotFound is a project this tenant does not have.
	ErrProjectNotFound = errors.New("workflow: project not found")
	// ErrInstancesUnavailable is a read of instances on a server that
	// wires no instance store.
	ErrInstancesUnavailable = errors.New("workflow: instances are not available on this server")
	// ErrInvalidQuery is an instance filter that cannot be answered.
	ErrInvalidQuery = errors.New("workflow: invalid query")
)

// Transactor runs fn in one tenant transaction.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// Store is Workflow's persistence inside a tenant transaction.
type Store interface {
	// InsertDefinition stores a new definition at version 1; a live
	// definition with the same name in the same scope is ErrConflict.
	InsertDefinition(ctx context.Context, d domain.DefinitionRecord) error
	GetDefinition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error)
	// LockDefinition reads a live definition and locks it until the
	// transaction ends.
	LockDefinition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error)
	// ListDefinitions lists the live definitions project may bind, or
	// all live ones when project is uuid.Nil.
	ListDefinitions(ctx context.Context, project uuid.UUID) ([]domain.DefinitionRecord, error)
	CountLiveDefinitions(ctx context.Context) (int, error)
	// SetLatest moves a definition's latest version from n-1 to n.
	SetLatest(ctx context.Context, id uuid.UUID, n int) error
	MarkDeleted(ctx context.Context, d domain.DefinitionRecord) error

	// InsertVersion appends a version; a number already taken is
	// ErrConflict.
	InsertVersion(ctx context.Context, v domain.Version) error
	GetVersion(ctx context.Context, definition uuid.UUID, n int) (domain.Version, error)
	ListVersions(ctx context.Context, definition uuid.UUID) ([]domain.Version, error)

	// InsertBinding stores b and returns it with its Position; a
	// binding with the same selector is ErrConflict.
	InsertBinding(ctx context.Context, b domain.Binding) (domain.Binding, error)
	GetBinding(ctx context.Context, id uuid.UUID) (domain.Binding, error)
	// ListBindings lists project's bindings for subject ("" for all), in
	// creation order.
	ListBindings(ctx context.Context, project uuid.UUID, subject domain.SubjectKind) ([]domain.Binding, error)
	DeleteBinding(ctx context.Context, id uuid.UUID) error
	// DeleteBindingsOf removes a definition's bindings and says how
	// many there were.
	DeleteBindingsOf(ctx context.Context, definition uuid.UUID) (int, error)

	// Publish records a domain event in this transaction (outbox).
	Publish(ctx context.Context, e outbox.Event) error
}

// Catalog is Catalog's application port, as Workflow uses it: Workflow
// keys rows by Catalog's project id without a foreign key (migration
// 0040) and learns about projects, and resolves message keys, only
// through here.
type Catalog interface {
	// Project answers ErrProjectNotFound for a project this tenant does
	// not have.
	Project(ctx context.Context, project uuid.UUID) error
	// MessageID resolves a message key; ErrNotFound for a key the
	// project does not have, ErrProjectNotFound for the project.
	MessageID(ctx context.Context, project uuid.UUID, key string) (uuid.UUID, error)
}

// Permissions answers whether a permission exists, so a definition's
// actor_has_permission guard cannot name one Identity does not have.
// Identity owns the list (and the M5 identity slice grows it), so it is
// asked rather than copied.
type Permissions interface {
	Known(permission string) bool
}
