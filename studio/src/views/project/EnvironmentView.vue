<script setup lang="ts">
/**
 * One environment (RFC 0006 §5): what it serves and ships, who must
 * approve a release into it, and its staged rollouts — the running one
 * with its share, candidate and stable release, and the history.
 *
 * Advancing changes the share under `If-Match` (another person may have
 * moved it); completing moves the pointer to the candidate; aborting is
 * instant and never waits on a tag — it sends none, so a stale one can
 * never slow it down (§5.2, intent §74.2).
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { isApiError } from "../../api/errors";
import { useReleaseOps } from "../../api/release-ops";
import type { Rollout } from "../../api/release-ops-schemas";
import { newIdempotencyKey, useReleases } from "../../api/releases";
import type { Environment, Release } from "../../api/schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { policyText } from "../../components/releases/book";
import { usePartyNames } from "../../components/releases/party-names";
import PolicyDialog from "../../components/releases/PolicyDialog.vue";
import { activeRollout, DEFAULT_DURATION_DAYS, durationSeconds, requirementText, validPercent } from "../../lib/release-ops";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const ro = strings.releaseOps;
const s = strings.releases;
const route = useRoute();
const ops = useReleaseOps();
const releases = useReleases();
const { tenant, projectId, grant } = useProject();
const person = usePeople(() => tenant.value);
const names = usePartyNames(() => tenant.value);
const project = () => ({ tenant: tenant.value, project: projectId.value });
const name = computed(() => String(route.params.environment));
const canPublish = computed(() => allows(grant.value, "releases.publish"));
const canGovern = computed(() => allows(grant.value, "workflows.manage"));
const STEPS = [1, 5, 10, 25, 50, 100];

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const env = shallowRef<Environment>();
const list = shallowRef<Rollout[]>([]);
const rolloutsError = ref<unknown>(null);
/** The running rollout with its ETag, for `If-Match` on an advance. */
const active = shallowRef<{ rollout: Rollout; etag: string | undefined }>();
const known = shallowRef(new Map<string, Release>());
const candidates = shallowRef<Release[]>([]);
const status = ref("");
const busy = ref(false);
const actionError = ref<unknown>(null);
const heading = useTemplateRef<HTMLHeadingElement>("heading");
/** The share an advance asks for; follows the running rollout's when it is read. */
const percent = ref(10);

async function ensure(ids: ReadonlyArray<string | undefined>): Promise<void> {
  const missing = [...new Set(ids.filter((id): id is string => !!id && !known.value.has(id)))];
  const got = await Promise.allSettled(missing.map((id) => releases.release(project(), id)));
  const m = new Map(known.value);
  for (const g of got) if (g.status === "fulfilled") m.set(g.value.id, g.value);
  known.value = m;
}
const label = (id: string | undefined) => {
  if (!id) return s.noneYet;
  const r = known.value.get(id);
  return r ? s.version(r.version) : "…";
};

async function loadRollouts(): Promise<void> {
  rolloutsError.value = null;
  try {
    list.value = await ops.rollouts(project(), name.value);
    const a = activeRollout(list.value);
    active.value = a ? { rollout: a, etag: (await ops.rollout(project(), name.value, a.id)).etag } : undefined;
    if (a) percent.value = a.percent;
    await ensure(list.value.flatMap((r) => [r.release_id, r.stable_release_id]));
  } catch (e) {
    rolloutsError.value = e;
  }
}

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  try {
    env.value = (await releases.environment(project(), name.value)).value;
    phase.value = "ready";
    await Promise.all([
      loadRollouts(),
      ensure([env.value.current_release_id]),
      releases
        .releases(project())
        .then((page) => (candidates.value = page.items))
        .catch(() => (candidates.value = [])),
    ]);
  } catch (e) {
    loadError.value = e;
    phase.value = "failed";
  }
}
watch([projectId, name], load, { immediate: true });

async function announce(message: string): Promise<void> {
  status.value = message;
  await nextTick();
  heading.value?.focus();
}

// ── advance ─────────────────────────────────────────────────────────
async function advance(): Promise<void> {
  const a = active.value;
  if (!a || busy.value || !validPercent(percent.value)) return;
  busy.value = true;
  actionError.value = null;
  status.value = "";
  try {
    if (!a.etag) throw new Error(ro.stale);
    const r = await ops.advance(project(), name.value, a.rollout.id, percent.value, a.etag);
    active.value = { rollout: r.value, etag: r.etag };
    await loadRollouts();
    await announce(ro.advanced(r.value.percent));
  } catch (e) {
    if (isApiError(e, "precondition_failed")) {
      await loadRollouts();
      await announce(ro.stale);
    } else if (isApiError(e, "rollout_ended")) {
      await loadRollouts();
      actionError.value = e;
    } else actionError.value = e;
  } finally {
    busy.value = false;
  }
}

// ── complete and abort ──────────────────────────────────────────────
const confirming = ref<"complete" | "abort">();
async function end(how: "complete" | "abort"): Promise<void> {
  const a = active.value;
  if (!a || busy.value) return;
  busy.value = true;
  actionError.value = null;
  try {
    // Abort sends no tag: it must never be refused for a stale one.
    if (how === "abort") await ops.abort(project(), name.value, a.rollout.id);
    else await ops.complete(project(), name.value, a.rollout.id, a.etag);
    confirming.value = undefined;
    const candidate = label(a.rollout.release_id);
    await load();
    await announce(how === "abort" ? ro.aborted : ro.completed(name.value, candidate));
  } catch (e) {
    confirming.value = undefined;
    if (isApiError(e, "precondition_failed")) {
      await loadRollouts();
      await announce(ro.stale);
    } else {
      if (isApiError(e, "rollout_ended")) await loadRollouts();
      actionError.value = e;
    }
  } finally {
    busy.value = false;
  }
}

// ── start ───────────────────────────────────────────────────────────
const start = ref({ release_id: "", percent: 10, days: DEFAULT_DURATION_DAYS, force: false, force_reason: "" });
const startError = ref<unknown>(null);
const needsApproval = ref(false);
const policyNotMet = ref(false);
let startKey = newIdempotencyKey();
watch(start, () => (startKey = newIdempotencyKey()), { deep: true });
const startable = computed(() => candidates.value.filter((r) => r.id !== env.value?.current_release_id && r.source_locale === (known.value.get(env.value?.current_release_id ?? "")?.source_locale ?? r.source_locale)));
const duration = computed(() => durationSeconds(start.value.days));
const startValid = computed(
  () => !!start.value.release_id && validPercent(start.value.percent) && duration.value !== undefined && (!start.value.force || !!start.value.force_reason.trim()),
);

async function startRollout(): Promise<void> {
  if (!startValid.value || busy.value) return;
  busy.value = true;
  startError.value = null;
  needsApproval.value = false;
  try {
    const f = start.value;
    const r = await ops.startRollout(
      project(),
      name.value,
      {
        release_id: f.release_id,
        percent: f.percent,
        ...(duration.value !== DEFAULT_DURATION_DAYS * 86_400 ? { max_duration_seconds: duration.value! } : {}),
        ...(f.force ? { force: true, force_reason: f.force_reason.trim() } : {}),
      },
      startKey,
    );
    start.value = { release_id: "", percent: 10, days: DEFAULT_DURATION_DAYS, force: false, force_reason: "" };
    policyNotMet.value = false;
    await loadRollouts();
    await announce(ro.started(label(r.value.release_id), r.value.percent));
  } catch (e) {
    if (isApiError(e, "rollout_needs_approval")) needsApproval.value = true;
    else {
      if (isApiError(e, "policy_not_met")) policyNotMet.value = true;
      if (isApiError(e, "rollout_active")) await loadRollouts();
      startError.value = e;
    }
  } finally {
    busy.value = false;
  }
}

// ── settings ────────────────────────────────────────────────────────
const editing = ref(false);
async function settingsSaved(message: string): Promise<void> {
  editing.value = false;
  await load();
  await announce(message);
}
const requestsRoute = computed(() => ({ name: "release-requests", params: { tenant: tenant.value, project: projectId.value }, query: { environment: name.value } }));
const outcome = (r: Rollout) => (r.status === "active" ? ro.running : (ro.end[r.end ?? r.status] ?? r.status));
</script>

<template>
  <div class="page stack environment">
    <RouterLink :to="{ name: 'releases', params: { tenant, project: projectId } }" class="muted">{{ ro.backToReleases }}</RouterLink>
    <p role="status" aria-live="polite" data-testid="env-status">{{ status }}</p>

    <p v-if="phase === 'loading'" class="muted" data-testid="env-loading">{{ ro.envLoading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="env-failed">
      <p class="alert-title">{{ ro.envFailed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ ro.retry }}</button></div>
    </div>
    <template v-else-if="env">
      <div class="page-header">
        <h1 ref="heading" tabindex="-1">{{ ro.envTitle(env.name) }}</h1>
        <button v-if="canPublish" type="button" class="btn" data-testid="env-settings" @click="editing = true">{{ ro.editSettings }}</button>
      </div>
      <p v-if="!canPublish" class="alert" data-testid="env-read-only">{{ ro.readOnly }}</p>

      <dl class="summary card">
        <dt>{{ ro.serving }}</dt>
        <dd data-testid="env-serving">{{ env.current_release_id ? label(env.current_release_id) : s.nothingServed }}</dd>
        <dt>{{ ro.policy }}</dt>
        <dd>{{ policyText(env.policy) }}</dd>
        <template v-if="env.kind !== 'branch'">
          <dt>{{ ro.approval }}</dt>
          <dd data-testid="env-approval">
            {{ env.approval ? requirementText(env.approval, names) : ro.noApproval }}
            <RouterLink v-if="env.approval" :to="requestsRoute"> · {{ ro.requestsLink }}</RouterLink>
          </dd>
        </template>
      </dl>

      <section v-if="env.kind !== 'branch'" class="stack" aria-labelledby="ro-h">
        <div class="stack-sm">
          <h2 id="ro-h">{{ ro.rollout }}</h2>
          <p class="muted lead">{{ ro.rolloutLead }}</p>
        </div>
        <ErrorAlert :error="rolloutsError" />
        <ErrorAlert :error="actionError" />

        <article v-if="active" class="card stack-sm" aria-labelledby="ro-active-h" data-testid="rollout-active">
          <div class="row">
            <h3 id="ro-active-h">{{ ro.active }}</h3>
            <span class="pill pill-warn">{{ ro.rolloutSummary(active.rollout.percent, label(active.rollout.release_id)) }}</span>
          </div>
          <label for="ro-meter" class="hint">{{ ro.percentOf(active.rollout.percent) }}</label>
          <progress id="ro-meter" max="100" :value="active.rollout.percent" data-testid="rollout-meter">{{ active.rollout.percent }}%</progress>
          <dl class="summary">
            <dt>{{ ro.candidate }}</dt>
            <dd data-testid="rollout-candidate">{{ label(active.rollout.release_id) }}</dd>
            <dt>{{ ro.stable }}</dt>
            <dd data-testid="rollout-stable">{{ label(active.rollout.stable_release_id) }}</dd>
          </dl>
          <p class="muted">
            {{ ro.startedBy(person(active.rollout.started_by), relativeTime(active.rollout.started_at)) }}.
            <time :datetime="active.rollout.expires_at">{{ ro.expires(absoluteTime(active.rollout.expires_at)) }}</time>
          </p>
          <p v-if="active.rollout.forced" class="alert alert-warn" data-testid="rollout-forced">{{ ro.rolloutForced(active.rollout.force_reason ?? "") }}</p>
          <template v-if="canPublish">
            <form class="stack-sm" data-testid="rollout-advance" @submit.prevent="advance">
              <div class="field">
                <label for="ro-percent">{{ ro.advanceTo }}</label>
                <input id="ro-percent" v-model.number="percent" type="number" min="0" max="100" step="1" aria-describedby="ro-percent-hint" :disabled="busy" />
                <span id="ro-percent-hint" class="hint">{{ ro.advanceHint }}</span>
              </div>
              <div class="row" role="group" :aria-label="ro.advanceTo">
                <button v-for="step in STEPS" :key="step" type="button" class="btn btn-sm btn-ghost" :aria-pressed="percent === step" :disabled="busy" @click="percent = step">{{ step }}%</button>
              </div>
              <div class="row">
                <button type="submit" class="btn btn-primary" :disabled="busy || !validPercent(percent) || percent === active.rollout.percent">{{ ro.advance }}</button>
                <button type="button" class="btn" :disabled="busy" data-testid="rollout-complete" @click="confirming = 'complete'">{{ ro.complete }}</button>
                <button type="button" class="btn btn-danger" :disabled="busy" data-testid="rollout-abort" @click="confirming = 'abort'">{{ ro.abort }}</button>
              </div>
            </form>
          </template>
        </article>

        <template v-else>
          <p class="muted" data-testid="rollout-none">{{ ro.noActive }}</p>
          <form v-if="canPublish" class="card stack-sm" aria-labelledby="ro-start-h" data-testid="rollout-start" @submit.prevent="startRollout">
            <h3 id="ro-start-h">{{ ro.start }}</h3>
            <div v-if="needsApproval" class="alert alert-warn stack-sm" role="alert" data-testid="rollout-needs-approval">
              <p>{{ ro.needsApproval }}</p>
              <p><RouterLink :to="requestsRoute">{{ ro.requestsLink }}</RouterLink></p>
            </div>
            <div class="field">
              <label for="ro-release">{{ ro.startRelease }}</label>
              <select id="ro-release" v-model="start.release_id" :disabled="busy">
                <option value="" disabled>{{ s.choose }}</option>
                <option v-for="r in startable" :key="r.id" :value="r.id">{{ s.version(r.version) }} · {{ r.environment }}{{ r.note ? ` · ${r.note}` : "" }}</option>
              </select>
            </div>
            <div class="field">
              <label for="ro-start-percent">{{ ro.startPercent }}</label>
              <input id="ro-start-percent" v-model.number="start.percent" type="number" min="0" max="100" step="1" :disabled="busy" />
            </div>
            <div class="field">
              <label for="ro-days">{{ ro.startDuration }}</label>
              <input id="ro-days" v-model.number="start.days" type="number" min="0.05" max="90" step="any" aria-describedby="ro-days-hint" :disabled="busy" />
              <span id="ro-days-hint" class="hint">{{ ro.startDurationHint }}</span>
            </div>
            <p v-if="policyNotMet" class="hint" data-testid="rollout-policy-not-met">{{ ro.policyNotMet }}</p>
            <label class="check">
              <input v-model="start.force" type="checkbox" :disabled="busy" data-testid="rollout-force" />
              <span>{{ ro.startForce }}</span>
            </label>
            <div v-if="start.force" class="field">
              <label for="ro-force-reason">{{ ro.startForceReason }}</label>
              <textarea id="ro-force-reason" v-model="start.force_reason" rows="2" maxlength="1000" required :disabled="busy" />
            </div>
            <ErrorAlert :error="startError" />
            <div><button type="submit" class="btn btn-primary" :disabled="busy || !startValid">{{ ro.startConfirm }}</button></div>
          </form>
        </template>

        <section class="stack-sm" aria-labelledby="ro-history-h">
          <h3 id="ro-history-h">{{ ro.history }}</h3>
          <p v-if="!list.length" class="muted">{{ ro.historyEmpty }}</p>
          <div v-else class="scroll">
            <table class="table" data-testid="rollout-history">
              <thead>
                <tr>
                  <th scope="col">{{ ro.historyColumns.candidate }}</th>
                  <th scope="col">{{ ro.historyColumns.stable }}</th>
                  <th scope="col" class="num">{{ ro.historyColumns.percent }}</th>
                  <th scope="col">{{ ro.historyColumns.outcome }}</th>
                  <th scope="col">{{ ro.historyColumns.started }}</th>
                  <th scope="col">{{ ro.historyColumns.ended }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in list" :key="r.id" :data-rollout="r.id">
                  <th scope="row">{{ label(r.release_id) }}</th>
                  <td>{{ label(r.stable_release_id) }}</td>
                  <td class="num">{{ r.percent }}%</td>
                  <td>{{ outcome(r) }}</td>
                  <td>{{ person(r.started_by) }}, <time :datetime="r.started_at" :title="absoluteTime(r.started_at)">{{ relativeTime(r.started_at) }}</time></td>
                  <td>
                    <template v-if="r.ended_at">
                      <template v-if="r.ended_by">{{ person(r.ended_by) }}, </template>
                      <time :datetime="r.ended_at" :title="absoluteTime(r.ended_at)">{{ relativeTime(r.ended_at) }}</time>
                    </template>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </section>
    </template>

    <ModalDialog :open="confirming !== undefined" :title="confirming === 'abort' ? ro.abortTitle : ro.completeTitle" @close="confirming = undefined">
      <p v-if="active && confirming === 'abort'">{{ ro.abortLead(name, label(active.rollout.stable_release_id)) }}</p>
      <p v-else-if="active">{{ ro.completeLead(name, label(active.rollout.release_id)) }}</p>
      <template #actions>
        <button type="button" class="btn" :disabled="busy" @click="confirming = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn" :class="confirming === 'abort' ? 'btn-danger' : 'btn-primary'" :disabled="busy" data-testid="rollout-confirm" @click="end(confirming!)">
          {{ confirming === "abort" ? ro.abort : ro.complete }}
        </button>
      </template>
    </ModalDialog>
    <PolicyDialog :open="editing" :port="releases" :project="project()" :environment="name" :can-govern="canGovern" @close="editing = false" @done="settingsSaved" />
  </div>
</template>

<style scoped>
.environment {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.summary {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-1) var(--kl-space-4);
  margin: 0;
}
.summary dt {
  color: var(--kl-ink-secondary);
}
.summary dd {
  margin: 0;
}
progress {
  inline-size: 100%;
}
.scroll {
  overflow-x: auto;
}
.num {
  text-align: end;
  font-variant-numeric: tabular-nums;
}
textarea {
  inline-size: 100%;
}
</style>
