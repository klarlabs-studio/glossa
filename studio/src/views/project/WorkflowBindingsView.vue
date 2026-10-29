<script setup lang="ts">
/**
 * A project's workflow bindings (RFC 0006 §2.3): which definition its
 * work runs under, per locale and namespace, in the order the server
 * tries them — the check policy's precedence rule, shown rather than
 * described. With no binding nothing changes: M4's behaviour.
 *
 * "Which workflow applies?" asks the server (the resolution endpoint),
 * not a copy of its rule, so the answer is the one it would act on.
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink } from "vue-router";
import { useWorkflows } from "../../api/workflows";
import type { WorkflowBinding, WorkflowDefinition } from "../../api/workflows-schemas";
type WorkflowSubject = "translation" | "release_request";
import type { WorkflowResolution } from "../../api/work-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { absoluteTime, relativeTime } from "../../lib/time";
import { precedence, specificity } from "../../lib/workflow";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const s = strings.workflows;
const b = s.bindings;
const port = useWorkflows();
const { tenant, projectId, targets, grant } = useProject();
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canManage = computed(() => allows(grant.value, "workflows.manage"));
const person = usePeople(() => tenant.value);
const heading = useTemplateRef<HTMLHeadingElement>("heading");

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const bindings = shallowRef<WorkflowBinding[]>([]);
const definitions = shallowRef<WorkflowDefinition[]>([]);
const definitionsError = ref<unknown>(null);
const status = ref("");

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  const [bs, ds] = await Promise.allSettled([port.bindings(project()), port.definitions(tenant.value, projectId.value)]);
  definitions.value = ds.status === "fulfilled" ? ds.value : [];
  definitionsError.value = ds.status === "rejected" ? ds.reason : null;
  if (bs.status === "rejected") {
    bindings.value = [];
    loadError.value = bs.reason;
    phase.value = "failed";
    return;
  }
  bindings.value = bs.value;
  phase.value = "ready";
}
watch(projectId, () => void load(), { immediate: true });

const SUBJECTS: WorkflowSubject[] = ["translation", "release_request"];
const ordered = computed(() => SUBJECTS.map((subject) => ({ subject, list: precedence(bindings.value.filter((x) => x.subject === subject)) })));
const nameOf = (id: string) => definitions.value.find((d) => d.id === id)?.name;

// ── unbind ─────────────────────────────────────────────────────────
const busy = ref<string>();
const actionError = ref<unknown>(null);
async function unbind(x: WorkflowBinding): Promise<void> {
  if (busy.value) return;
  busy.value = x.id;
  actionError.value = null;
  status.value = "";
  try {
    await port.unbind(project(), x.id);
    bindings.value = bindings.value.filter((y) => y.id !== x.id);
    resolution.value = undefined;
    status.value = b.removed(nameOf(x.definition_id) ?? b.unknownDefinition(x.definition_id));
    await nextTick();
    heading.value?.focus();
  } catch (e) {
    actionError.value = e;
  } finally {
    busy.value = undefined;
  }
}

// ── bind ───────────────────────────────────────────────────────────
const form = ref({ definition: "", locales: [] as string[], namespace: "" });
const bindError = ref<unknown>(null);
const binding = ref(false);
const chosen = computed(() => definitions.value.find((d) => d.id === form.value.definition));
async function bind(): Promise<void> {
  const d = chosen.value;
  if (!d || binding.value) return;
  binding.value = true;
  bindError.value = null;
  status.value = "";
  try {
    const created = await port.bind(project(), {
      definition_id: d.id,
      ...(d.subject === "translation" && form.value.locales.length ? { locales: form.value.locales } : {}),
      ...(d.subject === "translation" && form.value.namespace.trim() ? { namespace: form.value.namespace.trim() } : {}),
    });
    bindings.value = [...bindings.value, created];
    form.value = { definition: "", locales: [], namespace: "" };
    resolution.value = undefined;
    status.value = b.bound(d.name);
  } catch (e) {
    bindError.value = e;
  } finally {
    binding.value = false;
  }
}

// ── which applies ──────────────────────────────────────────────────
const ask = ref({ subject: "translation" as WorkflowSubject, locale: "", namespace: "" });
const resolution = shallowRef<WorkflowResolution>();
const resolving = ref(false);
const resolveError = ref<unknown>(null);
watch(targets, (t) => {
  if (!ask.value.locale && t[0]) ask.value.locale = t[0].code;
}, { immediate: true });
async function resolve(): Promise<void> {
  resolving.value = true;
  resolveError.value = null;
  resolution.value = undefined;
  try {
    const q = ask.value;
    resolution.value = await port.resolve(project(), {
      subject: q.subject,
      ...(q.subject === "translation" && q.locale ? { locale: q.locale } : {}),
      ...(q.subject === "translation" && q.namespace.trim() ? { namespace: q.namespace.trim() } : {}),
    });
  } catch (e) {
    resolveError.value = e;
  } finally {
    resolving.value = false;
  }
}
const matchedId = computed(() => resolution.value?.binding?.id);
const matchedRank = computed(() => {
  const r = resolution.value;
  if (!r?.binding) return undefined;
  const list = ordered.value.find((o) => o.subject === r.binding!.subject)?.list ?? [];
  const i = list.findIndex((x) => x.id === r.binding!.id);
  return i >= 0 ? i + 1 : undefined;
});
</script>

<template>
  <div class="page stack bindings-page">
    <div class="page-header">
      <div class="stack-sm">
        <h1 ref="heading" tabindex="-1">{{ b.title }}</h1>
        <p class="muted lead">{{ b.lead }}</p>
      </div>
      <RouterLink class="btn" :to="{ name: 'workflow-instances', params: { tenant, project: projectId } }" data-testid="bindings-instances">{{ b.instances }}</RouterLink>
    </div>
    <p v-if="!canManage" class="alert" data-testid="bindings-read-only">{{ b.readOnly }}</p>
    <p role="status" aria-live="polite" data-testid="bindings-status">{{ status }}</p>
    <ErrorAlert :error="actionError" />

    <p v-if="phase === 'loading'" class="muted" data-testid="bindings-loading">{{ b.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="bindings-failed">
      <p class="alert-title">{{ b.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button></div>
    </div>
    <template v-else>
      <p class="hint order">{{ b.order }}</p>
      <section v-for="group in ordered" :key="group.subject" class="stack-sm" :aria-labelledby="`bind-h-${group.subject}`" :data-testid="`bindings-${group.subject}`">
        <h2 :id="`bind-h-${group.subject}`">{{ s.subject[group.subject] }}</h2>
        <p v-if="group.subject === 'release_request'" class="hint">{{ b.releaseDefault }}</p>
        <p v-if="!group.list.length" class="card muted" data-testid="bindings-none">{{ group.subject === "translation" ? b.none : b.releaseDefault }}</p>
        <div v-else class="scroll">
          <table class="table">
            <thead>
              <tr>
                <th scope="col">{{ b.columns.order }}</th>
                <th scope="col">{{ b.columns.workflow }}</th>
                <th v-if="group.subject === 'translation'" scope="col">{{ b.columns.locales }}</th>
                <th v-if="group.subject === 'translation'" scope="col">{{ b.columns.namespace }}</th>
                <th scope="col">{{ b.columns.specificity }}</th>
                <th scope="col">{{ b.columns.created }}</th>
                <th v-if="canManage" scope="col"><span class="visually-hidden">{{ strings.releases.columns.actions }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(x, i) in group.list" :key="x.id" :data-binding="x.id" :class="{ matched: matchedId === x.id }" :aria-current="matchedId === x.id ? 'true' : undefined">
                <td class="num">{{ i + 1 }}</td>
                <th scope="row">
                  <RouterLink v-if="nameOf(x.definition_id)" class="mono" :to="{ name: 'workflow', params: { tenant, definition: x.definition_id } }">{{ nameOf(x.definition_id) }}</RouterLink>
                  <span v-else class="muted">{{ b.unknownDefinition(x.definition_id) }}</span>
                </th>
                <td v-if="group.subject === 'translation'">{{ x.locales.length ? x.locales.join(", ") : b.everyLocale }}</td>
                <td v-if="group.subject === 'translation'" class="mono">{{ x.namespace ?? b.everyNamespace }}</td>
                <td>{{ b.fields(specificity(x)) }}</td>
                <td>
                  <time :datetime="x.created_at" :title="absoluteTime(x.created_at)">{{ relativeTime(x.created_at) }}</time>
                  <span class="muted"> · {{ person(x.created_by) }}</span>
                </td>
                <td v-if="canManage" class="actions">
                  <button type="button" class="btn btn-sm btn-ghost" :disabled="!!busy" data-testid="binding-remove" @click="unbind(x)">
                    {{ b.remove }}<span class="visually-hidden">{{ b.removeOf(nameOf(x.definition_id) ?? "") }}</span>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card stack-sm" aria-labelledby="resolve-h" data-testid="bindings-resolve">
        <h2 id="resolve-h">{{ b.resolveTitle }}</h2>
        <p class="hint">{{ b.resolveLead }}</p>
        <form class="row fields" @submit.prevent="resolve">
          <div class="field">
            <label for="rs-subject">{{ b.resolveSubject }}</label>
            <select id="rs-subject" v-model="ask.subject">
              <option v-for="sub in SUBJECTS" :key="sub" :value="sub">{{ s.subject[sub] }}</option>
            </select>
          </div>
          <template v-if="ask.subject === 'translation'">
            <div class="field">
              <label for="rs-locale">{{ b.resolveLocale }}</label>
              <select id="rs-locale" v-model="ask.locale">
                <option v-for="l in targets" :key="l.code" :value="l.code">{{ l.code }}</option>
              </select>
            </div>
            <div class="field">
              <label for="rs-ns">{{ b.resolveNamespace }}</label>
              <input id="rs-ns" v-model="ask.namespace" class="mono" maxlength="256" />
            </div>
          </template>
          <button type="submit" class="btn" :disabled="resolving" data-testid="resolve-ask">{{ resolving ? b.resolving : b.resolve }}</button>
        </form>
        <div aria-live="polite" data-testid="resolve-answer">
          <template v-if="resolution">
            <p v-if="!resolution.bound">{{ b.resolvedNone }}</p>
            <template v-else>
              <p>{{ b.resolvedTo(resolution.definition_name ?? "", resolution.version ?? 1) }}</p>
              <p v-if="matchedRank" class="hint">{{ b.resolvedBinding(matchedRank) }}</p>
            </template>
          </template>
        </div>
        <ErrorAlert :error="resolveError" />
      </section>

      <section v-if="canManage" class="card stack-sm" aria-labelledby="bind-h" data-testid="bindings-add">
        <h2 id="bind-h">{{ b.addTitle }}</h2>
        <p v-if="definitionsError" class="alert alert-warn">{{ problemText(definitionsError) }}</p>
        <div v-else-if="!definitions.length" class="stack-sm">
          <p class="muted">{{ b.noDefinitions }}</p>
          <div><RouterLink class="btn btn-sm" :to="{ name: 'workflows', params: { tenant } }">{{ b.writeOne }}</RouterLink></div>
        </div>
        <form v-else class="stack" @submit.prevent="bind">
          <div class="field">
            <label for="bd-def">{{ b.definition }}</label>
            <select id="bd-def" v-model="form.definition" required data-testid="bind-definition">
              <option value="" disabled>{{ b.choose }}</option>
              <option v-for="d in definitions" :key="d.id" :value="d.id">{{ d.name }} · {{ s.subject[d.subject] }} · {{ s.version(d.version) }}</option>
            </select>
          </div>
          <template v-if="!chosen || chosen.subject === 'translation'">
            <fieldset class="stack-sm" aria-describedby="bd-locales-hint">
              <legend class="label">{{ b.locales }}</legend>
              <div class="row wrap">
                <label v-for="l in targets" :key="l.code" class="check">
                  <input v-model="form.locales" type="checkbox" :value="l.code" />
                  <span>{{ l.code }}</span>
                </label>
              </div>
              <span id="bd-locales-hint" class="hint">{{ b.localesHint }}</span>
            </fieldset>
            <div class="field">
              <label for="bd-ns">{{ b.namespace }}</label>
              <input id="bd-ns" v-model="form.namespace" class="mono" maxlength="256" aria-describedby="bd-ns-hint" />
              <span id="bd-ns-hint" class="hint">{{ b.namespaceHint }}</span>
            </div>
          </template>
          <ErrorAlert :error="bindError" />
          <div><button type="submit" class="btn btn-primary" :disabled="binding || !chosen" data-testid="bind-submit">{{ b.bind }}</button></div>
        </form>
      </section>
    </template>
  </div>
</template>

<style scoped>
.bindings-page {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead,
.order {
  max-inline-size: 52rem;
}
.scroll {
  overflow-x: auto;
}
.num {
  font-variant-numeric: tabular-nums;
}
.actions {
  text-align: end;
}
tr.matched {
  background: var(--kl-accent-dim);
}
.fields {
  align-items: flex-end;
}
.wrap {
  flex-wrap: wrap;
}
fieldset {
  border: none;
  margin: 0;
  padding: 0;
}
</style>
