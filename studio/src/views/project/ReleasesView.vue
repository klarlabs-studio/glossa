<script setup lang="ts">
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink } from "vue-router";
import { useReleaseOps } from "../../api/release-ops";
import type { Rollout } from "../../api/release-ops-schemas";
import { useReleases } from "../../api/releases";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { createReleaseBook } from "../../components/releases/book";
import DeploymentsDialog from "../../components/releases/DeploymentsDialog.vue";
import EnvironmentCard from "../../components/releases/EnvironmentCard.vue";
import PolicyDialog from "../../components/releases/PolicyDialog.vue";
import PromoteDialog from "../../components/releases/PromoteDialog.vue";
import PublishDialog from "../../components/releases/PublishDialog.vue";
import RollbackDialog from "../../components/releases/RollbackDialog.vue";
import { usePartyNames } from "../../components/releases/party-names";
import { activeRollout } from "../../lib/release-ops";
import { servingMap } from "../../lib/releases";
import { useShortcuts } from "../../lib/shortcuts";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "./context";

const port = useReleases();
const { tenant, projectId, grant } = useProject();
const s = strings.releases;
const c = s.columns;
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canPublish = computed(() => allows(grant.value, "releases.publish"));
const canGovern = computed(() => allows(grant.value, "workflows.manage"));
const ops = useReleaseOps();
const ro = strings.releaseOps;
const partyNames = usePartyNames(() => tenant.value);
const envRoute = (environment: string) => ({ name: "environment", params: { tenant: tenant.value, project: projectId.value, environment } });
const requestsRoute = (environment?: string) => ({
  name: "release-requests",
  params: { tenant: tenant.value, project: projectId.value },
  query: environment ? { environment } : {},
});

/**
 * RFC 0006 §5, best effort: pending release requests per environment and
 * each environment's running rollout. Neither blocks the page; a failure
 * only leaves the summaries out.
 */
const pending = shallowRef<Map<string, number>>();
const rollouts = shallowRef(new Map<string, Rollout>());
async function loadOps(): Promise<void> {
  const p = project();
  const envs = book.environments.value.filter((e) => e.kind !== "branch");
  const [reqs, ...rs] = await Promise.allSettled([ops.releaseRequests(p, { state: "pending" }), ...envs.map((e) => ops.rollouts(p, e.name))]);
  if (reqs.status === "fulfilled") {
    const m = new Map<string, number>();
    for (const r of reqs.value) m.set(r.environment, (m.get(r.environment) ?? 0) + 1);
    pending.value = m;
  } else pending.value = undefined;
  const active = new Map<string, Rollout>();
  rs.forEach((r, i) => {
    const a = r.status === "fulfilled" ? activeRollout(r.value) : undefined;
    if (a) active.set(envs[i]!.name, a);
  });
  rollouts.value = active;
  await book.ensure([...active.values()].map((r) => r.release_id)).catch(() => undefined);
}
const pendingTotal = computed(() => (pending.value ? [...pending.value.values()].reduce((a, b) => a + b, 0) : undefined));
const person = usePeople(() => tenant.value);
const book = createReleaseBook(port, project);
const serving = computed(() => servingMap(book.environments.value));
const releaseRoute = (id: string) => ({ name: "release", params: { tenant: tenant.value, project: projectId.value, release: id } });

const error = ref<unknown>(null);
const status = ref("");

async function load(): Promise<void> {
  error.value = null;
  try {
    await book.load();
    void loadOps();
  } catch (e) {
    error.value = e;
  }
}
watch(projectId, load, { immediate: true });

async function more(): Promise<void> {
  try {
    await book.more();
  } catch (e) {
    error.value = e;
  }
}

// ── dialogs ─────────────────────────────────────────────────────────
type Dialog =
  | { kind: "publish"; environment?: string | undefined }
  | { kind: "promote"; environment?: string | undefined; releaseId?: string | undefined }
  | { kind: "rollback" | "history" | "policy"; environment: string };
const dialog = ref<Dialog | null>(null);
const envOf = (d: Dialog | null) => (d && "environment" in d ? d.environment ?? "" : "");

function open(d: Dialog): void {
  status.value = "";
  dialog.value = d;
}
function close(): void {
  dialog.value = null;
}
async function changed(message: string): Promise<void> {
  dialog.value = null;
  status.value = message;
  await load();
}

useShortcuts({
  publish: () => {
    if (!canPublish.value || !book.loaded.value) return false;
    open({ kind: "publish" });
  },
});
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <div class="row">
        <RouterLink class="btn" :to="requestsRoute()" data-testid="requests-link">
          {{ ro.requestsLink }}<template v-if="pendingTotal"> · {{ ro.pendingCount(pendingTotal) }}</template>
        </RouterLink>
        <button v-if="canPublish" type="button" class="btn btn-primary" :disabled="!book.loaded.value" aria-keyshortcuts="p" @click="open({ kind: 'publish' })">
          {{ s.publish }}
        </button>
      </div>
    </div>
    <p v-if="!canPublish" class="alert" data-testid="releases-read-only">{{ s.readOnly }}</p>
    <ErrorAlert :error="error" />
    <p v-if="status" class="alert alert-ok" role="status" data-testid="releases-status">{{ status }}</p>

    <section class="stack" aria-labelledby="envs-h">
      <h2 id="envs-h">{{ s.environments }}</h2>
      <p v-if="!book.loaded.value && !error" class="muted" role="status">{{ strings.app.loading }}</p>
      <div class="envs">
        <EnvironmentCard
          v-for="e in book.environments.value"
          :key="e.name"
          :env="e"
          :release="book.byId.value.get(e.current_release_id ?? '')"
          :latest="book.latest.value.get(e.name)"
          :can-publish="canPublish"
          :person="person"
          :release-route="releaseRoute"
          :env-route="e.kind === 'branch' ? undefined : envRoute(e.name)"
          :requests-route="requestsRoute(e.name)"
          :pending="pending?.get(e.name)"
          :rollout="rollouts.get(e.name)"
          :rollout-label="book.label(rollouts.get(e.name)?.release_id)"
          :party-names="partyNames"
          @promote="open({ kind: 'promote', environment: e.name })"
          @rollback="open({ kind: 'rollback', environment: e.name })"
          @history="open({ kind: 'history', environment: e.name })"
          @policy="open({ kind: 'policy', environment: e.name })"
        />
      </div>
    </section>

    <section class="stack" aria-labelledby="rels-h">
      <h2 id="rels-h">{{ s.releases }}</h2>
      <p v-if="book.loaded.value && !book.releases.value.length" class="muted">{{ s.noReleases }}</p>
      <div v-else-if="book.releases.value.length" class="scroll">
        <table class="table" data-testid="release-list">
          <thead>
            <tr>
              <th scope="col">{{ c.release }}</th>
              <th scope="col">{{ c.builtFor }}</th>
              <th scope="col">{{ c.serving }}</th>
              <th scope="col" class="num">{{ c.locales }}</th>
              <th scope="col" class="num">{{ c.messages }}</th>
              <th scope="col">{{ c.note }}</th>
              <th scope="col">{{ c.published }}</th>
              <th v-if="canPublish" scope="col"><span class="visually-hidden">{{ c.actions }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in book.releases.value" :key="r.id">
              <th scope="row">
                <RouterLink :to="releaseRoute(r.id)">{{ s.version(r.version) }}</RouterLink>
              </th>
              <td>{{ r.environment }}</td>
              <td>
                <span v-for="env in serving.get(r.id) ?? []" :key="env" class="pill pill-accent">{{ env }}</span>
              </td>
              <td class="num">{{ r.locales.length }}</td>
              <td class="num">{{ r.counts.messages.toLocaleString() }}</td>
              <td class="note">{{ r.note }}</td>
              <td>
                <time :datetime="r.created_at" :title="absoluteTime(r.created_at)">{{ relativeTime(r.created_at) }}</time>
                <span class="muted"> · {{ person(r.author) }}</span>
              </td>
              <td v-if="canPublish" class="actions">
                <button type="button" class="btn btn-sm" @click="open({ kind: 'promote', releaseId: r.id })">
                  {{ s.promote }}<span class="visually-hidden">{{ s.ofRelease(s.version(r.version)) }}</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="book.hasMore.value"><button type="button" class="btn" @click="more">{{ s.loadMore }}</button></div>
    </section>

    <PublishDialog
      :open="dialog?.kind === 'publish'"
      :port="port"
      :project="project()"
      :book="book"
      :environment="dialog?.kind === 'publish' ? dialog.environment : undefined"
      @close="close"
      @published="load"
    />
    <PromoteDialog
      :open="dialog?.kind === 'promote'"
      :port="port"
      :project="project()"
      :book="book"
      :environment="dialog?.kind === 'promote' ? dialog.environment : undefined"
      :release-id="dialog?.kind === 'promote' ? dialog.releaseId : undefined"
      @close="close"
      @done="changed"
    />
    <RollbackDialog
      :open="dialog?.kind === 'rollback'"
      :port="port"
      :project="project()"
      :book="book"
      :environment="envOf(dialog)"
      @close="close"
      @done="changed"
    />
    <PolicyDialog
      :open="dialog?.kind === 'policy'"
      :port="port"
      :project="project()"
      :environment="envOf(dialog)"
      :can-govern="canGovern"
      @close="close"
      @done="changed"
    />
    <DeploymentsDialog
      :open="dialog?.kind === 'history'"
      :port="port"
      :project="project()"
      :book="book"
      :environment="envOf(dialog)"
      :person="person"
      :release-route="releaseRoute"
      @close="close"
    />
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: var(--kl-content-md);
}
.envs {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr));
  gap: var(--kl-space-4);
}
.envs > * {
  display: flex;
  flex-direction: column;
}
.scroll {
  overflow-x: auto;
}
.num {
  text-align: end;
  font-variant-numeric: tabular-nums;
}
.note {
  max-inline-size: 18rem;
  overflow-wrap: anywhere;
}
.actions {
  text-align: end;
}
td .pill + .pill {
  margin-inline-start: var(--kl-space-1);
}
</style>
