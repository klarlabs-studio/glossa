<script setup lang="ts">
/**
 * The approvals inbox (RFC 0006 §3.2): translation approvals waiting for
 * a decision I may make, each with the text under approval and who wrote
 * it, granted or denied with a reason.
 *
 * Approving is human and four-eyes: the server refuses a token or an
 * agent (`person_required`), the author of the text (`own_text`) and
 * anyone outside the eligible party (`not_eligible`). Each refusal is a
 * sentence saying which, never a generic error. Where Studio can tell
 * beforehand that the text is mine, it says so and offers no decision.
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { useRoute } from "vue-router";
import { isApiError } from "../../api/errors";
import type { ProjectTranslation } from "../../api/schemas";
import { useWork } from "../../api/work";
import type { Approval, Group } from "../../api/work-schemas";
import { absoluteTime } from "../../lib/time";
import { eligibility, grantCount, hasDecided, type Eligibility } from "../../lib/work";
import { allowsFor } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { membershipFor, useGrant, useSession } from "../../session/session";
import { problemText, strings } from "../../strings";

const s = strings.approvals;
const route = useRoute();
const port = useWork();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const { person } = useSession();
const self = computed(() => (person.value ? `person:${person.value.id}` : ""));
const label = usePeople(() => tenant.value);

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const items = shallowRef<Array<Approval & { eligibility: Eligibility }>>([]);
const groups = shallowRef<Group[] | undefined>();
const projectNames = shallowRef(new Map<string, string>());
const status = ref("");
const busy = ref<string>();
const errors = ref<Record<string, string>>({});
const reasons = ref<Record<string, string>>({});
const heading = useTemplateRef<HTMLHeadingElement>("heading");

/** The text under approval, per project and locale: read once for every unit there. */
type Texts = { state: "loading" } | { state: "ready"; byMessage: Map<string, ProjectTranslation> } | { state: "failed"; error: unknown };
const texts = ref<Record<string, Texts>>({});
const textKey = (a: Pick<Approval, "project_id" | "locale">) => `${a.project_id}/${a.locale ?? ""}`;

async function loadTexts(list: readonly Approval[]): Promise<void> {
  const wanted = new Map<string, { project: string; locale: string; ids: Set<string> }>();
  for (const a of list) {
    if (!a.locale) continue;
    const k = textKey(a);
    const w = wanted.get(k) ?? { project: a.project_id, locale: a.locale, ids: new Set<string>() };
    w.ids.add(a.subject_id);
    wanted.set(k, w);
  }
  texts.value = Object.fromEntries([...wanted.keys()].map((k) => [k, { state: "loading" } as Texts]));
  await Promise.all(
    [...wanted].map(async ([k, w]) => {
      try {
        const byMessage = await port.unitTexts({ tenant: tenant.value, project: w.project }, w.locale, w.ids);
        texts.value = { ...texts.value, [k]: { state: "ready", byMessage } };
      } catch (error) {
        texts.value = { ...texts.value, [k]: { state: "failed", error } };
      }
    }),
  );
}

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  const [pending, gs, projects] = await Promise.allSettled([
    port.approvals(tenant.value, { state: "pending", subject: "translation" }),
    port.groups(tenant.value),
    port.projects(tenant.value),
  ]);
  groups.value = gs.status === "fulfilled" ? gs.value : undefined;
  if (projects.status === "fulfilled") projectNames.value = new Map(projects.value.map((p) => [p.id, p.name]));
  if (pending.status === "rejected") {
    items.value = [];
    loadError.value = pending.reason;
    phase.value = "failed";
    return;
  }
  const m = membershipFor(tenant.value);
  const me = { memberId: m?.member_id ?? "", roles: m?.roles ?? [], groups: groups.value };
  items.value = pending.value
    .map((a) => ({ ...a, eligibility: eligibility(a, me) }))
    .filter((a) => a.eligibility !== "no" && !!a.locale && allowsFor(grant.value, "approvals.decide", a.locale) && !hasDecided(a, self.value));
  phase.value = "ready";
  void loadTexts(items.value);
}
watch(tenant, () => void load(), { immediate: true });

const textOf = (a: Approval) => texts.value[textKey(a)];
const translationOf = (a: Approval): ProjectTranslation | undefined => {
  const t = textOf(a);
  return t?.state === "ready" ? t.byMessage.get(a.subject_id) : undefined;
};
const textError = (a: Approval): string => {
  const t = textOf(a);
  return t?.state === "failed" ? problemText(t.error) : "";
};
const keyOf = (a: Approval) => translationOf(a)?.key ?? s.unknownKey(a.subject_id);
/** Four-eyes, known beforehand: the text is mine. The server would refuse with `own_text`. */
const mine = (a: Approval) => a.distinct_from_author && !!self.value && translationOf(a)?.author === self.value;
/** No decision on text that isn't on screen. */
const decidable = (a: Approval) => textOf(a)?.state === "ready" && !mine(a);
const groupName = (id?: string) => groups.value?.find((g) => g.id === id)?.name ?? id ?? "";
const askedOf = (a: Approval) => {
  const e = a.eligible;
  if (e.kind === "member") return s.askedOfYou;
  if (e.kind === "role") return s.askedOfRole(e.role ?? "");
  return s.askedOfGroup(groupName(e.id));
};
const groupsUnknown = computed(() => groups.value === undefined && items.value.some((a) => a.eligibility === "maybe"));

async function focusAfter(index: number): Promise<void> {
  await nextTick();
  const next = items.value[Math.min(index, items.value.length - 1)];
  const el = next ? document.getElementById(`ap-h-${next.id}`) : null;
  (el ?? heading.value)?.focus();
}

async function decide(a: Approval, decision: "granted" | "denied"): Promise<void> {
  if (busy.value) return;
  busy.value = a.id;
  status.value = "";
  const { [a.id]: _cleared, ...rest } = errors.value;
  errors.value = rest;
  const index = items.value.findIndex((x) => x.id === a.id);
  const reason = reasons.value[a.id]?.trim();
  try {
    await port.decide(tenant.value, a.id, decision, reason || undefined);
    // Decided by me, so no longer mine to decide, whatever it still waits for.
    items.value = items.value.filter((x) => x.id !== a.id);
    status.value = (decision === "granted" ? s.granted : s.denied)(keyOf(a), a.locale ?? "");
    await focusAfter(index);
  } catch (e) {
    if (isApiError(e, "approval_closed") || isApiError(e, "approval_superseded") || isApiError(e, "not_found")) {
      await load();
      status.value = isApiError(e, "approval_superseded") ? s.superseded : s.closed;
      await focusAfter(index);
    } else {
      // own_text, not_eligible and person_required each have their own sentence (strings.problems).
      errors.value = { ...errors.value, [a.id]: problemText(e) };
      await nextTick();
      document.getElementById(`ap-h-${a.id}`)?.focus();
    }
  } finally {
    busy.value = undefined;
  }
}
</script>

<template>
  <div class="page stack approvals">
    <div class="page-header">
      <div class="stack-sm">
        <h1 ref="heading" tabindex="-1">{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>
    <p role="status" aria-live="polite" data-testid="approvals-status">{{ status }}</p>

    <p v-if="phase === 'loading'" class="muted" data-testid="approvals-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="approvals-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button></div>
    </div>
    <p v-else-if="!items.length" class="card muted" data-testid="approvals-empty">{{ s.empty }}</p>
    <template v-else>
      <p class="hint">{{ s.count(items.length) }}</p>
      <p v-if="groupsUnknown" class="alert alert-warn" data-testid="approvals-groups-unknown">{{ s.groupsFailed }}</p>
      <ul class="cards">
        <li v-for="a in items" :key="a.id">
          <article class="card stack-sm" :aria-labelledby="`ap-h-${a.id}`" data-testid="approval" :data-approval="a.id">
            <div class="row">
              <h2 :id="`ap-h-${a.id}`" class="mono key" tabindex="-1">{{ keyOf(a) }}</h2>
              <span class="pill pill-neutral">{{ a.locale }}</span>
              <span class="pill pill-warn" data-testid="approval-state">{{ s.pending }}</span>
            </div>
            <p class="meta">
              {{ projectNames.get(a.project_id) ?? a.project_id }} · {{ askedOf(a) }} ·
              <span data-testid="approval-progress">{{ s.progress(grantCount(a), a.required) }}</span>
              <template v-if="a.due_at"> · <time :datetime="a.due_at">{{ s.due(absoluteTime(a.due_at)) }}</time></template>
            </p>

            <div class="stack-sm">
              <h3 class="label">{{ s.text }}</h3>
              <p v-if="!textOf(a) || textOf(a)!.state === 'loading'" class="muted" data-testid="approval-text-loading">{{ s.textLoading }}</p>
              <div v-else-if="textOf(a)!.state === 'failed'" class="alert alert-error" role="alert" data-testid="approval-text-failed">
                <p>{{ s.textFailed }}</p>
                <p>{{ textError(a) }}</p>
              </div>
              <p v-else-if="!translationOf(a)" class="muted" data-testid="approval-no-text">{{ s.noText }}</p>
              <template v-else>
                <p class="text" :lang="a.locale" data-testid="approval-text">{{ translationOf(a)!.text }}</p>
                <p class="hint" data-testid="approval-author">{{ s.author(label(translationOf(a)!.author)) }}</p>
              </template>
            </div>

            <p v-if="mine(a)" class="alert alert-warn" data-testid="approval-own-text">{{ s.ownText }}</p>
            <p v-else-if="a.distinct_from_author" class="hint">{{ s.fourEyes }}</p>
            <p v-if="errors[a.id]" class="alert alert-error" role="alert" data-testid="approval-error">{{ errors[a.id] }}</p>

            <template v-if="!mine(a)">
              <div class="field">
                <label :for="`ap-reason-${a.id}`">{{ s.reason }}</label>
                <textarea :id="`ap-reason-${a.id}`" v-model="reasons[a.id]" rows="2" maxlength="2000" data-testid="approval-reason" />
              </div>
              <div class="row">
                <button type="button" class="btn btn-primary" :disabled="!!busy || !decidable(a)" data-testid="approval-grant" @click="decide(a, 'granted')">{{ s.grant }}</button>
                <button type="button" class="btn btn-danger" :disabled="!!busy || !decidable(a)" data-testid="approval-deny" @click="decide(a, 'denied')">{{ s.deny }}</button>
              </div>
            </template>
          </article>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.approvals {
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
.key {
  margin: 0;
  font-size: var(--kl-text-lg, 1.125rem);
  word-break: break-all;
}
.meta {
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
}
.text {
  white-space: pre-wrap;
  padding: var(--kl-space-3);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
}
textarea {
  inline-size: 100%;
}
</style>
