<script setup lang="ts">
/**
 * AI settings (RFC 0003 §3, §7): for the workspace — data handling
 * consent, providers, budget and spend, prices, routing — and for this
 * project — namespace tags, auto-translate, review routing — plus
 * insights. Changes need `intelligence.manage`.
 */
import { computed, onMounted, ref, shallowRef } from "vue";
import type { Versioned } from "../../api/errors";
import { isApiError } from "../../api/errors";
import type { AIBudget, AIPrice, AIPrices, AIProjectSettings, AIProvider, AISettings, AISpendEntry } from "../../api/intelligence-schemas";
import { useIntelligence, type ProjectSettingsUpdate, type SettingsUpdate } from "../../api/intelligence";
import BudgetCard from "../../components/ai/BudgetCard.vue";
import ConsentCard from "../../components/ai/ConsentCard.vue";
import InsightsCard from "../../components/ai/InsightsCard.vue";
import PricesCard from "../../components/ai/PricesCard.vue";
import ProjectPolicyCard from "../../components/ai/ProjectPolicyCard.vue";
import ProvidersCard from "../../components/ai/ProvidersCard.vue";
import RoutingCard from "../../components/ai/RoutingCard.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { allows } from "../../session/permissions";
import { useSession } from "../../session/session";
import { strings } from "../../strings";
import { useProject } from "./context";

const { tenant, projectId, targets, grant } = useProject();
const { person } = useSession();
const s = strings.aiSettings;
const port = useIntelligence();
const canManage = computed(() => allows(grant.value, "intelligence.manage"));
const p = () => ({ tenant: tenant.value, project: projectId.value });

const settings = shallowRef<Versioned<AISettings>>();
const prices = shallowRef<Versioned<AIPrices>>();
const budget = shallowRef<AIBudget>();
const spend = shallowRef<AISpendEntry[]>([]);
const providers = shallowRef<AIProvider[]>([]);
const project = shallowRef<Versioned<AIProjectSettings>>();
const error = ref<unknown>(null);
const status = ref("");
const busy = ref(false);

async function loadTenant(): Promise<void> {
  const [st, pr, b, sp, pv] = await Promise.all([port.settings(tenant.value), port.prices(tenant.value), port.budget(tenant.value), port.spend(tenant.value), port.providers(tenant.value)]);
  settings.value = st;
  prices.value = pr;
  budget.value = b;
  spend.value = sp;
  providers.value = pv;
}
async function loadProject(): Promise<void> {
  project.value = await port.projectSettings(p());
}
onMounted(async () => {
  try {
    await Promise.all([loadTenant(), loadProject()]);
  } catch (e) {
    error.value = e;
  }
});

/** Run a change; a stale ETag reloads and says so. */
async function change(run: () => Promise<void>, done: string, reload: () => Promise<void>): Promise<void> {
  busy.value = true;
  error.value = null;
  status.value = "";
  try {
    await run();
    status.value = done;
  } catch (e) {
    error.value = isApiError(e, "precondition_failed") ? new Error(s.changedElsewhere) : e;
    if (isApiError(e, "precondition_failed")) await reload().catch(() => undefined);
  } finally {
    busy.value = false;
  }
}

/**
 * Every write carries the ETag it was based on — `"0"` for settings nobody
 * saved yet, which the server takes as "only while still unsaved". A
 * change before its settings have loaded (no ETag yet) is refused here.
 */
const etagOf = (v: Versioned<unknown> | undefined): string => {
  if (!v?.etag) throw new Error(s.notLoaded);
  return v.etag;
};
const updateSettings = (body: SettingsUpdate, done: string) =>
  change(
    async () => {
      settings.value = await port.updateSettings(tenant.value, body, etagOf(settings.value));
      budget.value = await port.budget(tenant.value);
    },
    done,
    loadTenant,
  );
const setConsent = (consent: boolean) => updateSettings({ provider_consent: consent }, consent ? s.consentOn : s.consentOff);
const saveBudget = (u: { monthly_budget_micro_usd: number; max_concurrent_jobs: number }) => updateSettings(u, s.budgetSaved);
const savePrices = (overrides: Record<string, AIPrice>) =>
  change(
    async () => {
      prices.value = await port.putPrices(tenant.value, overrides, etagOf(prices.value));
      // Prices and settings share a version.
      settings.value = await port.settings(tenant.value);
    },
    s.pricesSaved,
    loadTenant,
  );
const saveProject = (body: ProjectSettingsUpdate) =>
  change(
    async () => {
      project.value = await port.updateProjectSettings(p(), body, etagOf(project.value));
    },
    s.projectSaved,
    loadProject,
  );
async function onProvidersChanged(): Promise<void> {
  providers.value = await port.providers(tenant.value);
}
</script>

<template>
  <div class="page stack ai-settings">
    <div class="stack-sm">
      <h1>{{ s.title }}</h1>
      <p class="muted lead">{{ s.lead }}</p>
    </div>
    <p v-if="!canManage" class="alert">{{ s.readOnly }}</p>
    <ErrorAlert :error="error" />
    <p class="muted" role="status" data-testid="ai-settings-status">{{ status }}</p>

    <h2 class="section">{{ s.workspace }}</h2>
    <ConsentCard :settings="settings?.value" :can-manage="canManage" :busy="busy" :self-id="person?.id" @change="setConsent" />
    <ProvidersCard :tenant="tenant" :providers="providers" :can-manage="canManage" @changed="onProvidersChanged" />
    <BudgetCard :settings="settings?.value" :budget="budget" :spend="spend" :can-manage="canManage" :busy="busy" @save="saveBudget" />
    <PricesCard :prices="prices?.value" :can-manage="canManage" :busy="busy" @save="savePrices" />
    <RoutingCard :tenant="tenant" :project-id="projectId" :providers="providers" :can-manage="canManage" />

    <h2 class="section">{{ s.project }}</h2>
    <ProjectPolicyCard :settings="project?.value" :targets="targets" :can-manage="canManage" :busy="busy" @save="saveProject" />
    <InsightsCard :tenant="tenant" :project-id="projectId" />
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 48rem;
}
.section {
  font-size: var(--kl-text-sm);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--kl-ink-secondary);
  padding-block-start: var(--kl-space-4);
  border-block-start: 1px solid var(--kl-border);
}
</style>
