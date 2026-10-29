<script setup lang="ts">
/**
 * Audit exports (RFC 0006 §6.2): start an export of a range of the
 * trail, watch its job, download its two files, and see how to verify
 * them offline against the key the deployment publishes.
 *
 * Exporting needs `audit.export` (owners, by default). A deployment with
 * no audit key says `audit_export_unavailable`, and this screen says
 * what that means instead of offering a form that can't work.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { KEYS_PATH, useAudit, type AuditExportRange } from "../../api/audit";
import type { AuditExportJob, AuditKeyDocument } from "../../api/audit-schemas";
import { isApiError } from "../../api/errors";
import { newIdempotencyKey } from "../../api/releases";
import { absoluteTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const props = withDefaults(defineProps<{ pollMs?: number }>(), { pollMs: 2000 });
const a = strings.audit;
const s = a.exports;
const route = useRoute();
const port = useAudit();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const canExport = computed(() => allows(grant.value, "audit.export"));

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const jobs = shallowRef<AuditExportJob[]>([]);
/** `undefined`: not read yet or failed; `null`: the deployment publishes none. */
const keys = shallowRef<AuditKeyDocument | null | undefined>();
const keysFailed = ref(false);

const unavailable = ref(false);

async function load(): Promise<void> {
  if (!canExport.value) return;
  phase.value = "loading";
  loadError.value = null;
  const [js, ks] = await Promise.allSettled([port.exportJobs(tenant.value), port.keys()]);
  keysFailed.value = ks.status === "rejected";
  keys.value = ks.status === "fulfilled" ? ks.value : undefined;
  // No published key and no way to make one: the deployment has exports off.
  unavailable.value = ks.status === "fulfilled" && ks.value === null;
  if (js.status === "rejected") {
    jobs.value = [];
    loadError.value = js.reason;
    phase.value = "failed";
    return;
  }
  jobs.value = js.value;
  phase.value = "ready";
  schedule();
}
watch([tenant, canExport], () => void load(), { immediate: true });

// ── watching running jobs ───────────────────────────────────────────
const active = (j: AuditExportJob) => j.state === "queued" || j.state === "running";
let timer: ReturnType<typeof setTimeout> | undefined;

function schedule(): void {
  clearTimeout(timer);
  if (!jobs.value.some(active)) return;
  timer = setTimeout(() => void refresh(), props.pollMs);
}

async function refresh(): Promise<void> {
  const open = jobs.value.filter(active);
  const updated = await Promise.allSettled(open.map((j) => port.exportJob(tenant.value, j.id)));
  const byId = new Map<string, AuditExportJob>();
  updated.forEach((r, i) => {
    if (r.status === "fulfilled") byId.set(open[i]!.id, r.value);
  });
  jobs.value = jobs.value.map((j) => byId.get(j.id) ?? j);
  schedule();
}
onBeforeUnmount(() => clearTimeout(timer));

// ── starting an export ──────────────────────────────────────────────
const form = ref({ kind: "time" as "time" | "sequence", from: "", to: "", first: "" as string | number, last: "" as string | number });
const busy = ref(false);
const startError = ref("");
const started = ref(false);
let key = newIdempotencyKey();
watch(form, () => (key = newIdempotencyKey()), { deep: true });

const instant = (local: string) => new Date(local).toISOString();

function range(): AuditExportRange | undefined {
  const f = form.value;
  if (f.kind === "time") {
    if (!f.from || !f.to || Date.parse(f.to) <= Date.parse(f.from)) {
      startError.value = s.invalidTime;
      return undefined;
    }
    return { from: instant(f.from), to: instant(f.to) };
  }
  // type=number inputs hand v-model a number once they hold one.
  const lastText = String(f.last).trim();
  const first = String(f.first).trim() === "" ? Number.NaN : Number(f.first);
  const last = lastText === "" ? undefined : Number(lastText);
  if (!Number.isInteger(first) || first < 1 || (last !== undefined && (!Number.isInteger(last) || last < first))) {
    startError.value = s.invalidSequence;
    return undefined;
  }
  return last === undefined ? { first_sequence: first } : { first_sequence: first, last_sequence: last };
}

async function start(): Promise<void> {
  if (busy.value) return;
  startError.value = "";
  started.value = false;
  const r = range();
  if (!r) return;
  busy.value = true;
  try {
    const job = await port.createExport(tenant.value, r, key);
    jobs.value = [job, ...jobs.value.filter((j) => j.id !== job.id)];
    started.value = true;
    key = newIdempotencyKey();
    schedule();
  } catch (e) {
    if (isApiError(e, "audit_export_unavailable")) unavailable.value = true;
    startError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}

// ── presentation ────────────────────────────────────────────────────
const bytes = (n: number) => (n < 1024 ? `${n} B` : n < 1024 * 1024 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`);

function rangeText(j: AuditExportJob): string {
  if (j.first_sequence !== undefined && j.last_sequence !== undefined) return s.sequences(j.first_sequence, j.last_sequence);
  if (j.from && j.to) return s.timeRange(absoluteTime(j.from), absoluteTime(j.to));
  return "—";
}

const published = (id: string | undefined) => !!id && !!keys.value?.keys.some((k) => k.key_id === id);
const origin = typeof window === "undefined" ? "" : window.location.origin;
const keysUrl = computed(() => `${origin}${KEYS_PATH}`);
const saveKeys = computed(() => `curl -fsS ${keysUrl.value} > audit-keys.json`);
const verifyCommand = "glossa audit verify ./audit-export --public-key audit-keys.json";
const failureText = (j: AuditExportJob) =>
  j.failure_code === "range_not_contiguous" ? s.rangeNotContiguous : (j.failure_message ?? s.failedWith(j.failure_code ?? "internal"));
const kept = (j: AuditExportJob) => !j.files_deleted_at && Date.parse(j.expires_at) > Date.now();
</script>

<template>
  <div class="page stack audit-exports">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'audit-log', params: { tenant } }">{{ s.back }}</RouterLink>
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>

    <p v-if="!canExport" class="alert" role="status" data-testid="audit-export-no-access">{{ s.noAccess }}</p>
    <template v-else>
      <p v-if="unavailable" class="alert alert-warn" role="alert" data-testid="audit-export-unavailable">{{ s.unavailable }}</p>

      <form v-else class="card stack-sm" aria-labelledby="ae-start-h" data-testid="audit-export-form" @submit.prevent="start">
        <h2 id="ae-start-h">{{ s.start }}</h2>
        <fieldset class="field">
          <legend>{{ s.rangeKind }}</legend>
          <label><input v-model="form.kind" type="radio" name="ae-kind" value="time" /> {{ s.byTime }}</label>
          <label><input v-model="form.kind" type="radio" name="ae-kind" value="sequence" /> {{ s.bySequence }}</label>
        </fieldset>
        <div v-if="form.kind === 'time'" class="grid">
          <div class="field">
            <label for="ae-from">{{ s.from }}</label>
            <input id="ae-from" v-model="form.from" type="datetime-local" />
          </div>
          <div class="field">
            <label for="ae-to">{{ s.to }}</label>
            <input id="ae-to" v-model="form.to" type="datetime-local" />
          </div>
        </div>
        <div v-else class="grid">
          <div class="field">
            <label for="ae-first">{{ s.firstSequence }}</label>
            <input id="ae-first" v-model="form.first" type="number" min="1" inputmode="numeric" />
          </div>
          <div class="field">
            <label for="ae-last">{{ s.lastSequence }}</label>
            <input id="ae-last" v-model="form.last" type="number" min="1" inputmode="numeric" aria-describedby="ae-last-hint" />
            <span id="ae-last-hint" class="hint">{{ s.lastHint }}</span>
          </div>
        </div>
        <p v-if="startError" class="alert alert-error" role="alert" data-testid="audit-export-error">{{ startError }}</p>
        <p v-if="started" class="alert" role="status" data-testid="audit-export-started">{{ s.started }}</p>
        <div><button type="submit" class="btn btn-primary" :disabled="busy">{{ s.submit }}</button></div>
      </form>

      <p v-if="phase === 'loading'" class="muted" role="status" data-testid="audit-exports-loading">{{ s.loading }}</p>
      <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="audit-exports-failed">
        <p class="alert-title">{{ s.failed }}</p>
        <p>{{ problemText(loadError) }}</p>
        <div><button type="button" class="btn btn-sm" @click="load">{{ a.retry }}</button></div>
      </div>
      <p v-else-if="!jobs.length" class="card muted" data-testid="audit-exports-empty">{{ s.empty }}</p>
      <div v-else class="scroll">
        <table class="table" data-testid="audit-export-list">
          <thead>
            <tr>
              <th scope="col">{{ s.columns.created }}</th>
              <th scope="col">{{ s.columns.state }}</th>
              <th scope="col">{{ s.columns.range }}</th>
              <th scope="col" class="num">{{ s.columns.entries }}</th>
              <th scope="col">{{ s.columns.key }}</th>
              <th scope="col">{{ s.columns.files }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in jobs" :key="j.id" data-testid="audit-export-row" :data-job="j.id" :data-state="j.state">
              <th scope="row">
                <time :datetime="j.created_at">{{ absoluteTime(j.created_at) }}</time>
              </th>
              <td data-testid="audit-export-state">
                {{ s.states[j.state] ?? j.state }}
                <p v-if="j.state === 'failed'" class="muted" data-testid="audit-export-failure">{{ failureText(j) }}</p>
              </td>
              <td>{{ rangeText(j) }}</td>
              <td class="num">{{ j.entry_count?.toLocaleString() ?? "—" }}</td>
              <td class="mono" data-testid="audit-export-key">
                {{ j.key_id ?? "—" }}
                <p v-if="j.state === 'succeeded' && keys && j.key_id" class="muted" :data-published="published(j.key_id)">
                  {{ published(j.key_id) ? s.verify.keyMatches(j.key_id) : s.verify.keyMissing(j.key_id) }}
                </p>
              </td>
              <td>
                <template v-if="j.state === 'succeeded'">
                  <ul v-if="kept(j)" class="files">
                    <li v-for="f in [j.entries, j.manifest]" :key="f?.path">
                      <a
                        v-if="f?.download_url"
                        :href="f.download_url"
                        download
                        :aria-label="s.downloadLabel(f.path, bytes(f.bytes))"
                        data-testid="audit-export-download"
                        :data-file="f.path"
                      >
                        {{ f.path }}
                      </a>
                    </li>
                  </ul>
                  <span v-else class="muted">{{ s.expired }}</span>
                  <p v-if="kept(j)" class="muted">{{ s.expires(absoluteTime(j.expires_at)) }}</p>
                </template>
                <span v-else>—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <section class="card stack-sm" aria-labelledby="ae-verify-h" data-testid="audit-verify">
        <h2 id="ae-verify-h">{{ s.verify.title }}</h2>
        <p class="muted">{{ s.verify.lead }}</p>
        <template v-if="keys">
          <ul class="keys" data-testid="audit-keys">
            <li v-for="k in keys.keys" :key="k.key_id">
              <span class="muted">{{ s.verify.keyId }}</span> <code class="mono" data-testid="audit-key-id">{{ k.key_id }}</code>
              <span class="muted"> ({{ k.active ? s.verify.keyActive : s.verify.keyRetired }})</span>
            </li>
          </ul>
          <p>{{ s.verify.saveKeys }}</p>
          <pre class="mono cmd" data-testid="audit-save-keys">{{ saveKeys }}</pre>
          <p>{{ s.verify.command }}</p>
          <pre class="mono cmd" data-testid="audit-verify-command">{{ verifyCommand }}</pre>
          <p class="muted">{{ s.verify.why }}</p>
        </template>
        <p v-else-if="keysFailed" class="alert alert-warn" data-testid="audit-keys-failed">{{ s.verify.keysFailed }}</p>
        <p v-else-if="keys === null" class="muted" data-testid="audit-no-keys">{{ s.verify.noKeys }}</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.audit-exports {
  max-inline-size: var(--kl-content-lg, 72rem);
}
.lead {
  max-inline-size: 60ch;
}
.files {
  list-style: none;
  margin: 0;
  padding: 0;
}
.keys {
  margin: 0;
  padding-inline-start: 1.25rem;
}
.cmd {
  margin: 0;
  padding: 0.5rem 0.75rem;
  overflow-x: auto;
  white-space: pre;
}
fieldset.field {
  border: 0;
  padding: 0;
  display: grid;
  gap: 0.25rem;
}
</style>
