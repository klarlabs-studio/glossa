<script setup lang="ts">
/**
 * Add or edit a style guide: its scope (on creation only), name,
 * structured fields — an empty field inherits from a broader guide — and
 * rules with rationale and good/bad examples.
 */
import { computed, reactive, ref, watch } from "vue";
import type { Versioned } from "../../api/errors";
import { isApiError } from "../../api/errors";
import type { StyleGuide } from "../../api/knowledge-schemas";
import { useKnowledge } from "../../api/knowledge";
import { newIdempotencyKey } from "../../api/releases";
import type { ProjectLocale } from "../../api/schemas";
import { fieldsOf, formOf, RULE_ID, ruleFormOf, ruleIdFrom, ruleOf, type RuleForm, type StyleForm } from "../../lib/style";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

export type ScopeLevel = "tenant" | "project" | "locale" | "namespace";

const props = defineProps<{
  open: boolean;
  tenant: string;
  projectId: string;
  locales: ProjectLocale[];
  guide: Versioned<StyleGuide> | undefined;
}>();
const emit = defineEmits<{ close: []; saved: [guide: StyleGuide, created: boolean] }>();
const s = strings.style;
const port = useKnowledge();

const scope = reactive({ level: "project" as ScopeLevel, locale: "", namespace: "" });
const name = ref("");
const form = ref<StyleForm>(formOf({}));
const rules = ref<RuleForm[]>([]);
const error = ref<unknown>(null);
const busy = ref(false);
let key = newIdempotencyKey();

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    const g = props.guide?.value;
    error.value = null;
    key = newIdempotencyKey();
    name.value = g?.name ?? "";
    form.value = formOf(g?.fields ?? {});
    rules.value = (g?.rules ?? []).map(ruleFormOf);
    scope.level = "project";
    scope.locale = props.locales.find((l) => !l.is_source)?.code ?? props.locales[0]?.code ?? "";
    scope.namespace = "";
  },
  { immediate: true },
);

const needsLocale = computed(() => scope.level === "locale" || scope.level === "namespace");

function addRule(): void {
  rules.value.push({ id: "", title: "", rationale: "", good: "", bad: "", disabled: false });
}

async function save(): Promise<void> {
  error.value = null;
  const list = rules.value.map((r) => ({ ...r, id: r.id.trim() || ruleIdFrom(r.title) }));
  const bad = list.find((r) => !RULE_ID.test(r.id));
  if (bad) {
    error.value = new Error(s.invalidRuleId(bad.id));
    return;
  }
  busy.value = true;
  const content = { name: name.value.trim(), fields: fieldsOf(form.value), rules: list.map(ruleOf) };
  try {
    const existing = props.guide;
    if (existing) {
      const r = await port.replaceStyleGuide(props.tenant, existing.value.id, content, existing.etag ?? "");
      emit("saved", r.value, false);
    } else {
      const where = {
        ...(scope.level !== "tenant" ? { project_id: props.projectId } : {}),
        ...(needsLocale.value && scope.locale ? { locale: scope.locale } : {}),
        ...(scope.level === "namespace" && scope.namespace.trim() ? { namespace: scope.namespace.trim() } : {}),
      };
      const r = await port.createStyleGuide(props.tenant, { ...where, ...content }, key);
      emit("saved", r.value, true);
    }
  } catch (e) {
    error.value = isApiError(e, "precondition_failed") ? new Error(s.changedElsewhere) : e;
  } finally {
    busy.value = false;
  }
}

const tri = [
  ["space_before_unit", s.spaceBeforeUnit],
  ["space_before_punctuation", s.spaceBeforePunctuation],
  ["serial_comma", s.serialComma],
] as const;
</script>

<template>
  <ModalDialog :open="open" :title="guide ? s.dialogEdit(guide.value.name || s.tenantScope) : s.dialogNew" wide @close="emit('close')">
    <form id="style-form" class="stack" @submit.prevent="save">
      <fieldset v-if="!guide" class="stack-sm">
        <legend class="label">{{ s.scope }}</legend>
        <div class="row">
          <label v-for="lvl in (['tenant', 'project', 'locale', 'namespace'] as const)" :key="lvl" class="check">
            <input v-model="scope.level" type="radio" name="sg-level" :value="lvl" />
            <span>{{ s.scopeLevel[lvl] }}</span>
          </label>
        </div>
        <div class="row">
          <div v-if="needsLocale" class="field">
            <label for="sg-locale">{{ s.locale }}</label>
            <select id="sg-locale" v-model="scope.locale">
              <option v-for="l in locales" :key="l.code" :value="l.code">{{ l.code }}</option>
            </select>
          </div>
          <div v-if="scope.level === 'namespace'" class="field">
            <label for="sg-ns">{{ s.namespace }}</label>
            <input id="sg-ns" v-model="scope.namespace" class="mono" pattern="[a-z0-9][a-z0-9_-]{0,63}" required />
          </div>
        </div>
      </fieldset>
      <div class="field">
        <label for="sg-name">{{ s.name }}</label>
        <input id="sg-name" v-model="name" maxlength="200" />
      </div>

      <fieldset class="stack-sm">
        <legend class="label">{{ s.fields }}</legend>
        <div class="grid">
          <div class="field">
            <label for="sg-register">{{ s.register }}</label>
            <select id="sg-register" v-model="form.register">
              <option value="">{{ s.inherit }}</option>
              <option v-for="r in (['formal', 'informal', 'neutral'] as const)" :key="r" :value="r">{{ s.registers[r] }}</option>
            </select>
          </div>
          <div class="field">
            <label for="sg-pronoun">{{ s.pronoun }}</label>
            <input id="sg-pronoun" v-model="form.pronoun" aria-describedby="sg-pronoun-hint" />
            <span id="sg-pronoun-hint" class="hint">{{ s.pronounHint }}</span>
          </div>
          <div class="field wide">
            <label for="sg-tone">{{ s.tone }}</label>
            <input id="sg-tone" v-model="form.tone" aria-describedby="sg-tone-hint" />
            <span id="sg-tone-hint" class="hint">{{ s.toneHint }}</span>
          </div>
          <div class="field">
            <label for="sg-quotes">{{ s.quotes }}</label>
            <input id="sg-quotes" v-model="form.quotes" />
          </div>
          <div class="field">
            <label for="sg-nested">{{ s.nestedQuotes }}</label>
            <input id="sg-nested" v-model="form.nested_quotes" />
          </div>
          <div class="field">
            <label for="sg-dash">{{ s.dash }}</label>
            <select id="sg-dash" v-model="form.dash">
              <option value="">{{ s.inherit }}</option>
              <option v-for="d in (['hyphen', 'en', 'em'] as const)" :key="d" :value="d">{{ s.summary.dashes[d] }}</option>
            </select>
          </div>
          <div class="field">
            <label for="sg-ellipsis">{{ s.ellipsis }}</label>
            <input id="sg-ellipsis" v-model="form.ellipsis" />
          </div>
          <div v-for="[k, label] in tri" :key="k" class="field">
            <label :for="`sg-${k}`">{{ label }}</label>
            <select :id="`sg-${k}`" v-model="form[k]">
              <option value="">{{ s.inherit }}</option>
              <option value="yes">{{ s.yes }}</option>
              <option value="no">{{ s.no }}</option>
            </select>
          </div>
          <div class="field">
            <label for="sg-decimal">{{ s.decimal }}</label>
            <input id="sg-decimal" v-model="form.decimal_separator" />
          </div>
          <div class="field">
            <label for="sg-grouping">{{ s.grouping }}</label>
            <input id="sg-grouping" v-model="form.grouping_separator" />
          </div>
          <div class="field wide">
            <label for="sg-numnotes">{{ s.numberNotes }}</label>
            <input id="sg-numnotes" v-model="form.number_notes" />
          </div>
          <div class="field">
            <label for="sg-date">{{ s.dateFormat }}</label>
            <input id="sg-date" v-model="form.date_format" class="mono" />
          </div>
          <div class="field">
            <label for="sg-datenotes">{{ s.dateNotes }}</label>
            <input id="sg-datenotes" v-model="form.date_notes" />
          </div>
        </div>
      </fieldset>

      <fieldset class="stack-sm">
        <legend class="label">{{ s.rulesTitle }}</legend>
        <div v-for="(r, i) in rules" :key="i" class="rule stack-sm" data-testid="rule">
          <div class="grid">
            <div class="field">
              <label :for="`rule-title-${i}`">{{ s.ruleTitle }} {{ i + 1 }}</label>
              <input :id="`rule-title-${i}`" v-model="r.title" maxlength="200" :disabled="r.disabled" />
            </div>
            <div class="field">
              <label :for="`rule-id-${i}`">{{ s.ruleId }}</label>
              <input :id="`rule-id-${i}`" v-model="r.id" class="mono" maxlength="64" :placeholder="r.title ? ruleIdFrom(r.title) : ''" :aria-describedby="`rule-id-hint-${i}`" />
              <span :id="`rule-id-hint-${i}`" class="hint">{{ s.ruleIdHint }}</span>
            </div>
            <div class="field wide">
              <label :for="`rule-why-${i}`">{{ s.rationale }}</label>
              <textarea :id="`rule-why-${i}`" v-model="r.rationale" rows="2" maxlength="4000" :disabled="r.disabled" />
            </div>
            <div class="field">
              <label :for="`rule-good-${i}`">{{ s.goodExamples }}</label>
              <textarea :id="`rule-good-${i}`" v-model="r.good" rows="2" :disabled="r.disabled" />
            </div>
            <div class="field">
              <label :for="`rule-bad-${i}`">{{ s.badExamples }}</label>
              <textarea :id="`rule-bad-${i}`" v-model="r.bad" rows="2" :disabled="r.disabled" />
            </div>
          </div>
          <div class="row">
            <label class="check">
              <input v-model="r.disabled" type="checkbox" />
              <span>{{ s.disableRule }}</span>
            </label>
            <span class="spacer" />
            <button type="button" class="btn btn-sm btn-danger" @click="rules.splice(i, 1)">{{ s.removeRule(i + 1) }}</button>
          </div>
        </div>
        <div>
          <button type="button" class="btn btn-sm" @click="addRule">{{ s.addRule }}</button>
        </div>
      </fieldset>
    </form>
    <ErrorAlert :error="error" />
    <template #actions>
      <button type="button" class="btn" @click="emit('close')">{{ strings.app.cancel }}</button>
      <button type="submit" form="style-form" class="btn btn-primary" :disabled="busy">{{ busy ? strings.app.saving : s.saveGuide }}</button>
    </template>
  </ModalDialog>
</template>

<style scoped>
fieldset {
  border: none;
  margin: 0;
  padding: 0;
}
.grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--kl-space-3);
}
.wide {
  grid-column: 1 / -1;
}
.rule {
  padding: var(--kl-space-3);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
}
</style>
