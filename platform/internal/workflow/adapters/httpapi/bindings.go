package httpapi

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

func toBinding(b domain.Binding) apiv1.WorkflowBinding {
	out := apiv1.WorkflowBinding{
		Id: b.ID.String(), ProjectId: b.ProjectID.String(), DefinitionId: b.DefinitionID.String(),
		Subject: apiv1.WorkflowSubject(b.Subject), Locales: make([]apiv1.Locale, len(b.Locales)),
		Position: int(b.Position), CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	}
	copy(out.Locales, b.Locales)
	if b.Namespace != "" {
		out.Namespace = apiconv.Ptr(b.Namespace)
	}
	return out
}

func projectPath(ctx context.Context, project, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + "/projects/" + project + sub
}

// ListWorkflowBindings lists a project's bindings in creation order.
func (a *API) ListWorkflowBindings(ctx context.Context, req apiv1.ListWorkflowBindingsRequestObject) (apiv1.ListWorkflowBindingsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	bs, err := a.svc.Bindings(ctx, project)
	if err != nil {
		return nil, mapError(err, nil)
	}
	items, next := paged(bs, page, func(b domain.Binding) string { return ordinal(b.Position) })
	out := apiv1.ListWorkflowBindings200JSONResponse{Items: make([]apiv1.WorkflowBinding, len(items)), NextPageToken: next}
	for i, b := range items {
		out.Items[i] = toBinding(b)
	}
	return out, nil
}

// CreateWorkflowBinding binds a definition to the project.
func (a *API) CreateWorkflowBinding(ctx context.Context, req apiv1.CreateWorkflowBindingRequestObject) (apiv1.CreateWorkflowBindingResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body names a definition_id")
	}
	definition, err := pathID(req.Body.DefinitionId, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	b, err := a.svc.Bind(ctx, app.NewBinding{
		ProjectID: project, Locales: deref(req.Body.Locales), Namespace: deref(req.Body.Namespace), DefinitionID: definition,
	})
	if err != nil {
		return nil, mapError(err, problem.New(http.StatusConflict, codeBindingExists,
			"the project already has a binding with this selector; delete it first"))
	}
	return apiv1.CreateWorkflowBinding201JSONResponse{Body: toBinding(b), Headers: apiv1.CreateWorkflowBinding201ResponseHeaders{
		Location: apiconv.Ptr(projectPath(ctx, req.Project, "/workflow-bindings/"+b.ID.String())),
	}}, nil
}

// DeleteWorkflowBinding removes one of the project's bindings.
func (a *API) DeleteWorkflowBinding(ctx context.Context, req apiv1.DeleteWorkflowBindingRequestObject) (apiv1.DeleteWorkflowBindingResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.WorkflowBinding, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	if err := a.svc.Unbind(ctx, project, id); err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.DeleteWorkflowBinding204Response{}, nil
}

// ResolveWorkflow answers which binding, and which definition version, a
// new instance for a subject would start under.
func (a *API) ResolveWorkflow(ctx context.Context, req apiv1.ResolveWorkflowRequestObject) (apiv1.ResolveWorkflowResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	t := domain.Target{ProjectID: project, Subject: domain.SubjectTranslation, Namespace: deref(req.Params.Namespace)}
	if req.Params.Subject != nil {
		t.Subject = domain.SubjectKind(*req.Params.Subject)
	}
	if !t.Subject.Valid() {
		return nil, mapError(errQuery("subject %q is not one", t.Subject), nil)
	}
	if l := deref(req.Params.Locale); l != "" {
		tags, err := domain.CanonicalLocales([]string{l})
		if err != nil {
			return nil, mapError(errQuery("locale %q is not a BCP 47 tag", l), nil)
		}
		t.Locale = tags[0]
	}
	res, found, err := a.svc.Resolve(ctx, t)
	if err != nil {
		return nil, mapError(err, nil)
	}
	if !found {
		return apiv1.ResolveWorkflow200JSONResponse{Bound: false}, nil
	}
	b := toBinding(res.Binding)
	def, err := a.svc.Definition(ctx, res.Binding.DefinitionID)
	if err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.ResolveWorkflow200JSONResponse{
		Bound: true, Binding: &b, DefinitionId: apiconv.Ptr(res.Binding.DefinitionID.String()),
		DefinitionName: apiconv.Ptr(def.Name), Version: apiconv.Ptr(res.Version.Number),
	}, nil
}
