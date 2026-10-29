import { createRouter, createWebHistory, type RouteRecordRaw, type Router } from "vue-router";
import { homeTenant, membershipFor, refreshSession, rememberTenant, useSession } from "./session/session";

declare module "vue-router" {
  interface RouteMeta {
    /** Reachable without a session. */
    public?: boolean;
    title?: string;
  }
}

export const routes: RouteRecordRaw[] = [
  { path: "/auth/sign-in", name: "sign-in", component: () => import("./views/auth/SignInView.vue"), meta: { public: true, title: "Sign in" } },
  { path: "/auth/register", name: "register", component: () => import("./views/auth/RegisterView.vue"), meta: { public: true, title: "Create an account" } },
  { path: "/auth/reset-password", name: "reset-password", component: () => import("./views/auth/ResetPasswordView.vue"), meta: { public: true, title: "Reset password" } },
  // The in-product editor's authorization popup (RFC 0004 §5.2). It sits
  // outside the app shell on purpose: it is a small window a product's
  // preview deployment opened, not a place to navigate from. It needs a
  // session — an anonymous visitor is sent to sign in and comes back.
  {
    path: "/in-context/authorize",
    name: "in-context-authorize",
    component: () => import("./views/in-context/AuthorizeView.vue"),
    meta: { title: "Allow in-product editing" },
  },
  // Device sign-in for the Glossa CLI (RFC 0006 §7.2, RFC 8628): the
  // `verification_uri` the CLI prints. Outside the shell like the popup
  // above, and like it not public — an anonymous visitor signs in and
  // comes back to `/device?code=…` with the code still filled in.
  { path: "/device", name: "device", component: () => import("./views/device/DeviceView.vue"), meta: { title: "Sign in a device" } },
  // The marketing site (site/) serves "/" in production; the ingress never
  // sends it here. Under the dev server and the e2e suite it still lands in
  // the app.
  { path: "/", redirect: { name: "home" } },
  {
    path: "/",
    component: () => import("./components/AppShell.vue"),
    children: [
      { path: "app", name: "home", redirect: () => ({ name: "projects", params: { tenant: homeTenant() ?? "none" } }) },
      { path: "account", name: "account", component: () => import("./views/AccountView.vue"), meta: { title: "Account & security" } },
      { path: "organizations/new", name: "new-organization", component: () => import("./views/NewOrganizationView.vue"), meta: { title: "New organization" } },
      { path: "t/:tenant", name: "projects", component: () => import("./views/ProjectsView.vue"), meta: { title: "Projects" } },
      // RFC 0006 §3: the work given to me, and the approvals waiting for me.
      { path: "t/:tenant/work", name: "my-work", component: () => import("./views/work/MyWorkView.vue"), meta: { title: "My work" } },
      { path: "t/:tenant/approvals", name: "approvals", component: () => import("./views/work/ApprovalsView.vue"), meta: { title: "Approvals" } },
      {
        path: "t/:tenant/settings/knowledge",
        name: "workspace-knowledge",
        component: () => import("./views/workspace/KnowledgeFilesView.vue"),
        meta: { title: "Translation memory & termbase" },
      },
      {
        path: "t/:tenant/settings/github",
        name: "workspace-github",
        component: () => import("./views/workspace/GitHubView.vue"),
        meta: { title: "GitHub" },
      },
      // RFC 0006 §8: the workflow editor, and the organization's groups and vendors.
      { path: "t/:tenant/settings/workflows", name: "workflows", component: () => import("./views/workflow/WorkflowsView.vue"), meta: { title: "Workflows" } },
      { path: "t/:tenant/settings/workflows/new", name: "workflow-new", component: () => import("./views/workflow/WorkflowEditorView.vue"), meta: { title: "New workflow" } },
      { path: "t/:tenant/settings/workflows/:definition", name: "workflow", component: () => import("./views/workflow/WorkflowEditorView.vue"), meta: { title: "Workflow" } },
      { path: "t/:tenant/settings/groups", name: "groups", component: () => import("./views/directory/GroupsView.vue"), meta: { title: "Groups" } },
      { path: "t/:tenant/settings/vendors", name: "vendors", component: () => import("./views/directory/VendorsView.vue"), meta: { title: "Vendors" } },
      { path: "t/:tenant/settings/vendors/:vendor", name: "vendor", component: () => import("./views/directory/VendorView.vue"), meta: { title: "Vendor" } },
      // RFC 0006 §6, wave 6: the audit log, one entry, and the exports.
      { path: "t/:tenant/settings/audit", name: "audit-log", component: () => import("./views/audit/AuditLogView.vue"), meta: { title: "Audit log" } },
      { path: "t/:tenant/settings/audit/exports", name: "audit-exports", component: () => import("./views/audit/AuditExportsView.vue"), meta: { title: "Audit exports" } },
      { path: "t/:tenant/settings/audit/entries/:sequence(\\d+)", name: "audit-entry", component: () => import("./views/audit/AuditEntryView.vue"), meta: { title: "Audit entry" } },
      {
        path: "t/:tenant/settings/knowledge/imports/:job",
        name: "workspace-import-job",
        component: () => import("./views/workspace/KnowledgeImportJobView.vue"),
        meta: { title: "Import" },
      },
      {
        path: "t/:tenant/p/:project",
        component: () => import("./views/project/ProjectLayout.vue"),
        children: [
          { path: "", name: "project", redirect: (to) => ({ name: "translate", params: to.params }) },
          { path: "translate", name: "translate", component: () => import("./views/project/WorkspaceView.vue"), meta: { title: "Translate" } },
          { path: "review", name: "review", component: () => import("./views/project/ReviewQueueView.vue"), meta: { title: "Review queue" } },
          { path: "terms", name: "terms", component: () => import("./views/project/TermbaseView.vue"), meta: { title: "Termbase" } },
          { path: "style", name: "style", component: () => import("./views/project/StyleGuidesView.vue"), meta: { title: "Style guides" } },
          { path: "ai", name: "ai", component: () => import("./views/project/AiSettingsView.vue"), meta: { title: "AI settings" } },
          { path: "locales", name: "locales", component: () => import("./views/project/LocalesView.vue"), meta: { title: "Locales" } },
          { path: "settings", name: "settings", component: () => import("./views/project/SettingsView.vue"), meta: { title: "Settings" } },
          { path: "quality", name: "quality", component: () => import("./views/project/QualityView.vue"), meta: { title: "Quality" } },
          { path: "quality/policy", name: "check-policy", component: () => import("./views/project/CheckPolicyView.vue"), meta: { title: "Check policy" } },
          { path: "quality/waivers", name: "waivers", component: () => import("./views/project/WaiversView.vue"), meta: { title: "Waivers" } },
          { path: "releases", name: "releases", component: () => import("./views/project/ReleasesView.vue"), meta: { title: "Releases" } },
          // RFC 0006 §5: release requests, and an environment's approval requirement and rollouts.
          { path: "releases/requests", name: "release-requests", component: () => import("./views/project/ReleaseRequestsView.vue"), meta: { title: "Release requests" } },
          { path: "releases/requests/:request", name: "release-request", component: () => import("./views/project/ReleaseRequestView.vue"), meta: { title: "Release request" } },
          { path: "releases/environments/:environment", name: "environment", component: () => import("./views/project/EnvironmentView.vue"), meta: { title: "Environment" } },
          { path: "releases/:release", name: "release", component: () => import("./views/project/ReleaseDetailView.vue"), meta: { title: "Release" } },
          // RFC 0006 §2: the project's workflow bindings, and the instances that run under them.
          { path: "workflow", name: "project-workflow", component: () => import("./views/project/WorkflowBindingsView.vue"), meta: { title: "Workflow" } },
          { path: "workflow/instances", name: "workflow-instances", component: () => import("./views/project/InstancesView.vue"), meta: { title: "Workflow instances" } },
          { path: "workflow/instances/:instance", name: "workflow-instance", component: () => import("./views/project/InstanceView.vue"), meta: { title: "Workflow instance" } },
          { path: "files", name: "files", component: () => import("./views/project/ImportExportView.vue"), meta: { title: "Import & export" } },
          { path: "files/import", name: "import", component: () => import("./views/project/ImportView.vue"), meta: { title: "Import a file" } },
          { path: "files/imports/:job", name: "import-job", component: () => import("./views/project/ImportJobView.vue"), meta: { title: "Import" } },
        ],
      },
      { path: ":pathMatch(.*)*", name: "not-found", component: () => import("./views/NotFoundView.vue"), meta: { title: "Not found" } },
    ],
  },
];

export function installGuards(router: Router): void {
  const { status } = useSession();
  router.beforeEach(async (to) => {
    if (status.value === "unknown") await refreshSession().catch(() => null);
    const signedIn = status.value === "authenticated";
    if (to.meta.public) {
      // A signed-in person landing on sign-in without a link goes home.
      if (signedIn && to.name === "sign-in" && !to.hash.includes("token=")) return { name: "home" };
      return true;
    }
    if (!signedIn) return { name: "sign-in", query: to.fullPath === "/app" ? {} : { next: to.fullPath } };
    const tenant = typeof to.params.tenant === "string" ? to.params.tenant : undefined;
    if (tenant) {
      if (!membershipFor(tenant)) {
        const home = homeTenant();
        return home && home !== tenant ? { name: "projects", params: { tenant: home } } : true;
      }
      rememberTenant(tenant);
    }
    return true;
  });
  router.afterEach((to) => {
    document.title = to.meta.title ? `${to.meta.title} · Glossa Studio` : "Glossa Studio";
  });
}

export function createStudioRouter(): Router {
  const router = createRouter({ history: createWebHistory(), routes });
  installGuards(router);
  return router;
}
