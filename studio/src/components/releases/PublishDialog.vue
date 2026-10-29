<script setup lang="ts">
/**
 * Publish, with a dry run first: for the chosen environment the server
 * builds the release without storing it (POST …/release-previews), and
 * the dialog shows what would ship per locale and what would change
 * against what the environment serves — or why the catalog can't be
 * released — before anything is published.
 */
import { computed, ref, watch } from "vue";
import { RouterLink } from "vue-router";
import { isHeld, newIdempotencyKey, type ProjectRef, type ReleasesPort } from "../../api/releases";
import type { ReleaseHeld } from "../../api/release-ops-schemas";
import { partyText } from "../../lib/release-ops";
import type { Release, ReleaseDiff, ReleasePreview } from "../../api/schemas";
import { isEmptyDiff } from "../../lib/releases";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import { bytesText, policyText, type ReleaseBook } from "./book";
import LocaleTable from "./LocaleTable.vue";

const props = defineProps<{ open: boolean; port: ReleasesPort; project: ProjectRef; book: ReleaseBook; environment?: string | undefined }>();
const emit = defineEmits<{ close: []; published: [result: Release | ReleaseHeld] }>();
const s = strings.releases;
const ro = strings.releaseOps;
/** "reviewers" in "2 approvals from reviewers". */
const heldParty = computed(() => (held.value ? partyText(held.value.release_request.approval.from) : ""));

const env = ref("development");
const note = ref("");
const busy = ref(false);
const error = ref<unknown>(null);
const result = ref<Release>();
/** Into an environment that requires approval: recorded, held, nothing deployed (RFC 0006 §5.1). */
const held = ref<ReleaseHeld>();
const resultDiff = ref<ReleaseDiff>();
let key = newIdempotencyKey();

const dryRun = ref<ReleasePreview>();
const dryRunError = ref<unknown>(null);
const checking = ref(false);
let dryRunSeq = 0;

async function runDryRun(): Promise<void> {
  const seq = ++dryRunSeq;
  checking.value = true;
  dryRun.value = undefined;
  dryRunError.value = null;
  try {
    const pv = await props.port.previewPublish(props.project, env.value);
    if (seq !== dryRunSeq) return;
    dryRun.value = pv;
    await props.book.ensure([pv.base_release_id]);
  } catch (e) {
    if (seq === dryRunSeq) dryRunError.value = e;
  } finally {
    if (seq === dryRunSeq) checking.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    env.value = props.environment ?? props.book.environments.value[0]?.name ?? "development";
    note.value = "";
    error.value = null;
    result.value = undefined;
    held.value = undefined;
    resultDiff.value = undefined;
    key = newIdempotencyKey();
    void runDryRun();
  },
  { immediate: true },
);
watch(env, () => {
  if (props.open && !result.value && !held.value) void runDryRun();
});
// A different request needs a different key; a retry of the same one reuses it.
watch([env, note], () => {
  key = newIdempotencyKey();
});

const target = computed(() => props.book.env(env.value));
const localeCodes = (r: { locales?: Array<{ code: string }> | undefined }) => (r.locales ?? []).map((l) => l.code);
const dryRunDiff = computed<ReleaseDiff | undefined>(() =>
  dryRun.value?.changes ? { release_id: "preview", locales: dryRun.value.changes, ...(dryRun.value.base_release_id ? { base_release_id: dryRun.value.base_release_id } : {}) } : undefined,
);
const unchanged = computed(() => !!dryRun.value?.base_release_id && !!dryRunDiff.value && isEmptyDiff(dryRunDiff.value));
/** Publishing is refused only when the dry run says the catalog can't be released. */
const blocked = computed(() => dryRun.value?.releasable === false);

async function publish(): Promise<void> {
  if (blocked.value) return;
  busy.value = true;
  error.value = null;
  try {
    const r = await props.port.publish(props.project, { environment: env.value, note: note.value.trim() || undefined }, key);
    emit("published", r);
    if (isHeld(r)) {
      held.value = r;
      return;
    }
    result.value = r;
    if (r.parent_id) {
      [resultDiff.value] = await Promise.all([props.port.diff(props.project, r.id), props.book.ensure([r.parent_id])]);
    }
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog :open="open" :title="s.publishTitle" wide @close="emit('close')">
    <div v-if="held" class="alert alert-warn stack-sm" role="status" data-testid="publish-held">
      <p class="alert-title">{{ ro.heldTitle }}</p>
      <p>{{ ro.held(held.release_request.environment, held.release_request.approval.n, heldParty) }}</p>
      <p>
        <RouterLink :to="{ name: 'release-request', params: { tenant: project.tenant, project: project.project, request: held.release_request_id } }" data-testid="held-link">
          {{ ro.openRequest }}
        </RouterLink>
      </p>
    </div>
    <template v-else-if="!result">
      <form id="publish-form" class="stack" @submit.prevent="publish">
        <div class="field">
          <label for="pub-env">{{ s.environment }}</label>
          <select id="pub-env" v-model="env" :disabled="busy">
            <option v-for="e in book.environments.value" :key="e.name" :value="e.name">{{ e.name }}</option>
          </select>
        </div>
        <div class="field">
          <label for="pub-note">{{ s.note }}</label>
          <textarea id="pub-note" v-model="note" rows="2" maxlength="1000" aria-describedby="pub-note-hint" :disabled="busy" />
          <span id="pub-note-hint" class="hint">{{ s.noteHint }}</span>
        </div>
      </form>
      <section class="stack-sm" aria-labelledby="pub-ships-h" aria-live="polite" data-testid="publish-preview">
        <h3 id="pub-ships-h">{{ s.willShip }}</h3>
        <p v-if="dryRun || target">{{ s.policyLead(env, policyText((dryRun ?? target)!.policy)) }}</p>
        <p v-if="checking" class="muted" role="status">{{ s.dryRunChecking }}</p>
        <template v-else-if="dryRun && !dryRun.releasable">
          <div class="alert alert-error" role="alert" data-testid="dry-run-problems">
            <span class="alert-title">{{ s.notReleasable(dryRun.problems.length) }}</span>
            <ul class="problems">
              <li v-for="(p, i) in dryRun.problems" :key="i">
                <code v-if="p.key">{{ p.key }}</code> <code v-if="p.locale">{{ p.locale }}</code> {{ p.detail }}
              </li>
            </ul>
          </div>
        </template>
        <template v-else-if="dryRun && dryRun.counts">
          <p data-testid="dry-run-summary">
            {{ s.dryRunSummary(dryRun.counts.messages, localeCodes(dryRun).length, dryRun.counts.new_artifacts, bytesText(dryRun.counts.bytes)) }}
          </p>
          <p v-if="dryRun.base_release_id">{{ s.dryRunAgainst(env, book.label(dryRun.base_release_id)) }}</p>
          <p v-else>{{ s.firstRelease(env) }}</p>
          <p v-if="unchanged" class="muted" data-testid="dry-run-unchanged">{{ s.dryRunUnchanged(env) }}</p>
          <LocaleTable
            :locales="localeCodes(dryRun)"
            :source-locale="dryRun.source_locale"
            :counts="dryRun.counts.locales"
            :diff="dryRunDiff"
            :caption="s.willShip"
          />
          <p class="muted">{{ s.dryRunNote }}</p>
        </template>
        <template v-else-if="dryRunError">
          <p class="alert alert-warn">{{ s.dryRunFailed }}</p>
          <ErrorAlert :error="dryRunError" />
        </template>
      </section>
    </template>
    <template v-else>
      <p class="alert alert-ok" role="status">{{ s.published(s.version(result.version), result.environment) }}</p>
      <template v-if="resultDiff">
        <h3>{{ s.changesSince(book.label(result.parent_id)) }}</h3>
        <p v-if="isEmptyDiff(resultDiff)" class="muted">{{ s.noChanges }}</p>
        <LocaleTable
          :locales="localeCodes(result)"
          :source-locale="result.source_locale"
          :counts="result.counts.locales"
          :diff="resultDiff"
          :caption="s.changesSince(book.label(result.parent_id))"
        />
      </template>
      <template v-else>
        <p class="muted">{{ s.everythingNew }}</p>
        <LocaleTable
          :locales="localeCodes(result)"
          :source-locale="result.source_locale"
          :counts="result.counts.locales"
          :caption="s.published(s.version(result.version), result.environment)"
        />
      </template>
    </template>
    <ErrorAlert :error="error" />
    <template #actions>
      <template v-if="!result && !held">
        <button type="button" class="btn" :disabled="busy" @click="emit('close')">{{ strings.app.cancel }}</button>
        <button type="submit" form="publish-form" class="btn btn-primary" :disabled="busy || checking || blocked">{{ busy ? s.publishing : s.publishTo(env) }}</button>
      </template>
      <button v-else type="button" class="btn btn-primary" @click="emit('close')">{{ s.done }}</button>
    </template>
  </ModalDialog>
</template>

<style scoped>
.problems {
  margin: var(--kl-space-1) 0 0;
  padding-inline-start: var(--kl-space-4);
}
</style>
