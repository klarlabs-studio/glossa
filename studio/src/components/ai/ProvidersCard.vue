<script setup lang="ts">
/**
 * Bring-your-own AI providers. The API key is write-only: it's sealed at
 * rest and never returned, so the list says only whether one is stored,
 * and editing a provider never shows its key.
 */
import { reactive, ref, shallowRef } from "vue";
import type { AIProvider, AIProviderKind } from "../../api/intelligence-schemas";
import { useIntelligence, type ProviderUpdate } from "../../api/intelligence";
import { newIdempotencyKey } from "../../api/releases";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

const props = defineProps<{ tenant: string; providers: AIProvider[]; canManage: boolean }>();
const emit = defineEmits<{ changed: [] }>();
const s = strings.aiSettings;
const port = useIntelligence();

const KINDS: AIProviderKind[] = ["anthropic", "openai_compatible", "gemini"];
const editing = shallowRef<AIProvider>();
const open = ref(false);
const form = reactive({ name: "", kind: "anthropic" as AIProviderKind, base_url: "", models: "", enabled: true, api_key: "", clear_api_key: false });
const error = ref<unknown>(null);
const busy = ref(false);
const status = ref("");
let key = newIdempotencyKey();

function openFor(p: AIProvider | undefined): void {
  editing.value = p;
  Object.assign(form, {
    name: p?.name ?? "",
    kind: p?.kind ?? "anthropic",
    base_url: p?.base_url ?? "",
    models: p?.models.join(", ") ?? "",
    enabled: p?.enabled ?? true,
    api_key: "",
    clear_api_key: false,
  });
  error.value = null;
  key = newIdempotencyKey();
  open.value = true;
}

const models = () =>
  form.models
    .split(",")
    .map((m) => m.trim())
    .filter(Boolean);

async function save(): Promise<void> {
  busy.value = true;
  error.value = null;
  try {
    const p = editing.value;
    if (p) {
      const body: ProviderUpdate = { name: form.name.trim(), models: models(), enabled: form.enabled };
      if (form.base_url.trim()) body.base_url = form.base_url.trim();
      if (form.api_key) body.api_key = form.api_key;
      else if (form.clear_api_key) body.clear_api_key = true;
      const current = await port.provider(props.tenant, p.id);
      await port.updateProvider(props.tenant, p.id, body, current.etag ?? "");
    } else {
      await port.createProvider(
        props.tenant,
        {
          name: form.name.trim(),
          kind: form.kind,
          models: models(),
          enabled: form.enabled,
          ...(form.base_url.trim() ? { base_url: form.base_url.trim() } : {}),
          ...(form.api_key ? { api_key: form.api_key } : {}),
        },
        key,
      );
    }
    // The key leaves the page's memory as soon as it's sent.
    form.api_key = "";
    open.value = false;
    status.value = s.providerSaved;
    emit("changed");
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

const removing = shallowRef<AIProvider>();
async function remove(): Promise<void> {
  const p = removing.value;
  if (!p) return;
  try {
    await port.deleteProvider(props.tenant, p.id);
    emit("changed");
  } catch (e) {
    error.value = e;
  } finally {
    removing.value = undefined;
  }
}
</script>

<template>
  <section class="card stack-sm" aria-labelledby="providers-h" data-testid="providers">
    <div class="row">
      <h2 id="providers-h">{{ s.providers }}</h2>
      <span class="spacer" />
      <button v-if="canManage" type="button" class="btn btn-sm" @click="openFor(undefined)">{{ s.addProvider }}</button>
    </div>
    <p class="hint">{{ s.providersLead }}</p>
    <ErrorAlert v-if="!open" :error="error" />
    <p class="muted" role="status">{{ status }}</p>
    <p v-if="!providers.length" class="muted">{{ s.noProviders }}</p>
    <div v-else class="table-wrap">
      <table class="table">
        <thead>
          <tr>
            <th scope="col">{{ s.name }}</th>
            <th scope="col">{{ s.kind }}</th>
            <th scope="col">{{ s.models }}</th>
            <th scope="col">{{ s.apiKey }}</th>
            <th scope="col"><span class="visually-hidden">{{ strings.app.edit }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in providers" :key="p.id" :data-provider="p.name">
            <th scope="row" class="mono">
              {{ p.name }}
              <span v-if="!p.enabled" class="pill pill-neutral">{{ s.disabled }}</span>
            </th>
            <td>
              {{ s.kinds[p.kind] }}
              <span v-if="p.base_url" class="hint mono url">{{ p.base_url }}</span>
            </td>
            <td class="mono">{{ p.models.length ? p.models.join(", ") : s.anyModel }}</td>
            <td>
              <span class="pill" :class="p.api_key_set ? 'pill-ok' : 'pill-warn'">{{ p.api_key_set ? s.keySet : s.keyMissing }}</span>
            </td>
            <td class="actions">
              <template v-if="canManage">
                <button type="button" class="btn btn-sm" :aria-label="s.editProvider(p.name)" @click="openFor(p)">{{ strings.app.edit }}</button>
                <button type="button" class="btn btn-sm btn-danger" :aria-label="s.removeProvider(p.name)" @click="removing = p">{{ strings.app.remove }}</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ModalDialog :open="open" :title="editing ? s.editProvider(editing.name) : s.addProvider" @close="open = false">
      <form id="provider-form" class="stack-sm" autocomplete="off" @submit.prevent="save">
        <div class="field">
          <label for="pv-name">{{ s.name }}</label>
          <input id="pv-name" v-model="form.name" class="mono" required pattern="[a-z][a-z0-9-]{0,62}" aria-describedby="pv-name-hint" />
          <span id="pv-name-hint" class="hint">{{ s.nameHint }}</span>
        </div>
        <div class="field">
          <label for="pv-kind">{{ s.kind }}</label>
          <select id="pv-kind" v-model="form.kind" :disabled="!!editing">
            <option v-for="k in KINDS" :key="k" :value="k">{{ s.kinds[k] }}</option>
          </select>
        </div>
        <div class="field">
          <label for="pv-url">{{ s.baseUrl }}</label>
          <input id="pv-url" v-model="form.base_url" type="url" class="mono" maxlength="500" :required="form.kind === 'openai_compatible' && !editing" aria-describedby="pv-url-hint" />
          <span id="pv-url-hint" class="hint">{{ s.baseUrlHint }}</span>
        </div>
        <div class="field">
          <label for="pv-models">{{ s.models }}</label>
          <input id="pv-models" v-model="form.models" class="mono" aria-describedby="pv-models-hint" />
          <span id="pv-models-hint" class="hint">{{ s.modelsHint }}</span>
        </div>
        <div class="field">
          <label for="pv-key">{{ s.apiKey }}</label>
          <input id="pv-key" v-model="form.api_key" type="password" autocomplete="new-password" maxlength="1000" aria-describedby="pv-key-hint" data-testid="provider-key" />
          <span id="pv-key-hint" class="hint">{{ editing ? s.apiKeyHint : s.apiKeyNewHint }}</span>
        </div>
        <label v-if="editing?.api_key_set" class="check">
          <input v-model="form.clear_api_key" type="checkbox" :disabled="!!form.api_key" />
          <span>{{ s.clearKey }}</span>
        </label>
        <label class="check">
          <input v-model="form.enabled" type="checkbox" />
          <span>{{ s.enabled }}</span>
        </label>
      </form>
      <ErrorAlert :error="error" />
      <template #actions>
        <button type="button" class="btn" @click="open = false">{{ strings.app.cancel }}</button>
        <button type="submit" form="provider-form" class="btn btn-primary" :disabled="busy">{{ busy ? strings.app.saving : strings.app.save }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="!!removing" :title="removing ? s.confirmRemove(removing.name) : ''" @close="removing = undefined">
      <p>{{ s.confirmRemoveLead }}</p>
      <template #actions>
        <button type="button" class="btn" @click="removing = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" @click="remove">{{ strings.app.remove }}</button>
      </template>
    </ModalDialog>
  </section>
</template>

<style scoped>
.table-wrap {
  overflow-x: auto;
}
.url {
  display: block;
  word-break: break-all;
}
.actions {
  white-space: nowrap;
  text-align: end;
}
</style>
