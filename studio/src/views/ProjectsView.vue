<script setup lang="ts">
/**
 * The projects list, with the per-locale quality row RFC 0005 §8 asks
 * for: coverage, open errors and queue age per language, on each card.
 *
 * The summaries are read per project, after the list, and each one
 * fails on its own: a project whose numbers cannot be read says "not
 * measured" on its card while the others show theirs. Nothing here
 * blocks the list, and nothing here shows a zero it did not measure.
 */
import { computed, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { projects as projectsApi } from "../api/endpoints";
import { useQualitySummary } from "../api/quality-summary";
import type { QualitySummary } from "../api/quality-summary-schemas";
import type { Project } from "../api/schemas";
import ErrorAlert from "../components/ErrorAlert.vue";
import LocaleInput from "../components/LocaleInput.vue";
import ProjectLocaleRows from "../components/project/ProjectLocaleRows.vue";
import { checkLocale } from "../lib/bcp47";
import { SLUG_PATTERN, slugify } from "../lib/slug";
import { allows } from "../session/permissions";
import { membershipFor, useGrant } from "../session/session";
import { strings } from "../strings";

const route = useRoute();
const router = useRouter();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const canCreate = computed(() => allows(grant.value, "catalog.write"));
const tenantName = computed(() => membershipFor(tenant.value)?.tenant.name ?? "");
/** RFC 0006: workflows are read with `workflows.read`; groups and vendors exist only in an organization (an individual tenant is refused `individual_tenant`). */
const canReadWorkflows = computed(() => allows(grant.value, "workflows.read"));
const canReadAudit = computed(() => allows(grant.value, "audit.read"));
const isOrganization = computed(() => membershipFor(tenant.value)?.tenant.kind === "organization" && allows(grant.value, "members.read"));

const list = ref<Project[]>([]);
const loading = ref(true);
const loadError = ref<unknown>(null);

const summaryPort = useQualitySummary();
interface Health {
  summary: QualitySummary | undefined;
  state: "loading" | "ready" | "unavailable";
}
const health = ref(new Map<string, Health>());
const healthOf = (id: string): Health => health.value.get(id) ?? { summary: undefined, state: "loading" };

/** One summary per project, in parallel, each failing on its own. */
async function loadHealth(projects: Project[]): Promise<void> {
  health.value = new Map(projects.map((p) => [p.id, { summary: undefined, state: "loading" as const }]));
  await Promise.all(
    projects.map(async (p) => {
      let next: Health;
      try {
        next = { summary: await summaryPort.summary({ tenant: tenant.value, project: p.id }), state: "ready" };
      } catch {
        next = { summary: undefined, state: "unavailable" };
      }
      health.value = new Map(health.value).set(p.id, next);
    }),
  );
}

async function load(): Promise<void> {
  loading.value = true;
  loadError.value = null;
  try {
    list.value = await projectsApi.list(tenant.value);
    void loadHealth(list.value);
  } catch (e) {
    loadError.value = e;
  } finally {
    loading.value = false;
  }
}
watch(tenant, load, { immediate: true });

const creating = ref(false);
const name = ref("");
const slug = ref("");
const slugTouched = ref(false);
const sourceLocale = ref("en");
const syntax = ref<"mf1" | "mf2">("mf1");
const reviewRequired = ref(true);
const busy = ref(false);
const createError = ref<unknown>(null);
watch(name, (n) => {
  if (!slugTouched.value) slug.value = slugify(n);
});

async function create(): Promise<void> {
  const locale = checkLocale(sourceLocale.value);
  if (!locale.ok) return;
  createError.value = null;
  busy.value = true;
  try {
    const { value: p } = await projectsApi.create(tenant.value, {
      name: name.value.trim(),
      slug: slug.value,
      source_locale: locale.tag,
      settings: { default_syntax: syntax.value, review_required: reviewRequired.value },
    });
    await router.push({ name: "locales", params: { tenant: tenant.value, project: p.id } });
  } catch (e) {
    createError.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div class="stack-sm">
        <p class="muted">{{ tenantName }}</p>
        <h1>{{ strings.projects.title }}</h1>
      </div>
      <div class="row">
        <RouterLink class="btn" :to="{ name: 'workspace-knowledge', params: { tenant } }" :title="strings.tenantSettings.settingsLead">
          {{ strings.nav.workspaceSettings }}: {{ strings.tenantSettings.knowledgeLink }}
        </RouterLink>
        <RouterLink class="btn" :to="{ name: 'workspace-github', params: { tenant } }" :title="strings.tenantSettings.settingsLead">
          {{ strings.nav.workspaceSettings }}: {{ strings.tenantSettings.githubLink }}
        </RouterLink>
        <RouterLink v-if="canReadWorkflows" class="btn" :to="{ name: 'workflows', params: { tenant } }" :title="strings.tenantSettings.settingsLead" data-testid="nav-workflows">
          {{ strings.nav.workspaceSettings }}: {{ strings.nav.workflows }}
        </RouterLink>
        <RouterLink v-if="isOrganization" class="btn" :to="{ name: 'groups', params: { tenant } }" :title="strings.tenantSettings.settingsLead" data-testid="nav-groups">
          {{ strings.nav.workspaceSettings }}: {{ strings.nav.groups }}
        </RouterLink>
        <RouterLink v-if="isOrganization" class="btn" :to="{ name: 'vendors', params: { tenant } }" :title="strings.tenantSettings.settingsLead" data-testid="nav-vendors">
          {{ strings.nav.workspaceSettings }}: {{ strings.nav.vendors }}
        </RouterLink>
        <RouterLink v-if="canReadAudit" class="btn" :to="{ name: 'audit-log', params: { tenant } }" :title="strings.tenantSettings.settingsLead" data-testid="nav-audit">
          {{ strings.nav.workspaceSettings }}: {{ strings.nav.audit }}
        </RouterLink>
        <button v-if="canCreate && !creating" type="button" class="btn btn-primary" @click="creating = true">{{ strings.projects.create }}</button>
      </div>
    </div>

    <section v-if="creating" class="card stack create" aria-labelledby="create-h">
      <h2 id="create-h">{{ strings.projects.createTitle }}</h2>
      <ErrorAlert :error="createError" />
      <form class="grid" @submit.prevent="create">
        <div class="field">
          <label for="p-name">{{ strings.projects.name }}</label>
          <input id="p-name" v-model="name" maxlength="200" required />
        </div>
        <div class="field">
          <label for="p-slug">{{ strings.projects.slug }}</label>
          <input id="p-slug" v-model="slug" class="mono" :pattern="SLUG_PATTERN" maxlength="63" required aria-describedby="p-slug-hint" @input="slugTouched = true" />
          <span id="p-slug-hint" class="hint">{{ strings.tenants.slugHint }}</span>
        </div>
        <LocaleInput id="p-source" v-model="sourceLocale" :label="strings.projects.sourceLocale" :hint="strings.projects.sourceLocaleHint" required />
        <div class="field">
          <label for="p-syntax">{{ strings.projects.defaultSyntax }}</label>
          <select id="p-syntax" v-model="syntax">
            <option value="mf1">{{ strings.projects.syntaxMf1 }}</option>
            <option value="mf2">{{ strings.projects.syntaxMf2 }}</option>
          </select>
        </div>
        <label class="check wide">
          <input v-model="reviewRequired" type="checkbox" aria-describedby="p-review-hint" />
          <span class="stack-sm">
            <span>{{ strings.projects.reviewRequired }}</span>
            <span id="p-review-hint" class="hint">{{ strings.projects.reviewRequiredHint }}</span>
          </span>
        </label>
        <div class="row wide">
          <button type="submit" class="btn btn-primary" :disabled="busy">{{ strings.projects.submit }}</button>
          <button type="button" class="btn btn-ghost" @click="creating = false">{{ strings.app.cancel }}</button>
        </div>
      </form>
    </section>

    <ErrorAlert :error="loadError" />
    <p v-if="loading" role="status" class="muted">{{ strings.app.loading }}</p>
    <p v-else-if="!loadError && list.length === 0" class="card muted">{{ strings.projects.empty }}</p>
    <ul v-else class="projects" role="list">
      <li v-for="p in list" :key="p.id" class="project card stack-sm" data-testid="project-card" :data-project="p.id">
        <RouterLink class="p-name" :to="{ name: 'translate', params: { tenant, project: p.id } }">{{ p.name }}</RouterLink>
        <p class="p-meta">
          <span class="mono muted">{{ p.slug }}</span>
          <span class="pill pill-neutral">{{ strings.projects.source(p.source_locale) }}</span>
        </p>
        <ProjectLocaleRows :project="p.id" :summary="healthOf(p.id).summary" :state="healthOf(p.id).state" />
      </li>
    </ul>
  </div>
</template>

<style scoped>
.create {
  margin-block-end: var(--kl-space-6);
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
  gap: var(--kl-space-4);
}
.wide {
  grid-column: 1 / -1;
}
.projects {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(24rem, 1fr));
  gap: var(--kl-space-4);
}
.project {
  align-items: flex-start;
  transition: border-color var(--kl-duration-fast) var(--kl-ease-default);
}
.project:hover {
  border-color: var(--kl-accent-border);
}
.p-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
.p-name {
  font-weight: var(--kl-weight-semibold);
  font-size: var(--kl-text-md);
  color: var(--kl-ink);
}
</style>
