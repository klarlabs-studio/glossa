<script setup lang="ts">
/**
 * Project settings → Pull request check (RFC 0004 §6.4): the project's
 * check policy.
 *
 * The same policy decides `glossa check` in a terminal and the Glossa
 * check on a pull request, so the card is written for the question
 * people actually ask — "what makes a pull request fail?" — and answers
 * it in a sentence under the controls, recomputed as they move.
 */
import { computed, reactive, ref, watch } from "vue";
import { projects } from "../../api/endpoints";
import { isApiError } from "../../api/errors";
import type { CheckPolicy } from "../../api/schemas";
import { allows } from "../../session/permissions";
import { strings } from "../../strings";
import { useProject } from "../../views/project/context";
import ErrorAlert from "../ErrorAlert.vue";

const { tenant, projectId, project, etag, locales, grant, reloadProject } = useProject();
const s = strings.checkPolicy;
const canWrite = computed(() => allows(grant.value, "catalog.write"));

/** The default a project that has never set one reads as. */
const DEFAULT: CheckPolicy = { require_complete: "all", locales: [], fail_on: "error", missing_translations: "error" };

const form = reactive<CheckPolicy>({ ...DEFAULT, locales: [] });
watch(
  project,
  (p) => {
    const stored = p?.settings.check_policy ?? DEFAULT;
    Object.assign(form, { ...stored, locales: [...(stored.locales ?? [])] });
  },
  { immediate: true },
);

/** Target locales: requiring the source locale to be complete says nothing. */
const pickable = computed(() => locales.value.filter((l) => !l.is_source).map((l) => l.code));
const listed = computed(() => form.require_complete === "listed");
const nothingPicked = computed(() => listed.value && form.locales?.length === 0);

function togglePicked(code: string, on: boolean): void {
  const picked = new Set(form.locales ?? []);
  if (on) picked.add(code);
  else picked.delete(code);
  form.locales = pickable.value.filter((c) => picked.has(c));
}

const severity = computed(() => (form.missing_translations === "error" ? s.anError : s.aWarning));
const failure = computed(() => {
  if (form.fail_on === "never") return s.effectNeverFails;
  return form.fail_on === "warning" ? s.effectFailsOnWarnings : s.effectFailsOnErrors;
});
const untranslated = computed(() => {
  if (form.require_complete === "none") return s.untranslatedNone;
  if (form.require_complete === "all") return s.untranslatedAll(severity.value);
  const picked = form.locales ?? [];
  if (picked.length === 0) return s.untranslatedNone;
  return s.untranslatedListed(picked.join(", "), severity.value);
});

const saving = ref(false);
const saved = ref(false);
const error = ref<unknown>(null);

async function save(): Promise<void> {
  const current = project.value;
  if (!current || !etag.value) return;
  saving.value = true;
  saved.value = false;
  error.value = null;
  try {
    // An empty pick is "none": an empty list of locales that must be
    // complete is exactly no locale having to be, and the server stores
    // it that way too.
    const policy: CheckPolicy =
      nothingPicked.value ? { ...form, require_complete: "none", locales: [] } : { ...form, locales: [...(form.locales ?? [])] };
    const r = await projects.update(
      { tenant: tenant.value, project: projectId.value },
      {
        settings: {
          default_syntax: current.settings.default_syntax,
          review_required: current.settings.review_required,
          check_policy: policy,
        },
      },
      etag.value,
    );
    project.value = r.value;
    etag.value = r.etag;
    saved.value = true;
  } catch (e) {
    error.value = isApiError(e, "precondition_failed") ? new Error(strings.settings.changedElsewhere) : e;
    if (isApiError(e, "precondition_failed")) await reloadProject();
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="card stack" aria-labelledby="check-policy-h">
    <h2 id="check-policy-h">{{ s.title }}</h2>
    <p class="muted">{{ s.lead }}</p>
    <ErrorAlert :error="error" />
    <form class="stack" @submit.prevent="save">
      <div class="field">
        <label for="cp-require">{{ s.requireComplete }}</label>
        <select id="cp-require" v-model="form.require_complete" :disabled="!canWrite">
          <option value="all">{{ s.requireCompleteAll }}</option>
          <option value="listed">{{ s.requireCompleteListed }}</option>
          <option value="none">{{ s.requireCompleteNone }}</option>
        </select>
      </div>

      <fieldset v-if="listed" class="locales">
        <legend>{{ s.locales }}</legend>
        <p id="cp-locales-hint" class="hint">{{ s.localesHint }}</p>
        <label v-for="code in pickable" :key="code" class="check">
          <input
            type="checkbox"
            :value="code"
            :checked="form.locales?.includes(code)"
            :disabled="!canWrite"
            aria-describedby="cp-locales-hint"
            @change="togglePicked(code, ($event.target as HTMLInputElement).checked)"
          />
          <span class="mono">{{ code }}</span>
        </label>
        <p v-if="nothingPicked" class="hint">{{ s.noLocalesPicked }}</p>
      </fieldset>

      <div class="field">
        <label for="cp-missing">{{ s.missingTranslations }}</label>
        <select id="cp-missing" v-model="form.missing_translations" :disabled="!canWrite" aria-describedby="cp-missing-hint">
          <option value="error">{{ s.missingTranslationsError }}</option>
          <option value="warning">{{ s.missingTranslationsWarning }}</option>
        </select>
        <span id="cp-missing-hint" class="hint">{{ s.missingTranslationsHint }}</span>
      </div>

      <div class="field">
        <label for="cp-fail-on">{{ s.failOn }}</label>
        <select id="cp-fail-on" v-model="form.fail_on" :disabled="!canWrite">
          <option value="error">{{ s.failOnError }}</option>
          <option value="warning">{{ s.failOnWarning }}</option>
          <option value="never">{{ s.failOnNever }}</option>
        </select>
      </div>

      <aside class="effect stack-sm" aria-live="polite">
        <strong>{{ s.effectTitle }}</strong>
        <p>{{ failure }}</p>
        <p>{{ untranslated }}</p>
      </aside>

      <div v-if="canWrite" class="row">
        <button type="submit" class="btn btn-primary" :disabled="saving">
          {{ saving ? strings.app.saving : strings.app.save }}
        </button>
        <span v-if="saved" class="pill pill-ok" role="status">{{ s.saved }}</span>
      </div>
    </form>
  </section>
</template>

<style scoped>
.locales {
  border: 1px solid var(--gs-border, currentColor);
  border-radius: var(--kl-radius-2, 4px);
  padding: var(--kl-space-3);
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
}
.locales legend {
  padding-inline: var(--kl-space-2);
}
.locales .hint {
  flex-basis: 100%;
  margin: 0;
}
.effect {
  border-inline-start: 3px solid var(--gs-accent, currentColor);
  padding-inline-start: var(--kl-space-3);
}
.effect p {
  margin: 0;
}
</style>
