package authz_test

// Why each of these use cases reaches no project-aware authz check.
// Every other exported method of an application service must reach one
// (TestEveryUseCaseHasAnEnforcementDecision). An `assigned` member is
// still refused everything here that checks a permission: plain
// authz.Require refuses them all but tenant.read.
const (
	wiring     = "wiring: a setter or subscription the composition root calls once"
	background = "background: run by a job or an outbox subscriber as its own principal, never on a caller's behalf"
	signIn     = "sign-in or the person's own account: before or outside any tenant, so no project"
	tenantWide = "the tenant's people, credentials or configuration — in no project; the permission decides " +
		"(an assigned member is refused), and a scope a project-scoped admin sets is cut to their own (withinActor)"
	tenantCfg = "the tenant's AI configuration and spend, which belong to no project; changing it is RequireUnscoped " +
		"in the writes, and reading it is the permission's"
	facade  = "MCP's façade: every tool calls an application service that checks the project itself"
	noData  = "reads no tenant data"
	pure    = "a pure computation over values the caller already holds"
	runtime = "the delivery path, answered to a delivery key or a background principal, not to a member"
)

var notProjectAddressed = map[string]string{
	"audit.Service.Append":         background,
	"audit.Service.Backfill":       background,
	"audit.Service.HandleBatch":    background,
	"audit.Service.HandleEvent":    background,
	"audit.Service.Subscribe":      wiring,
	"audit.Service.Metrics":        "returns the recorder wired at startup; it reads no data",
	"audit.Service.RecordSignIn":   "records a sign-in, before any tenant's project is in play, as the system; it reads nothing back",
	"audit.Service.RecordToolCall": "records an MCP call after the tool's own application service checked the project; it reads nothing back",
	"audit.Service.Verify": "the tenant's hash chain, which belongs to no project; reached by no route until RFC 0006 " +
		"wave 5's audit API, which must gate it with audit.read and RequireUnscoped",
	"audit.Service.ExportKeys": noData + ": the deployment's audit key set, whose public half is published to anyone " +
		"(/.well-known/glossa-audit-keys.json) and whose private half only the export jobs sign with",

	"identity.Service.WaitAudits": "waits for in-flight sign-in audit writes at shutdown; reads no tenant data",

	"workflow.WorkService.Expire": "the timer sweep's background principal; a manager's own call is cut to their " +
		"projects by the inScope check change runs on the loaded row (passed as a value, which this walk cannot follow)",
	"workflow.WorkService.MyAssignments": "the caller's own assignments, cut in the query to their project scope " +
		"(AssignmentFilter.Within from Principal.Projects) — the ownWork rule, since an assigned member holds no tenant-wide read",

	"catalog.Service.SetCoverage":   wiring,
	"catalog.Service.SetImpact":     wiring,
	"catalog.Service.SetLocales":    wiring,
	"catalog.Service.SetProjection": wiring,

	"context.Service.Subscribe": wiring,

	"identity.Service.AddGroupMember":             tenantWide,
	"identity.Service.AddMember":                  tenantWide,
	"identity.Service.AuthenticateCIToken":        signIn,
	"identity.Service.AuthenticateInContextGrant": signIn,
	"identity.Service.AuthenticateSession":        signIn,
	"identity.Service.AuthenticateToken":          signIn,
	"identity.Service.Authorize":                  "builds the principal the checks read: it puts the project scope and visibility on it",
	"identity.Service.BeginPasskeyRegistration":   signIn,
	"identity.Service.BeginPasskeySignIn":         signIn,
	"identity.Service.BeginTOTP":                  signIn,
	"identity.Service.ConfirmTOTP":                signIn,
	"identity.Service.CreateGroup":                tenantWide,
	"identity.Service.CreateOrganization":         signIn,
	"identity.Service.CreateToken":                tenantWide,
	"identity.Service.CreateVendor":               tenantWide,
	"identity.Service.DeleteGroup":                tenantWide,
	"identity.Service.DeletePasskey":              signIn,
	"identity.Service.DeleteVendor":               tenantWide,
	"identity.Service.DisableTOTP":                signIn,
	"identity.Service.EmailEnabled":               noData,
	"identity.Service.ExchangeGitHubOIDC":         "mints a CI token for exactly one connected project, which its principal is then scoped to",
	"identity.Service.FinishPasskeyRegistration":  signIn,
	"identity.Service.FinishPasskeySignIn":        signIn,
	"identity.Service.GetGroup":                   tenantWide,
	"identity.Service.GetMe":                      signIn,
	"identity.Service.GetMember":                  tenantWide,
	"identity.Service.GetTenant":                  "the caller's own tenant: tenant.read, which an assigned member keeps",
	"identity.Service.GetToken":                   tenantWide,
	"identity.Service.GetVendor":                  tenantWide,
	"identity.Service.InviteMember":               tenantWide,
	"identity.Service.IssueToken":                 tenantWide,
	"identity.Service.ListGroups":                 tenantWide,
	"identity.Service.ListMembers":                tenantWide,
	"identity.Service.ListPasskeys":               signIn,
	"identity.Service.ListTenants":                signIn,
	"identity.Service.ListTokens":                 tenantWide,
	"identity.Service.ListVendors":                tenantWide,
	"identity.Service.PasskeysEnabled":            noData,
	"identity.Service.Person":                     signIn,
	"identity.Service.PreviewOriginRegistered":    "a CORS preflight, which carries no credentials and so no tenant or project",
	"identity.Service.PurgeExpiredCITokens":       background,
	"identity.Service.PurgeExpiredGrants":         background,
	"identity.Service.RedeemSignInLink":           signIn,
	"identity.Service.Register":                   signIn,
	"identity.Service.RemoveGroupMember":          tenantWide,
	"identity.Service.RemoveMember":               tenantWide,
	"identity.Service.RenameGroup":                tenantWide,
	"identity.Service.RequestPasswordReset":       signIn,
	"identity.Service.RequestSignInLink":          signIn,
	"identity.Service.ResetPassword":              signIn,
	"identity.Service.RestrictMember":             tenantWide,
	"identity.Service.RevokeToken":                tenantWide,
	"identity.Service.StartDeviceAuthorization":   signIn,
	"identity.Service.RedeemDeviceAuthorization":  signIn,
	"identity.Service.AuthenticateDeviceSession":  signIn,
	"identity.Service.SignOutDevice":              signIn,
	"identity.Service.DeviceSignInEnabled":        noData,
	"identity.Service.DeviceAuthorizationFor":     "the person looks up the code they are about to decide; a code belongs to no tenant or project, and it is cut to the one person's browser session",
	"identity.Service.DecideDeviceAuthorization":  "a person approves or denies a code as themselves; the session it creates is theirs and is cut to their own memberships when used, so no project is in play here",
	"identity.Service.SessionLifetime":            noData,
	"identity.Service.SetCoverage":                wiring,
	"identity.Service.SetGitHubOIDC":              wiring,
	"identity.Service.SignInMethods":              signIn,
	"identity.Service.SignInWithPassword":         signIn,
	"identity.Service.SignOut":                    signIn,
	"identity.Service.SignOutEverywhere":          signIn,
	"identity.Service.UpdateMember":               tenantWide,
	"identity.Service.UpdateVendor":               tenantWide,

	"integration.GitHubService.ConnectionsForRepository": "the CI token exchange's cross-tenant lookup, before any principal exists",
	"integration.GitHubService.Installations":            "the tenant's GitHub App installations, which serve every project; installing or forgetting one is RequireUnscoped",
	"integration.GitHubService.ReceiveWebhook":           "a signed GitHub delivery, with no principal",
	"integration.GitHubService.SubscribeChecks":          wiring,
	"integration.Service.Config":                         noData,
	"integration.Service.Subscribe":                      wiring,

	"intelligence.Service.CanRead":       tenantCfg,
	"intelligence.Service.GetBudget":     tenantCfg,
	"intelligence.Service.GetPrices":     tenantCfg,
	"intelligence.Service.GetProvider":   tenantCfg,
	"intelligence.Service.GetSettings":   tenantCfg,
	"intelligence.Service.ListProviders": tenantCfg,
	"intelligence.Service.ListSpend":     tenantCfg,
	"intelligence.Service.Subscribe":     wiring,

	"knowledge.Service.Subscribe":    wiring,
	"localization.Service.Subscribe": wiring,

	"mcp.Service.AllowToolset": facade,
	"mcp.Service.Authenticate": facade,
	"mcp.Service.Call":         facade,
	"mcp.Service.Open":         facade,
	"mcp.Service.Tools":        facade,

	"preview.Service.Preview": "the stateless message preview: no tenant, no project, no data",

	"quality.Service.SetLinguist":         wiring,
	"quality.Service.SetPullRequestLinks": wiring,
	"quality.Service.SetSummarySources":   wiring,

	"release.Service.ManifestBytes":     pure,
	"release.Service.RewriteKeyIndexes": background,
	"release.Service.Subscribe":         wiring,
	"release.Service.UseApprovals":      wiring,
	"release.Service.SyncDeliveryKey":   runtime,
	"release.Service.SyncEnvironment":   runtime,

	"workflow.Service.Lint": "compiles a document the caller sent; reads nothing of the tenant's",
}
