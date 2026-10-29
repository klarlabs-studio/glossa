<script setup lang="ts">
/**
 * This project's AI policy: namespace tags (sensitive text never reaches
 * a provider), auto-translate locales, and how suggestions are routed by
 * confidence — auto-approve off by default, with a warning when on.
 */
import { reactive, ref, watch } from "vue";
import type { AINamespaceTag, AIProjectSettings } from "../../api/intelligence-schemas";
import type { ProjectSettingsUpdate } from "../../api/intelligence";
import type { ProjectLocale } from "../../api/schemas";
import { strings } from "../../strings";

const props = defineProps<{ settings: AIProjectSettings | undefined; targets: ProjectLocale[]; canManage: boolean; busy: boolean }>();
const emit = defineEmits<{ save: [update: ProjectSettingsUpdate] }>();
const s = strings.aiSettings;
const TAGS: AINamespaceTag[] = ["sensitive", "legal", "marketing"];

const tags = ref<Array<{ namespace: string; tags: AINamespaceTag[] }>>([]);
const auto = ref<string[]>([]);
const review = reactive({ recommend_min: 0.75, auto_approve: false, auto_approve_min: 0.92, environments: "", force_review: "" });
const newNs = ref("");

watch(
  () => props.settings,
  (st) => {
    if (!st) return;
    tags.value = Object.entries(st.namespace_tags)
      .map(([namespace, t]) => ({ namespace, tags: [...t] }))
      .sort((a, b) => a.namespace.localeCompare(b.namespace));
    auto.value = [...st.auto_translate_locales];
    Object.assign(review, {
      recommend_min: st.review.recommend_min,
      auto_approve: st.review.auto_approve,
      auto_approve_min: st.review.auto_approve_min,
      environments: (st.review.auto_approve_environments ?? []).join(", "),
      force_review: (st.review.force_review ?? []).join(", "),
    });
  },
  { immediate: true },
);

const list = (t: string) =>
  t
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

function addNamespace(): void {
  const ns = newNs.value.trim();
  if (!ns || tags.value.some((t) => t.namespace === ns)) return;
  tags.value.push({ namespace: ns, tags: [] });
  newNs.value = "";
}

function save(): void {
  emit("save", {
    // Plain copies, not the form's reactive arrays.
    namespace_tags: Object.fromEntries(tags.value.filter((t) => t.tags.length).map((t) => [t.namespace, [...t.tags]])),
    auto_translate_locales: [...auto.value],
    review: {
      recommend_min: review.recommend_min,
      auto_approve: review.auto_approve,
      auto_approve_min: review.auto_approve_min,
      auto_approve_environments: list(review.environments),
      force_review: list(review.force_review),
    },
  });
}
</script>

<template>
  <section class="card stack" aria-labelledby="pp-h" data-testid="project-policy">
    <h2 id="pp-h">{{ s.projectPolicy }}</h2>
    <form class="stack" @submit.prevent="save">
      <fieldset class="stack-sm" :disabled="!canManage">
        <legend class="label">{{ s.namespaceTags }}</legend>
        <p class="hint">{{ s.namespaceTagsLead }}</p>
        <p v-if="!tags.length" class="muted">{{ s.noTags }}</p>
        <table v-else class="table small">
          <thead>
            <tr>
              <th scope="col">{{ s.namespace }}</th>
              <th v-for="t in TAGS" :key="t" scope="col">{{ s.tagNames[t] }}</th>
              <th scope="col"><span class="visually-hidden">{{ strings.app.remove }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in tags" :key="row.namespace">
              <th scope="row" class="mono">{{ row.namespace }}</th>
              <td v-for="t in TAGS" :key="t">
                <input v-model="row.tags" type="checkbox" :value="t" :aria-label="`${s.tagNames[t]}: ${row.namespace}`" />
              </td>
              <td><button type="button" class="btn btn-ghost btn-sm" :aria-label="s.removeNamespace(row.namespace)" @click="tags.splice(i, 1)">×</button></td>
            </tr>
          </tbody>
        </table>
        <div class="row add">
          <div class="field">
            <label for="pp-ns">{{ s.namespace }}</label>
            <input id="pp-ns" v-model="newNs" class="mono" pattern="[a-z0-9][a-z0-9_-]{0,63}" @keydown.enter.prevent="addNamespace" />
          </div>
          <button type="button" class="btn btn-sm" @click="addNamespace">{{ s.addNamespace }}</button>
        </div>
      </fieldset>

      <fieldset class="stack-sm" :disabled="!canManage">
        <legend class="label">{{ s.autoTranslate }}</legend>
        <p class="hint">{{ s.autoTranslateLead }}</p>
        <div class="row">
          <label v-for="l in targets" :key="l.code" class="check">
            <input v-model="auto" type="checkbox" :value="l.code" />
            <span class="mono">{{ l.code }}</span>
          </label>
        </div>
      </fieldset>

      <fieldset class="stack-sm" :disabled="!canManage">
        <legend class="label">{{ s.reviewRouting }}</legend>
        <p class="hint">{{ s.reviewRoutingLead }}</p>
        <div class="row add">
          <div class="field">
            <label for="pp-rec">{{ s.recommendMin }}</label>
            <input id="pp-rec" v-model.number="review.recommend_min" type="number" min="0.01" max="1" step="0.01" />
          </div>
          <div class="field grow">
            <label for="pp-force">{{ s.forceReview }}</label>
            <input id="pp-force" v-model="review.force_review" class="mono" aria-describedby="pp-force-hint" />
            <span id="pp-force-hint" class="hint">{{ s.forceReviewHint }}</span>
          </div>
        </div>
        <label class="check">
          <input v-model="review.auto_approve" type="checkbox" data-testid="auto-approve" />
          <span>{{ s.autoApprove }}</span>
        </label>
        <p v-if="review.auto_approve" class="alert alert-warn" role="note">{{ s.autoApproveWarning }}</p>
        <div v-if="review.auto_approve" class="row add">
          <div class="field">
            <label for="pp-auto">{{ s.autoApproveMin }}</label>
            <input id="pp-auto" v-model.number="review.auto_approve_min" type="number" min="0.01" max="1" step="0.01" />
          </div>
          <div class="field grow">
            <label for="pp-envs">{{ s.autoApproveEnvironments }}</label>
            <input id="pp-envs" v-model="review.environments" class="mono" aria-describedby="pp-envs-hint" />
            <span id="pp-envs-hint" class="hint">{{ s.autoApproveEnvironmentsHint }}</span>
          </div>
        </div>
      </fieldset>
      <div v-if="canManage">
        <button type="submit" class="btn btn-primary" :disabled="busy">{{ s.saveProject }}</button>
      </div>
    </form>
  </section>
</template>

<style scoped>
fieldset {
  border: none;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.small {
  font-size: var(--kl-text-sm);
}
.add {
  align-items: flex-end;
}
.grow {
  flex: 1;
  min-inline-size: 14rem;
}
</style>
