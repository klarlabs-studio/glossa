<script setup lang="ts">
/**
 * Workspace settings › GitHub (RFC 0004 §6): connect the Glossa GitHub
 * app to an account, and tie its repositories to projects.
 *
 * The install is a round trip. "Connect" asks the server for a
 * single-use state and sends the person to GitHub; GitHub sends them
 * back to this same screen with `state`, `installation_id`, `code` and
 * `setup_action` in the query, and the screen hands those straight to
 * the server, which verifies through the person's own GitHub token
 * that they can actually see the installation before mapping it to the
 * workspace. None of that token passes through Studio, and the query is
 * cleared from the URL as soon as it has been used, so a reload cannot
 * replay it.
 *
 * Connecting needs `integration.manage` (mirrored here; the server
 * decides). Without a GitHub app the deployment answers
 * `github_not_configured`, and the screen says so instead of offering
 * a button that cannot work.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { applications, projects } from "../../api/endpoints";
import { ApiError } from "../../api/errors";
import { useGitHub } from "../../api/github";
import type { GitConnection, GitHubInstallation, GitHubRepository } from "../../api/github-schemas";
import type { Application, Project } from "../../api/schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { allows } from "../../session/permissions";
import { membershipFor, useGrant } from "../../session/session";
import { strings } from "../../strings";

const route = useRoute();
const router = useRouter();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const port = useGitHub();
const g = strings.tenantSettings.github;
const tenantName = computed(() => membershipFor(tenant.value)?.tenant.name ?? "");
const canManage = computed(() => allows(grant.value, "integration.manage"));

// ── state ─────────────────────────────────────────────────────────────
const installations = shallowRef<GitHubInstallation[]>([]);
const connections = shallowRef<GitConnection[]>([]);
const projectList = shallowRef<Project[]>([]);
const loading = ref(false);
const busy = ref(false);
const error = ref<unknown>(null);
const status = ref("");
/** Set when the deployment has no GitHub app: the screen then only explains. */
const unconfigured = ref(false);

const projectName = (id: string) => projectList.value.find((p) => p.id === id)?.name ?? g.unknownProject;

/** Every repository the connected accounts can see, for the picker. */
const repositories = computed(() =>
  installations.value
    .filter((i) => i.state === "active")
    .flatMap((i) => (i.repositories ?? []).map((r) => ({ installation: i.id, repo: r })))
    .sort((a, b) => a.repo.full_name.localeCompare(b.repo.full_name)),
);

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  unconfigured.value = false;
  try {
    const [i, c, p] = await Promise.all([port.installations(tenant.value), port.connections(tenant.value), projects.list(tenant.value)]);
    installations.value = i;
    connections.value = c;
    projectList.value = p;
  } catch (e) {
    if (e instanceof ApiError && e.code === "github_not_configured") unconfigured.value = true;
    else error.value = e;
  } finally {
    loading.value = false;
  }
}
watch(tenant, load, { immediate: true });

// ── the install round trip ────────────────────────────────────────────

async function startInstall(): Promise<void> {
  busy.value = true;
  error.value = null;
  status.value = g.connecting;
  try {
    const intent = await port.startInstall(tenant.value);
    window.location.assign(intent.install_url);
  } catch (e) {
    error.value = e;
    status.value = "";
    busy.value = false;
  }
}

/**
 * finishInstall runs when GitHub sends the person back. The query is
 * removed first, so a reload cannot replay a state the server has
 * already burned, and only then is the callback sent.
 */
async function finishInstall(state: string, installationId: number, code: string, setupAction: string | undefined): Promise<void> {
  busy.value = true;
  status.value = g.finishing;
  error.value = null;
  await router.replace({ name: "workspace-github", params: { tenant: tenant.value } });
  try {
    const installed = await port.completeInstall(tenant.value, { state, installation_id: installationId, code, setup_action: setupAction });
    status.value = g.connected(installed.account_login);
    await load();
  } catch (e) {
    if (e instanceof ApiError && e.code === "github_not_configured") unconfigured.value = true;
    else error.value = e;
    status.value = "";
  } finally {
    busy.value = false;
  }
}

watch(
  () => route.query,
  (q) => {
    const state = typeof q.state === "string" ? q.state : "";
    const installationId = Number(typeof q.installation_id === "string" ? q.installation_id : "");
    const code = typeof q.code === "string" ? q.code : "";
    const setupAction = typeof q.setup_action === "string" ? q.setup_action : undefined;
    if (state && code && Number.isFinite(installationId) && installationId > 0) {
      void finishInstall(state, installationId, code, setupAction);
    }
  },
  { immediate: true },
);

// ── forgetting an account ─────────────────────────────────────────────
const forgetting = ref<GitHubInstallation | null>(null);

async function forget(): Promise<void> {
  const i = forgetting.value;
  if (!i) return;
  busy.value = true;
  error.value = null;
  try {
    await port.forgetInstallation(tenant.value, i.id);
    forgetting.value = null;
    status.value = g.forgot;
    await load();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

// ── connecting a repository ───────────────────────────────────────────
const adding = ref(false);
const chosenRepository = ref("");
const chosenProject = ref("");
const chosenApplication = ref("");
const chosenPath = ref("");
const chosenBranch = ref("");
const applicationList = shallowRef<Application[]>([]);

/** The picked repository, with the installation it belongs to. */
const picked = computed(() => repositories.value.find((r) => String(r.repo.repository_id) === chosenRepository.value));

watch(chosenProject, async (id) => {
  applicationList.value = [];
  chosenApplication.value = "";
  if (!id) return;
  try {
    applicationList.value = await applications.list({ tenant: tenant.value, project: id });
  } catch (e) {
    error.value = e;
  }
});

watch(picked, (p) => {
  // The repository's own default branch is the sensible default; the
  // server uses it anyway when the field is left empty.
  if (p && !chosenBranch.value) chosenBranch.value = p.repo.default_branch;
});

function openAdd(): void {
  chosenRepository.value = "";
  chosenProject.value = "";
  chosenApplication.value = "";
  chosenPath.value = "";
  chosenBranch.value = "";
  error.value = null;
  adding.value = true;
}

const canAdd = computed(() => !!picked.value && !!chosenProject.value && !!chosenApplication.value && !busy.value);

async function add(): Promise<void> {
  const p = picked.value;
  if (!p || !canAdd.value) return;
  busy.value = true;
  error.value = null;
  status.value = g.adding;
  try {
    await port.connect(
      tenant.value,
      {
        installation_id: p.installation,
        repository_id: p.repo.repository_id,
        project_id: chosenProject.value,
        application_id: chosenApplication.value,
        ...(chosenPath.value.trim() ? { path: chosenPath.value.trim() } : {}),
        ...(chosenBranch.value.trim() ? { default_branch: chosenBranch.value.trim() } : {}),
      },
      crypto.randomUUID(),
    );
    adding.value = false;
    status.value = g.added(p.repo.full_name);
    await load();
  } catch (e) {
    error.value = e;
    status.value = "";
  } finally {
    busy.value = false;
  }
}

// ── disconnecting ─────────────────────────────────────────────────────
const removing = ref<GitConnection | null>(null);

async function remove(): Promise<void> {
  const c = removing.value;
  if (!c) return;
  busy.value = true;
  error.value = null;
  try {
    await port.disconnect(tenant.value, c.id);
    removing.value = null;
    status.value = g.removed;
    await load();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

const repoLabel = (r: GitHubRepository) => (r.private ? `${r.full_name} (private)` : r.full_name);
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <nav :aria-label="strings.nav.breadcrumb" class="crumbs">
          <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ g.back }}</RouterLink>
          <span aria-hidden="true">/</span>
          <span>{{ strings.nav.workspaceSettings }}</span>
        </nav>
        <p v-if="tenantName" class="muted">{{ tenantName }}</p>
        <h1>{{ g.title }}</h1>
        <p class="muted lead">{{ g.lead }}</p>
      </div>
    </div>

    <p v-if="unconfigured" class="card muted" data-testid="github-unconfigured">{{ g.notConfigured }}</p>

    <template v-else>
      <section class="card stack" aria-labelledby="gh-accounts-h">
        <h2 id="gh-accounts-h">{{ g.connectTitle }}</h2>
        <p class="hint">{{ g.connectLead }}</p>
        <p v-if="!canManage" class="alert" data-testid="github-no-manage">{{ g.noManageRights }}</p>

        <p v-if="!loading && installations.length === 0" class="muted" data-testid="github-no-installations">{{ g.noInstallations }}</p>
        <div v-else-if="installations.length" class="scroll">
          <table class="table" data-testid="github-installations">
            <thead>
              <tr>
                <th scope="col">{{ g.account }}</th>
                <th scope="col">{{ g.repositories }}</th>
                <th scope="col"><span class="visually-hidden">{{ g.forget }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="i in installations" :key="i.id">
                <td>
                  <div class="stack-sm">
                    <strong>{{ i.account_login }}</strong>
                    <span class="pill" :class="i.state === 'active' ? 'pill-ok' : i.state === 'suspended' ? 'pill-warn' : 'pill-err'">
                      {{ g.installState[i.state] }}
                    </span>
                    <span v-if="i.state === 'suspended'" class="hint">{{ g.suspendedHint }}</span>
                    <span v-else-if="i.state === 'revoked'" class="hint">{{ g.revokedHint }}</span>
                  </div>
                </td>
                <td>
                  <p v-if="i.repositories_unavailable" class="hint">{{ g.repositoriesUnavailable }}</p>
                  <ul v-else class="plain-list">
                    <li v-for="r in i.repositories ?? []" :key="r.repository_id" class="mono">{{ repoLabel(r) }}</li>
                  </ul>
                </td>
                <td>
                  <button
                    v-if="canManage"
                    type="button"
                    class="btn btn-ghost btn-sm"
                    :aria-label="`${g.forget}: ${i.account_login}`"
                    @click="forgetting = i"
                  >
                    {{ g.forget }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="canManage" class="row">
          <button type="button" class="btn btn-primary" :disabled="busy" data-testid="github-connect" @click="startInstall()">{{ g.connect }}</button>
        </div>
      </section>

      <section class="card stack" aria-labelledby="gh-connections-h">
        <h2 id="gh-connections-h">{{ g.connectionsTitle }}</h2>
        <p class="hint">{{ g.connectionsLead }}</p>

        <p v-if="!loading && connections.length === 0" class="muted" data-testid="github-no-connections">{{ g.noConnections }}</p>
        <div v-else-if="connections.length" class="scroll">
          <table class="table" data-testid="github-connections">
            <thead>
              <tr>
                <th scope="col">{{ g.repository }}</th>
                <th scope="col">{{ g.path }}</th>
                <th scope="col">{{ g.project }}</th>
                <th scope="col">{{ g.branch }}</th>
                <th scope="col"><span class="visually-hidden">{{ g.remove }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in connections" :key="c.id">
                <td class="mono">{{ c.repository_name || c.repository_id }}</td>
                <td class="mono">{{ c.path || g.wholeRepository }}</td>
                <td>{{ projectName(c.project_id) }}</td>
                <td class="mono">{{ c.default_branch }}</td>
                <td>
                  <button
                    v-if="canManage"
                    type="button"
                    class="btn btn-ghost btn-sm"
                    :aria-label="`${g.remove}: ${c.repository_name}`"
                    @click="removing = c"
                  >
                    {{ g.remove }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="canManage" class="row">
          <button type="button" class="btn" :disabled="busy || repositories.length === 0" data-testid="github-add" @click="openAdd()">{{ g.add }}</button>
          <span v-if="repositories.length === 0 && installations.length > 0" class="hint">{{ g.noRepositories }}</span>
        </div>
      </section>
    </template>

    <ErrorAlert :error="error" />
    <p class="muted" role="status">{{ status }}</p>

    <ModalDialog :open="adding" :title="g.addTitle" @close="adding = false">
      <form class="stack" :aria-label="g.addTitle" @submit.prevent="add()">
        <div class="field">
          <label for="gh-repo">{{ g.repository }}</label>
          <select id="gh-repo" v-model="chosenRepository" required>
            <option value="">{{ g.chooseRepository }}</option>
            <option v-for="r in repositories" :key="r.repo.repository_id" :value="String(r.repo.repository_id)">{{ repoLabel(r.repo) }}</option>
          </select>
        </div>
        <div class="field">
          <label for="gh-project">{{ g.project }}</label>
          <select id="gh-project" v-model="chosenProject" required>
            <option value="">{{ g.chooseProject }}</option>
            <option v-for="p in projectList" :key="p.id" :value="p.id">{{ p.name }}</option>
          </select>
          <p v-if="projectList.length === 0" class="hint">{{ g.noProjects }}</p>
        </div>
        <div class="field">
          <label for="gh-application">{{ g.application }}</label>
          <select id="gh-application" v-model="chosenApplication" :disabled="!chosenProject" required>
            <option value="">{{ g.chooseApplication }}</option>
            <option v-for="a in applicationList" :key="a.id" :value="a.id">{{ a.name }}</option>
          </select>
          <p v-if="chosenProject && applicationList.length === 0" class="hint">{{ g.noApplications }}</p>
        </div>
        <div class="field">
          <label for="gh-path">{{ g.path }}</label>
          <input id="gh-path" v-model="chosenPath" type="text" aria-describedby="gh-path-hint" placeholder="apps/web" />
          <p id="gh-path-hint" class="hint">{{ g.pathHint }}</p>
        </div>
        <div class="field">
          <label for="gh-branch">{{ g.branch }}</label>
          <input id="gh-branch" v-model="chosenBranch" type="text" aria-describedby="gh-branch-hint" />
          <p id="gh-branch-hint" class="hint">{{ g.branchHint }}</p>
        </div>
      </form>
      <template #actions>
        <button type="button" class="btn btn-ghost" @click="adding = false">{{ g.cancel }}</button>
        <button type="button" class="btn btn-primary" :disabled="!canAdd" data-testid="github-add-confirm" @click="add()">{{ g.add }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="!!forgetting" :title="g.forgetTitle" @close="forgetting = null">
      <p>{{ forgetting ? g.forgetBody(forgetting.account_login) : "" }}</p>
      <template #actions>
        <button type="button" class="btn btn-ghost" @click="forgetting = null">{{ g.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="busy" data-testid="github-forget-confirm" @click="forget()">{{ g.forgetConfirm }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="!!removing" :title="g.removeTitle" @close="removing = null">
      <p>{{ removing ? g.removeBody(removing.repository_name) : "" }}</p>
      <template #actions>
        <button type="button" class="btn btn-ghost" @click="removing = null">{{ g.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="busy" data-testid="github-remove-confirm" @click="remove()">{{ g.removeConfirm }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.plain-list {
  margin: 0;
  padding-left: 1rem;
}
.crumbs {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}
</style>
