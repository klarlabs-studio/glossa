<script setup lang="ts">
/**
 * Add or edit a termbase concept: definition, domain, note and its terms
 * per locale with status, part of speech and case sensitivity. Editing
 * replaces the concept with If-Match; terms that stay keep their IDs.
 */
import { computed, reactive, ref, watch } from "vue";
import type { Versioned } from "../../api/errors";
import { isApiError } from "../../api/errors";
import type { PartOfSpeech, TermConcept, TermStatus } from "../../api/knowledge-schemas";
import { useKnowledge, type TermInput } from "../../api/knowledge";
import { newIdempotencyKey } from "../../api/releases";
import type { ProjectLocale } from "../../api/schemas";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

const props = defineProps<{
  open: boolean;
  tenant: string;
  projectId: string;
  locales: ProjectLocale[];
  /** The concept being edited, with its ETag; none to add one. */
  concept: Versioned<TermConcept> | undefined;
}>();
const emit = defineEmits<{ close: []; saved: [concept: TermConcept, created: boolean] }>();
const s = strings.termbase;
const port = useKnowledge();

interface TermRow {
  locale: string;
  text: string;
  status: TermStatus;
  part_of_speech: PartOfSpeech | "";
  case_sensitive: boolean;
  note: string;
}
const STATUSES: TermStatus[] = ["preferred", "admitted", "deprecated", "forbidden"];
const POS: Array<PartOfSpeech | ""> = ["", "noun", "verb", "adjective", "adverb", "proper_noun", "phrase", "other"];

const form = reactive({ definition: "", domain: "", note: "", scope: "project" as "project" | "tenant", terms: [] as TermRow[] });
const error = ref<unknown>(null);
const busy = ref(false);
let key = newIdempotencyKey();

const blankRow = (locale: string): TermRow => ({ locale, text: "", status: "preferred", part_of_speech: "", case_sensitive: false, note: "" });

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    error.value = null;
    key = newIdempotencyKey();
    const c = props.concept?.value;
    form.definition = c?.definition ?? "";
    form.domain = c?.domain ?? "";
    form.note = c?.note ?? "";
    form.scope = c && !c.project_id ? "tenant" : "project";
    form.terms = c
      ? c.terms.map((t) => ({ locale: t.locale, text: t.text, status: t.status, part_of_speech: t.part_of_speech ?? "", case_sensitive: t.case_sensitive, note: t.note ?? "" }))
      : props.locales.slice(0, 2).map((l) => blankRow(l.code));
  },
  { immediate: true },
);

const localeCodes = computed(() => {
  const codes = props.locales.map((l) => l.code);
  for (const t of form.terms) if (t.locale && !codes.includes(t.locale)) codes.push(t.locale);
  return codes;
});

function terms(): TermInput[] {
  return form.terms
    .filter((t) => t.text.trim())
    .map((t) => ({
      locale: t.locale,
      text: t.text.trim(),
      status: t.status,
      case_sensitive: t.case_sensitive,
      ...(t.part_of_speech ? { part_of_speech: t.part_of_speech } : {}),
      ...(t.note.trim() ? { note: t.note.trim() } : {}),
    }));
}

async function save(): Promise<void> {
  const list = terms();
  if (!list.length) {
    error.value = new Error(s.needsTerm);
    return;
  }
  busy.value = true;
  error.value = null;
  const body = { definition: form.definition.trim(), domain: form.domain.trim(), note: form.note.trim(), terms: list };
  try {
    const existing = props.concept;
    if (existing) {
      const r = await port.replaceConcept(props.tenant, existing.value.id, body, existing.etag ?? "");
      emit("saved", r.value, false);
    } else {
      const r = await port.createConcept(props.tenant, { ...body, ...(form.scope === "project" ? { project_id: props.projectId } : {}) }, key);
      emit("saved", r.value, true);
    }
  } catch (e) {
    error.value = isApiError(e, "precondition_failed") ? new Error(s.changedElsewhere) : e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog :open="open" :title="concept ? s.dialogEdit : s.dialogNew" wide @close="emit('close')">
    <form id="concept-form" class="stack" @submit.prevent="save">
      <div class="grid">
        <div class="field wide">
          <label for="c-def">{{ s.definition }}</label>
          <textarea id="c-def" v-model="form.definition" rows="2" maxlength="4000" />
        </div>
        <div class="field">
          <label for="c-domain">{{ s.domain }}</label>
          <input id="c-domain" v-model="form.domain" maxlength="100" />
        </div>
        <div class="field">
          <label for="c-scope">{{ s.scope }}</label>
          <select id="c-scope" v-model="form.scope" :disabled="!!concept" aria-describedby="c-scope-hint">
            <option value="project">{{ s.scopeProject }}</option>
            <option value="tenant">{{ s.scopeTenant }}</option>
          </select>
          <span id="c-scope-hint" class="hint">{{ s.scopeHint }}</span>
        </div>
        <div class="field wide">
          <label for="c-note">{{ s.note }}</label>
          <input id="c-note" v-model="form.note" maxlength="4000" />
        </div>
      </div>
      <fieldset class="stack-sm">
        <legend class="label">{{ s.terms }}</legend>
        <div class="table-wrap">
          <table class="table terms">
            <thead>
              <tr>
                <th scope="col">{{ s.locale }}</th>
                <th scope="col">{{ s.term }}</th>
                <th scope="col">{{ s.status }}</th>
                <th scope="col">{{ s.partOfSpeech }}</th>
                <th scope="col">{{ s.caseSensitive }}</th>
                <th scope="col">{{ s.termNote }}</th>
                <th scope="col"><span class="visually-hidden">{{ strings.app.remove }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(t, i) in form.terms" :key="i" data-testid="term-row">
                <td>
                  <select v-model="t.locale" :aria-label="`${s.locale} ${i + 1}`">
                    <option v-for="c in localeCodes" :key="c" :value="c">{{ c }}</option>
                  </select>
                </td>
                <td><input v-model="t.text" maxlength="200" :lang="t.locale" :aria-label="`${s.term} ${i + 1}`" /></td>
                <td>
                  <select v-model="t.status" :aria-label="`${s.status} ${i + 1}`">
                    <option v-for="st in STATUSES" :key="st" :value="st">{{ strings.terms.status[st] }}</option>
                  </select>
                </td>
                <td>
                  <select v-model="t.part_of_speech" :aria-label="`${s.partOfSpeech} ${i + 1}`">
                    <option v-for="p in POS" :key="p" :value="p">{{ s.pos[p] }}</option>
                  </select>
                </td>
                <td class="center"><input v-model="t.case_sensitive" type="checkbox" :aria-label="`${s.caseSensitive} ${i + 1}`" /></td>
                <td><input v-model="t.note" maxlength="4000" :aria-label="`${s.termNote} ${i + 1}`" /></td>
                <td>
                  <button type="button" class="btn btn-ghost btn-sm" :aria-label="s.removeTerm(i + 1)" @click="form.terms.splice(i, 1)">×</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div>
          <button type="button" class="btn btn-sm" @click="form.terms.push(blankRow(locales[0]?.code ?? 'en'))">{{ s.addTerm }}</button>
        </div>
      </fieldset>
    </form>
    <ErrorAlert :error="error" />
    <template #actions>
      <button type="button" class="btn" @click="emit('close')">{{ strings.app.cancel }}</button>
      <button type="submit" form="concept-form" class="btn btn-primary" :disabled="busy">{{ busy ? strings.app.saving : s.saveConcept }}</button>
    </template>
  </ModalDialog>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--kl-space-3);
}
.wide {
  grid-column: 1 / -1;
}
fieldset {
  border: none;
  margin: 0;
  padding: 0;
}
.table-wrap {
  overflow-x: auto;
}
.terms td {
  padding: var(--kl-space-1);
}
.terms input:not([type="checkbox"]),
.terms select {
  inline-size: 100%;
  min-inline-size: 5rem;
}
.center {
  text-align: center;
}
</style>
