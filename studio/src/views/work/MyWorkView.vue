<script setup lang="ts">
/**
 * My work (RFC 0006 §3.1): the translation units assigned to me, one
 * batch per assignment, each opening into the translator workspace
 * scoped to its units. It is a vendor member's main screen (§3.3): they
 * see nothing else, so everything they need to do with their work is
 * here — accept it, open it, mark it complete, hand it back.
 *
 * "Could not be read" and "nothing assigned" are different states, and
 * an assignment's state is always a word before it is a colour.
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { isApiError } from "../../api/errors";
import type { Project } from "../../api/schemas";
import { useWork } from "../../api/work";
import { isLive, type Assignment } from "../../api/work-schemas";
import ModalDialog from "../../components/ModalDialog.vue";
import { absoluteTime } from "../../lib/time";
import { byUrgency, dueState, unitLocales } from "../../lib/work";
import { problemText, strings } from "../../strings";

const s = strings.work;
const route = useRoute();
const port = useWork();
const tenant = computed(() => String(route.params.tenant));

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const items = shallowRef<Assignment[]>([]);
const projectNames = shallowRef(new Map<string, string>());
const status = ref("");
const busy = ref<string>();
const cardErrors = ref<Record<string, string>>({});
const heading = useTemplateRef<HTMLHeadingElement>("heading");

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  // Project names are a label, not the work: without them a card names
  // its project by id, while a failed assignment read fails the screen.
  const [work, projects] = await Promise.allSettled([port.myAssignments(tenant.value), port.projects(tenant.value)]);
  if (projects.status === "fulfilled") projectNames.value = new Map(projects.value.map((p: Project) => [p.id, p.name]));
  if (work.status === "rejected") {
    items.value = [];
    loadError.value = work.reason;
    phase.value = "failed";
    return;
  }
  items.value = [...work.value].sort(byUrgency);
  phase.value = "ready";
}
watch(tenant, () => void load(), { immediate: true });

const live = computed(() => items.value.filter(isLive));
const finished = computed(() => items.value.filter((a) => !isLive(a)));
const projectName = (a: Assignment) => projectNames.value.get(a.project_id) ?? s.unknownProject(a.project_id);
const tone = (a: Assignment) => s.stateTone[a.state] ?? "neutral";
const due = (a: Assignment) => {
  if (!a.due_at) return s.noDue;
  const when = absoluteTime(a.due_at);
  return dueState(a) === "overdue" ? s.overdue(when) : s.due(when);
};
/** Done work stays readable for 30 days (§3.3), so it still opens. */
const opens = (a: Assignment) => isLive(a) || a.state === "done";
const workspaceLink = (a: Assignment) => ({
  name: "translate",
  params: { tenant: tenant.value, project: a.project_id },
  query: { assignment: a.id, locale: unitLocales(a)[0] },
});

async function focusCard(id: string): Promise<void> {
  await nextTick();
  const el = document.getElementById(`as-h-${id}`);
  (el ?? heading.value)?.focus();
}

async function act(a: Assignment, run: () => Promise<Assignment>, done: (project: string) => string): Promise<void> {
  if (busy.value) return;
  busy.value = a.id;
  status.value = "";
  const { [a.id]: _cleared, ...rest } = cardErrors.value;
  cardErrors.value = rest;
  try {
    const next = await run();
    items.value = items.value.map((x) => (x.id === next.id ? next : x)).sort(byUrgency);
    status.value = done(projectName(next));
    await focusCard(next.id);
  } catch (e) {
    if (isApiError(e, "not_found") || isApiError(e, "assignment_state")) {
      // The list was stale: say what happened, then read it again.
      await load();
      status.value = isApiError(e, "not_found") ? s.gone : s.moved;
      await focusCard(a.id);
    } else {
      cardErrors.value = { ...cardErrors.value, [a.id]: isApiError(e, "forbidden") ? s.forbidden : problemText(e) };
      await focusCard(a.id);
    }
  } finally {
    busy.value = undefined;
  }
}

const accept = (a: Assignment) => act(a, () => port.accept(tenant.value, a.id), s.accepted);
const complete = (a: Assignment) => act(a, () => port.complete(tenant.value, a.id), s.completed);

const declining = ref<Assignment>();
const declineReason = ref("");
function startDecline(a: Assignment): void {
  declining.value = a;
  declineReason.value = "";
}
async function confirmDecline(): Promise<void> {
  const a = declining.value;
  if (!a) return;
  declining.value = undefined;
  const reason = declineReason.value.trim();
  await act(a, () => port.decline(tenant.value, a.id, reason || undefined), s.declined);
}
</script>

<template>
  <div class="page stack my-work">
    <div class="page-header">
      <div class="stack-sm">
        <h1 ref="heading" tabindex="-1">{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>
    <p class="status" role="status" aria-live="polite" data-testid="work-status">{{ status }}</p>

    <p v-if="phase === 'loading'" class="muted" data-testid="work-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="work-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button></div>
    </div>
    <p v-else-if="!items.length" class="card muted" data-testid="work-empty">{{ s.empty }}</p>
    <template v-else>
      <section class="stack-sm" aria-labelledby="work-live-h">
        <h2 id="work-live-h">{{ s.live }} <span class="muted count">{{ s.count(live.length) }}</span></h2>
        <p v-if="!live.length" class="muted" data-testid="work-none-live">{{ s.noneLive }}</p>
        <ul v-else class="cards" data-testid="work-live">
          <li v-for="a in live" :key="a.id">
            <article class="card stack-sm" :aria-labelledby="`as-h-${a.id}`" data-testid="assignment" :data-assignment="a.id">
              <div class="row">
                <h3 :id="`as-h-${a.id}`" tabindex="-1">{{ projectName(a) }}</h3>
                <span class="pill" :class="`pill-${tone(a)}`" data-testid="assignment-state">{{ s.state[a.state] }}</span>
                <span v-if="dueState(a) === 'overdue'" class="pill pill-err" data-testid="assignment-overdue">{{ due(a) }}</span>
              </div>
              <p class="meta">
                <span data-testid="assignment-units">{{ s.units(a.units.length) }}</span>
                · <span data-testid="assignment-locales">{{ s.locales(unitLocales(a).join(", ")) }}</span>
                <template v-if="dueState(a) !== 'overdue'">
                  · <time v-if="a.due_at" :datetime="a.due_at" data-testid="assignment-due">{{ due(a) }}</time><span v-else data-testid="assignment-due">{{ due(a) }}</span>
                </template>
              </p>
              <p v-if="cardErrors[a.id]" class="alert alert-error" role="alert" data-testid="assignment-error">{{ cardErrors[a.id] }}</p>
              <div class="row">
                <RouterLink class="btn btn-sm" :to="workspaceLink(a)" data-testid="assignment-open">{{ s.open }}</RouterLink>
                <button v-if="a.state === 'open'" type="button" class="btn btn-sm btn-primary" :disabled="!!busy" data-testid="assignment-accept" @click="accept(a)">{{ s.accept }}</button>
                <button v-if="a.state === 'accepted'" type="button" class="btn btn-sm btn-primary" :disabled="!!busy" data-testid="assignment-complete" @click="complete(a)">{{ s.complete }}</button>
                <button type="button" class="btn btn-sm" :disabled="!!busy" data-testid="assignment-decline" @click="startDecline(a)">{{ s.decline }}</button>
              </div>
            </article>
          </li>
        </ul>
      </section>
      <section v-if="finished.length" class="stack-sm" aria-labelledby="work-done-h">
        <h2 id="work-done-h">{{ s.finished }} <span class="muted count">{{ s.count(finished.length) }}</span></h2>
        <ul class="cards" data-testid="work-finished">
          <li v-for="a in finished" :key="a.id">
            <article class="card stack-sm" :aria-labelledby="`as-h-${a.id}`" data-testid="assignment" :data-assignment="a.id">
              <div class="row">
                <h3 :id="`as-h-${a.id}`" tabindex="-1">{{ projectName(a) }}</h3>
                <span class="pill" :class="`pill-${tone(a)}`" data-testid="assignment-state">{{ s.state[a.state] }}</span>
              </div>
              <p class="meta">
                <span data-testid="assignment-units">{{ s.units(a.units.length) }}</span>
                · <span data-testid="assignment-locales">{{ s.locales(unitLocales(a).join(", ")) }}</span>
                <template v-if="a.closed_at"> · <time :datetime="a.closed_at">{{ s.closed(absoluteTime(a.closed_at)) }}</time></template>
              </p>
              <p v-if="a.reason" class="hint">{{ s.reason(a.reason) }}</p>
              <div v-if="opens(a)" class="row">
                <RouterLink class="btn btn-sm" :to="workspaceLink(a)" data-testid="assignment-open">{{ s.open }}</RouterLink>
              </div>
            </article>
          </li>
        </ul>
      </section>
    </template>

    <ModalDialog :open="!!declining" :title="s.declineTitle" @close="declining = undefined">
      <p>{{ s.declineLead }}</p>
      <div class="field">
        <label for="decline-reason">{{ s.declineReason }}</label>
        <textarea id="decline-reason" v-model="declineReason" rows="3" maxlength="2000" data-testid="decline-reason" />
      </div>
      <template #actions>
        <button type="button" class="btn" @click="declining = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" data-testid="decline-confirm" @click="confirmDecline">{{ s.declineConfirm }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.my-work {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.cards {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.count {
  font-size: var(--kl-text-sm);
  font-weight: var(--kl-weight-normal, 400);
}
.meta {
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
}
h3 {
  margin: 0;
}
textarea {
  inline-size: 100%;
}
</style>
