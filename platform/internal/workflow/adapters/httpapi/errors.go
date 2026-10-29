package httpapi

import (
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Problem codes Workflow adds, as documented in api/openapi.yaml. A code
// here is part of the contract: never rename one.
const (
	codeInvalidWorkflow      problem.Code = "invalid_workflow"
	codeDefinitionExists     problem.Code = "workflow_definition_exists"
	codeBindingExists        problem.Code = "workflow_binding_exists"
	codeLimitReached         problem.Code = "workflow_limit_reached"
	codeInvalidBinding       problem.Code = "invalid_workflow_binding"
	codeOutOfScope           problem.Code = "workflow_definition_out_of_scope"
	codeInstancesUnavailable problem.Code = "workflow_instances_unavailable"
	codeInvalidQuery         problem.Code = "invalid_query"
	// Rebase (RFC 0006 §2.3).
	codeInstanceFinished   problem.Code = "workflow_instance_finished"
	codeInvalidRebase      problem.Code = "invalid_workflow_rebase"
	codeRebaseStateMissing problem.Code = "workflow_rebase_state_missing"
	codeRebaseStateFinal   problem.Code = "workflow_rebase_state_final"
	// Assignments and approvals (RFC 0006 §3).
	codeInvalidAssignment  problem.Code = "invalid_assignment"
	codeInvalidApproval    problem.Code = "invalid_approval"
	codeUnknownParty       problem.Code = "unknown_party"
	codeAssignmentState    problem.Code = "assignment_state"
	codeApprovalClosed     problem.Code = "approval_closed"
	codeApprovalSuperseded problem.Code = "approval_superseded"
	codePersonRequired     problem.Code = "person_required"
	codeNotEligible        problem.Code = "not_eligible"
	codeOwnText            problem.Code = "own_text"
	// Release requests (RFC 0006 §5.1).
	codeApprovalNotRequested     problem.Code = "approval_not_requested"
	codeReleaseRequestClosed     problem.Code = "release_request_closed"
	detailInvalidWorkflow                     = "the workflow definition does not compile or lint; see findings"
	detailStaleDefinitionVersion              = "the definition has a newer version; read it and retry with its ETag"
	detailStaleInstanceVersion                = "the instance runs on another version now; read it and retry with its ETag"
)

// problems maps Workflow's errors to the codes documented in
// api/openapi.yaml. An empty detail means the error's own text.
// ErrConflict is not here: what a conflict means depends on the write
// (a name taken, a selector taken, a save overtaken), so each handler
// says.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrProjectNotFound, http.StatusNotFound, problem.CodeNotFound, "no such project"},
	{app.ErrNotFound, http.StatusNotFound, problem.CodeNotFound, "no such workflow resource"},
	{app.ErrLimit, http.StatusConflict, codeLimitReached, ""},
	{app.ErrOutOfScope, http.StatusUnprocessableEntity, codeOutOfScope, ""},
	{domain.ErrInvalidBinding, http.StatusUnprocessableEntity, codeInvalidBinding, ""},
	{app.ErrInvalidQuery, http.StatusBadRequest, codeInvalidQuery, ""},
	{domain.ErrInstanceFinished, http.StatusConflict, codeInstanceFinished, "the instance has finished; there is nothing left to rebase"},
	{domain.ErrRebaseNotNewer, http.StatusUnprocessableEntity, codeInvalidRebase, ""},
	{app.ErrRebaseVersion, http.StatusUnprocessableEntity, codeInvalidRebase, ""},
	{domain.ErrRebaseStateMissing, http.StatusUnprocessableEntity, codeRebaseStateMissing, ""},
	{domain.ErrRebaseStateFinal, http.StatusUnprocessableEntity, codeRebaseStateFinal, ""},
	// A server without an instance store says so, rather than answering
	// an empty list that would read as "nothing is in flight".
	{app.ErrInstancesUnavailable, http.StatusServiceUnavailable, codeInstancesUnavailable, ""},
	{app.ErrReportUnavailable, http.StatusServiceUnavailable, codeInstancesUnavailable, ""},
	// Who is deciding (§3.2, §9.3) — each is also authz.ErrForbidden,
	// and answered with its own code rather than the generic one.
	{domain.ErrNotHuman, http.StatusForbidden, codePersonRequired,
		"only a signed-in person can decide an approval, not an API token or an agent"},
	{domain.ErrNotEligible, http.StatusForbidden, codeNotEligible, "the approval does not ask you"},
	{domain.ErrOwnText, http.StatusForbidden, codeOwnText, "the author of the text cannot approve it"},
	{domain.ErrInvalidAssignment, http.StatusUnprocessableEntity, codeInvalidAssignment, ""},
	{domain.ErrInvalidApproval, http.StatusUnprocessableEntity, codeInvalidApproval, ""},
	{app.ErrUnknownParty, http.StatusUnprocessableEntity, codeUnknownParty, ""},
	{domain.ErrAssignmentState, http.StatusConflict, codeAssignmentState, ""},
	{domain.ErrApprovalClosed, http.StatusConflict, codeApprovalClosed, ""},
	{app.ErrSuperseded, http.StatusConflict, codeApprovalSuperseded, ""},
	{app.ErrApprovalNotRequested, http.StatusConflict, codeApprovalNotRequested, ""},
	{app.ErrReleaseRequestClosed, http.StatusConflict, codeReleaseRequestClosed, ""},
	{app.ErrIdempotencyReuse, http.StatusUnprocessableEntity, "idempotency_key_reused", ""},
	{idempotency.ErrInvalidKey, http.StatusBadRequest, "invalid_idempotency_key", ""},
}

// mapError turns Workflow's errors into problem details; a conflict
// becomes onConflict. Anything else (authorization, unexpected
// failures) passes through for Identity's error writer.
func mapError(err error, onConflict *problem.Details) error {
	if errors.Is(err, app.ErrConflict) && onConflict != nil {
		return onConflict
	}
	for _, p := range problems {
		if errors.Is(err, p.err) {
			detail := p.detail
			if detail == "" {
				detail = err.Error()
			}
			return problem.New(p.status, p.code, detail)
		}
	}
	return err
}

// invalidWorkflow reports whether err refuses a definition document,
// and the findings that say why. A rename or a change of subject is a
// refusal of the document too, with one finding naming the field.
func invalidWorkflow(err error) ([]domain.Finding, bool) {
	var inv *domain.InvalidError
	switch {
	case errors.As(err, &inv):
		return inv.Findings, true
	case errors.Is(err, domain.ErrRenamed):
		return []domain.Finding{{Rule: domain.RuleEnvelope, Severity: domain.SeverityError, Path: "name", Message: err.Error()}}, true
	case errors.Is(err, domain.ErrSubjectChanged):
		return []domain.Finding{{Rule: domain.RuleSubject, Severity: domain.SeverityError, Path: "subject", Message: err.Error()}}, true
	}
	return nil, false
}

// workflowProblem is the 422 body: the problem, with every finding.
func workflowProblem(fs []domain.Finding) apiv1.InvalidWorkflowApplicationProblemPlusJSONResponse {
	d := problem.New(http.StatusUnprocessableEntity, codeInvalidWorkflow, detailInvalidWorkflow)
	findings := toFindings(fs)
	return apiv1.InvalidWorkflowApplicationProblemPlusJSONResponse{
		Type: d.Type, Title: d.Title, Status: d.Status, Code: string(d.Code), Detail: &d.Detail, Findings: &findings,
	}
}
