<script setup lang="ts">
/**
 * Project settings → Delivery keys: create (the key shown in full once,
 * with snippets for every runtime), list, revoke with confirmation.
 */
import { computed, ref, watch } from "vue";
import { newIdempotencyKey, useReleases } from "../../api/releases";
import type { DeliveryKey, DeliveryKeyScope, Environment, SigningKey } from "../../api/schemas";
import { DEFAULT_ENVIRONMENTS } from "../../lib/releases";
import { configuredEdge, EDGE_PLACEHOLDER, edgeOrigin, maskKey, snippets } from "../../lib/snippets";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { loadMeta } from "../../session/meta";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "../../views/project/context";
import CopyButton from "../CopyButton.vue";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import SnippetTabs from "./SnippetTabs.vue";

const port = useReleases();
const { tenant, projectId, grant } = useProject();
const s = strings.deliveryKeys;
const c = s.columns;
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canManage = computed(() => allows(grant.value, "releases.publish"));
const person = usePeople(() => tenant.value);

const keys = ref<DeliveryKey[]>([]);
const signing = ref<SigningKey[]>([]);
const environments = ref<Environment[]>([]);
const error = ref<unknown>(null);
const status = ref("");

async function load(): Promise<void> {
  try {
    keys.value = await port.deliveryKeys(project());
  } catch (e) {
    error.value = e;
  }
  // Only to pin keys in snippets; without them the snippets still work.
  signing.value = await port.signingKeys(project()).catch(() => []);
  environments.value = await port.environments(project()).catch(() => []);
}
watch(projectId, load, { immediate: true });

// ── scope ───────────────────────────────────────────────────────────
// A key reads the environments it names and, as a preview key, every
// branch preview (RFC 0004 §4.3). Branch environments are never named:
// they come and go with their branches.
const standardEnvironments = computed(() => {
  const named = environments.value.filter((e) => e.kind !== "branch").map((e) => e.name);
  const all = [...DEFAULT_ENVIRONMENTS, ...named.filter((n) => !(DEFAULT_ENVIRONMENTS as readonly string[]).includes(n))];
  return all;
});

/** "production" or "preview + branch previews"; what a key reads, in words. */
function readsText(scope: DeliveryKeyScope): string {
  const named = scope.environments.join(", ");
  if (scope.branches) return named ? `${named} + ${s.branchPreviews}` : s.branchPreviews;
  return named;
}

/** A scope editor's state: the checkboxes of the form and the dialog. */
function scopeState(initial: DeliveryKeyScope) {
  return { environments: ref<string[]>([...initial.environments]), branches: ref(initial.branches) };
}

const newScope = scopeState({ environments: ["production"], branches: false });
const scopeOf = (st: ReturnType<typeof scopeState>): DeliveryKeyScope => ({
  environments: standardEnvironments.value.filter((e) => st.environments.value.includes(e)),
  branches: st.branches.value,
});
const scopeIsEmpty = (st: ReturnType<typeof scopeState>) => st.environments.value.length === 0 && !st.branches.value;

// ── create ──────────────────────────────────────────────────────────
const name = ref("");
const creating = ref(false);
let idemKey = newIdempotencyKey();
watch(name, () => {
  idemKey = newIdempotencyKey();
});
const created = ref<DeliveryKey | null>(null);
const snippetEnv = ref<string>("production");

async function create(): Promise<void> {
  creating.value = true;
  error.value = null;
  status.value = "";
  try {
    created.value = await port.createDeliveryKey(project(), name.value.trim(), scopeOf(newScope), idemKey);
    snippetEnv.value = created.value.scope.environments[0] ?? "production";
    name.value = "";
    await load();
  } catch (e) {
    error.value = e;
  } finally {
    creating.value = false;
  }
}

// ── change a key's scope ────────────────────────────────────────────
const scoping = ref<DeliveryKey | null>(null);
const scopeEdit = scopeState({ environments: [], branches: false });
const scopeBusy = ref(false);
const scopeError = ref<unknown>(null);

function askScope(k: DeliveryKey): void {
  scopeError.value = null;
  status.value = "";
  scopeEdit.environments.value = [...k.scope.environments];
  scopeEdit.branches.value = k.scope.branches;
  scoping.value = k;
}

async function saveScope(): Promise<void> {
  const k = scoping.value;
  if (!k || scopeIsEmpty(scopeEdit)) return;
  scopeBusy.value = true;
  scopeError.value = null;
  try {
    const updated = await port.setDeliveryKeyScope(project(), k.id, scopeOf(scopeEdit));
    scoping.value = null;
    status.value = s.scopeChanged(updated.name, readsText(updated.scope));
    await load();
  } catch (e) {
    scopeError.value = e;
  } finally {
    scopeBusy.value = false;
  }
}

const edge = ref(configuredEdge());
void edgeOrigin({ announced: async () => (await loadMeta()).edge_url }).then((e) => {
  edge.value = e;
});
const codeSnippets = computed(() =>
  created.value
    ? snippets({
        edge: edge.value ?? EDGE_PLACEHOLDER,
        deliveryKey: created.value.key,
        environment: snippetEnv.value,
        publicKeys: signing.value.filter((k) => k.active).map((k) => ({ keyId: k.key_id, key: k.public_key })),
      })
    : [],
);

// ── revoke ──────────────────────────────────────────────────────────
const revoking = ref<DeliveryKey | null>(null);
const revokeBusy = ref(false);
const revokeError = ref<unknown>(null);

function askRevoke(k: DeliveryKey): void {
  revokeError.value = null;
  status.value = "";
  revoking.value = k;
}

async function revoke(): Promise<void> {
  const k = revoking.value;
  if (!k) return;
  revokeBusy.value = true;
  revokeError.value = null;
  try {
    await port.revokeDeliveryKey(project(), k.id);
    revoking.value = null;
    status.value = s.revokedDone(k.name);
    await load();
  } catch (e) {
    revokeError.value = e;
  } finally {
    revokeBusy.value = false;
  }
}
</script>

<template>
  <section class="card stack" aria-labelledby="dk-h">
    <h2 id="dk-h">{{ s.title }}</h2>
    <p class="muted">{{ s.lead }}</p>
    <ErrorAlert :error="error" />
    <p v-if="status" class="alert alert-ok" role="status">{{ status }}</p>
    <div v-if="keys.length" class="scroll">
      <table class="table" data-testid="delivery-keys">
        <thead>
          <tr>
            <th scope="col">{{ c.name }}</th>
            <th scope="col">{{ c.key }}</th>
            <th scope="col">{{ c.reads }}</th>
            <th scope="col">{{ c.created }}</th>
            <th scope="col">{{ c.status }}</th>
            <th v-if="canManage" scope="col"><span class="visually-hidden">{{ c.actions }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="k in keys" :key="k.id">
            <th scope="row">{{ k.name }}</th>
            <td><code>{{ maskKey(k.key) }}</code></td>
            <td data-testid="key-scope">{{ readsText(k.scope) }}</td>
            <td>
              <time :datetime="k.created_at" :title="absoluteTime(k.created_at)">{{ relativeTime(k.created_at) }}</time>
              <span class="muted"> · {{ person(k.created_by) }}</span>
            </td>
            <td>
              <span v-if="k.revoked_at" class="pill pill-neutral" :title="absoluteTime(k.revoked_at)">{{ s.revoked }}</span>
              <span v-else class="pill pill-ok">{{ s.active }}</span>
            </td>
            <td v-if="canManage" class="actions">
              <template v-if="!k.revoked_at">
                <button type="button" class="btn btn-sm" @click="askScope(k)">
                  {{ s.changeScope }}<span class="visually-hidden">{{ " " + k.name }}</span>
                </button>
                <button type="button" class="btn btn-sm btn-danger" @click="askRevoke(k)">
                  {{ s.revoke }}<span class="visually-hidden">{{ " " + k.name }}</span>
                </button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="muted">{{ s.empty }}</p>

    <form v-if="canManage" class="stack create" @submit.prevent="create">
      <div class="field">
        <label for="dk-name">{{ s.name }}</label>
        <input id="dk-name" v-model="name" maxlength="200" required aria-describedby="dk-name-hint" autocomplete="off" />
        <span id="dk-name-hint" class="hint">{{ s.nameHint }}</span>
      </div>
      <fieldset class="stack-sm scope" data-testid="new-key-scope">
        <legend>{{ s.scope }}</legend>
        <label v-for="e in standardEnvironments" :key="e" class="check">
          <input v-model="newScope.environments.value" type="checkbox" :value="e" />
          <span>{{ e }}</span>
        </label>
        <label class="check">
          <input v-model="newScope.branches.value" type="checkbox" data-testid="new-key-preview" />
          <span>{{ s.previewLabel }}</span>
        </label>
        <p class="hint">{{ s.scopeHint }}</p>
        <p v-if="scopeIsEmpty(newScope)" class="hint">{{ s.scopeEmpty }}</p>
      </fieldset>
      <button type="submit" class="btn" :disabled="creating || !name.trim() || scopeIsEmpty(newScope)">
        {{ creating ? s.creating : s.create }}
      </button>
    </form>

    <ModalDialog :open="created !== null" :title="s.createdTitle(created?.name ?? '')" wide @close="created = null">
      <template v-if="created">
        <p>{{ s.createdLead }}</p>
        <div class="field">
          <label for="dk-created">{{ s.key }}</label>
          <div class="row">
            <input id="dk-created" :value="created.key" readonly class="mono key" data-testid="created-key" @focus="($event.target as HTMLInputElement).select()" />
            <CopyButton :text="created.key" :label="s.key" />
          </div>
        </div>
        <h3>{{ s.snippets }}</h3>
        <div class="field env">
          <label for="dk-env">{{ s.snippetEnv }}</label>
          <select id="dk-env" v-model="snippetEnv">
            <option v-for="e in DEFAULT_ENVIRONMENTS" :key="e" :value="e">{{ e }}</option>
          </select>
        </div>
        <SnippetTabs :snippets="codeSnippets" :label="s.snippets" />
        <p v-if="!edge" class="hint">{{ s.edgePlaceholder(EDGE_PLACEHOLDER) }}</p>
        <p v-if="signing.some((k) => k.active)" class="hint">{{ s.signed }}</p>
      </template>
      <template #actions>
        <button type="button" class="btn btn-primary" @click="created = null">{{ strings.releases.done }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="scoping !== null" :title="s.changeScopeTitle(scoping?.name ?? '')" @close="scoping = null">
      <p>{{ s.changeScopeLead }}</p>
      <fieldset class="stack-sm scope" data-testid="scope-edit">
        <legend>{{ s.scope }}</legend>
        <label v-for="e in standardEnvironments" :key="e" class="check">
          <input v-model="scopeEdit.environments.value" type="checkbox" :value="e" />
          <span>{{ e }}</span>
        </label>
        <label class="check">
          <input v-model="scopeEdit.branches.value" type="checkbox" data-testid="scope-edit-preview" />
          <span>{{ s.previewLabel }}</span>
        </label>
        <p class="hint">{{ s.scopeHint }}</p>
        <p v-if="scopeIsEmpty(scopeEdit)" class="hint">{{ s.scopeEmpty }}</p>
      </fieldset>
      <ErrorAlert :error="scopeError" />
      <template #actions>
        <button type="button" class="btn" :disabled="scopeBusy" @click="scoping = null">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-primary" :disabled="scopeBusy || scopeIsEmpty(scopeEdit)" @click="saveScope">
          {{ s.changeScopeConfirm }}
        </button>
      </template>
    </ModalDialog>

    <ModalDialog :open="revoking !== null" :title="s.revokeTitle(revoking?.name ?? '')" @close="revoking = null">
      <p v-if="revoking">{{ s.revokeBody(maskKey(revoking.key)) }}</p>
      <ErrorAlert :error="revokeError" />
      <template #actions>
        <button type="button" class="btn" :disabled="revokeBusy" @click="revoking = null">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="revokeBusy" @click="revoke">{{ s.revokeConfirm }}</button>
      </template>
    </ModalDialog>
  </section>
</template>

<style scoped>
.scroll {
  overflow-x: auto;
}
.actions {
  text-align: end;
}
.create {
  align-items: flex-start;
}
.create .field {
  max-inline-size: 20rem;
}
.scope {
  border: 1px solid var(--gs-control-border);
  border-radius: var(--kl-radius-md);
  padding: var(--kl-space-3) var(--kl-space-4);
}
/* Each checkbox is its own target: 24x24 at least (WCAG 2.2). */
.scope .check {
  align-items: center;
  min-block-size: 2rem;
}
.scope input[type="checkbox"] {
  /* flex-shrink would squeeze it below the 24px target. */
  flex: 0 0 auto;
  inline-size: 1.75rem;
  block-size: 1.75rem;
}

.key {
  flex: 1 1 22rem;
}
.env {
  max-inline-size: 16rem;
}
</style>
