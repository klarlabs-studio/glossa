<script setup lang="ts">
/**
 * Promote: point an environment at an existing release. Before
 * confirming it names the pointer move (production: v3 → v5), checks the
 * target's policy covers the release (mirrored; the server decides) and
 * shows per locale what changes compared with what the target serves.
 */
import { computed, ref, watch } from "vue";
import { RouterLink } from "vue-router";
import { isHeld, type ProjectRef, type ReleasesPort } from "../../api/releases";
import type { ReleaseHeld } from "../../api/release-ops-schemas";
import { partyText } from "../../lib/release-ops";
import type { Release, ReleaseDiff } from "../../api/schemas";
import { covers, uncovered } from "../../lib/releases";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import { type ReleaseBook } from "./book";
import LocaleTable from "./LocaleTable.vue";

const props = defineProps<{
  open: boolean;
  port: ReleasesPort;
  project: ProjectRef;
  book: ReleaseBook;
  environment?: string | undefined;
  releaseId?: string | undefined;
}>();
const emit = defineEmits<{ close: []; done: [message: string] }>();
const s = strings.releases;
const ro = strings.releaseOps;
/** Into an environment that requires approval: a release request, nothing deployed (RFC 0006 §5.1). */
const held = ref<ReleaseHeld>();

const releaseId = ref("");
const envName = ref("");
const diff = ref<ReleaseDiff>();
const busy = ref(false);
const error = ref<unknown>(null);

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    releaseId.value = props.releaseId ?? "";
    envName.value = props.environment ?? "";
    error.value = null;
    held.value = undefined;
  },
  { immediate: true },
);

const release = computed<Release | undefined>(() => props.book.byId.value.get(releaseId.value));
const target = computed(() => props.book.env(envName.value));
const currentId = computed(() => target.value?.current_release_id);
const current = computed(() => props.book.byId.value.get(currentId.value ?? ""));

type Verdict = { kind: "incomplete" } | { kind: "same" } | { kind: "ineligible"; text: string } | { kind: "ok" };
const verdict = computed<Verdict>(() => {
  const r = release.value;
  const t = target.value;
  if (!r || !t) return { kind: "incomplete" };
  if (t.current_release_id === r.id) return { kind: "same" };
  if (!covers(t.policy, r.policy)) {
    const miss = uncovered(t.policy, r.policy);
    const v = s.version(r.version);
    const text = miss.states.length
      ? s.ineligible(t.name, v, miss.states.map((st) => s.state[st] ?? st).join(", "))
      : s.ineligibleOutdated(t.name, v);
    return { kind: "ineligible", text };
  }
  return { kind: "ok" };
});

watch(
  [release, currentId, () => verdict.value.kind],
  async ([r, cur, kind]) => {
    diff.value = undefined;
    if (!props.open || !r || !cur || kind !== "ok") return;
    try {
      const [d] = await Promise.all([props.port.diff(props.project, r.id, cur), props.book.ensure([cur])]);
      if (release.value?.id === r.id && currentId.value === cur) diff.value = d;
    } catch (e) {
      error.value = e;
    }
  },
  { immediate: true },
);

const releaseOption = (r: Release) => `${s.version(r.version)} · ${r.environment}${r.note ? ` · ${r.note}` : ""}`;

async function promote(): Promise<void> {
  const r = release.value;
  const t = target.value;
  if (!r || !t || verdict.value.kind !== "ok") return;
  busy.value = true;
  error.value = null;
  try {
    const moved = await props.port.promote(props.project, t.name, r.id);
    if (isHeld(moved)) held.value = moved;
    else emit("done", s.promoted(s.version(r.version), t.name));
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog :open="open" :title="s.promoteTitle" wide @close="emit('close')">
    <div v-if="held" class="alert alert-warn stack-sm" role="status" data-testid="promote-held">
      <p class="alert-title">{{ ro.heldTitle }}</p>
      <p>{{ ro.held(held.release_request.environment, held.release_request.approval.n, partyText(held.release_request.approval.from)) }}</p>
      <p>
        <RouterLink :to="{ name: 'release-request', params: { tenant: project.tenant, project: project.project, request: held.release_request_id } }" data-testid="held-link">
          {{ ro.openRequest }}
        </RouterLink>
      </p>
    </div>
    <template v-else>
    <form id="promote-form" class="row fields" @submit.prevent="promote">
      <div class="field">
        <label for="prm-release">{{ s.release }}</label>
        <select id="prm-release" v-model="releaseId" :disabled="busy">
          <option value="" disabled>{{ s.choose }}</option>
          <option v-if="release && !book.releases.value.some((r) => r.id === release!.id)" :value="release.id">{{ releaseOption(release) }}</option>
          <option v-for="r in book.releases.value" :key="r.id" :value="r.id">{{ releaseOption(r) }}</option>
        </select>
      </div>
      <div class="field">
        <label for="prm-env">{{ s.target }}</label>
        <select id="prm-env" v-model="envName" :disabled="busy">
          <option value="" disabled>{{ s.choose }}</option>
          <option v-for="e in book.environments.value" :key="e.name" :value="e.name">{{ e.name }}</option>
        </select>
      </div>
    </form>

    <section v-if="release && target" class="stack-sm" aria-live="polite" data-testid="promote-summary">
      <p class="move">{{ s.pointerMove(target.name, book.label(currentId), s.version(release.version)) }}</p>
      <p v-if="verdict.kind === 'same'" class="alert">{{ s.alreadyServing(target.name, s.version(release.version)) }}</p>
      <p v-else-if="verdict.kind === 'ineligible'" class="alert alert-warn" data-testid="promote-ineligible">{{ verdict.text }}</p>
      <template v-else>
        <template v-if="current && diff">
          <h3>{{ s.diffTo(s.version(current.version)) }}</h3>
          <LocaleTable
            :locales="release.locales.map((l) => l.code)"
            :source-locale="release.source_locale"
            :counts="release.counts.locales"
            :diff="diff"
            :caption="s.diffTo(s.version(current.version))"
          />
        </template>
        <template v-else-if="!currentId">
          <p class="muted">{{ s.everythingNew }}</p>
          <LocaleTable
            :locales="release.locales.map((l) => l.code)"
            :source-locale="release.source_locale"
            :counts="release.counts.locales"
            :caption="s.pointerMove(target.name, book.label(currentId), s.version(release.version))"
          />
        </template>
        <p v-else class="muted" role="status">{{ strings.app.loading }}</p>
        <p class="muted">{{ s.pointerOnly }}</p>
      </template>
    </section>
    </template>
    <ErrorAlert :error="error" />
    <template #actions>
      <button v-if="held" type="button" class="btn btn-primary" @click="emit('done', ro.heldStatus(held.release_request.environment))">{{ s.done }}</button>
      <template v-else>
        <button type="button" class="btn" :disabled="busy" @click="emit('close')">{{ strings.app.cancel }}</button>
        <button type="submit" form="promote-form" class="btn btn-primary" :disabled="busy || verdict.kind !== 'ok'">
          {{ release && target ? s.promoteConfirm(s.version(release.version), target.name) : s.promote }}
        </button>
      </template>
    </template>
  </ModalDialog>
</template>

<style scoped>
.fields {
  align-items: flex-end;
}
.fields .field {
  flex: 1 1 14rem;
}
.move {
  font-weight: var(--kl-weight-semibold);
  font-family: var(--kl-font-mono);
}
</style>
