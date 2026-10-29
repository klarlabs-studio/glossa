<script setup lang="ts">
/**
 * Project settings → Preview origins (RFC 0004 §5.2): where the
 * in-product editor may run.
 *
 * This is not a convenience list. Registering an origin says that a page
 * served from there may ask this project's people to lend it their
 * permissions, and that the API will answer it across origins. So the
 * card says that in words, marks a developer's own machine as what it
 * is, and asks for confirmation before removing one — removing ends the
 * editor sessions on that origin at once.
 */
import { computed, ref, watch } from "vue";
import { useInContext } from "../../api/in-context";
import type { PreviewOrigin } from "../../api/in-context-schemas";
import { newIdempotencyKey } from "../../api/releases";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "../../views/project/context";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

const port = useInContext();
const { tenant, projectId, grant } = useProject();
const s = strings.previewOrigins;
const c = s.columns;
const canManage = computed(() => allows(grant.value, "tokens.manage"));
const person = usePeople(() => tenant.value);

const origins = ref<PreviewOrigin[]>([]);
const error = ref<unknown>(null);
const status = ref("");
const busy = ref(false);

const origin = ref("");
const label = ref("");
let key = newIdempotencyKey();
// A retry of the same confirmation is safe; a changed form is a new one.
watch([origin, label], () => {
  key = newIdempotencyKey();
});

async function load(): Promise<void> {
  try {
    origins.value = await port.origins(tenant.value, projectId.value);
  } catch (e) {
    error.value = e;
  }
}
watch(projectId, load, { immediate: true });

async function register(): Promise<void> {
  error.value = null;
  status.value = "";
  busy.value = true;
  try {
    const body = label.value.trim() ? { origin: origin.value.trim(), label: label.value.trim() } : { origin: origin.value.trim() };
    const added = await port.register(tenant.value, projectId.value, body, key);
    origin.value = "";
    label.value = "";
    key = newIdempotencyKey();
    status.value = s.registered(added.origin);
    await load();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

const removing = ref<PreviewOrigin | null>(null);

async function remove(): Promise<void> {
  const o = removing.value;
  if (!o) return;
  error.value = null;
  status.value = "";
  busy.value = true;
  try {
    await port.unregister(tenant.value, projectId.value, o.id);
    status.value = s.removed(o.origin);
    removing.value = null;
    await load();
  } catch (e) {
    error.value = e;
    removing.value = null;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="card stack" aria-labelledby="po-h">
    <h2 id="po-h">{{ s.title }}</h2>
    <p class="muted">{{ s.lead }}</p>
    <ErrorAlert :error="error" />
    <p v-if="status" class="alert alert-ok" role="status">{{ status }}</p>

    <div v-if="origins.length" class="scroll">
      <table class="table" data-testid="preview-origins">
        <thead>
          <tr>
            <th scope="col">{{ c.origin }}</th>
            <th scope="col">{{ c.label }}</th>
            <th scope="col">{{ c.added }}</th>
            <th v-if="canManage" scope="col"><span class="visually-hidden">{{ c.actions }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in origins" :key="o.id">
            <th scope="row">
              <code>{{ o.origin }}</code>
              <span v-if="o.development" class="pill pill-neutral">{{ s.development }}</span>
            </th>
            <td>{{ o.label }}</td>
            <td>
              <time :datetime="o.created_at" :title="absoluteTime(o.created_at)">{{ relativeTime(o.created_at) }}</time>
              <span class="muted"> · {{ person(o.created_by) }}</span>
            </td>
            <td v-if="canManage" class="actions">
              <button type="button" class="btn btn-sm btn-danger" :disabled="busy" @click="removing = o">
                {{ s.remove }}<span class="visually-hidden">{{ " " + o.origin }}</span>
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="muted">{{ s.empty }}</p>

    <form v-if="canManage" class="stack create" @submit.prevent="register">
      <div class="field">
        <label for="po-origin">{{ s.origin }}</label>
        <input
          id="po-origin"
          v-model="origin"
          type="url"
          required
          maxlength="255"
          placeholder="https://preview.example.com"
          aria-describedby="po-origin-hint"
          autocomplete="off"
          spellcheck="false"
        />
        <span id="po-origin-hint" class="hint">{{ s.originHint }}</span>
      </div>
      <div class="field">
        <label for="po-label">{{ s.label }}</label>
        <input id="po-label" v-model="label" maxlength="100" aria-describedby="po-label-hint" autocomplete="off" />
        <span id="po-label-hint" class="hint">{{ s.labelHint }}</span>
      </div>
      <div>
        <button type="submit" class="btn btn-primary" :disabled="busy || !origin.trim()">{{ s.add }}</button>
      </div>
    </form>
    <p v-else class="hint">{{ s.needsPermission }}</p>

    <ModalDialog
      :open="removing !== null"
      :title="s.removeTitle"
      @close="removing = null"
    >
      <p>{{ removing ? s.removeConfirm(removing.origin) : "" }}</p>
      <template #actions>
        <button type="button" class="btn" @click="removing = null">{{ s.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="busy" @click="remove">{{ s.remove }}</button>
      </template>
    </ModalDialog>
  </section>
</template>

<style scoped>
.pill {
  margin-inline-start: var(--kl-space-2);
}
.actions {
  white-space: nowrap;
}
</style>
