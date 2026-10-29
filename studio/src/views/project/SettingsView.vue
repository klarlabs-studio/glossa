<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { applications as appsApi, projects } from "../../api/endpoints";
import { isApiError } from "../../api/errors";
import type { Application, Platform } from "../../api/schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import CheckPolicyCard from "../../components/project/CheckPolicyCard.vue";
import PreviewOrigins from "../../components/project/PreviewOrigins.vue";
import DeliveryKeys from "../../components/releases/DeliveryKeys.vue";
import { SLUG_PATTERN, slugify } from "../../lib/slug";
import { allows } from "../../session/permissions";
import { strings } from "../../strings";
import { useProject } from "./context";

const router = useRouter();
const { tenant, projectId, project, etag, grant, reloadProject } = useProject();
const s = strings.settings;
const projectRef = () => ({ tenant: tenant.value, project: projectId.value });
const canWrite = computed(() => allows(grant.value, "catalog.write"));
const canDelete = computed(() => allows(grant.value, "tenant.manage"));
const canReadReleases = computed(() => allows(grant.value, "releases.read"));
// Everyone who can see the project sees where its editor may run; only
// tokens.manage changes the list (RFC 0004 §5.2).
const canSeeOrigins = computed(() => allows(grant.value, "catalog.read"));

// ── general ─────────────────────────────────────────────────────────
const form = reactive({ name: "", slug: "", review_required: true, default_syntax: "mf1" as "mf1" | "mf2" });
watch(
  project,
  (p) => {
    if (!p) return;
    Object.assign(form, { name: p.name, slug: p.slug, ...p.settings });
  },
  { immediate: true },
);
const saving = ref(false);
const saved = ref(false);
const saveError = ref<unknown>(null);

async function save(): Promise<void> {
  if (!etag.value) return;
  saving.value = true;
  saved.value = false;
  saveError.value = null;
  try {
    const r = await projects.update(
      projectRef(),
      { name: form.name.trim(), slug: form.slug, settings: { review_required: form.review_required, default_syntax: form.default_syntax } },
      etag.value,
    );
    project.value = r.value;
    etag.value = r.etag;
    saved.value = true;
  } catch (e) {
    saveError.value = isApiError(e, "precondition_failed") ? new Error(s.changedElsewhere) : e;
    if (isApiError(e, "precondition_failed")) await reloadProject();
  } finally {
    saving.value = false;
  }
}

// ── applications ────────────────────────────────────────────────────
const apps = ref<Application[]>([]);
const appError = ref<unknown>(null);
const appForm = reactive({ name: "", platform: "web" as Platform });
const platforms: Platform[] = ["web", "api", "ios", "android", "other"];

async function loadApps(): Promise<void> {
  try {
    apps.value = await appsApi.list(projectRef());
  } catch (e) {
    appError.value = e;
  }
}
watch(projectId, loadApps, { immediate: true });

async function addApp(): Promise<void> {
  appError.value = null;
  try {
    await appsApi.create(projectRef(), { name: appForm.name.trim(), slug: slugify(appForm.name), platform: appForm.platform });
    appForm.name = "";
    await loadApps();
  } catch (e) {
    appError.value = e;
  }
}

async function removeApp(a: Application): Promise<void> {
  appError.value = null;
  try {
    await appsApi.remove({ ...projectRef(), application: a.id });
    await loadApps();
  } catch (e) {
    appError.value = e;
  }
}

// ── delete ──────────────────────────────────────────────────────────
const confirmSlug = ref("");
const deleteError = ref<unknown>(null);
async function deleteProject(): Promise<void> {
  deleteError.value = null;
  try {
    await projects.remove(projectRef(), etag.value);
    await router.push({ name: "projects", params: { tenant: tenant.value } });
  } catch (e) {
    deleteError.value = e;
  }
}
</script>

<template>
  <div class="page stack">
    <h1>{{ s.title }}</h1>

    <section class="card stack" aria-labelledby="general-h">
      <h2 id="general-h">{{ s.general }}</h2>
      <ErrorAlert :error="saveError" />
      <form class="grid" @submit.prevent="save">
        <div class="field">
          <label for="s-name">{{ strings.projects.name }}</label>
          <input id="s-name" v-model="form.name" maxlength="200" required :disabled="!canWrite" />
        </div>
        <div class="field">
          <label for="s-slug">{{ strings.projects.slug }}</label>
          <input id="s-slug" v-model="form.slug" class="mono" :pattern="SLUG_PATTERN" maxlength="63" required :disabled="!canWrite" />
        </div>
        <div class="field">
          <label for="s-source">{{ strings.projects.sourceLocale }}</label>
          <input id="s-source" :value="project?.source_locale" class="mono" disabled aria-describedby="s-source-hint" />
          <span id="s-source-hint" class="hint">{{ strings.projects.sourceLocaleHint }}</span>
        </div>
        <div class="field">
          <label for="s-syntax">{{ strings.projects.defaultSyntax }}</label>
          <select id="s-syntax" v-model="form.default_syntax" :disabled="!canWrite">
            <option value="mf1">{{ strings.projects.syntaxMf1 }}</option>
            <option value="mf2">{{ strings.projects.syntaxMf2 }}</option>
          </select>
        </div>
        <label class="check wide">
          <input v-model="form.review_required" type="checkbox" :disabled="!canWrite" aria-describedby="s-review-hint" />
          <span class="stack-sm">
            <span>{{ strings.projects.reviewRequired }}</span>
            <span id="s-review-hint" class="hint">{{ strings.projects.reviewRequiredHint }}</span>
          </span>
        </label>
        <div v-if="canWrite" class="row wide">
          <button type="submit" class="btn btn-primary" :disabled="saving">{{ saving ? strings.app.saving : strings.app.save }}</button>
          <span v-if="saved" class="pill pill-ok" role="status">{{ s.saved }}</span>
        </div>
      </form>
    </section>

    <section class="card stack" aria-labelledby="apps-h">
      <h2 id="apps-h">{{ s.applications }}</h2>
      <p class="muted">{{ s.applicationsLead }}</p>
      <ErrorAlert :error="appError" />
      <table v-if="apps.length" class="table">
        <thead>
          <tr>
            <th scope="col">{{ strings.projects.name }}</th>
            <th scope="col">{{ strings.projects.slug }}</th>
            <th scope="col">{{ s.platform }}</th>
            <th scope="col"><span class="visually-hidden">{{ strings.app.remove }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in apps" :key="a.id">
            <td>{{ a.name }}</td>
            <td><code>{{ a.slug }}</code></td>
            <td>{{ a.platform }}</td>
            <td class="actions">
              <button v-if="canWrite" type="button" class="btn btn-sm btn-ghost" :aria-label="`${strings.app.remove} ${a.name}`" @click="removeApp(a)">
                {{ strings.app.remove }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted">{{ s.noApplications }}</p>
      <form v-if="canWrite" class="row app-form" @submit.prevent="addApp">
        <div class="field">
          <label for="a-name">{{ strings.projects.name }}</label>
          <input id="a-name" v-model="appForm.name" maxlength="200" required />
        </div>
        <div class="field">
          <label for="a-platform">{{ s.platform }}</label>
          <select id="a-platform" v-model="appForm.platform">
            <option v-for="p in platforms" :key="p" :value="p">{{ p }}</option>
          </select>
        </div>
        <button type="submit" class="btn">{{ s.addApplication }}</button>
      </form>
    </section>

    <CheckPolicyCard />

    <PreviewOrigins v-if="canSeeOrigins" />

    <DeliveryKeys v-if="canReadReleases" />

    <section v-if="canDelete" class="card stack danger" aria-labelledby="danger-h">
      <h2 id="danger-h">{{ s.danger }}</h2>
      <p class="muted">{{ s.dangerLead }}</p>
      <ErrorAlert :error="deleteError" />
      <form class="row app-form" @submit.prevent="deleteProject">
        <div class="field">
          <label for="d-confirm">{{ s.deleteConfirm(project?.slug ?? "") }}</label>
          <input id="d-confirm" v-model="confirmSlug" class="mono" autocomplete="off" />
        </div>
        <button type="submit" class="btn btn-danger" :disabled="confirmSlug !== project?.slug">{{ s.deleteProject }}</button>
      </form>
    </section>
  </div>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
  gap: var(--kl-space-4);
}
.wide {
  grid-column: 1 / -1;
}
.actions {
  text-align: end;
}
.app-form {
  align-items: flex-end;
}
.danger {
  border-color: var(--gs-err);
}
</style>
