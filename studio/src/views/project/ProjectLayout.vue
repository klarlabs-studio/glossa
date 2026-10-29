<script setup lang="ts">
import { computed, provide, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute } from "vue-router";
import { locales as localesApi, projects } from "../../api/endpoints";
import { notReported, useQualitySummary } from "../../api/quality-summary";
import type { QualitySummary } from "../../api/quality-summary-schemas";
import type { Project, ProjectLocale } from "../../api/schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { openErrors } from "../../lib/health";
import { allows, type Permission } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { strings } from "../../strings";
import { PROJECT, type HealthState, type ProjectContext } from "./context";

const route = useRoute();
const tenant = computed(() => String(route.params.tenant));
const projectId = computed(() => String(route.params.project));
const project = ref<Project>();
const etag = ref<string>();
const locales = ref<ProjectLocale[]>([]);
const error = ref<unknown>(null);
const grant = useGrant(() => tenant.value);
const summaryPort = useQualitySummary();
const health = ref<QualitySummary>();
const healthState = ref<HealthState>("loading");

async function reloadProject(): Promise<void> {
  const r = await projects.get({ tenant: tenant.value, project: projectId.value });
  project.value = r.value;
  etag.value = r.etag;
}
async function reloadLocales(): Promise<void> {
  locales.value = await localesApi.list({ tenant: tenant.value, project: projectId.value });
}
/**
 * The quality summary, on its own and never blocking the project
 * (RFC 0005 §8). A failure here is not a broken project: it means the
 * numbers are unknown, so every surface reading them says "not
 * measured" rather than showing a zero.
 */
async function reloadHealth(): Promise<void> {
  healthState.value = "loading";
  try {
    health.value = await summaryPort.summary({ tenant: tenant.value, project: projectId.value });
    healthState.value = "ready";
  } catch (e) {
    health.value = undefined;
    healthState.value = notReported(e) ? "unreported" : "failed";
  }
}

watch(
  [tenant, projectId],
  async () => {
    error.value = null;
    project.value = undefined;
    health.value = undefined;
    healthState.value = "loading";
    void reloadHealth();
    try {
      await Promise.all([reloadProject(), reloadLocales()]);
    } catch (e) {
      error.value = e;
    }
  },
  { immediate: true },
);

const ctx: ProjectContext = {
  tenant,
  projectId,
  project,
  etag,
  locales,
  targets: computed(() => locales.value.filter((l) => !l.is_source)),
  grant,
  health,
  healthState,
  reloadProject,
  reloadLocales,
  reloadHealth,
};
provide(PROJECT, ctx);

const ALL_TABS: ReadonlyArray<{ name: string; label: string; needs?: Permission }> = [
  { name: "translate", label: strings.nav.translate },
  { name: "review", label: strings.nav.review },
  { name: "terms", label: strings.nav.termbase },
  { name: "style", label: strings.nav.style },
  { name: "locales", label: strings.nav.locales },
  { name: "quality", label: strings.nav.quality },
  { name: "releases", label: strings.nav.releases },
  // RFC 0006 §2: offered to whoever may read workflows, as the API asks.
  { name: "project-workflow", label: strings.nav.workflow, needs: "workflows.read" },
  { name: "files", label: strings.nav.files },
  { name: "ai", label: strings.nav.ai },
  { name: "settings", label: strings.nav.settings },
];
const tabs = computed(() => ALL_TABS.filter((t) => !t.needs || allows(grant.value, t.needs)));
/** Routes that live under a tab without being its own. */
const UNDER: Record<string, readonly string[]> = {
  releases: ["release", "release-requests", "release-request", "environment"],
  files: ["import", "import-job"],
  quality: ["check-policy", "waivers"],
  "project-workflow": ["workflow-instances", "workflow-instance"],
};
const activeUnder = (tab: string) => UNDER[tab]?.includes(String(route.name)) ?? false;

/**
 * The one number the navigation carries (RFC 0005 §8). One, not seven:
 * a dashboard nobody reads is worse than a check that fails.
 *
 * `undefined` — nothing has been checked, or the summary is not
 * readable — shows no badge at all. A measured zero *does* show, as
 * "0", so that a missing badge can only ever mean "not measured" and
 * never "you are fine".
 */
const errors = computed(() => openErrors(health.value?.project.findings));
</script>

<template>
  <div class="project-layout">
    <div class="project-bar">
      <nav :aria-label="strings.nav.breadcrumb" class="crumbs">
        <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ strings.nav.projects }}</RouterLink>
        <span aria-hidden="true">/</span>
        <span class="current" aria-current="page">{{ project?.name ?? "…" }}</span>
      </nav>
      <nav :aria-label="strings.nav.projectSections" class="tabs">
        <RouterLink
          v-for="t in tabs"
          :key="t.name"
          :to="{ name: t.name, params: { tenant, project: projectId } }"
          class="tab"
          :class="{ 'router-link-active': activeUnder(t.name) }"
        >
          {{ t.label }}
          <span
            v-if="t.name === 'quality' && errors !== undefined"
            class="pill badge"
            :class="errors > 0 ? 'pill-err' : 'pill-ok'"
            data-testid="nav-open-errors"
          >
            <!-- The digit is for the eye; the sentence beside it is what a screen reader reads out. -->
            <span aria-hidden="true">{{ errors.toLocaleString() }}</span>
            <span class="visually-hidden">{{ strings.health.navErrors(errors) }}</span>
          </span>
        </RouterLink>
      </nav>
    </div>
    <div v-if="error" class="page"><ErrorAlert :error="error" /></div>
    <RouterView v-else-if="project" />
    <p v-else class="page muted" role="status">{{ strings.app.loading }}</p>
  </div>
</template>

<style scoped>
.project-layout {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-block-size: 0;
}
.project-bar {
  display: flex;
  align-items: center;
  gap: var(--kl-space-6);
  padding: 0 var(--kl-space-4);
  border-block-end: 1px solid var(--kl-border);
  flex-wrap: wrap;
}
.crumbs {
  display: flex;
  gap: var(--kl-space-2);
  align-items: center;
  padding-block: var(--kl-space-3);
}
.crumbs a {
  color: var(--kl-ink-secondary);
}
.current {
  font-weight: var(--kl-weight-semibold);
}
.tabs {
  display: flex;
  gap: var(--kl-space-1);
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: var(--kl-space-2);
  padding: var(--kl-space-3) var(--kl-space-3);
  color: var(--kl-ink-secondary);
  text-decoration: none;
  border-block-end: 2px solid transparent;
  margin-block-end: -1px;
}
.tab:hover {
  color: var(--kl-ink);
}
.badge {
  font-size: var(--kl-text-xs, 0.75rem);
  font-variant-numeric: tabular-nums;
}
.tab.router-link-active {
  color: var(--kl-ink);
  border-block-end-color: var(--kl-accent);
  font-weight: var(--kl-weight-medium);
}
</style>
