//go:build system

package m5_test

// The M5 surfaces this test drives, in one place.
//
// Nothing below exists yet when wave 1 lands: RFC 0006 §8 names the
// surfaces and §13 assigns them to waves 2–5, but no wave-1 slice
// touches platform/api/openapi.yaml. So these paths are the exit
// test's reading of the RFC — tenant-scoped like every /v1 path, nouns
// for actions as the existing API spells them (`promotions`,
// `rollbacks`, `cancellation`). The slice that lands an operation in
// the spec owns making this table agree with it, in the same pull
// request; the criterion then turns green or says, precisely, what is
// still missing. No criterion depends on a path being spelled this way
// rather than another, only on the operation existing.

// Workflow (§2, §8): definitions, bindings, instances, transitions.
// These are in the spec since wave 2 (tag `workflows`); the paths below
// are the spec's: definitions are the tenant's (a project's own one is
// created with `?project=`), bindings and instances are addressed under
// their project, and an instance's log is under the instance.
func (s *scenario) workflowDefinitionsPath() string { return s.tenantPath("/workflow-definitions") }
func (s *scenario) workflowBindingsPath(project string) string {
	return s.projectPathOf(project, "/workflow-bindings")
}
func (s *scenario) workflowInstancesPath(project string) string {
	return s.projectPathOf(project, "/workflow-instances")
}
func (s *scenario) workflowTransitionsPath(project, instance string) string {
	return s.projectPathOf(project, "/workflow-instances/"+instance+"/transitions")
}

// Assignments, approvals, groups and vendors (§3, §4.3). In the spec
// since wave 3 (tags `assignments`, `approvals`, `groups`, `vendors`),
// tenant-level with a project filter. GET …/assignments answers what
// the caller may see — a manager every assignment, everyone else (a
// vendor's member included) their own work; an assignment is created
// with `project_id` once and its units by message key; its actions are
// …/acceptance, …/completion and …/decline. Approvals are listed by
// `project`, `message` (a key) and `locale`, carry a `state`, and take
// decisions as `{"decision": "granted"|"denied", "reason"}`. A member is
// invited into a vendor with `vendor_id`, `visibility` and `projects`.
func (s *scenario) assignmentsPath() string         { return s.tenantPath("/assignments") }
func (s *scenario) assignmentPath(id string) string { return s.tenantPath("/assignments/" + id) }
func (s *scenario) approvalsPath() string           { return s.tenantPath("/approvals") }
func (s *scenario) approvalDecisions(id string) string {
	return s.tenantPath("/approvals/" + id + "/decisions")
}
func (s *scenario) vendorsPath() string { return s.tenantPath("/vendors") }

// Release requests and rollouts (§5). In the spec since wave 4 (tags
// `release-requests`, `rollouts`). A publish or promote into an
// environment with an `approval` answers 202 with the release's `id` and
// `release_request_id`; a request is read under …/release-requests and
// granted with `{"decision": "granted"}` at …/approvals (201). A rollout
// starts with `{"release_id", "percent"}` (201, ETag), advances with a
// PATCH of `{"percent"}` carrying that ETag as If-Match, and ends at
// …/completion or …/abort.
func (s *scenario) releaseRequestsPath(project string) string {
	return s.projectPathOf(project, "/release-requests")
}
func (s *scenario) releaseRequestApprovals(project, id string) string {
	return s.projectPathOf(project, "/release-requests/"+id+"/approvals")
}
func (s *scenario) rolloutsPath(project, env string) string {
	// §5.2 spells this one: POST …/environments/{env}/rollouts.
	return s.projectPathOf(project, "/environments/"+env+"/rollouts")
}
func (s *scenario) rolloutPath(project, env, id string) string {
	return s.rolloutsPath(project, env) + "/" + id
}

// Audit (§6.2). The RFC writes `GET /v1/audit/entries` as shorthand;
// every /v1 path is tenant-scoped, and so is the audit chain.
func (s *scenario) auditEntriesPath() string        { return s.tenantPath("/audit-entries") }
func (s *scenario) auditExportJobsPath() string     { return s.tenantPath("/audit-export-jobs") }
func (s *scenario) auditExportJob(id string) string { return s.tenantPath("/audit-export-jobs/" + id) }

// auditKeysPath is where glossa-server publishes the audit key's public
// half (RFC 0006 §6.2, amended in wave 4): deployment-wide, outside
// /v1, readable without a token.
const auditKeysPath = "/.well-known/glossa-audit-keys.json"

// The member fields §3.3 and §4.1 add to an invitation.
const (
	visibilityAssigned = "assigned"
)

// The CLI commands §8 names. They run in process, through cli.Main, as
// every exit test runs the CLI.
var (
	cliAuditVerify = []string{"audit", "verify"}
	cliImportV0DB  = []string{"import", "--from", "v0", "--v0-db"}
)
