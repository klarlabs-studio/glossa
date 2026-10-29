<script setup lang="ts">
/**
 * Waiver management (RFC 0005 §2.3, §9). The findings screen shows the
 * waivers of the run it is looking at; this screen is about the
 * waivers themselves: every one this project has ever made, standing or
 * not, filtered by what it accepts, by its fingerprint or by whether it
 * still stands, with its reason, author, reach and expiry, and
 * revocable from here.
 *
 * Two numbers are above the table that a plain count would hide:
 * - **unexamined** — a standing waiver no stored finding carries any
 *   more. Nobody is watching what it lets through, and it will keep
 *   standing until someone looks.
 * - **expiring soon** — a check that is about to get stricter without
 *   anyone having decided that it should.
 *
 * Revoking deletes nothing. The waiver stays as history, and what it
 * accepted is an ordinary finding again.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { MAX_WAIVERS, useQuality } from "../../api/quality";
import type { Waiver } from "../../api/quality-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import QualitySections from "../../components/quality/QualitySections.vue";
import WaiverFilters from "../../components/quality/WaiverFilters.vue";
import WaiverTable from "../../components/quality/WaiverTable.vue";
import { filterWaivers, waiverTotals, type WaiverFilterState } from "../../lib/waivers";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "./context";

const route = useRoute();
const router = useRouter();
const port = useQuality();
const { tenant, projectId, grant } = useProject();
const s = strings.quality;
const person = usePeople(() => tenant.value);
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canRevoke = computed(() => allows(grant.value, "catalog.write"));

// ── URL state, so a filtered list is bookmarkable ──────────────────
const q = (name: string) => (typeof route.query[name] === "string" ? (route.query[name] as string) : "");
const filter = computed<WaiverFilterState>(() => ({ layer: q("layer"), code: q("code"), fingerprint: q("fingerprint"), state: q("state") }));
function setFilter(f: WaiverFilterState): void {
  const query: Record<string, string | undefined> = { ...route.query, ...f };
  for (const [k, v] of Object.entries(query)) if (v === undefined || v === "") delete query[k];
  void router.replace({ query });
}

const waivers = shallowRef<Waiver[]>([]);
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
    // Every waiver, filtered here: the counts above the table are the
    // project's, not the current query's.
    const got = await port.waivers(project(), {}, undefined, a.signal);
    if (a.signal.aborted) return;
    waivers.value = got;
  } catch (e) {
    if (!a.signal.aborted) error.value = e;
  } finally {
    if (!a.signal.aborted) loading.value = false;
  }
}
watch(projectId, load, { immediate: true });
onBeforeUnmount(() => abort?.abort());

const totals = computed(() => waiverTotals(waivers.value));
const shown = computed(() => filterWaivers(waivers.value, filter.value));
const truncated = computed(() => waivers.value.length >= MAX_WAIVERS);

async function revoke(waiver: Waiver): Promise<void> {
  busy.value = true;
  error.value = null;
  try {
    await port.revokeWaiver(project(), waiver.id);
    status.value = s.revoked(waiver.accepts?.code ?? waiver.fingerprint);
    await load();
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
        <h1>{{ s.waiversTitle }}</h1>
        <p class="muted lead">{{ s.waiversPageLead }}</p>
      </div>
      <button type="button" class="btn" :disabled="loading || busy" @click="load">{{ s.reload }}</button>
    </div>

    <QualitySections :tenant="tenant" :project-id="projectId" />

    <p v-if="!canRevoke" class="alert" data-testid="waivers-read-only">{{ s.waiversReadOnly }}</p>
    <ErrorAlert :error="error" />
    <p v-if="status" class="alert alert-ok" role="status" data-testid="waivers-status">{{ status }}</p>
    <p v-if="loading && !waivers.length" class="muted" role="status">{{ strings.app.loading }}</p>

    <template v-else>
      <!-- Each number is a sentence, so none of them depends on its colour. -->
      <p class="totals" data-testid="waiver-totals">
        <span class="pill pill-accent">{{ s.waiverStanding(totals.standing) }}</span>
        <span class="pill pill-neutral">{{ s.waiverPast(totals.past) }}</span>
        <span v-if="totals.unexamined" class="pill pill-warn" data-testid="waiver-unexamined-count">{{ s.waiverUnexaminedCount(totals.unexamined) }}</span>
        <span v-if="totals.expiringSoon" class="pill pill-warn" data-testid="waiver-expiring-count">{{ s.waiverExpiringCount(totals.expiringSoon) }}</span>
      </p>

      <WaiverFilters :filter="filter" :matched="shown.length" @update:filter="setFilter" />
      <p v-if="truncated" class="hint" data-testid="waivers-truncated">{{ s.waiverTruncated(waivers.length) }}</p>

      <p v-if="waivers.length && !shown.length" class="muted" data-testid="waivers-no-matches">{{ s.waiverNoMatches }}</p>
      <WaiverTable v-else :waivers="shown" :can-revoke="canRevoke" :busy="busy" :person="person" @revoke="revoke" />
    </template>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: var(--kl-content-md);
}
.totals {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
</style>
