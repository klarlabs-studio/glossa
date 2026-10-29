<script setup lang="ts">
/**
 * The routing policy editor: per task (and optionally target locales),
 * the preferred provider and model, then fallbacks in order. Edits the
 * project's own policy or the workspace's.
 */
import { computed, ref, shallowRef, watch } from "vue";
import type { Versioned } from "../../api/errors";
import { isApiError } from "../../api/errors";
import type { AIProvider, AIRoute, AIRoutingPolicy, AIRoutingPolicyView, AITask } from "../../api/intelligence-schemas";
import { useIntelligence } from "../../api/intelligence";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";

const props = defineProps<{ tenant: string; projectId: string; providers: AIProvider[]; canManage: boolean }>();
const s = strings.aiSettings;
const port = useIntelligence();
const TASKS: AITask[] = ["translate", "review", "explain", "assess"];

interface RuleRow {
  task: AITask;
  locales: string;
  routes: Array<{ provider: string; model: string; max_tokens: number; temperature: string; effort: "" | "low" | "medium" | "high" | "max" }>;
}

const scope = ref<"project" | "tenant">("project");
const view = shallowRef<Versioned<AIRoutingPolicyView>>();
const effective = shallowRef<AIRoutingPolicyView>();
const rules = ref<RuleRow[]>([]);
const error = ref<unknown>(null);
const status = ref("");
const busy = ref(false);
const p = () => ({ tenant: props.tenant, project: props.projectId });

const rowsOf = (policy: AIRoutingPolicy): RuleRow[] =>
  policy.rules.map((r) => ({
    task: r.task,
    locales: (r.locales ?? []).join(", "),
    routes: r.routes.map((x) => ({ provider: x.provider, model: x.model, max_tokens: x.max_tokens, temperature: x.temperature?.toString() ?? "", effort: x.effort ?? "" })),
  }));

async function load(): Promise<void> {
  error.value = null;
  try {
    const [proj, ten] = await Promise.all([port.projectRouting(p()), port.routing(props.tenant)]);
    effective.value = proj.value;
    view.value = scope.value === "project" ? proj : ten;
    rules.value = rowsOf(view.value.value.policy);
  } catch (e) {
    error.value = e;
  }
}
watch(scope, load, { immediate: true });

function policyOf(): AIRoutingPolicy {
  return {
    rules: rules.value.map((r) => {
      const locales = r.locales
        .split(",")
        .map((l) => l.trim())
        .filter(Boolean);
      return {
        task: r.task,
        ...(locales.length ? { locales } : {}),
        routes: r.routes.map((x): AIRoute => {
          const route: AIRoute = { provider: x.provider, model: x.model.trim(), max_tokens: x.max_tokens };
          if (x.temperature.trim() !== "") route.temperature = Number(x.temperature);
          if (x.effort) route.effort = x.effort;
          return route;
        }),
      };
    }),
  };
}

async function save(): Promise<void> {
  // The ETag of the scope's own policy: "0" while it has none (the project
  // inherits, or the tenant uses the default), so of two first saves one wins.
  const etag = view.value?.etag;
  if (etag === undefined) return;
  busy.value = true;
  error.value = null;
  status.value = "";
  try {
    if (scope.value === "project") await port.putProjectRouting(p(), policyOf(), etag);
    else await port.putRouting(props.tenant, policyOf(), etag);
    status.value = s.routingSaved;
    await load();
  } catch (e) {
    error.value = isApiError(e, "precondition_failed") ? new Error(s.changedElsewhere) : e;
    if (isApiError(e, "precondition_failed")) await load();
  } finally {
    busy.value = false;
  }
}

async function resetProject(): Promise<void> {
  busy.value = true;
  try {
    await port.deleteProjectRouting(p());
    await load();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

const providerNames = computed(() => {
  const names = props.providers.map((x) => x.name);
  for (const r of rules.value) for (const x of r.routes) if (x.provider && !names.includes(x.provider)) names.push(x.provider);
  return names;
});
const newRoute = () => ({ provider: providerNames.value[0] ?? "anthropic", model: "", max_tokens: 4096, temperature: "", effort: "" as const });
</script>

<template>
  <section class="card stack-sm" aria-labelledby="routing-h" data-testid="routing">
    <h2 id="routing-h">{{ s.routingTitle }}</h2>
    <p class="hint">{{ s.routingLead }}</p>
    <p v-if="effective" class="row">
      <span class="pill pill-neutral" data-testid="routing-source">{{ s.routingSource[effective.source] }}</span>
    </p>
    <div v-if="canManage" class="field scope">
      <label for="rt-scope">{{ s.routingScope }}</label>
      <select id="rt-scope" v-model="scope">
        <option value="project">{{ s.routingScopes.project }}</option>
        <option value="tenant">{{ s.routingScopes.tenant }}</option>
      </select>
    </div>
    <ErrorAlert :error="error" />
    <form class="stack-sm" @submit.prevent="save">
      <fieldset v-for="(r, i) in rules" :key="i" class="rule stack-sm" :disabled="!canManage">
        <legend class="label">{{ s.tasks[r.task] }}<template v-if="r.locales"> · {{ r.locales }}</template></legend>
        <div class="row">
          <div class="field">
            <label :for="`rt-task-${i}`">{{ s.task }}</label>
            <select :id="`rt-task-${i}`" v-model="r.task">
              <option v-for="t in TASKS" :key="t" :value="t">{{ s.tasks[t] }}</option>
            </select>
          </div>
          <div class="field grow">
            <label :for="`rt-loc-${i}`">{{ s.ruleLocales }}</label>
            <input :id="`rt-loc-${i}`" v-model="r.locales" class="mono" :placeholder="s.ruleLocalesHint" />
          </div>
          <button v-if="canManage" type="button" class="btn btn-sm btn-danger" @click="rules.splice(i, 1)">{{ s.removeRule(i + 1) }}</button>
        </div>
        <table class="table small">
          <caption class="visually-hidden">{{ s.routes }}</caption>
          <thead>
            <tr>
              <th scope="col">{{ s.provider }}</th>
              <th scope="col">{{ s.model }}</th>
              <th scope="col">{{ s.maxTokens }}</th>
              <th scope="col">{{ s.temperature }}</th>
              <th scope="col">{{ s.effort }}</th>
              <th scope="col"><span class="visually-hidden">{{ strings.app.remove }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(x, j) in r.routes" :key="j">
              <td>
                <select v-model="x.provider" :aria-label="`${s.provider} ${j + 1}`">
                  <option v-for="n in providerNames" :key="n" :value="n">{{ n }}</option>
                </select>
              </td>
              <td><input v-model="x.model" class="mono" required :aria-label="`${s.model} ${j + 1}`" /></td>
              <td><input v-model.number="x.max_tokens" type="number" min="1" max="64000" :aria-label="`${s.maxTokens} ${j + 1}`" /></td>
              <td><input v-model="x.temperature" inputmode="decimal" :aria-label="`${s.temperature} ${j + 1}`" /></td>
              <td>
                <select v-model="x.effort" :aria-label="`${s.effort} ${j + 1}`">
                  <option value="">{{ s.effortDefault }}</option>
                  <option v-for="e in (['low', 'medium', 'high', 'max'] as const)" :key="e" :value="e">{{ e }}</option>
                </select>
              </td>
              <td>
                <button v-if="canManage && r.routes.length > 1" type="button" class="btn btn-ghost btn-sm" :aria-label="s.removeRoute(j + 1)" @click="r.routes.splice(j, 1)">×</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="canManage && r.routes.length < 5">
          <button type="button" class="btn btn-sm" @click="r.routes.push(newRoute())">{{ s.addRoute }}</button>
        </div>
      </fieldset>
      <div v-if="canManage" class="row">
        <button type="button" class="btn btn-sm" @click="rules.push({ task: 'translate', locales: '', routes: [newRoute()] })">{{ s.addRule }}</button>
        <span class="spacer" />
        <button v-if="scope === 'project' && effective?.source === 'project'" type="button" class="btn" :disabled="busy" @click="resetProject">{{ s.resetProject }}</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || !view">{{ s.saveRouting }}</button>
      </div>
      <p class="muted" role="status">{{ status }}</p>
    </form>
  </section>
</template>

<style scoped>
.scope {
  max-inline-size: 20rem;
}
.rule {
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  padding: var(--kl-space-3);
  margin: 0;
  min-inline-size: 0;
  overflow-x: auto;
}
.row {
  align-items: flex-end;
}
.grow {
  flex: 1;
}
.small {
  font-size: var(--kl-text-sm);
}
.small td {
  padding: var(--kl-space-1);
}
.small input,
.small select {
  inline-size: 100%;
  min-inline-size: 5rem;
}
</style>
