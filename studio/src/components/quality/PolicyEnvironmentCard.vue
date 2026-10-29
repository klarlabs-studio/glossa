<script setup lang="ts">
/**
 * One environment block of the check policy (RFC 0005 §4.1):
 * production may require locales a branch does not, and a review state
 * a branch does not.
 *
 * The one distinction this card is careful about: an environment that
 * names no completeness requirement **inherits the document's**, which
 * is not the same as "every locale". So "inherit" is the first option
 * and the default, and naming a review state does not quietly change
 * what must be translated.
 */
import { computed } from "vue";
import type { ProjectLocale } from "../../api/schemas";
import { REQUIREMENTS, type DraftEnvironment } from "../../lib/policy";
import { strings } from "../../strings";

const props = defineProps<{
  environment: DraftEnvironment;
  locales: ProjectLocale[];
  disabled: boolean;
  /** `unnamed` or `duplicate`, as lib/policy's `problems` names them. */
  problem: string | undefined;
}>();
const emit = defineEmits<{ update: [patch: Partial<DraftEnvironment>]; remove: [] }>();
const s = strings.policy;

const id = (field: string) => `env-${props.environment.key}-${field}`;
const set = (patch: Partial<DraftEnvironment>) => emit("update", patch);
const listed = computed(() => props.environment.requireComplete === "listed");

function toggleLocale(code: string, on: boolean): void {
  const next = on ? [...props.environment.locales, code] : props.environment.locales.filter((c) => c !== code);
  set({ locales: next });
}
</script>

<template>
  <li class="env card stack-sm" data-testid="policy-environment">
    <fieldset class="stack-sm">
      <legend class="visually-hidden">{{ environment.name || s.environmentNamePlaceholder }}</legend>
      <div class="field">
        <label :for="id('name')">{{ s.environmentName }}</label>
        <input
          :id="id('name')"
          type="text"
          :value="environment.name"
          :placeholder="s.environmentNamePlaceholder"
          :disabled="disabled"
          aria-required="true"
          :aria-invalid="problem ? 'true' : undefined"
          :aria-describedby="problem ? id('error') : undefined"
          data-testid="env-name"
          @change="set({ name: ($event.target as HTMLInputElement).value })"
        />
        <span v-if="problem" :id="id('error')" class="field-error" role="alert" data-testid="env-error">
          {{ problem === "duplicate" ? s.environmentDuplicate : s.environmentNameRequired }}
        </span>
      </div>

      <div class="field">
        <label :for="id('requirement')">{{ s.requireComplete }}</label>
        <select
          :id="id('requirement')"
          :value="environment.requireComplete"
          :disabled="disabled"
          data-testid="env-requirement"
          @change="set({ requireComplete: ($event.target as HTMLSelectElement).value as DraftEnvironment['requireComplete'] })"
        >
          <option value="">{{ s.environmentInherit }}</option>
          <option v-for="r in REQUIREMENTS" :key="r" :value="r">{{ s.requirement[r] ?? r }}</option>
        </select>
      </div>

      <fieldset v-if="listed" class="stack-sm">
        <legend class="label">{{ s.requireCompleteLocales }}</legend>
        <label v-for="l in locales" :key="l.code" class="check">
          <input
            type="checkbox"
            :checked="environment.locales.includes(l.code)"
            :disabled="disabled"
            @change="toggleLocale(l.code, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ l.code }}</span>
        </label>
      </fieldset>

      <label class="check">
        <input
          type="checkbox"
          :checked="environment.requireReview"
          :disabled="disabled"
          data-testid="env-review"
          @change="set({ requireReview: ($event.target as HTMLInputElement).checked })"
        />
        <span>{{ s.environmentReview }}</span>
      </label>
    </fieldset>

    <div>
      <button type="button" class="btn btn-sm btn-ghost" :disabled="disabled" data-testid="env-remove" @click="emit('remove')">
        {{ s.removeEnvironment }}<span class="visually-hidden">{{ s.removeEnvironmentFor(environment.name) }}</span>
      </button>
    </div>
  </li>
</template>

<style scoped>
.env {
  padding: var(--kl-space-4);
}
fieldset {
  border: none;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.field {
  max-inline-size: 22rem;
}
</style>
