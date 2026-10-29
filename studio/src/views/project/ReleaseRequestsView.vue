<script setup lang="ts">
/**
 * A project's release requests (RFC 0006 §5.1): every publish or promote
 * an approval requirement held, newest first, filtered by environment and
 * state — both kept in the address, so a filtered list can be linked to.
 *
 * A failed read says so; it is never shown as "no requests".
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useReleaseOps } from "../../api/release-ops";
import { ReleaseRequestState, type ReleaseRequest } from "../../api/release-ops-schemas";
import { useReleases } from "../../api/releases";
import type { Environment } from "../../api/schemas";
import { sortEnvironments } from "../../lib/releases";
import { absoluteTime, relativeTime } from "../../lib/time";
import { usePeople } from "../../session/people";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const ro = strings.releaseOps;
const s = strings.releases;
const route = useRoute();
const router = useRouter();
const ops = useReleaseOps();
const releases = useReleases();
const { tenant, projectId } = useProject();
const person = usePeople(() => tenant.value);
const project = () => ({ tenant: tenant.value, project: projectId.value });

const STATES = ReleaseRequestState.options;
const q = (k: string) => (typeof route.query[k] === "string" ? (route.query[k] as string) : "");
const environment = computed(() => q("environment"));
const state = computed(() => (STATES as readonly string[]).includes(q("state")) ? (q("state") as ReleaseRequestState) : undefined);

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const items = shallowRef<ReleaseRequest[]>([]);
const environments = shallowRef<Environment[]>([]);
const versions = shallowRef(new Map<string, number>());
let seq = 0;

async function load(): Promise<void> {
  const mine = ++seq;
  phase.value = "loading";
  loadError.value = null;
  try {
    const list = await ops.releaseRequests(project(), { ...(environment.value ? { environment: environment.value } : {}), ...(state.value ? { state: state.value } : {}) });
    if (mine !== seq) return;
    items.value = list;
    phase.value = "ready";
    void labelReleases(list);
  } catch (e) {
    if (mine !== seq) return;
    items.value = [];
    loadError.value = e;
    phase.value = "failed";
  }
}

/** Release versions for the labels, best effort: an unknown one shows "…". */
async function labelReleases(list: readonly ReleaseRequest[]): Promise<void> {
  const missing = [...new Set(list.map((r) => r.release_id))].filter((id) => !versions.value.has(id));
  const got = await Promise.allSettled(missing.map((id) => releases.release(project(), id)));
  const m = new Map(versions.value);
  for (const g of got) if (g.status === "fulfilled") m.set(g.value.id, g.value.version);
  versions.value = m;
}

watch([projectId, environment, state], load, { immediate: true });
watch(
  projectId,
  async () => {
    environments.value = sortEnvironments(await releases.environments(project()).catch(() => [])).filter((e) => e.kind !== "branch");
  },
  { immediate: true },
);

function setFilter(key: "environment" | "state", value: string): void {
  const query = { ...route.query };
  if (value) query[key] = value;
  else delete query[key];
  void router.replace({ query });
}
const label = (id: string) => {
  const v = versions.value.get(id);
  return v === undefined ? "…" : s.version(v);
};
const detail = (id: string) => ({ name: "release-request", params: { tenant: tenant.value, project: projectId.value, request: id } });
</script>

<template>
  <div class="page stack requests">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'releases', params: { tenant, project: projectId } }" class="muted">{{ ro.backToReleases }}</RouterLink>
        <h1>{{ ro.requestsTitle }}</h1>
        <p class="muted lead">{{ ro.requestsLead }}</p>
      </div>
    </div>

    <div class="row filters" role="group" :aria-label="ro.requestsTitle">
      <div class="field">
        <label for="rr-env">{{ ro.filterEnvironment }}</label>
        <select id="rr-env" :value="environment" data-testid="filter-environment" @change="setFilter('environment', ($event.target as HTMLSelectElement).value)">
          <option value="">{{ ro.anyEnvironment }}</option>
          <option v-if="environment && !environments.some((e) => e.name === environment)" :value="environment">{{ environment }}</option>
          <option v-for="e in environments" :key="e.name" :value="e.name">{{ e.name }}</option>
        </select>
      </div>
      <div class="field">
        <label for="rr-state">{{ ro.filterState }}</label>
        <select id="rr-state" :value="state ?? ''" data-testid="filter-state" @change="setFilter('state', ($event.target as HTMLSelectElement).value)">
          <option value="">{{ ro.anyState }}</option>
          <option v-for="st in STATES" :key="st" :value="st">{{ ro.state[st] }}</option>
        </select>
      </div>
    </div>

    <p v-if="phase === 'loading'" class="muted" role="status" data-testid="requests-loading">{{ ro.requestsLoading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="requests-failed">
      <p class="alert-title">{{ ro.requestsFailed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ ro.retry }}</button></div>
    </div>
    <p v-else-if="!items.length" class="card muted" data-testid="requests-empty">{{ ro.requestsEmpty }}</p>
    <div v-else class="scroll">
      <table class="table" data-testid="requests-list">
        <thead>
          <tr>
            <th scope="col">{{ ro.columns.release }}</th>
            <th scope="col">{{ ro.columns.environment }}</th>
            <th scope="col">{{ ro.columns.action }}</th>
            <th scope="col">{{ ro.columns.requester }}</th>
            <th scope="col">{{ ro.columns.state }}</th>
            <th scope="col">{{ ro.columns.when }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in items" :key="r.id" :data-request="r.id">
            <th scope="row">
              <RouterLink :to="detail(r.id)">{{ ro.requestTitle(label(r.release_id), r.environment) }}</RouterLink>
            </th>
            <td>{{ r.environment }}</td>
            <td>{{ ro.action[r.action] ?? r.action }}</td>
            <td>{{ person(r.requester) }}</td>
            <td>
              <span class="pill" :class="`pill-${ro.stateTone[r.state] ?? 'neutral'}`" data-testid="request-state">{{ ro.state[r.state] ?? r.state }}</span>
              <span v-if="r.forced" class="pill pill-err forced" data-testid="request-forced">{{ ro.forcedMarker }}</span>
            </td>
            <td><time :datetime="r.created_at" :title="absoluteTime(r.created_at)">{{ relativeTime(r.created_at) }}</time></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.requests {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.filters {
  align-items: flex-end;
}
.scroll {
  overflow-x: auto;
}
.forced {
  margin-inline-start: var(--kl-space-1);
}
</style>
