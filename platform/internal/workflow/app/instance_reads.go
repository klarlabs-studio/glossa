package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// Instances is the read side of workflow instances as the API serves it
// (RFC 0006 §2.5, §8): InstanceQueries behind `workflows.read`, scoped to
// the project in the request's path. The instance runner owns the
// writes and implements InstanceQueries; this type only checks who may
// read, and what.
type Instances struct {
	q       InstanceQueries
	catalog Catalog
}

// NewInstances returns the read side over q. q may be nil — a server
// built without an instance store — and every read then answers
// ErrInstancesUnavailable rather than an empty list, which would read as
// "nothing is in flight" when nobody looked. catalog may be nil, and
// then projects are not checked and a message key cannot be resolved.
func NewInstances(q InstanceQueries, catalog Catalog) *Instances {
	return &Instances{q: q, catalog: catalog}
}

// InstanceQuery is a list request: the filter, plus a message by key,
// which is how every other surface addresses a message.
type InstanceQuery struct {
	InstanceFilter
	// MessageKey narrows to one message's translation units. It and
	// SubjectID are exclusive.
	MessageKey string
}

// List pages project's instances, filtered.
func (r *Instances) List(ctx context.Context, project uuid.UUID, q InstanceQuery) ([]InstanceView, string, error) {
	if err := r.authorize(ctx, project); err != nil {
		return nil, "", err
	}
	switch q.Status {
	case "", InstanceActive, InstanceFinished:
	default:
		return nil, "", fmt.Errorf("%w: status %q is not %s or %s", ErrInvalidQuery, q.Status, InstanceActive, InstanceFinished)
	}
	f := q.InstanceFilter
	f.Project = project
	if q.MessageKey != "" {
		if f.SubjectID != uuid.Nil {
			return nil, "", fmt.Errorf("%w: name a message or a subject, not both", ErrInvalidQuery)
		}
		if r.catalog == nil {
			return nil, "", fmt.Errorf("%w: messages cannot be resolved on this server", ErrInvalidQuery)
		}
		id, err := r.catalog.MessageID(ctx, project, q.MessageKey)
		if errors.Is(err, ErrNotFound) {
			// A key the project does not have has no instances: an empty
			// page, as for any filter that matches nothing.
			return []InstanceView{}, "", nil
		}
		if err != nil {
			return nil, "", err
		}
		f.SubjectID = id
	}
	return r.q.ListInstances(ctx, f)
}

// Get reads one of project's instances.
func (r *Instances) Get(ctx context.Context, project, id uuid.UUID) (InstanceView, error) {
	if err := r.authorize(ctx, project); err != nil {
		return InstanceView{}, err
	}
	return r.get(ctx, project, id)
}

// Transitions reads one of project's instances' transition log.
func (r *Instances) Transitions(ctx context.Context, project, id uuid.UUID) ([]TransitionView, error) {
	if err := r.authorize(ctx, project); err != nil {
		return nil, err
	}
	if _, err := r.get(ctx, project, id); err != nil {
		return nil, err
	}
	return r.q.ListTransitions(ctx, id)
}

// get reads an instance and refuses one of another project: the project
// in the path is part of the instance's address.
func (r *Instances) get(ctx context.Context, project, id uuid.UUID) (InstanceView, error) {
	inst, err := r.q.GetInstance(ctx, id)
	if err != nil {
		return InstanceView{}, err
	}
	if inst.Project != project {
		return InstanceView{}, ErrNotFound
	}
	return inst, nil
}

func (r *Instances) authorize(ctx context.Context, project uuid.UUID) error {
	if project == uuid.Nil {
		return ErrProjectNotFound
	}
	// RequireIn, not Require: a project outside the caller's scope
	// answers as one that doesn't exist (RFC 0006 §4.1), and an
	// `assigned` member is refused instances as every list.
	if err := authz.RequireIn(ctx, PermWorkflowsRead, project); err != nil {
		return err
	}
	if r.catalog != nil {
		if err := r.catalog.Project(ctx, project); err != nil {
			return err
		}
	}
	if r.q == nil {
		return ErrInstancesUnavailable
	}
	return nil
}
