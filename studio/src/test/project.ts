/** Mount a project screen with a project context, the ports it uses and a router, as ProjectLayout would. */
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { vi } from "vitest";
import { computed, defineComponent, ref, type Component } from "vue";
import { createMemoryHistory, createRouter } from "vue-router";
import { CONTEXT, type ContextPort } from "../api/context";
import { GITHUB, type GitHubPort } from "../api/github";
import { IN_CONTEXT, type InContextPort } from "../api/in-context";
import { INTEGRATION, type IntegrationPort } from "../api/integration";
import { INTELLIGENCE, type IntelligencePort } from "../api/intelligence";
import { KNOWLEDGE, type KnowledgePort } from "../api/knowledge";
import { CHECK_POLICY, type CheckPolicyPort } from "../api/policy";
import { QUALITY, type QualityPort } from "../api/quality";
import { QUALITY_SUMMARY, type QualitySummaryPort } from "../api/quality-summary";
import type { QualitySummary } from "../api/quality-summary-schemas";
import { RELEASES, type ReleasesPort } from "../api/releases";
import { WORK, type WorkPort } from "../api/work";
import { AUDIT, type AuditPort } from "../api/audit";
import { DIRECTORY, type DirectoryPort } from "../api/directory";
import { RELEASE_OPS, type ReleaseOpsPort } from "../api/release-ops";
import { WORKFLOWS, type WorkflowsPort } from "../api/workflows";
import type { Project, ProjectLocale, Role } from "../api/schemas";
import { grantFor } from "../session/permissions";
import { refreshSession } from "../session/session";
import { PROJECT, type HealthState, type ProjectContext } from "../views/project/context";

const Empty = defineComponent({ template: "<div />" });
const ROUTE_NAMES: Record<string, string> = {
  "releases/:release": "release",
  "files/import": "import",
  "files/imports/:job": "import-job",
  "quality/policy": "check-policy",
  "quality/waivers": "waivers",
  "releases/requests": "release-requests",
  "releases/requests/:request": "release-request",
  "releases/environments/:environment": "environment",
  workflow: "project-workflow",
  "workflow/instances": "workflow-instances",
  "workflow/instances/:instance": "workflow-instance",
};

const ME = {
  person: { id: "me", email: "me@example.com", email_verified: true, totp_enabled: false, individual_tenant_id: "t", created_at: "2026-09-01T00:00:00Z" },
  csrf_token: "csrf",
  memberships: [],
};

const NOW = "2026-09-19T08:00:00Z";
export const locale = (code: string, is_source = false): ProjectLocale => ({ code, direction: "ltr", is_source, created_at: NOW });
export const demoProject: Project = {
  id: "p",
  slug: "demo",
  name: "Demo",
  source_locale: "en",
  settings: { default_syntax: "mf1", review_required: true },
  created_at: NOW,
  updated_at: NOW,
};

export function projectContext(
  roles: Role[] = ["developer"],
  locales: ProjectLocale[] = [],
  memberLocales: string[] = [],
  project?: Project,
  etag?: string,
  /** The quality summary the screen reads. Absent: nothing was measured, as on a server without the endpoint. */
  health?: QualitySummary,
): ProjectContext {
  const all = ref(locales);
  const summary = ref(health);
  return {
    tenant: computed(() => "t"),
    projectId: computed(() => "p"),
    project: ref(project ?? (locales.length ? demoProject : undefined)),
    etag: ref(etag),
    locales: all,
    targets: computed(() => all.value.filter((l) => !l.is_source)),
    grant: computed(() => grantFor({ roles, locales: memberLocales })),
    health: summary,
    healthState: ref<HealthState>(health ? "ready" : "unreported"),
    reloadProject: async () => undefined,
    reloadLocales: async () => undefined,
    reloadHealth: async () => undefined,
  };
}

export interface ScreenOptions {
  port?: ReleasesPort;
  knowledge?: KnowledgePort;
  intelligence?: IntelligencePort;
  github?: GitHubPort;
  inContext?: InContextPort;
  integration?: IntegrationPort;
  quality?: QualityPort;
  checkPolicy?: CheckPolicyPort;
  qualitySummary?: QualitySummaryPort;
  /** The summary the project context already holds, as ProjectLayout would have loaded it. */
  health?: QualitySummary;
  context?: ContextPort;
  work?: WorkPort;
  workflows?: WorkflowsPort;
  directory?: DirectoryPort;
  releaseOps?: ReleaseOpsPort;
  roles?: Role[];
  /** Locale scope of the member (translators, reviewers). */
  memberLocales?: string[];
  locales?: ProjectLocale[];
  /** The project the context starts with (default: demoProject once there are locales). */
  project?: Project;
  /** Its ETag; screens that save need one. */
  etag?: string;
  path?: string;
  /** Answers for fetch calls that don't go through a port (by path). */
  fetch?: (path: string) => unknown;
  /** Anything else to provide, by injection key (a chart renderer, say). */
  provide?: Record<symbol, unknown>;
}

export async function mountProjectScreen(component: Component, options: ScreenOptions): Promise<VueWrapper> {
  // The signed-in person is `person:me` (the fake ports' author); member names come back empty.
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      const body = path === "/v1/me" ? ME : (options.fetch?.(path) ?? { items: [] });
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  await refreshSession();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      "releases",
      "releases/requests",
      "releases/requests/:request",
      "releases/environments/:environment",
      "releases/:release",
      "workflow",
      "workflow/instances",
      "workflow/instances/:instance",
      "settings",
      "translate",
      "review",
      "terms",
      "style",
      "ai",
      "quality",
      "quality/policy",
      "quality/waivers",
      "files",
      "files/import",
      "files/imports/:job",
    ]
      .map((p) => ({ path: `/t/:tenant/p/:project/${p}`, name: ROUTE_NAMES[p] ?? p, component: Empty }))
      .concat([
        { path: "/t/:tenant", name: "projects", component: Empty },
        { path: "/t/:tenant/settings/knowledge", name: "workspace-knowledge", component: Empty },
        { path: "/t/:tenant/work", name: "my-work", component: Empty },
        { path: "/t/:tenant/settings/workflows", name: "workflows", component: Empty },
        { path: "/t/:tenant/settings/workflows/:definition", name: "workflow", component: Empty },
      ]),
  });
  await router.push(options.path ?? "/t/t/p/p/releases");
  const provide: Record<symbol, unknown> = {
    [PROJECT as symbol]: projectContext(options.roles, options.locales, options.memberLocales, options.project, options.etag, options.health),
  };
  if (options.port) provide[RELEASES as symbol] = options.port;
  if (options.knowledge) provide[KNOWLEDGE as symbol] = options.knowledge;
  if (options.intelligence) provide[INTELLIGENCE as symbol] = options.intelligence;
  if (options.integration) provide[INTEGRATION as symbol] = options.integration;
  if (options.quality) provide[QUALITY as symbol] = options.quality;
  if (options.checkPolicy) provide[CHECK_POLICY as symbol] = options.checkPolicy;
  if (options.qualitySummary) provide[QUALITY_SUMMARY as symbol] = options.qualitySummary;
  if (options.context) provide[CONTEXT as symbol] = options.context;
  if (options.github) provide[GITHUB as symbol] = options.github;
  if (options.inContext) provide[IN_CONTEXT as symbol] = options.inContext;
  if (options.work) provide[WORK as symbol] = options.work;
  if (options.workflows) provide[WORKFLOWS as symbol] = options.workflows;
  if (options.directory) provide[DIRECTORY as symbol] = options.directory;
  if (options.releaseOps) provide[RELEASE_OPS as symbol] = options.releaseOps;
  Object.assign(provide, options.provide);
  const w = mount(component, { attachTo: document.body, global: { plugins: [router], provide } });
  await flushPromises();
  return w;
}

export interface TenantScreenOptions {
  integration?: IntegrationPort;
  work?: WorkPort;
  workflows?: WorkflowsPort;
  directory?: DirectoryPort;
  audit?: AuditPort;
  /** Locale scope of the member (translators, reviewers). */
  locales?: string[];
  github?: GitHubPort;
  qualitySummary?: QualitySummaryPort;
  roles?: Role[];
  path: string;
  /** Answers for the stubbed fetch, by path; the default is an empty page. */
  responses?: Record<string, unknown>;
  /** Anything else to provide, by injection key (a chart renderer, say). */
  provide?: Record<symbol, unknown>;
  /** Props for the screen itself (a shorter debounce, say). */
  props?: Record<string, unknown>;
}

/** Mount a workspace (tenant-level) screen: the member's grant comes from the session, as in the app. */
export async function mountTenantScreen(component: Component, options: TenantScreenOptions): Promise<VueWrapper> {
  const me = {
    ...ME,
    memberships: [
      {
        member_id: "m",
        tenant: { id: "t", kind: "organization", slug: "acme", name: "Acme", created_at: NOW },
        roles: options.roles ?? ["developer"],
        locales: options.locales ?? [],
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      const body = path === "/v1/me" ? me : (options.responses?.[path] ?? { items: [] });
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  await refreshSession();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/t/:tenant", name: "projects", component: Empty },
      { path: "/t/:tenant/p/:project/translate", name: "translate", component: Empty },
      { path: "/t/:tenant/p/:project/locales", name: "locales", component: Empty },
      { path: "/t/:tenant/settings/knowledge", name: "workspace-knowledge", component: Empty },
      { path: "/t/:tenant/settings/knowledge/imports/:job", name: "workspace-import-job", component: Empty },
      { path: "/t/:tenant/settings/github", name: "workspace-github", component: Empty },
      { path: "/t/:tenant/work", name: "my-work", component: Empty },
      { path: "/t/:tenant/approvals", name: "approvals", component: Empty },
      { path: "/t/:tenant/settings/workflows", name: "workflows", component: Empty },
      { path: "/t/:tenant/settings/workflows/new", name: "workflow-new", component: Empty },
      { path: "/t/:tenant/settings/workflows/:definition", name: "workflow", component: Empty },
      { path: "/t/:tenant/settings/groups", name: "groups", component: Empty },
      { path: "/t/:tenant/settings/vendors", name: "vendors", component: Empty },
      { path: "/t/:tenant/settings/vendors/:vendor", name: "vendor", component: Empty },
      { path: "/t/:tenant/settings/audit", name: "audit-log", component: Empty },
      { path: "/t/:tenant/settings/audit/exports", name: "audit-exports", component: Empty },
      { path: "/t/:tenant/settings/audit/entries/:sequence", name: "audit-entry", component: Empty },
    ],
  });
  await router.push(options.path);
  const provide: Record<symbol, unknown> = {};
  if (options.integration) provide[INTEGRATION as symbol] = options.integration;
  if (options.github) provide[GITHUB as symbol] = options.github;
  if (options.qualitySummary) provide[QUALITY_SUMMARY as symbol] = options.qualitySummary;
  if (options.work) provide[WORK as symbol] = options.work;
  if (options.workflows) provide[WORKFLOWS as symbol] = options.workflows;
  if (options.directory) provide[DIRECTORY as symbol] = options.directory;
  if (options.audit) provide[AUDIT as symbol] = options.audit;
  Object.assign(provide, options.provide);
  const w = mount(component, { attachTo: document.body, props: options.props ?? {}, global: { plugins: [router], provide } });
  await flushPromises();
  return w;
}
