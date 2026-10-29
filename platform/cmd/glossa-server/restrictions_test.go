package main

import (
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
)

// How project scope (RFC 0006 §4.1) reaches an operation.
type projectRule string

const (
	// projectPath: {project} is in the path. The edge answers 404 for a
	// project outside the principal's scope before any handler runs
	// (identity httpapi's projectScoped), and the use case checks the
	// same scope itself (authz.RequireIn and its siblings), which is
	// what MCP and every other caller rely on.
	projectPath projectRule = "path"
	// projectRows: a tenant-level route over rows a project owns (jobs,
	// suggestions, connections, a project's knowledge). Lists filter in
	// the query (authz.Projects), reads of one row answer not found
	// outside the scope (authz.InProject), and tenant-wide rows are
	// read by anyone and changed only by a principal limited to none.
	projectRows projectRule = "rows"
	// projectTenant: the tenant's own configuration or people — not in
	// any project. The permission decides; a token or member a scoped
	// principal creates is cut to their scope (identity's withinActor).
	projectTenant projectRule = "tenant"
	// projectUnscoped: acts on every project at once — a new project,
	// a GitHub installation, a tenant-wide TM or termbase job — and is
	// refused to a principal limited to some (authz.RequireUnscoped).
	projectUnscoped projectRule = "unscoped"
	// projectNone: tenantless or public; there is no project.
	projectNone projectRule = "none"
)

// How visibility `assigned` (RFC 0006 §3.3) reaches an operation.
type assignedRule string

const (
	// assignedDenied: plain authz.Require and friends refuse an
	// assigned member every permission but tenant.read, so the
	// operation answers 403 whatever the id.
	assignedDenied assignedRule = "denied"
	// assignedCovered: the use case admits an assigned member through a
	// coverage-aware check (authz.Visible, RequireUnit, RequireMessage,
	// RequireProject, RequireLocaleIn) — a list holds only covered
	// units, filtered in its query, and anything else is not found.
	assignedCovered assignedRule = "covered"
	// assignedAllowed: the member's own tenant (tenant.read).
	assignedAllowed assignedRule = "allowed"
	// assignedNone: tenantless or public; no tenant data.
	assignedNone assignedRule = "none"
	// assignedOwn: the member's own work (RFC 0006 §3.1, §3.3) — the
	// assignments given to them, their group or their vendor, and
	// nothing else. A list holds only those (Workflow's
	// VisibleAssignments falls back to MyAssignments, cut by their
	// affiliation in the query), an assignment that is not theirs is not
	// found, and acting on one takes being its assignee and holding its
	// permission for every unit (WorkService.work). Only assignment
	// routes may be decided so.
	assignedOwn assignedRule = "own"
)

type restriction struct {
	project  projectRule
	assigned assignedRule
}

var (
	pathDenied   = restriction{projectPath, assignedDenied}
	pathCovered  = restriction{projectPath, assignedCovered}
	rowsDenied   = restriction{projectRows, assignedDenied}
	rowsCovered  = restriction{projectRows, assignedCovered}
	rowsOwn      = restriction{projectRows, assignedOwn}
	tenantDenied = restriction{projectTenant, assignedDenied}
	unscoped     = restriction{projectUnscoped, assignedDenied}
	public       = restriction{projectNone, assignedNone}
)

// restrictions is the enforcement decision for every operation in
// api/openapi.yaml. TestEveryOperationHasARestrictionDecision fails for
// an operation without one (and for a decision whose operation is
// gone), so a new endpoint cannot ship without someone deciding how
// project scope and assignment visibility reach it; the integration
// sweep (TestRestrictionsOverHTTP) holds the GET operations to the
// decisions recorded here.
var restrictions = map[string]restriction{
	"DELETE /v1/auth/session":  public,
	"DELETE /v1/auth/sessions": public,
	// Device sign-in acts on the person, never on a tenant's data: the
	// device's session then carries the person's own scope and visibility.
	"GET /v1/auth/device-authorizations/{user_code}":                                  public,
	"POST /v1/auth/device-approvals":                                                  public,
	"POST /v1/auth/device-authorizations":                                             public,
	"POST /v1/auth/device-sessions":                                                   public,
	"DELETE /v1/me/passkeys/{passkey}":                                                public,
	"DELETE /v1/tenants/{tenant}/ai-providers/{ai_provider}":                          {projectUnscoped, assignedDenied},
	"DELETE /v1/tenants/{tenant}/github/connections/{connection}":                     rowsDenied,
	"DELETE /v1/tenants/{tenant}/github/installations/{installation}":                 unscoped,
	"DELETE /v1/tenants/{tenant}/members/{member}":                                    tenantDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}":                                  pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/ai-routing-policy":                pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/applications/{application}":       pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/delivery-keys/{delivery_key}":     pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/locales/{locale}":                 pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/preview-origins/{preview_origin}": pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/waivers/{waiver}":                 pathDenied,
	"DELETE /v1/tenants/{tenant}/style-guides/{style_guide}":                          rowsDenied,
	"DELETE /v1/tenants/{tenant}/term-concepts/{concept}":                             rowsDenied,
	"DELETE /v1/tenants/{tenant}/tm-units/{unit}":                                     rowsDenied,
	"DELETE /v1/tenants/{tenant}/tokens/{token}":                                      tenantDenied,

	"GET /v1/me":                                              public,
	"GET /v1/me/passkeys":                                     public,
	"GET /v1/meta":                                            public,
	"GET /v1/tenants":                                         public,
	"GET /v1/tenants/{tenant}":                                {projectTenant, assignedAllowed},
	"GET /v1/tenants/{tenant}/ai-budget":                      tenantDenied,
	"GET /v1/tenants/{tenant}/ai-disclosures":                 rowsDenied,
	"GET /v1/tenants/{tenant}/ai-eval-baseline":               tenantDenied,
	"GET /v1/tenants/{tenant}/ai-fills/{ai_fill}":             rowsDenied,
	"GET /v1/tenants/{tenant}/ai-jobs":                        rowsDenied,
	"GET /v1/tenants/{tenant}/ai-jobs/{ai_job}":               rowsDenied,
	"GET /v1/tenants/{tenant}/ai-prices":                      tenantDenied,
	"GET /v1/tenants/{tenant}/ai-providers":                   tenantDenied,
	"GET /v1/tenants/{tenant}/ai-providers/{ai_provider}":     tenantDenied,
	"GET /v1/tenants/{tenant}/ai-routing-policy":              tenantDenied,
	"GET /v1/tenants/{tenant}/ai-settings":                    tenantDenied,
	"GET /v1/tenants/{tenant}/ai-spend":                       tenantDenied,
	"GET /v1/tenants/{tenant}/ai-suggestions":                 rowsDenied,
	"GET /v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}": rowsDenied,
	// The style guide that applies to a unit's locale in a project.
	"GET /v1/tenants/{tenant}/effective-style-guide":                                       rowsCovered,
	"GET /v1/tenants/{tenant}/export-jobs":                                                 rowsDenied,
	"GET /v1/tenants/{tenant}/export-jobs/{export_job}":                                    rowsDenied,
	"GET /v1/tenants/{tenant}/export-jobs/{export_job}/file":                               rowsDenied,
	"GET /v1/tenants/{tenant}/github/connections":                                          rowsDenied,
	"GET /v1/tenants/{tenant}/github/connections/{connection}":                             rowsDenied,
	"GET /v1/tenants/{tenant}/github/installations":                                        tenantDenied,
	"GET /v1/tenants/{tenant}/import-jobs":                                                 rowsDenied,
	"GET /v1/tenants/{tenant}/import-jobs/{import_job}":                                    rowsDenied,
	"GET /v1/tenants/{tenant}/import-jobs/{import_job}/results":                            rowsDenied,
	"GET /v1/tenants/{tenant}/members":                                                     tenantDenied,
	"GET /v1/tenants/{tenant}/members/{member}":                                            tenantDenied,
	"GET /v1/tenants/{tenant}/projects":                                                    rowsCovered,
	"GET /v1/tenants/{tenant}/projects/{project}":                                          pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/ai-metrics":                               pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/ai-review-queue":                          pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/ai-routing-policy":                        pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/ai-settings":                              pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/applications":                             pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/applications/{application}":               pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/branches":                                 pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/branches/{branch}":                        pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/branches/{branch}/proposals":              pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/captures/{capture}/findings":              pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/captures/{capture}/image":                 pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/check-policy":                             pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/check-policy/export":                      pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/check-policy/versions":                    pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/check-policy/versions/{version}":          pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/check-runs":                               pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/check-runs/{check_run}":                   pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/context-builds":                           pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/delivery-keys":                            pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/environments":                             pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/environments/{environment}":               pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/environments/{environment}/deployments":   pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/fallback-graph":                           pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/findings":                                 pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/linguistic-jobs":                          pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/linguistic-jobs/{linguistic_job}":         pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/locales":                                  pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/locales/{locale}":                         pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages":                                 pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}":                       pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/captures":              pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/source-revisions":      pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations":          pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}": pathCovered,
	// The unit workspace's reads (RFC 0006 §3.3): the TM matches and AI
	// suggestions of one unit, through authz.RequireUnit, so an assigned
	// member reaches only the units of their assignments (404 otherwise)
	// — the answer to tenant TM search and project-wide suggestion lists
	// being refused to them. Matches carry no unit id and, for them, no key.
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/ai-suggestions": pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/tm-matches":     pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/revisions":      pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/messages/{message}/usages":                               pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/namespaces":                                              pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/preview-origins":                                         pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/quality-summary":                                         pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/release-signing-keys":                                    pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/releases":                                                pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/releases/{release}":                                      pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/releases/{release}/artifacts/{digest}":                   pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/releases/{release}/diff":                                 pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/releases/{release}/manifest":                             pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/terminology-findings":                                    pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/translation-stats":                                       pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/translations":                                            pathCovered,
	"GET /v1/tenants/{tenant}/projects/{project}/unused-messages":                                         pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/usages":                                                  pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/waivers":                                                 pathDenied,
	"GET /v1/tenants/{tenant}/style-guides":                                                               rowsDenied,
	"GET /v1/tenants/{tenant}/style-guides/{style_guide}":                                                 rowsDenied,
	"GET /v1/tenants/{tenant}/style-guides/{style_guide}/versions":                                        rowsDenied,
	"GET /v1/tenants/{tenant}/term-concepts":                                                              rowsDenied,
	"GET /v1/tenants/{tenant}/term-concepts/{concept}":                                                    rowsDenied,
	"GET /v1/tenants/{tenant}/term-concepts/{concept}/revisions":                                          rowsDenied,
	"GET /v1/tenants/{tenant}/termbase-export-jobs":                                                       rowsDenied,
	"GET /v1/tenants/{tenant}/termbase-import-jobs":                                                       rowsDenied,
	"GET /v1/tenants/{tenant}/tm-concordance":                                                             rowsDenied,
	"GET /v1/tenants/{tenant}/tm-export-jobs":                                                             rowsDenied,
	"GET /v1/tenants/{tenant}/tm-import-jobs":                                                             rowsDenied,
	"GET /v1/tenants/{tenant}/tm-units":                                                                   rowsDenied,
	"GET /v1/tenants/{tenant}/tm-units/{unit}":                                                            rowsDenied,
	"GET /v1/tenants/{tenant}/tokens":                                                                     tenantDenied,
	"GET /v1/tenants/{tenant}/tokens/{token}":                                                             tenantDenied,

	"PATCH /v1/tenants/{tenant}/ai-providers/{ai_provider}":                    unscoped,
	"PATCH /v1/tenants/{tenant}/github/connections/{connection}":               rowsDenied,
	"PATCH /v1/tenants/{tenant}/members/{member}":                              tenantDenied,
	"PATCH /v1/tenants/{tenant}/projects/{project}":                            pathDenied,
	"PATCH /v1/tenants/{tenant}/projects/{project}/applications/{application}": pathDenied,
	"PATCH /v1/tenants/{tenant}/projects/{project}/environments/{environment}": pathDenied,
	"PATCH /v1/tenants/{tenant}/projects/{project}/messages/{message}":         pathDenied,

	"POST /v1/auth/github-oidc-exchanges":                                 public,
	"POST /v1/auth/magic-link-redemptions":                                public,
	"POST /v1/auth/magic-links":                                           public,
	"POST /v1/auth/passkey-challenges":                                    public,
	"POST /v1/auth/passkey-sessions":                                      public,
	"POST /v1/auth/password-reset-redemptions":                            public,
	"POST /v1/auth/password-resets":                                       public,
	"POST /v1/auth/password-sessions":                                     public,
	"POST /v1/auth/registrations":                                         public,
	"POST /v1/integrations/github/webhooks":                               public,
	"POST /v1/me/passkey-challenges":                                      public,
	"POST /v1/me/passkeys":                                                public,
	"POST /v1/me/totp/confirmation":                                       public,
	"POST /v1/me/totp/deactivation":                                       public,
	"POST /v1/message-previews":                                           public,
	"POST /v1/tenants":                                                    public,
	"POST /v1/tenants/{tenant}/ai-fills/{ai_fill}/cancellation":           rowsDenied,
	"POST /v1/tenants/{tenant}/ai-jobs/{ai_job}/cancellation":             rowsDenied,
	"POST /v1/tenants/{tenant}/ai-providers":                              unscoped,
	"POST /v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/acceptance": rowsDenied,
	"POST /v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/rejection":  rowsDenied,
	"POST /v1/tenants/{tenant}/export-jobs":                               rowsDenied,
	"POST /v1/tenants/{tenant}/export-jobs/{export_job}/cancellation":     rowsDenied,
	"POST /v1/tenants/{tenant}/github/connections":                        rowsDenied,
	"POST /v1/tenants/{tenant}/github/install-intents":                    unscoped,
	"POST /v1/tenants/{tenant}/github/installations":                      unscoped,
	"POST /v1/tenants/{tenant}/import-jobs":                               rowsDenied,
	"POST /v1/tenants/{tenant}/import-jobs/{import_job}/cancellation":     rowsDenied,
	"POST /v1/tenants/{tenant}/members":                                   tenantDenied,
	// Groups and vendors (RFC 0006 §3.3, §4.3) are the organization's
	// people, in no project, like members: members.read reads them,
	// members.manage and vendors.manage change them, and an assigned
	// member is refused all of it — a vendor's translator does not read
	// who else works for the customer.
	"GET /v1/tenants/{tenant}/groups":                             tenantDenied,
	"POST /v1/tenants/{tenant}/groups":                            tenantDenied,
	"GET /v1/tenants/{tenant}/groups/{group}":                     tenantDenied,
	"PATCH /v1/tenants/{tenant}/groups/{group}":                   tenantDenied,
	"DELETE /v1/tenants/{tenant}/groups/{group}":                  tenantDenied,
	"PUT /v1/tenants/{tenant}/groups/{group}/members/{member}":    tenantDenied,
	"DELETE /v1/tenants/{tenant}/groups/{group}/members/{member}": tenantDenied,
	"GET /v1/tenants/{tenant}/vendors":                            tenantDenied,
	"POST /v1/tenants/{tenant}/vendors":                           tenantDenied,
	"GET /v1/tenants/{tenant}/vendors/{vendor}":                   tenantDenied,
	"PATCH /v1/tenants/{tenant}/vendors/{vendor}":                 tenantDenied,
	"DELETE /v1/tenants/{tenant}/vendors/{vendor}":                tenantDenied,
	// Workflow (RFC 0006 §2). A definition belongs to one project or to
	// the tenant: reads of one answer not found outside the scope, and
	// the tenant's own are changed only by a principal limited to none
	// (workflow/app readScope, writeScope). Linting stores nothing and
	// names no project. Bindings, resolution and instances are under a
	// project path. workflows.read is refused to an assigned member.
	"GET /v1/tenants/{tenant}/workflow-definitions":                                          rowsDenied,
	"POST /v1/tenants/{tenant}/workflow-definitions":                                         rowsDenied,
	"GET /v1/tenants/{tenant}/workflow-definitions/{workflow_definition}":                    rowsDenied,
	"DELETE /v1/tenants/{tenant}/workflow-definitions/{workflow_definition}":                 rowsDenied,
	"GET /v1/tenants/{tenant}/workflow-definitions/{workflow_definition}/versions":           rowsDenied,
	"POST /v1/tenants/{tenant}/workflow-definitions/{workflow_definition}/versions":          rowsDenied,
	"GET /v1/tenants/{tenant}/workflow-definitions/{workflow_definition}/versions/{version}": rowsDenied,
	"POST /v1/tenants/{tenant}/workflow-definition-lints":                                    tenantDenied,
	// Assignments and approvals (RFC 0006 §3.1–3.2). An assignment is a
	// project's row: a manager's list is cut to their project scope in
	// the query (authz.Projects) and one outside it is not found
	// (InProject); giving work takes assignments.manage in the project
	// (RequireIn). A vendor's member reaches their own work and nothing
	// else (assignedOwn). Approvals are workflows.read and
	// approvals.decide, which an assigned member never holds: a vendor
	// delivers work, it does not sign it off.
	// The vendor quality report (§3.4) is assignments.read in the
	// project scope, cut to it in the query, and refused to an assigned
	// member: the numbers are about vendors, and a vendor delivers work.
	"GET /v1/tenants/{tenant}/assignment-reports":                                                    rowsDenied,
	"GET /v1/tenants/{tenant}/assignments":                                                           rowsOwn,
	"POST /v1/tenants/{tenant}/assignments":                                                          rowsDenied,
	"GET /v1/tenants/{tenant}/assignments/{assignment}":                                              rowsOwn,
	"POST /v1/tenants/{tenant}/assignments/{assignment}/acceptance":                                  rowsOwn,
	"POST /v1/tenants/{tenant}/assignments/{assignment}/completion":                                  rowsOwn,
	"POST /v1/tenants/{tenant}/assignments/{assignment}/decline":                                     rowsOwn,
	"GET /v1/tenants/{tenant}/approvals":                                                             rowsDenied,
	"POST /v1/tenants/{tenant}/approvals":                                                            rowsDenied,
	"GET /v1/tenants/{tenant}/approvals/{approval}":                                                  rowsDenied,
	"POST /v1/tenants/{tenant}/approvals/{approval}/decisions":                                       rowsDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/workflow-bindings":                                  pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/workflow-bindings":                                 pathDenied,
	"DELETE /v1/tenants/{tenant}/projects/{project}/workflow-bindings/{workflow_binding}":            pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/workflow-resolution":                                pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/workflow-instances":                                 pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}":             pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}/transitions": pathDenied,
	// A rebase (wave 6) moves one instance of the project in the path:
	// workflows.manage through authz.RequireIn there, which an assigned
	// member never holds.
	"POST /v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}/rebase": pathDenied,
	// Release requests and rollouts (RFC 0006 §5): Release's, under
	// {project}. Reading takes releases.read in the project
	// (checkProject → RequireIn); withdrawing and every rollout change
	// releases.publish there. Deciding is Workflow's
	// (DecideReleaseRequest): approvals.decide in the request's
	// environment (RequireInEnvironment, which checks the project), and
	// human-only. An assigned member holds none of them: a release
	// ships every unit, not just theirs, and a vendor does not sign it
	// off.
	"GET /v1/tenants/{tenant}/projects/{project}/release-requests":                                          pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}":                        pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}/approvals":             pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}/withdrawal":            pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts":                       pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts":                      pathDenied,
	"GET /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}":             pathDenied,
	"PATCH /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}":           pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}/completion": pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}/abort":      pathDenied,
	// The v0.3 history import (RFC 0006 §7.2) writes the tenant's audit
	// trail, recording every row under the project in the path. It needs
	// audit.import, which only an owner holds, no token scope grants and
	// no background principal may be given, by a principal limited to
	// no project (authz.RequireUnscoped): the trail is the
	// organisation's. A scoped principal outside the project is not
	// found at the edge first. An assigned member is refused it like
	// every permission but tenant.read.
	"POST /v1/tenants/{tenant}/projects/{project}/audit-imports": pathDenied,
	// The trail's entries (RFC 0006 §6.2) are audit.read, owner and
	// admin. A project-scoped principal lists only its projects' entries
	// (authz.Projects in the query) and none of the tenant-level ones —
	// sign-ins, members, tokens, vendors, groups, audit exports belong
	// to the organisation, not to a project — and reads one outside its
	// projects as not found. An assigned member is refused.
	"GET /v1/tenants/{tenant}/audit-entries":            rowsDenied,
	"GET /v1/tenants/{tenant}/audit-entries/{sequence}": rowsDenied,
	// An audit export is one unbroken segment of the tenant's chain,
	// never one project's (a project's entries are not a chain and
	// could not be verified): audit.export, owner only, no token scope,
	// by a principal limited to no project (authz.RequireUnscoped).
	"GET /v1/tenants/{tenant}/audit-export-jobs":                             unscoped,
	"POST /v1/tenants/{tenant}/audit-export-jobs":                            unscoped,
	"GET /v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}":          unscoped,
	"GET /v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}/file":     unscoped,
	"GET /v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}/manifest": unscoped,

	"POST /v1/tenants/{tenant}/projects":                                                            unscoped,
	"POST /v1/tenants/{tenant}/projects/{project}/ai-fill-previews":                                 pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/ai-fills":                                         pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/applications":                                     pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/branch-pushes":                                    pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/branches":                                         pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/branches/{branch}/closure":                        pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/branches/{branch}/merge":                          pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/captures":                                         pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/check-policy":                                     pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/check-policy/import":                              pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/check-runs":                                       pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/context-builds":                                   pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/delivery-keys":                                    pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments":                                     pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments/{environment}/promotions":            pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments/{environment}/release-previews":      pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollbacks":             pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/in-context-grants":                                pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/linguistic-jobs":                                  pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/linguistic-jobs/{linguistic_job}/cancellation":    pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/locales":                                          pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/message-upserts":                                  pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/messages":                                         pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/messages/{message}/obsoletion":                    pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/messages/{message}/renames":                       pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/reviews": pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/preview-origins":                                  pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/releases":                                         pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/translation-imports":                              pathDenied,
	"POST /v1/tenants/{tenant}/projects/{project}/waivers":                                          pathDenied,
	"POST /v1/tenants/{tenant}/style-guides":                                                        rowsDenied,
	"POST /v1/tenants/{tenant}/term-concepts":                                                       rowsDenied,
	// The termbase that applies to a unit's locale in a project.
	"POST /v1/tenants/{tenant}/term-recognitions":    rowsCovered,
	"POST /v1/tenants/{tenant}/termbase-export-jobs": unscoped,
	"POST /v1/tenants/{tenant}/termbase-import-jobs": unscoped,
	"POST /v1/tenants/{tenant}/terminology-checks":   rowsCovered,
	"POST /v1/tenants/{tenant}/tm-export-jobs":       unscoped,
	"POST /v1/tenants/{tenant}/tm-import-jobs":       unscoped,
	// Never for an assigned member: TM matches for their units come from
	// the workspace, not from a search over the tenant's memory (§3.3).
	"POST /v1/tenants/{tenant}/tm-lookups": rowsDenied,
	"POST /v1/tenants/{tenant}/tokens":     tenantDenied,

	"PUT /v1/me/totp":                                                                      public,
	"PUT /v1/tenants/{tenant}/ai-prices":                                                   unscoped,
	"PUT /v1/tenants/{tenant}/ai-routing-policy":                                           unscoped,
	"PUT /v1/tenants/{tenant}/ai-settings":                                                 unscoped,
	"PUT /v1/tenants/{tenant}/import-jobs/{import_job}/file":                               rowsDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/ai-routing-policy":                        pathDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/ai-settings":                              pathDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/branches/{branch}/preview":                pathDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/delivery-keys/{delivery_key}/scope":       pathDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/fallback-graph":                           pathDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/messages/{message}/source":                pathDenied,
	"PUT /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}": pathCovered,
	"PUT /v1/tenants/{tenant}/style-guides/{style_guide}":                                  rowsDenied,
	"PUT /v1/tenants/{tenant}/term-concepts/{concept}":                                     rowsDenied,
}

// TestEveryOperationHasARestrictionDecision generates the operation
// list from the contract (RFC 0006 §11.1 rule 4): every operation needs
// a recorded decision, every decision an operation, and the decisions
// must agree with the operation's shape — {project} in the path is
// project scope by path, a tenantless operation has nothing to scope.
func TestEveryOperationHasARestrictionDecision(t *testing.T) {
	ops, err := apiv1.Requirements()
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for pattern, req := range ops {
		d, ok := restrictions[pattern]
		if !ok {
			missing = append(missing, pattern)
			continue
		}
		_, path, _ := strings.Cut(pattern, " ")
		inProject := strings.Contains(path, "{project}")
		inTenant := strings.HasPrefix(path, "/v1/tenants/{tenant}")
		switch {
		case inProject && d.project != projectPath:
			t.Errorf("%s names {project} but is decided %q, not %q", pattern, d.project, projectPath)
		case !inProject && d.project == projectPath:
			t.Errorf("%s has no {project} but is decided %q", pattern, d.project)
		case !inTenant && (d.project != projectNone || d.assigned != assignedNone):
			t.Errorf("%s is tenantless but decided %+v", pattern, d)
		case inTenant && (d.project == projectNone || d.assigned == assignedNone):
			t.Errorf("%s is in a tenant but decided %+v: nothing in a tenant is beyond the restriction", pattern, d)
		case req.Public && inTenant:
			t.Errorf("%s is public inside a tenant", pattern)
		}
		if d.assigned == assignedCovered && !strings.HasPrefix(pattern, "GET ") &&
			pattern != "PUT /v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}" &&
			pattern != "POST /v1/tenants/{tenant}/term-recognitions" && pattern != "POST /v1/tenants/{tenant}/terminology-checks" {
			t.Errorf("%s admits an assigned member; only reads, the term look-ups and a translation write in a covered unit may (RFC 0006 §3.3)", pattern)
		}
		if d.assigned == assignedOwn && !strings.HasPrefix(path, "/v1/tenants/{tenant}/assignments") {
			t.Errorf("%s is decided %q; only the member's own assignments are theirs (RFC 0006 §3.1)", pattern, d.assigned)
		}
	}
	slices.Sort(missing)
	for _, m := range missing {
		t.Errorf("%s has no restriction decision: decide how project scope and assigned visibility reach it (RFC 0006 §3.3, §4.1)", m)
	}
	for pattern := range restrictions {
		if _, ok := ops[pattern]; !ok {
			t.Errorf("a decision is recorded for %s, which the contract no longer has", pattern)
		}
	}
}
