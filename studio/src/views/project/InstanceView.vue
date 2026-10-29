<script setup lang="ts">
/**
 * One workflow instance and its transition log (RFC 0006 §2.5).
 *
 * The log is the explanation: for every event, where the instance was and
 * where it went, which guards held, which actions ran and what became of
 * them, and whose event it was — actions run as that actor. `ignored`
 * and `refused` each get a sentence, because neither is an error a
 * person should chase: the first is the world having moved on, the
 * second a permission the actor lacked, with the instance left in place.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { useWorkflows } from "../../api/workflows";
import type { WorkflowDefinition, WorkflowInstance, WorkflowTransition } from "../../api/workflows-schemas";
import { absoluteTime, relativeTime } from "../../lib/time";
import { usePeople } from "../../session/people";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const s = strings.instances;
const route = useRoute();
const port = useWorkflows();
const { tenant, projectId } = useProject();
const person = usePeople(() => tenant.value);
const instanceId = computed(() => String(route.params.instance));
/** The message key, when the link that led here knew it. */
const key = computed(() => (typeof route.query.key === "string" && route.query.key ? route.query.key : undefined));

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const instance = shallowRef<WorkflowInstance>();
const definition = shallowRef<WorkflowDefinition>();
const log = shallowRef<WorkflowTransition[]>([]);
const logError = ref<unknown>(null);

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  logError.value = null;
  const p = { tenant: tenant.value, project: projectId.value };
  const [inst, transitions] = await Promise.allSettled([port.instance(p, instanceId.value), port.transitions(p, instanceId.value)]);
  if (inst.status === "rejected") {
    loadError.value = inst.reason;
    phase.value = "failed";
    return;
  }
  instance.value = inst.value;
  if (transitions.status === "fulfilled") log.value = transitions.value;
  else {
    log.value = [];
    logError.value = transitions.reason;
  }
  phase.value = "ready";
  // The definition's name, best effort: without it the id stands in.
  try {
    definition.value = (await port.definition(tenant.value, inst.value.definition_id)).value;
  } catch {
    definition.value = undefined;
  }
}
watch([projectId, instanceId], () => void load(), { immediate: true });

const subject = computed(() => {
  const i = instance.value;
  if (!i) return "";
  if (i.subject === "release_request") return s.releaseRequest(i.subject_id);
  return key.value ?? s.message_(i.subject_id);
});
const definitionName = computed(() => definition.value?.name ?? s.unknownDefinition(instance.value?.definition_id ?? ""));
</script>

<template>
  <div class="page stack instance">
    <p v-if="phase === 'loading'" class="muted" role="status" data-testid="instance-loading">{{ s.instanceLoading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="instance-failed">
      <p class="alert-title">{{ s.instanceFailed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div class="row">
        <button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button>
        <RouterLink :to="{ name: 'workflow-instances', params: { tenant, project: projectId } }">{{ s.allInstances }}</RouterLink>
      </div>
    </div>
    <template v-else-if="instance">
      <div class="page-header">
        <div class="stack-sm">
          <RouterLink :to="{ name: 'workflow-instances', params: { tenant, project: projectId } }">{{ s.allInstances }}</RouterLink>
          <h1 class="mono">{{ s.instanceTitle(subject) }}</h1>
        </div>
      </div>

      <section class="card stack-sm" aria-labelledby="inst-facts-h" data-testid="instance-facts">
        <h2 id="inst-facts-h">{{ s.facts }}</h2>
        <dl class="facts">
          <dt>{{ s.subject }}</dt>
          <dd>
            {{ s.subjectKind[instance.subject] ?? instance.subject }}: <span class="mono">{{ subject }}</span>
            <template v-if="instance.subject === 'release_request'">
              ·
              <RouterLink :to="{ name: 'release-request', params: { tenant, project: projectId, request: instance.subject_id } }" data-testid="instance-request-link">{{ s.openRequest }}</RouterLink>
            </template>
            <template v-else-if="key && instance.locale">
              ·
              <RouterLink :to="{ name: 'translate', params: { tenant, project: projectId }, query: { locale: instance.locale, key } }" data-testid="instance-workspace-link">{{ s.openWorkspace }}</RouterLink>
            </template>
          </dd>
          <template v-if="instance.locale">
            <dt>{{ s.locale }}</dt>
            <dd>{{ instance.locale }}</dd>
          </template>
          <dt>{{ s.workflow }}</dt>
          <dd>
            <RouterLink :to="{ name: 'workflow', params: { tenant, definition: instance.definition_id }, query: { version: String(instance.definition_version) } }" data-testid="instance-definition-link">
              {{ s.workflowVersion(definitionName, instance.definition_version) }}
            </RouterLink>
            <span class="hint"> — {{ s.runsOn }}</span>
          </dd>
          <dt>{{ s.state }}</dt>
          <dd>
            <span class="mono" data-testid="instance-state">{{ instance.state }}</span>
            <span class="pill" :class="`pill-${s.statusTone[instance.status] ?? 'neutral'}`" data-testid="instance-status">{{ s.statusName[instance.status] ?? instance.status }}</span>
          </dd>
          <dt>{{ s.started }}</dt>
          <dd><time :datetime="instance.created_at">{{ absoluteTime(instance.created_at) }}</time></dd>
          <dt>{{ s.updated }}</dt>
          <dd><time :datetime="instance.updated_at" :title="absoluteTime(instance.updated_at)">{{ relativeTime(instance.updated_at) }}</time></dd>
        </dl>
      </section>

      <section class="stack" aria-labelledby="inst-log-h">
        <div class="stack-sm">
          <h2 id="inst-log-h">{{ s.log }}</h2>
          <p class="muted">{{ s.logLead }}</p>
        </div>
        <div v-if="logError" class="alert alert-error" role="alert" data-testid="instance-log-failed">
          <p>{{ s.logFailed }}</p>
          <p>{{ problemText(logError) }}</p>
        </div>
        <p v-else-if="!log.length" class="muted" data-testid="instance-log-empty">{{ s.logEmpty }}</p>
        <ol v-else class="log" data-testid="instance-log">
          <li v-for="t in log" :key="t.seq">
            <article class="card stack-sm" :aria-labelledby="`tr-${t.seq}`" data-testid="transition" :data-outcome="t.outcome">
              <div class="row">
                <h3 :id="`tr-${t.seq}`" class="mono event">{{ s.seq(t.seq) }} {{ t.event }}</h3>
                <span class="pill" :class="`pill-${s.outcomeTone[t.outcome] ?? 'neutral'}`" data-testid="transition-outcome">{{ s.outcome[t.outcome] ?? t.outcome }}</span>
                <span class="spacer" />
                <time :datetime="t.at" :title="absoluteTime(t.at)">{{ relativeTime(t.at) }}</time>
              </div>
              <p class="mono" data-testid="transition-move">{{ t.outcome === "applied" ? s.move(t.from, t.to) : s.stayed(t.from) }}</p>
              <p class="hint" data-testid="transition-explain">{{ s.outcomeText[t.outcome] }}</p>
              <div class="cols">
                <div class="stack-sm">
                  <h4 class="label">{{ s.guards }}</h4>
                  <p v-if="!t.guards.length" class="muted">{{ s.noGuards }}</p>
                  <ul v-else class="plain" data-testid="transition-guards">
                    <li v-for="g in t.guards" :key="g.guard">
                      <span class="mono">{{ g.guard }}</span>:
                      <span :class="g.passed ? 'ok' : 'err'">{{ g.passed ? s.guardPassed : s.guardFailed }}</span>
                    </li>
                  </ul>
                </div>
                <div class="stack-sm">
                  <h4 class="label">{{ s.actions }}</h4>
                  <p v-if="!t.actions.length" class="muted">{{ s.noActions }}</p>
                  <ul v-else class="plain" data-testid="transition-actions">
                    <li v-for="(a, idx) in t.actions" :key="`${a.name}-${idx}`">
                      <span class="mono">{{ a.name }}</span>
                      <span class="pill" :class="`pill-${s.actionTone[a.outcome] ?? 'neutral'}`">{{ s.actionOutcome[a.outcome] ?? a.outcome }}</span>
                      <span v-if="a.detail" class="hint"> — {{ a.detail }}</span>
                    </li>
                  </ul>
                </div>
              </div>
              <p class="meta" data-testid="transition-actor">{{ s.actor(person(t.actor)) }}</p>
            </article>
          </li>
        </ol>
      </section>
    </template>
  </div>
</template>

<style scoped>
.instance {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.facts {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-2) var(--kl-space-4);
  margin: 0;
}
.facts dt {
  font-weight: var(--kl-weight-medium);
}
.facts dd {
  margin: 0;
}
.log {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.event {
  margin: 0;
  font-size: var(--kl-text-md, 1rem);
}
.cols {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  gap: var(--kl-space-4);
}
.plain {
  margin: 0;
  padding-inline-start: var(--kl-space-4);
}
.ok {
  color: var(--kl-success, inherit);
}
.err {
  color: var(--kl-danger, inherit);
}
.meta {
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
}
</style>
