// Package httpapi is the Workflow context's HTTP edge (RFC 0006 §8): its
// operations of the generated /v1 strict server — definitions with
// their immutable versions and lint, a project's bindings and which one
// resolves for a subject, and the read side of instances and their
// transition logs. The composition root embeds API next to the other
// contexts' handlers; Identity's Guard has authenticated the caller and
// resolved the tenant before any of these run, and the application layer
// checks `workflows.read` and `workflows.manage`.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// API serves Workflow's operations.
type API struct {
	svc       *app.Service
	instances *app.Instances
	work      *app.WorkService
	catalog   app.Catalog
	rebase    Rebaser
}

// New returns the API. instances is the instance runner's read side; nil
// makes the instance operations answer `workflow_instances_unavailable`.
// catalog checks projects and resolves message keys for the instance
// reads, assignments and approvals (the service has its own, set with
// app.WithCatalog). work is assignments and approvals (RFC 0006 §3).
func New(svc *app.Service, instances app.InstanceQueries, catalog app.Catalog, work *app.WorkService) *API {
	return &API{svc: svc, instances: app.NewInstances(instances, catalog), work: work, catalog: catalog}
}

func tenantPath(ctx context.Context, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + sub
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// pathID parses an id in the path or a query; a malformed one is not
// found, as an id this tenant does not have.
func pathID(s string, missing error) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(missing, nil)
	}
	return id, nil
}

func projectID(s string) (uuid.UUID, error) { return pathID(s, app.ErrProjectNotFound) }

// optionalProject reads an optional `project` query parameter.
func optionalProject(s *string) (uuid.UUID, error) {
	if s == nil || *s == "" {
		return uuid.Nil, nil
	}
	return projectID(*s)
}

// document is the request body as the bytes the domain compiles.
func document(body *apiv1.WorkflowDocument) ([]byte, error) {
	if body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is a glossa.workflow/v1 document")
	}
	return json.Marshal(*body)
}

// paged pages items in the order the API lists them, by a key that
// sorts the same way: a page starts after the key its token names, so a
// row removed between pages shifts nothing. The lists it pages are
// small and bounded (RFC 0006 §9.6), so they are read whole and paged
// here rather than in SQL.
func paged[T any](items []T, page pagination.Page, key func(T) string) ([]T, *string) {
	sorted := slices.SortedFunc(slices.Values(items), func(a, b T) int { return strings.Compare(key(a), key(b)) })
	start := 0
	if page.After != "" {
		start = len(sorted)
		for i, t := range sorted {
			if key(t) > page.After {
				start = i
				break
			}
		}
	}
	return pagination.Trim(sorted[start:], page, key)
}

// ordinal is a sort key for a non-negative number.
func ordinal(n int64) string { return fmt.Sprintf("%020d", n) }

// ── definitions ─────────────────────────────────────────────────────

func optionalID(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return apiconv.Ptr(id.String())
}

func toDefinition(d domain.DefinitionRecord) apiv1.WorkflowDefinition {
	return apiv1.WorkflowDefinition{
		Id: d.ID.String(), ProjectId: optionalID(d.ProjectID), Name: d.Name, Subject: apiv1.WorkflowSubject(d.Subject),
		Version: d.Latest, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, DeletedAt: d.DeletedAt,
	}
}

func toSaved(s app.Saved) apiv1.WorkflowDefinitionSaved {
	return apiv1.WorkflowDefinitionSaved{
		Id: s.Definition.ID.String(), ProjectId: optionalID(s.Definition.ProjectID), Name: s.Definition.Name,
		Subject: apiv1.WorkflowSubject(s.Definition.Subject), Version: s.Version.Number,
		CreatedBy: s.Version.CreatedBy, CreatedAt: s.Version.CreatedAt, Findings: toFindings(s.Findings),
	}
}

func toFindings(fs []domain.Finding) []apiv1.WorkflowFinding {
	out := make([]apiv1.WorkflowFinding, len(fs))
	for i, f := range fs {
		out[i] = apiv1.WorkflowFinding{Rule: f.Rule, Severity: apiv1.WorkflowFindingSeverity(f.Severity), Message: f.Message}
		if f.Path != "" {
			out[i].Path = apiconv.Ptr(f.Path)
		}
		if f.State != "" {
			out[i].State = apiconv.Ptr(f.State)
		}
		if f.Event != "" {
			out[i].Event = apiconv.Ptr(f.Event)
		}
	}
	return out
}

func toVersion(v domain.Version) (apiv1.WorkflowDefinitionVersion, error) {
	var doc apiv1.WorkflowDocument
	if err := json.Unmarshal(v.Document, &doc); err != nil {
		return apiv1.WorkflowDefinitionVersion{}, fmt.Errorf("workflow: stored version %s/%d: %w", v.DefinitionID, v.Number, err)
	}
	return apiv1.WorkflowDefinitionVersion{
		DefinitionId: v.DefinitionID.String(), Version: v.Number, Schema: v.Schema, Document: doc,
		CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
	}, nil
}

func definitionPath(ctx context.Context, id uuid.UUID) string {
	return tenantPath(ctx, "/workflow-definitions/"+id.String())
}

func versionPath(ctx context.Context, id uuid.UUID, n int) string {
	return fmt.Sprintf("%s/versions/%d", definitionPath(ctx, id), n)
}

// ListWorkflowDefinitions lists the live definitions, by name.
func (a *API) ListWorkflowDefinitions(ctx context.Context, req apiv1.ListWorkflowDefinitionsRequestObject) (apiv1.ListWorkflowDefinitionsResponseObject, error) {
	project, err := optionalProject(req.Params.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	defs, err := a.svc.Definitions(ctx, project)
	if err != nil {
		return nil, mapError(err, nil)
	}
	items, next := paged(defs, page, func(d domain.DefinitionRecord) string { return d.Name + "/" + d.ID.String() })
	out := apiv1.ListWorkflowDefinitions200JSONResponse{Items: make([]apiv1.WorkflowDefinition, len(items)), NextPageToken: next}
	for i, d := range items {
		out.Items[i] = toDefinition(d)
	}
	return out, nil
}

// CreateWorkflowDefinition compiles, lints and stores a document as
// version 1 of a new definition.
func (a *API) CreateWorkflowDefinition(ctx context.Context, req apiv1.CreateWorkflowDefinitionRequestObject) (apiv1.CreateWorkflowDefinitionResponseObject, error) {
	project, err := optionalProject(req.Params.Project)
	if err != nil {
		return nil, err
	}
	doc, err := document(req.Body)
	if err != nil {
		return nil, err
	}
	saved, err := a.svc.CreateDefinition(ctx, app.NewDefinition{ProjectID: project, Document: doc})
	if fs, ok := invalidWorkflow(err); ok {
		return apiv1.CreateWorkflowDefinition422ApplicationProblemPlusJSONResponse{InvalidWorkflowApplicationProblemPlusJSONResponse: workflowProblem(fs)}, nil
	}
	if err != nil {
		return nil, mapError(err, problem.New(http.StatusConflict, codeDefinitionExists,
			"a live definition with this name exists in this scope; save a new version of it instead"))
	}
	return apiv1.CreateWorkflowDefinition201JSONResponse{Body: toSaved(saved), Headers: apiv1.CreateWorkflowDefinition201ResponseHeaders{
		ETag: apiconv.ETag(saved.Definition.Latest), Location: apiconv.Ptr(definitionPath(ctx, saved.Definition.ID)),
	}}, nil
}

// GetWorkflowDefinition reads a definition; its ETag is its latest
// version.
func (a *API) GetWorkflowDefinition(ctx context.Context, req apiv1.GetWorkflowDefinitionRequestObject) (apiv1.GetWorkflowDefinitionResponseObject, error) {
	id, err := pathID(req.WorkflowDefinition, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	d, err := a.svc.Definition(ctx, id)
	if err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.GetWorkflowDefinition200JSONResponse{Body: toDefinition(d),
		Headers: apiv1.GetWorkflowDefinition200ResponseHeaders{ETag: apiconv.ETag(d.Latest)}}, nil
}

// DeleteWorkflowDefinition deletes a definition and its bindings.
func (a *API) DeleteWorkflowDefinition(ctx context.Context, req apiv1.DeleteWorkflowDefinitionRequestObject) (apiv1.DeleteWorkflowDefinitionResponseObject, error) {
	id, err := pathID(req.WorkflowDefinition, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteDefinition(ctx, id); err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.DeleteWorkflowDefinition204Response{}, nil
}

// ListWorkflowDefinitionVersions lists a definition's versions, newest
// first.
func (a *API) ListWorkflowDefinitionVersions(ctx context.Context, req apiv1.ListWorkflowDefinitionVersionsRequestObject) (apiv1.ListWorkflowDefinitionVersionsResponseObject, error) {
	id, err := pathID(req.WorkflowDefinition, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	vs, err := a.svc.Versions(ctx, id)
	if err != nil {
		return nil, mapError(err, nil)
	}
	// Newest first: the key counts down from the largest version.
	items, next := paged(vs, page, func(v domain.Version) string { return ordinal(int64(1<<31 - v.Number)) })
	out := apiv1.ListWorkflowDefinitionVersions200JSONResponse{Items: make([]apiv1.WorkflowDefinitionVersion, len(items)), NextPageToken: next}
	for i, v := range items {
		if out.Items[i], err = toVersion(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SaveWorkflowDefinitionVersion appends the next version, if the
// author's If-Match is still the latest.
func (a *API) SaveWorkflowDefinitionVersion(ctx context.Context, req apiv1.SaveWorkflowDefinitionVersionRequestObject) (apiv1.SaveWorkflowDefinitionVersionResponseObject, error) {
	id, err := pathID(req.WorkflowDefinition, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	ifLatest, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	doc, err := document(req.Body)
	if err != nil {
		return nil, err
	}
	saved, err := a.svc.SaveVersion(ctx, id, ifLatest, doc)
	if fs, ok := invalidWorkflow(err); ok {
		return apiv1.SaveWorkflowDefinitionVersion422ApplicationProblemPlusJSONResponse{InvalidWorkflowApplicationProblemPlusJSONResponse: workflowProblem(fs)}, nil
	}
	if err != nil {
		return nil, mapError(err, problem.New(http.StatusPreconditionFailed, problem.CodePreconditionFailed, detailStaleDefinitionVersion))
	}
	return apiv1.SaveWorkflowDefinitionVersion201JSONResponse{Body: toSaved(saved), Headers: apiv1.SaveWorkflowDefinitionVersion201ResponseHeaders{
		ETag: apiconv.ETag(saved.Definition.Latest), Location: apiconv.Ptr(versionPath(ctx, id, saved.Version.Number)),
	}}, nil
}

// GetWorkflowDefinitionVersion reads one version with its document.
func (a *API) GetWorkflowDefinitionVersion(ctx context.Context, req apiv1.GetWorkflowDefinitionVersionRequestObject) (apiv1.GetWorkflowDefinitionVersionResponseObject, error) {
	id, err := pathID(req.WorkflowDefinition, app.ErrNotFound)
	if err != nil {
		return nil, err
	}
	if req.Version < 1 {
		return nil, mapError(app.ErrNotFound, nil)
	}
	v, err := a.svc.Version(ctx, id, req.Version)
	if err != nil {
		return nil, mapError(err, nil)
	}
	out, err := toVersion(v)
	if err != nil {
		return nil, err
	}
	return apiv1.GetWorkflowDefinitionVersion200JSONResponse(out), nil
}

// LintWorkflowDefinition compiles and lints a document and stores
// nothing: a refused document is a 200 with valid false.
func (a *API) LintWorkflowDefinition(ctx context.Context, req apiv1.LintWorkflowDefinitionRequestObject) (apiv1.LintWorkflowDefinitionResponseObject, error) {
	doc, err := document(req.Body)
	if err != nil {
		return nil, err
	}
	fs, err := a.svc.Lint(ctx, doc)
	if refused, ok := invalidWorkflow(err); ok {
		return apiv1.LintWorkflowDefinition200JSONResponse{Valid: false, Findings: toFindings(refused)}, nil
	}
	if err != nil {
		return nil, mapError(err, nil)
	}
	return apiv1.LintWorkflowDefinition200JSONResponse{Valid: true, Findings: toFindings(fs)}, nil
}
