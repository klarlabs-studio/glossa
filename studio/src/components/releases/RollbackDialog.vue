<script setup lang="ts">
/**
 * Rollback: point an environment back at a release it served before.
 * Preselects what the server would pick (the newest older release it
 * served) and always sends the choice explicitly, so a retry can't walk
 * two steps back.
 */
import { computed, ref, watch } from "vue";
import type { ProjectRef, ReleasesPort } from "../../api/releases";
import type { Release, ReleaseDiff } from "../../api/schemas";
import { defaultRollbackTarget, rollbackCandidates } from "../../lib/releases";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import { type ReleaseBook } from "./book";
import LocaleTable from "./LocaleTable.vue";

const props = defineProps<{ open: boolean; port: ReleasesPort; project: ProjectRef; book: ReleaseBook; environment: string }>();
const emit = defineEmits<{ close: []; done: [message: string] }>();
const s = strings.releases;

const candidates = ref<Release[]>([]);
const targetId = ref("");
const diff = ref<ReleaseDiff>();
const loading = ref(false);
const busy = ref(false);
const error = ref<unknown>(null);

const env = computed(() => props.book.env(props.environment));
const current = computed(() => props.book.byId.value.get(env.value?.current_release_id ?? ""));
const target = computed(() => candidates.value.find((r) => r.id === targetId.value));

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    error.value = null;
    candidates.value = [];
    targetId.value = "";
    loading.value = true;
    try {
      const history = await props.port.deployments(props.project, props.environment, 100);
      await props.book.ensure([...history.map((d) => d.release_id), env.value?.current_release_id]);
      candidates.value = rollbackCandidates(history, props.book.byId.value, env.value?.current_release_id);
      targetId.value = (defaultRollbackTarget(candidates.value, current.value) ?? candidates.value[0])?.id ?? "";
    } catch (e) {
      error.value = e;
    } finally {
      loading.value = false;
    }
  },
  { immediate: true },
);

watch([target, current], async ([t, cur]) => {
  diff.value = undefined;
  if (!t || !cur) return;
  try {
    const d = await props.port.diff(props.project, t.id, cur.id);
    if (targetId.value === t.id) diff.value = d;
  } catch (e) {
    error.value = e;
  }
});

async function rollback(): Promise<void> {
  const t = target.value;
  if (!t) return;
  busy.value = true;
  error.value = null;
  try {
    await props.port.rollback(props.project, props.environment, t.id);
    emit("done", s.rolledBack(props.environment, s.version(t.version)));
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog :open="open" :title="s.rollbackTitle(environment)" wide @close="emit('close')">
    <p v-if="loading" class="muted" role="status">{{ strings.app.loading }}</p>
    <p v-else-if="!candidates.length && !error" class="alert">{{ s.noRollback(environment) }}</p>
    <template v-else-if="candidates.length">
      <form id="rollback-form" class="field" @submit.prevent="rollback">
        <label for="rb-target">{{ s.rollbackTo }}</label>
        <select id="rb-target" v-model="targetId" :disabled="busy">
          <option v-for="r in candidates" :key="r.id" :value="r.id">
            {{ s.version(r.version) }} · {{ r.environment }}{{ r.note ? ` · ${r.note}` : "" }}
          </option>
        </select>
      </form>
      <section v-if="target" class="stack-sm" aria-live="polite" data-testid="rollback-summary">
        <p class="move">{{ s.pointerMove(environment, book.label(current?.id), s.version(target.version)) }}</p>
        <template v-if="diff && current">
          <h3>{{ s.diffTo(s.version(current.version)) }}</h3>
          <LocaleTable
            :locales="target.locales.map((l) => l.code)"
            :source-locale="target.source_locale"
            :counts="target.counts.locales"
            :diff="diff"
            :caption="s.diffTo(s.version(current.version))"
          />
        </template>
        <p class="muted">{{ s.pointerOnly }}</p>
      </section>
    </template>
    <ErrorAlert :error="error" />
    <template #actions>
      <button type="button" class="btn" :disabled="busy" @click="emit('close')">{{ strings.app.cancel }}</button>
      <button v-if="target" type="submit" form="rollback-form" class="btn btn-danger" :disabled="busy">
        {{ s.rollbackConfirm(environment, s.version(target.version)) }}
      </button>
    </template>
  </ModalDialog>
</template>

<style scoped>
.move {
  font-weight: var(--kl-weight-semibold);
  font-family: var(--kl-font-mono);
}
</style>
