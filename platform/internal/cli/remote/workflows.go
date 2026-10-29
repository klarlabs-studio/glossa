package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Workflow and assignment types as the Workflow API serves them (RFC
// 0006 §2–§3), re-exported so commands don't import the generated
// package.
type (
	WorkflowDefinition        = apiclient.WorkflowDefinition
	WorkflowDefinitionSaved   = apiclient.WorkflowDefinitionSaved
	WorkflowDefinitionVersion = apiclient.WorkflowDefinitionVersion
	WorkflowFinding           = apiclient.WorkflowFinding
	WorkflowLintResult        = apiclient.WorkflowLintResult
	WorkflowBinding           = apiclient.WorkflowBinding
	WorkflowInstance          = apiclient.WorkflowInstance
	WorkflowTransition        = apiclient.WorkflowTransition
	Assignment                = apiclient.Assignment
	AssignmentReport          = apiclient.AssignmentReport
	AssignmentReportRow       = apiclient.AssignmentReportRow
	Party                     = apiclient.Party
	Role                      = apiclient.Role
)

// InvalidWorkflowError is a save the server refused because the
// document does not compile or lint (422 invalid_workflow): the problem,
// with every finding.
type InvalidWorkflowError struct {
	*APIError
	Findings []WorkflowFinding
}

func (e *InvalidWorkflowError) Unwrap() error { return e.APIError }

// invalidWorkflow attaches a 422's findings to err.
func invalidWorkflow(err error, body []byte) error {
	ae, ok := err.(*APIError)
	if !ok || ae.Status != http.StatusUnprocessableEntity || ae.Code != "invalid_workflow" {
		return err
	}
	var p apiclient.WorkflowProblem
	_ = json.Unmarshal(body, &p)
	out := &InvalidWorkflowError{APIError: ae}
	if p.Findings != nil {
		out.Findings = *p.Findings
	}
	return out
}

// ── definitions ─────────────────────────────────────────────────────

// WorkflowDefinitions lists the live definitions, every page: with
// project, the ones that project may bind (the tenant's and its own).
func (c *Client) WorkflowDefinitions(ctx context.Context, tenant, project string) ([]WorkflowDefinition, error) {
	size := pageSize
	return collect(func(tok *string) ([]WorkflowDefinition, *string, error) {
		r, err := c.api.ListWorkflowDefinitionsWithResponse(ctx, tenant,
			&apiclient.ListWorkflowDefinitionsParams{PageSize: &size, PageToken: tok, Project: optional(project)})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/workflow-definitions")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// WorkflowDefinition reads a definition and its ETag (its latest
// version, for If-Match on the next save).
func (c *Client) WorkflowDefinition(ctx context.Context, tenant, id string) (WorkflowDefinition, string, error) {
	r, err := c.api.GetWorkflowDefinitionWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/workflow-definitions/%s", id)); err != nil {
		return WorkflowDefinition{}, "", err
	}
	return *r.JSON200, r.HTTPResponse.Header.Get("ETag"), nil
}

// WorkflowDefinitionVersions lists a definition's versions, newest
// first, with their documents.
func (c *Client) WorkflowDefinitionVersions(ctx context.Context, tenant, id string) ([]WorkflowDefinitionVersion, error) {
	size := pageSize
	return collect(func(tok *string) ([]WorkflowDefinitionVersion, *string, error) {
		r, err := c.api.ListWorkflowDefinitionVersionsWithResponse(ctx, tenant, id,
			&apiclient.ListWorkflowDefinitionVersionsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/workflow-definitions/%s/versions", id)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// WorkflowDefinitionVersion reads one version with its document.
func (c *Client) WorkflowDefinitionVersion(ctx context.Context, tenant, id string, version int) (WorkflowDefinitionVersion, error) {
	r, err := c.api.GetWorkflowDefinitionVersionWithResponse(ctx, tenant, id, version)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/workflow-definitions/%s/versions/%s", id, strconv.Itoa(version))); err != nil {
		return WorkflowDefinitionVersion{}, err
	}
	return *r.JSON200, nil
}

// LintWorkflow compiles and lints a document without saving it. An
// invalid document is a 200 with valid false.
func (c *Client) LintWorkflow(ctx context.Context, tenant string, doc map[string]any) (WorkflowLintResult, error) {
	r, err := c.api.LintWorkflowDefinitionWithResponse(idempotent(ctx), tenant, doc)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/workflow-definition-lints")); err != nil {
		return WorkflowLintResult{}, err
	}
	return *r.JSON200, nil
}

// CreateWorkflowDefinition stores a document as version 1 of a new
// definition: the project's own when project is set, else the
// tenant's. Not retried: a retry of a create that landed answers 409.
func (c *Client) CreateWorkflowDefinition(ctx context.Context, tenant, project string, doc map[string]any) (WorkflowDefinitionSaved, error) {
	r, err := c.api.CreateWorkflowDefinitionWithResponse(ctx, tenant,
		&apiclient.CreateWorkflowDefinitionParams{Project: optional(project)}, doc)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/workflow-definitions")); err != nil {
		return WorkflowDefinitionSaved{}, invalidWorkflow(err, rawBody(r))
	}
	return *r.JSON201, nil
}

// SaveWorkflowDefinitionVersion stores the next version if ifMatch (an
// ETag) is still the latest; a save another overtook is 412.
func (c *Client) SaveWorkflowDefinitionVersion(ctx context.Context, tenant, id, ifMatch string, doc map[string]any) (WorkflowDefinitionSaved, error) {
	r, err := c.api.SaveWorkflowDefinitionVersionWithResponse(ctx, tenant, id,
		&apiclient.SaveWorkflowDefinitionVersionParams{IfMatch: ifMatch}, doc)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/workflow-definitions/%s/versions", id)); err != nil {
		return WorkflowDefinitionSaved{}, invalidWorkflow(err, rawBody(r))
	}
	return *r.JSON201, nil
}

// ── bindings ────────────────────────────────────────────────────────

// WorkflowBindings lists a project's bindings in creation order.
func (c *Client) WorkflowBindings(ctx context.Context, s Scope) ([]WorkflowBinding, error) {
	size := pageSize
	return collect(func(tok *string) ([]WorkflowBinding, *string, error) {
		r, err := c.api.ListWorkflowBindingsWithResponse(ctx, s.Tenant, s.Project,
			&apiclient.ListWorkflowBindingsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/workflow-bindings", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// BindWorkflow binds a definition to the project, optionally narrowed
// to locales and one namespace.
func (c *Client) BindWorkflow(ctx context.Context, s Scope, definition string, locales []string, namespace string) (WorkflowBinding, error) {
	body := apiclient.CreateWorkflowBinding{DefinitionId: definition, Namespace: optional(namespace)}
	if len(locales) > 0 {
		body.Locales = &locales
	}
	r, err := c.api.CreateWorkflowBindingWithResponse(ctx, s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/workflow-bindings", s.Tenant, s.Project)); err != nil {
		return WorkflowBinding{}, err
	}
	return *r.JSON201, nil
}

// UnbindWorkflow removes a binding.
func (c *Client) UnbindWorkflow(ctx context.Context, s Scope, id string) error {
	r, err := c.api.DeleteWorkflowBindingWithResponse(ctx, s.Tenant, s.Project, id)
	return check(r, err, http.MethodDelete, c.path("/v1/tenants/%s/projects/%s/workflow-bindings/%s", s.Tenant, s.Project, id))
}

// ── instances ───────────────────────────────────────────────────────

// InstanceFilter narrows WorkflowInstances; an empty field doesn't.
type InstanceFilter struct {
	Definition, Status, Locale, Message string
}

// WorkflowInstances lists a project's instances, every page.
func (c *Client) WorkflowInstances(ctx context.Context, s Scope, f InstanceFilter) ([]WorkflowInstance, error) {
	size := pageSize
	params := apiclient.ListWorkflowInstancesParams{PageSize: &size, Definition: optional(f.Definition),
		Locale: optional(f.Locale), Message: optional(f.Message)}
	if f.Status != "" {
		st := apiclient.WorkflowInstanceStatus(f.Status)
		params.Status = &st
	}
	return collect(func(tok *string) ([]WorkflowInstance, *string, error) {
		p := params
		p.PageToken = tok
		r, err := c.api.ListWorkflowInstancesWithResponse(ctx, s.Tenant, s.Project, &p)
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/workflow-instances", s.Tenant, s.Project)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// WorkflowInstance reads one instance.
func (c *Client) WorkflowInstance(ctx context.Context, s Scope, id string) (WorkflowInstance, error) {
	r, err := c.api.GetWorkflowInstanceWithResponse(ctx, s.Tenant, s.Project, id)
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/workflow-instances/%s", s.Tenant, s.Project, id)); err != nil {
		return WorkflowInstance{}, err
	}
	return *r.JSON200, nil
}

// RebaseWorkflowInstance moves a running instance to version (0: the
// definition's latest) if it still runs on fromVersion — the instance's
// ETag is that version, so a rebase another overtook is 412.
func (c *Client) RebaseWorkflowInstance(ctx context.Context, s Scope, id string, fromVersion, version int) (WorkflowInstance, error) {
	body := apiclient.RebaseWorkflowInstance{}
	if version > 0 {
		body.Version = &version
	}
	r, err := c.api.RebaseWorkflowInstanceWithResponse(ctx, s.Tenant, s.Project, id,
		&apiclient.RebaseWorkflowInstanceParams{IfMatch: strconv.Quote(strconv.Itoa(fromVersion))}, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/workflow-instances/%s/rebase", s.Tenant, s.Project, id)); err != nil {
		return WorkflowInstance{}, err
	}
	return *r.JSON200, nil
}

// WorkflowTransitions lists an instance's transition log, oldest first.
func (c *Client) WorkflowTransitions(ctx context.Context, s Scope, id string) ([]WorkflowTransition, error) {
	size := pageSize
	return collect(func(tok *string) ([]WorkflowTransition, *string, error) {
		r, err := c.api.ListWorkflowTransitionsWithResponse(ctx, s.Tenant, s.Project, id,
			&apiclient.ListWorkflowTransitionsParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/workflow-instances/%s/transitions", s.Tenant, s.Project, id)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// ── assignments ─────────────────────────────────────────────────────

// AssignmentFilter narrows Assignments; an empty field doesn't. Mine
// asks for the caller's own work only.
type AssignmentFilter struct {
	Project, Message, Locale, State string
	Mine                            bool
}

// Assignments lists the assignments the caller may see, every page.
func (c *Client) Assignments(ctx context.Context, tenant string, f AssignmentFilter) ([]Assignment, error) {
	size := pageSize
	params := apiclient.ListAssignmentsParams{PageSize: &size, Project: optional(f.Project),
		Message: optional(f.Message), Locale: optional(f.Locale)}
	if f.State != "" {
		st := apiclient.AssignmentState(f.State)
		params.State = &st
	}
	if f.Mine {
		mine := true
		params.Mine = &mine
	}
	return collect(func(tok *string) ([]Assignment, *string, error) {
		p := params
		p.PageToken = tok
		r, err := c.api.ListAssignmentsWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/assignments")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// AssignmentReportFilter narrows AssignmentReport; an empty field
// doesn't. Vendor is a vendor's id.
type AssignmentReportFilter struct {
	Project, Vendor string
	Since           *time.Time
}

// AssignmentReport reads the per-vendor quality numbers (RFC 0006 §3.4).
func (c *Client) AssignmentReport(ctx context.Context, tenant string, f AssignmentReportFilter) (AssignmentReport, error) {
	params := apiclient.GetAssignmentReportParams{Project: optional(f.Project), Vendor: optional(f.Vendor), Since: f.Since}
	r, err := c.api.GetAssignmentReportWithResponse(ctx, tenant, &params)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/assignment-reports")); err != nil {
		return AssignmentReport{}, err
	}
	return *r.JSON200, nil
}

// Assignment reads one assignment.
func (c *Client) Assignment(ctx context.Context, tenant, id string) (Assignment, error) {
	r, err := c.api.GetAssignmentWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/assignments/%s", id)); err != nil {
		return Assignment{}, err
	}
	return *r.JSON200, nil
}

// AssignmentUnitRef names a translation unit by message key and locale.
type AssignmentUnitRef struct{ Message, Locale string }

// NewAssignment is an assignment to create.
type NewAssignment struct {
	Project    string
	Units      []AssignmentUnitRef
	To         Party
	Permission string
	DueAt      *time.Time
}

// CreateAssignment gives units to someone. The Idempotency-Key makes a
// retried request create it once; replayed reports such a retry.
func (c *Client) CreateAssignment(ctx context.Context, tenant string, in NewAssignment, idempotencyKey string) (a Assignment, replayed bool, err error) {
	body := apiclient.CreateAssignment{ProjectId: in.Project, Assignee: in.To, Permission: optional(in.Permission)}
	for _, u := range in.Units {
		body.Units = append(body.Units, struct {
			Locale  apiclient.Locale     `json:"locale"`
			Message apiclient.MessageKey `json:"message"`
		}{Locale: u.Locale, Message: u.Message})
	}
	if in.DueAt != nil {
		due := in.DueAt.UTC()
		body.DueAt = &due
	}
	r, err := c.api.CreateAssignmentWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateAssignmentParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/assignments")); err != nil {
		return Assignment{}, false, err
	}
	return *r.JSON201, r.HTTPResponse.Header.Get("Idempotent-Replayed") == "true", nil
}

// AcceptAssignment takes an open assignment on.
func (c *Client) AcceptAssignment(ctx context.Context, tenant, id string) (Assignment, error) {
	r, err := c.api.AcceptAssignmentWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/assignments/%s/acceptance", id)); err != nil {
		return Assignment{}, err
	}
	return *r.JSON200, nil
}

// CompleteAssignment claims an assignment done.
func (c *Client) CompleteAssignment(ctx context.Context, tenant, id string) (Assignment, error) {
	r, err := c.api.CompleteAssignmentWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/assignments/%s/completion", id)); err != nil {
		return Assignment{}, err
	}
	return *r.JSON200, nil
}

// DeclineAssignment hands an assignment back, with an optional reason.
func (c *Client) DeclineAssignment(ctx context.Context, tenant, id, reason string) (Assignment, error) {
	r, err := c.api.DeclineAssignmentWithResponse(ctx, tenant, id, apiclient.AssignmentDecline{Reason: optional(reason)})
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/assignments/%s/decline", id)); err != nil {
		return Assignment{}, err
	}
	return *r.JSON200, nil
}
