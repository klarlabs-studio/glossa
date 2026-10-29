package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

func errQuery(format string, args ...any) error {
	return fmt.Errorf("%w: %s", app.ErrInvalidQuery, fmt.Sprintf(format, args...))
}

func toInstance(v app.InstanceView) apiv1.WorkflowInstance {
	out := apiv1.WorkflowInstance{
		Id: v.ID.String(), ProjectId: v.Project.String(), DefinitionId: v.Definition.String(), DefinitionVersion: v.Version,
		Subject: apiv1.WorkflowSubject(v.Kind), SubjectId: v.SubjectID.String(), State: v.State,
		Status: apiv1.WorkflowInstanceStatus(v.Status), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}
	if v.Locale != "" {
		out.Locale = apiconv.Ptr(v.Locale)
	}
	return out
}

func toTransition(t app.TransitionView) apiv1.WorkflowTransition {
	out := apiv1.WorkflowTransition{
		Seq: t.Seq, From: t.From, Event: t.Event, To: t.To, Outcome: apiv1.WorkflowTransitionOutcome(t.Outcome),
		Guards:  make([]apiv1.WorkflowGuardOutcome, len(t.Guards)),
		Actions: make([]apiv1.WorkflowActionOutcome, len(t.Actions)),
		Actor:   t.Actor, At: t.At,
	}
	for i, g := range t.Guards {
		out.Guards[i] = apiv1.WorkflowGuardOutcome{Guard: g.Guard, Passed: g.Passed}
	}
	for i, a := range t.Actions {
		out.Actions[i] = apiv1.WorkflowActionOutcome{Name: a.Action, Outcome: apiv1.WorkflowActionOutcomeOutcome(a.Outcome)}
		if a.Detail != "" {
			out.Actions[i].Detail = apiconv.Ptr(a.Detail)
		}
	}
	if t.OutboxEventID != uuid.Nil {
		out.OutboxEventId = apiconv.Ptr(t.OutboxEventID.String())
	}
	return out
}

// optionalFilterID reads an id filter; a malformed one is a 400, since a
// filter names no resource that could be "not found".
func optionalFilterID(name string, s *string) (uuid.UUID, error) {
	if s == nil || *s == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return uuid.Nil, mapError(errQuery("%s %q is not an id", name, *s), nil)
	}
	return id, nil
}

// ListWorkflowInstances pages the project's instances. The cursor is the
// instance store's own; the API only wraps it as a page token.
func (a *API) ListWorkflowInstances(ctx context.Context, req apiv1.ListWorkflowInstancesRequestObject) (apiv1.ListWorkflowInstancesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	q := app.InstanceQuery{
		InstanceFilter: app.InstanceFilter{Status: string(deref(req.Params.Status)), After: page.After, Limit: page.Size},
		MessageKey:     deref(req.Params.Message),
	}
	if q.Definition, err = optionalFilterID("definition", req.Params.Definition); err != nil {
		return nil, err
	}
	if q.SubjectID, err = optionalFilterID("subject_id", req.Params.SubjectId); err != nil {
		return nil, err
	}
	if l := deref(req.Params.Locale); l != "" {
		tags, err := domain.CanonicalLocales([]string{l})
		if err != nil {
			return nil, mapError(errQuery("locale %q is not a BCP 47 tag", l), nil)
		}
		q.Locale = tags[0]
	}
	items, next, err := a.instances.List(ctx, project, q)
	if err != nil {
		return nil, mapError(err, nil)
	}
	out := apiv1.ListWorkflowInstances200JSONResponse{Items: make([]apiv1.WorkflowInstance, len(items)), NextPageToken: pagination.Token(next)}
	for i, v := range items {
		out.Items[i] = toInstance(v)
	}
	return out, nil
}

// GetWorkflowInstance reads one of the project's instances.
func (a *API) GetWorkflowInstance(ctx context.Context, req apiv1.GetWorkflowInstanceRequestObject) (apiv1.GetWorkflowInstanceResponseObject, error) {
	project, id, err := instanceAddress(req.Project, req.WorkflowInstance)
	if err != nil {
		return nil, err
	}
	v, err := a.instances.Get(ctx, project, id)
	if err != nil {
		return nil, mapError(err, nil)
	}
	// The ETag is the version the instance runs on: what a rebase's
	// If-Match names (RFC 0006 §2.3).
	return apiv1.GetWorkflowInstance200JSONResponse{Body: toInstance(v),
		Headers: apiv1.GetWorkflowInstance200ResponseHeaders{ETag: apiconv.ETag(v.Version)}}, nil
}

// Rebaser moves a running instance to a newer version of its definition:
// the instance runner, which owns every write to an instance.
type Rebaser interface {
	Rebase(ctx context.Context, in app.RebaseInput) (app.InstanceView, error)
}

// WithRebase serves rebaseWorkflowInstance through r; without one it
// answers workflow_instances_unavailable, as the reads do without a
// store.
func (a *API) WithRebase(r Rebaser) *API {
	a.rebase = r
	return a
}

// RebaseWorkflowInstance moves one of the project's running instances
// to a newer version of its definition, if its If-Match is still the
// version it runs on.
func (a *API) RebaseWorkflowInstance(ctx context.Context, req apiv1.RebaseWorkflowInstanceRequestObject) (apiv1.RebaseWorkflowInstanceResponseObject, error) {
	project, id, err := instanceAddress(req.Project, req.WorkflowInstance)
	if err != nil {
		return nil, err
	}
	ifVersion, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if a.rebase == nil {
		return nil, mapError(app.ErrInstancesUnavailable, nil)
	}
	in := app.RebaseInput{Project: project, Instance: id, IfVersion: ifVersion}
	if req.Body != nil && req.Body.Version != nil {
		in.Version = *req.Body.Version
	}
	v, err := a.rebase.Rebase(ctx, in)
	if err != nil {
		return nil, mapError(err, problem.New(http.StatusPreconditionFailed, problem.CodePreconditionFailed, detailStaleInstanceVersion))
	}
	return apiv1.RebaseWorkflowInstance200JSONResponse{Body: toInstance(v),
		Headers: apiv1.RebaseWorkflowInstance200ResponseHeaders{ETag: apiconv.ETag(v.Version)}}, nil
}

// ListWorkflowTransitions pages an instance's transition log, oldest
// first.
func (a *API) ListWorkflowTransitions(ctx context.Context, req apiv1.ListWorkflowTransitionsRequestObject) (apiv1.ListWorkflowTransitionsResponseObject, error) {
	project, id, err := instanceAddress(req.Project, req.WorkflowInstance)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ts, err := a.instances.Transitions(ctx, project, id)
	if err != nil {
		return nil, mapError(err, nil)
	}
	items, next := paged(ts, page, func(t app.TransitionView) string { return ordinal(t.Seq) })
	out := apiv1.ListWorkflowTransitions200JSONResponse{Items: make([]apiv1.WorkflowTransition, len(items)), NextPageToken: next}
	for i, t := range items {
		out.Items[i] = toTransition(t)
	}
	return out, nil
}

func instanceAddress(project, instance string) (uuid.UUID, uuid.UUID, error) {
	p, err := projectID(project)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := pathID(instance, app.ErrNotFound)
	return p, id, err
}
