<script setup lang="ts">
/**
 * The quality view (RFC 0005 §8, §9): one check run's findings, grouped
 * by the layer that found them, with the §2.1 filters, the visual
 * findings shown as cropped and outlined screenshots, and the waiver
 * flow.
 *
 * Wave 5 adds the project health header above it (RFC 0005 §8): the
 * seven numbers, and the same three per locale with the layers that can
 * run for each language at all (intent §41).
 *
 * Three things this screen refuses to do, on purpose:
 * - hide a waived finding. It stays in its layer, marked, with the
 *   reason it was accepted for, and is counted on its own.
 * - let a layer the run never computed read as clean. The run names the
 *   layers it computed; the rest are listed as not checked.
 * - accept a waiver with no reason. The field is required here as it is
 *   at the API, so nobody waits for a round trip to be told.
 *
 * It consumes the Quality API and changes nothing about it.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useQuality, type Findings } from "../../api/quality";
import type { CheckRun, Finding, Waiver } from "../../api/quality-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import FindingItem from "../../components/quality/FindingItem.vue";
import HealthHeader from "../../components/quality/HealthHeader.vue";
import LocaleHealth from "../../components/quality/LocaleHealth.vue";
import QualityFilters from "../../components/quality/QualityFilters.vue";
import QualitySections from "../../components/quality/QualitySections.vue";
import WaiveDialog from "../../components/quality/WaiveDialog.vue";
import WaiverTable from "../../components/quality/WaiverTable.vue";
import { unmeasured } from "../../lib/health";
import { groupByLayer, isFiltered, notChecked, toFindingFilter, waiversById, type QualityFilterState } from "../../lib/quality";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "./context";

const route = useRoute();
const router = useRouter();
const port = useQuality();
const { tenant, projectId, locales, grant, health, healthState, reloadHealth } = useProject();
const s = strings.quality;
const person = usePeople(() => tenant.value);
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canWaive = computed(() => allows(grant.value, "catalog.write"));

// ── URL state, so a filtered view is bookmarkable ──────────────────
const q = (name: string) => (typeof route.query[name] === "string" ? (route.query[name] as string) : "");
const filter = computed<QualityFilterState>(() => ({ run: q("run"), layer: q("layer"), severity: q("severity"), locale: q("locale"), code: q("code") }));
function setFilter(f: QualityFilterState): void {
  const query: Record<string, string | undefined> = { ...route.query, ...f };
  for (const [k, v] of Object.entries(query)) if (v === undefined || v === "") delete query[k];
  void router.replace({ query });
}

// ── what the screen holds ──────────────────────────────────────────
const found = shallowRef<Findings>();
const waivers = shallowRef<Waiver[]>([]);
const runs = shallowRef<CheckRun[]>([]);
const loading = ref(false);
const busy = ref(false);
const error = ref<unknown>(null);
const status = ref("");
let abort: AbortController | undefined;

async function load(): Promise<void> {
  abort?.abort();
  const a = (abort = new AbortController());
  loading.value = true;
  error.value = null;
  try {
    const [f, w, r] = await Promise.all([
      port.findings(project(), toFindingFilter(filter.value), undefined, a.signal),
      port.waivers(project(), {}, undefined, a.signal),
      port.checkRuns(project(), undefined, a.signal),
    ]);
    if (a.signal.aborted) return;
    found.value = f;
    waivers.value = w;
    runs.value = r.items;
  } catch (e) {
    if (!a.signal.aborted) error.value = e;
  } finally {
    if (!a.signal.aborted) loading.value = false;
  }
}
watch([projectId, filter], load, { immediate: true, deep: true });
onBeforeUnmount(() => abort?.abort());

/** The findings and the numbers above them, together: waiving one changes both. */
async function refresh(): Promise<void> {
  await Promise.all([load(), reloadHealth()]);
}

/**
 * The locales to show health for. With no summary the project's own
 * locales are listed with nothing measured, rather than not listed at
 * all: "we know nothing about your German" is an answer, "German isn't
 * here" is not.
 */
const healthLocaleRows = computed(() => health.value?.locales ?? unmeasured(locales.value));

const run = computed(() => found.value?.run);
const counts = computed(() => found.value?.counts ?? { errors: 0, warnings: 0, waived: 0 });
const findings = computed(() => found.value?.items ?? []);
const byId = computed(() => waiversById(waivers.value));
/**
 * Grouped by layer. A filtered list gets no "clean" section for a layer
 * the filter excluded: the run may well have found something there, and
 * saying "nothing found" would be a lie about the filter, not the run.
 */
const groups = computed(() => groupByLayer(findings.value, isFiltered(filter.value) ? [] : (run.value?.layers ?? [])));
const missing = computed(() => notChecked(run.value?.layers ?? []));
/** Nothing has ever been checked: the list came back without a run at all. */
const neverChecked = computed(() => !!found.value && !run.value);
const conclusion = computed(() => (run.value?.conclusion ? (s.conclusion[run.value.conclusion] ?? run.value.conclusion) : s.conclusion.running));
const runLabel = (r: CheckRun) => s.runOption(r.ref, absoluteTime(r.started_at), r.conclusion ? (s.conclusion[r.conclusion] ?? r.conclusion) : s.conclusion.running!);

// ── the waiver flow ────────────────────────────────────────────────
const waiving = shallowRef<Finding>();
const dialogOpen = ref(false);

function openWaive(f: Finding): void {
  status.value = "";
  waiving.value = f;
  dialogOpen.value = true;
}
function closeWaive(): void {
  dialogOpen.value = false;
}
async function accepted(code: string): Promise<void> {
  dialogOpen.value = false;
  status.value = s.accepted(code);
  await refresh();
}
async function revoke(waiver: Waiver, finding?: Finding): Promise<void> {
  busy.value = true;
  error.value = null;
  try {
    await port.revokeWaiver(project(), waiver.id);
    status.value = s.revoked(finding?.code ?? waiver.accepts?.code ?? waiver.fingerprint);
    await refresh();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <button type="button" class="btn" :disabled="loading" @click="refresh">{{ s.reload }}</button>
    </div>

    <QualitySections :tenant="tenant" :project-id="projectId" />

    <p v-if="!canWaive" class="alert" data-testid="quality-read-only">{{ s.readOnly }}</p>
    <ErrorAlert :error="error" />
    <p v-if="status" class="alert alert-ok" role="status" data-testid="quality-status">{{ status }}</p>
    <!-- ── the seven numbers, and the same three per locale ───────── -->
    <HealthHeader :summary="health" :state="healthState" />
    <LocaleHealth :locales="healthLocaleRows" :measured="!!health" />

    <p v-if="loading && !found" class="muted" role="status">{{ strings.app.loading }}</p>

    <template v-else-if="found">
      <!-- Nothing checked yet is not "clean": say which it is. -->
      <div v-if="neverChecked" class="stack-sm" data-testid="quality-never-checked">
        <p>{{ s.nothingChecked }}</p>
        <p class="hint">{{ s.nothingCheckedHint }}</p>
      </div>

      <template v-else-if="run">
        <!-- ── the run, and what it stands at now ──────────────── -->
        <section class="card stack-sm" aria-labelledby="run-h">
          <h2 id="run-h">{{ s.counts }}</h2>
          <p class="run-line">
            <span class="pill" :class="run.conclusion === 'failure' ? 'pill-err' : run.conclusion === 'success' ? 'pill-ok' : 'pill-neutral'">{{ conclusion }}</span>
            <span>{{ s.ranAgainst(run.ref) }}</span>
            <code v-if="run.commit">{{ s.commit(run.commit) }}</code>
            <span class="muted">{{ s.trigger[run.trigger] ?? run.trigger }}</span>
            <span class="muted">{{ s.policyVersion(run.policy_version) }}</span>
            <time class="muted" :datetime="run.started_at" :title="absoluteTime(run.started_at)">{{ relativeTime(run.started_at) }}</time>
          </p>
          <p class="counts" data-testid="quality-counts">
            <span class="pill pill-err">{{ s.errors(counts.errors) }}</span>
            <span class="pill pill-warn">{{ s.warnings(counts.warnings) }}</span>
            <span class="pill pill-neutral">{{ s.waivedCount(counts.waived) }}</span>
          </p>
          <p class="hint">{{ s.waivedNote }}</p>
          <div v-if="runs.length > 1" class="field run-field">
            <label for="q-run">{{ s.run }}</label>
            <select id="q-run" :value="filter.run" data-testid="quality-run" @change="setFilter({ ...filter, run: ($event.target as HTMLSelectElement).value })">
              <option value="">{{ s.runLatest }}</option>
              <option v-for="r in runs" :key="r.id" :value="r.id">{{ runLabel(r) }}</option>
            </select>
          </div>
        </section>

        <!-- ── filters ─────────────────────────────────────────── -->
        <QualityFilters :filter="filter" :locales="locales" :matched="findings.length" @update:filter="setFilter" />
        <p v-if="found.truncated" class="hint" data-testid="quality-truncated">{{ s.truncated(findings.length) }}</p>

        <!-- ── findings, by the layer that found them ──────────── -->
        <p v-if="!findings.length && !groups.length" class="muted" data-testid="quality-empty">{{ s.noFindings }}</p>
        <p v-else-if="!findings.length" class="muted" data-testid="quality-no-matches">{{ s.noMatches }}</p>
        <section v-for="g in groups" :key="g.layer" class="stack-sm layer" :aria-labelledby="`layer-${g.layer}`" :data-layer="g.layer" data-testid="layer">
          <h2 :id="`layer-${g.layer}`">
            {{ s.layerName[g.layer] ?? g.layer }}
            <span class="muted summary">{{ s.layerSummary(g.errors, g.warnings, g.waived) }}</span>
          </h2>
          <p v-if="!g.findings.length" class="muted" data-testid="layer-clean">{{ s.layerClean }}</p>
          <ul v-else class="findings">
            <FindingItem
              v-for="f in g.findings"
              :key="f.fingerprint"
              :tenant="tenant"
              :project-id="projectId"
              :finding="f"
              :waiver="f.waiver ? byId.get(f.waiver) : undefined"
              :can-waive="canWaive"
              :busy="busy"
              :person="person"
              @waive="openWaive"
              @revoke="revoke"
            />
          </ul>
        </section>

        <!-- A layer the run never computed is not a green one (intent §41). -->
        <p v-if="missing.length" class="hint" data-testid="quality-not-checked">
          {{ s.notCheckedHint(missing.map((l) => s.layerName[l] ?? l).join(", ")) }}
        </p>
      </template>

      <!-- ── the waivers, and taking one back ─────────────────── -->
      <section class="stack-sm" aria-labelledby="waivers-h">
        <div class="waivers-head">
          <h2 id="waivers-h">{{ s.waiversTitle }}</h2>
          <RouterLink :to="{ name: 'waivers', params: { tenant, project: projectId } }" data-testid="quality-manage-waivers">{{ s.manageWaivers }}</RouterLink>
        </div>
        <p class="muted">{{ s.waiversLead }}</p>
        <WaiverTable :waivers="waivers" :can-revoke="canWaive" :busy="busy" :person="person" @revoke="revoke" />
      </section>
    </template>

    <WaiveDialog
      :open="dialogOpen"
      :port="port"
      :project="project()"
      :finding="waiving"
      :default-ref="run?.ref"
      @close="closeWaive"
      @done="accepted"
    />
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: var(--kl-content-md);
}
.run-line,
.counts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
.run-field {
  max-inline-size: 22rem;
}
.layer h2 {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
  align-items: baseline;
}
.summary {
  font-size: var(--kl-text-sm);
  font-weight: normal;
}
.waivers-head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
  align-items: baseline;
  justify-content: space-between;
}
.findings {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
</style>
