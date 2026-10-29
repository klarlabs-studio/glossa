<script setup lang="ts">
/**
 * The check-policy editor (RFC 0005 §4, §9): the base fields, the rules
 * with their selectors in the order that decides them, the
 * environments, and — before every save — the impact preview.
 *
 * Two rules this screen will not bend on:
 *
 * 1. **The preview comes before the save, never after it.** There is no
 *    button here that stores a policy without having first asked, with
 *    `dry_run: true`, how many stored findings change severity and how
 *    many open pull requests would newly fail. Edit anything and the
 *    preview goes stale and the save button goes back to being a
 *    preview button. This is the feature that makes a policy change
 *    safe to reason about (§4.3); a save that skipped it would be the
 *    bug.
 * 2. **Order is visible and changeable.** Ties in specificity go to the
 *    later rule, so the rules are an ordered list with numbered
 *    positions, per-rule specificity, a sentence naming exactly which
 *    other rules a rule's position decides against, and move controls
 *    that are ordinary buttons — reachable from the keyboard, with
 *    focus following the rule and the move announced.
 *
 * It consumes the policy API and changes nothing about it.
 */
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { useCheckPolicy } from "../../api/policy";
import type { PolicyDocument, PolicySaved, PolicyState, PolicyVersion } from "../../api/policy-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import PolicyEnvironmentCard from "../../components/quality/PolicyEnvironmentCard.vue";
import PolicyImpact from "../../components/quality/PolicyImpact.vue";
import PolicyRuleCard from "../../components/quality/PolicyRuleCard.vue";
import QualitySections from "../../components/quality/QualitySections.vue";
import {
  FAIL_ON,
  MISSING_TRANSLATIONS,
  REQUIREMENTS,
  canonical,
  emptyEnvironment,
  emptyRule,
  moveRule,
  problems,
  ruleOrder,
  toDocument,
  toDraft,
  type DraftEnvironment,
  type DraftPolicy,
  type DraftRule,
} from "../../lib/policy";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "./context";

const port = useCheckPolicy();
const { tenant, projectId, locales, grant } = useProject();
const s = strings.policy;
const person = usePeople(() => tenant.value);
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canWrite = computed(() => allows(grant.value, "catalog.write"));

// ── what the screen holds ──────────────────────────────────────────
const state = shallowRef<PolicyState>();
const versions = shallowRef<PolicyVersion[]>([]);
const draft = ref<DraftPolicy>();
const loading = ref(false);
const busy = ref(false);
const error = ref<unknown>(null);
const status = ref("");
/** The measured document, kept beside its canonical form so an edit can make it stale. */
const previewed = shallowRef<{ doc: string; saved: PolicySaved }>();
const graceDays = ref(14);
let abort: AbortController | undefined;

/**
 * The rule cards by their client-side key, not by index: Vue does not
 * promise that a `v-for` ref array keeps the source order, and after a
 * move the index is exactly what changed. The key follows the rule.
 */
interface RuleCard {
  focusMove(delta: number): void;
}
const cards = new Map<string, RuleCard>();
function setCard(key: string, el: unknown): void {
  if (el) cards.set(key, el as RuleCard);
  else cards.delete(key);
}

async function load(): Promise<void> {
  abort?.abort();
  const a = (abort = new AbortController());
  loading.value = true;
  error.value = null;
  previewed.value = undefined;
  try {
    const [p, v] = await Promise.all([port.policy(project(), a.signal), port.versions(project(), undefined, a.signal)]);
    if (a.signal.aborted) return;
    state.value = p;
    versions.value = v.items;
    draft.value = toDraft(p.document);
  } catch (e) {
    if (!a.signal.aborted) error.value = e;
  } finally {
    if (!a.signal.aborted) loading.value = false;
  }
}
watch(projectId, load, { immediate: true });
onBeforeUnmount(() => abort?.abort());

// ── the document as it stands in the editor ────────────────────────
const candidate = computed<PolicyDocument | undefined>(() => (draft.value ? toDocument(draft.value) : undefined));
const candidateKey = computed(() => (candidate.value ? canonical(candidate.value) : ""));
const storedKey = computed(() => (state.value ? canonical(state.value.document) : ""));
const dirty = computed(() => !!state.value && candidateKey.value !== storedKey.value);

const order = computed(() => (draft.value ? ruleOrder(draft.value.rules) : []));
const localeCodes = computed(() => locales.value.map((l) => l.code));
const issues = computed(() => (draft.value ? problems(draft.value, localeCodes.value) : []));
const problemAt = (where: string) => issues.value.find((p) => p.where === where)?.detail;
const blocked = computed(() => issues.value.length > 0);

/** A preview is only good for the document it measured. Anything else is a guess. */
const fresh = computed(() => !!previewed.value && previewed.value.doc === candidateKey.value);
const nextVersion = computed(() => (state.value?.version ?? 0) + 1);

// ── editing ────────────────────────────────────────────────────────
function patchRule(i: number, patch: Partial<DraftRule>): void {
  const d = draft.value;
  if (!d) return;
  const rule = d.rules[i];
  if (rule) Object.assign(rule, patch);
}
function addRule(): void {
  const d = draft.value;
  if (!d) return;
  d.rules.push(emptyRule());
  status.value = s.ruleAdded(d.rules.length, d.rules.length);
}
function removeRuleAt(i: number): void {
  const d = draft.value;
  if (!d) return;
  d.rules.splice(i, 1);
  status.value = s.ruleRemoved(i + 1);
}
/**
 * Moving a rule is a change of meaning, so it is announced as one, and
 * the keyboard goes with the rule rather than being dropped at the top
 * of the list.
 */
async function move(i: number, delta: number): Promise<void> {
  const d = draft.value;
  if (!d) return;
  const to = i + delta;
  if (to < 0 || to >= d.rules.length) return;
  const moved = d.rules[i];
  d.rules = moveRule(d.rules, i, to);
  status.value = s.moved(i + 1, to + 1, d.rules.length);
  // The button that was pressed may be the one the move disables, and a
  // disabled control drops focus to the document. Put the keyboard back
  // on the rule that moved.
  await nextTick();
  if (moved) cards.get(moved.key)?.focusMove(delta);
}
function addEnvironment(): void {
  draft.value?.environments.push(emptyEnvironment());
}
function patchEnvironment(i: number, patch: Partial<DraftEnvironment>): void {
  const env = draft.value?.environments[i];
  if (env) Object.assign(env, patch);
}
function removeEnvironment(i: number): void {
  draft.value?.environments.splice(i, 1);
}
function toggleRequiredLocale(code: string, on: boolean): void {
  const d = draft.value;
  if (!d) return;
  d.locales = on ? [...d.locales, code] : d.locales.filter((c) => c !== code);
}
function discard(): void {
  if (!state.value) return;
  draft.value = toDraft(state.value.document);
  previewed.value = undefined;
  status.value = s.discarded;
}
function loadVersion(v: PolicyVersion): void {
  draft.value = toDraft(v.document);
  previewed.value = undefined;
  status.value = s.historyLoaded(v.version);
}

// ── preview, then save ─────────────────────────────────────────────
async function preview(): Promise<void> {
  const doc = candidate.value;
  if (!doc || blocked.value) return;
  busy.value = true;
  error.value = null;
  status.value = "";
  try {
    const saved = await port.save(project(), doc, { dryRun: true, graceDays: graceDays.value });
    previewed.value = { doc: canonical(doc), saved };
  } catch (e) {
    previewed.value = undefined;
    error.value = e;
  } finally {
    busy.value = false;
  }
}

async function save(): Promise<void> {
  const doc = candidate.value;
  // The guard is the point of the screen: nothing is stored that was
  // not measured first, and an edit after the measurement invalidates it.
  if (!doc || !fresh.value || blocked.value) return;
  busy.value = true;
  error.value = null;
  try {
    const saved = await port.save(project(), doc, { graceDays: graceDays.value });
    state.value = saved.policy;
    draft.value = toDraft(saved.policy.document);
    previewed.value = undefined;
    status.value = s.saved(saved.policy.version);
    const v = await port.versions(project());
    versions.value = v.items;
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

const graceValid = computed(() => Number.isInteger(graceDays.value) && graceDays.value >= 0 && graceDays.value <= 90);
const versionSummary = (v: PolicyVersion) =>
  s.summary(s.requirement[v.document.require_complete] ?? v.document.require_complete, s.failOnName[v.document.fail_on] ?? v.document.fail_on, v.document.rules?.length ?? 0);
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <button type="button" class="btn" :disabled="loading || busy" @click="load">{{ strings.quality.reload }}</button>
    </div>

    <QualitySections :tenant="tenant" :project-id="projectId" />

    <p v-if="!canWrite" class="alert" data-testid="policy-read-only">{{ s.readOnly }}</p>
    <ErrorAlert :error="error" />
    <p v-if="status" class="alert alert-ok" role="status" data-testid="policy-status">{{ status }}</p>
    <p v-if="loading && !state" class="muted" role="status">{{ s.loading }}</p>

    <template v-else-if="state && draft">
      <!-- ── the version in force, and what it pins ──────────────── -->
      <section class="card stack-sm" aria-labelledby="version-h" data-testid="policy-version">
        <h2 id="version-h">{{ s.version(state.version) }}</h2>
        <p v-if="state.version === 0" class="muted">{{ s.versionNever }}</p>
        <p v-if="state.created_at" class="muted" data-testid="policy-saved-by">
          <time :datetime="state.created_at" :title="absoluteTime(state.created_at)">
            {{ s.savedBy(state.created_by ? person(state.created_by) : "—", relativeTime(state.created_at)) }}
          </time>
        </p>
        <!-- Grace is how a stricter policy rolls out without breaking open pull requests, so it is stated, never implied. -->
        <p v-if="state.grace_until && state.pinned_version !== undefined" class="hint" data-testid="policy-grace">
          {{ s.graceUntil(absoluteTime(state.grace_until), state.pinned_version) }}
        </p>
        <p v-else class="hint" data-testid="policy-no-grace">{{ s.graceNone }}</p>
        <p v-if="dirty" class="pill pill-accent unsaved" data-testid="policy-dirty">{{ s.unsaved }}</p>
      </section>

      <!-- ── the base fields ─────────────────────────────────────── -->
      <section class="card stack" aria-labelledby="base-h">
        <div class="stack-sm">
          <h2 id="base-h">{{ s.baseTitle }}</h2>
          <p class="muted">{{ s.baseLead }}</p>
        </div>
        <div class="field narrow">
          <label for="p-require">{{ s.requireComplete }}</label>
          <select
            id="p-require"
            v-model="draft.requireComplete"
            :disabled="!canWrite || busy"
            :aria-describedby="problemAt('require_complete') ? 'p-require-error' : undefined"
            data-testid="policy-require"
          >
            <option v-for="r in REQUIREMENTS" :key="r" :value="r">{{ s.requirement[r] ?? r }}</option>
          </select>
          <span v-if="problemAt('require_complete')" id="p-require-error" class="field-error" role="alert" data-testid="policy-require-error">
            {{ s.requireCompleteEmpty }}
          </span>
        </div>
        <fieldset v-if="draft.requireComplete === 'listed'" class="stack-sm">
          <legend class="label">{{ s.requireCompleteLocales }}</legend>
          <label v-for="l in locales" :key="l.code" class="check">
            <input
              type="checkbox"
              :checked="draft.locales.includes(l.code)"
              :disabled="!canWrite || busy"
              :data-testid="`policy-locale-${l.code}`"
              @change="toggleRequiredLocale(l.code, ($event.target as HTMLInputElement).checked)"
            />
            <span>{{ l.code }}</span>
          </label>
        </fieldset>
        <div class="field narrow">
          <label for="p-fail-on">{{ s.failOn }}</label>
          <select id="p-fail-on" v-model="draft.failOn" :disabled="!canWrite || busy" data-testid="policy-fail-on">
            <option v-for="f in FAIL_ON" :key="f" :value="f">{{ s.failOnName[f] ?? f }}</option>
          </select>
        </div>
        <div class="field narrow">
          <label for="p-missing">{{ s.missingTranslations }}</label>
          <select id="p-missing" v-model="draft.missingTranslations" :disabled="!canWrite || busy" aria-describedby="p-missing-hint" data-testid="policy-missing">
            <option v-for="m in MISSING_TRANSLATIONS" :key="m" :value="m">{{ s.missingName[m] ?? m }}</option>
          </select>
          <span id="p-missing-hint" class="hint">{{ s.missingHint }}</span>
        </div>
      </section>

      <!-- ── the rules, in the order they are read ────────────────── -->
      <section class="stack" aria-labelledby="rules-h">
        <div class="stack-sm">
          <h2 id="rules-h">{{ s.rulesTitle }}</h2>
          <p class="muted">{{ s.rulesLead }}</p>
        </div>
        <p v-if="!draft.rules.length" class="muted" data-testid="policy-no-rules">{{ s.noRules }}</p>
        <ol v-else class="rules" data-testid="policy-rules">
          <PolicyRuleCard
            v-for="(r, i) in draft.rules"
            :key="r.key"
            :ref="(el) => setCard(r.key, el)"
            :rule="r"
            :index="i"
            :total="draft.rules.length"
            :order="order[i]!"
            :locales="locales"
            :disabled="!canWrite || busy"
            @update="patchRule(i, $event)"
            @move="move(i, $event)"
            @remove="removeRuleAt(i)"
          />
        </ol>
        <div>
          <button type="button" class="btn" :disabled="!canWrite || busy" data-testid="policy-add-rule" @click="addRule">{{ s.addRule }}</button>
        </div>
      </section>

      <!-- ── environments ────────────────────────────────────────── -->
      <section class="stack" aria-labelledby="envs-h">
        <div class="stack-sm">
          <h2 id="envs-h">{{ s.environmentsTitle }}</h2>
          <p class="muted">{{ s.environmentsLead }}</p>
        </div>
        <p v-if="!draft.environments.length" class="muted" data-testid="policy-no-environments">{{ s.noEnvironments }}</p>
        <ul v-else class="envs" data-testid="policy-environments">
          <PolicyEnvironmentCard
            v-for="(e, i) in draft.environments"
            :key="e.key"
            :environment="e"
            :locales="locales"
            :disabled="!canWrite || busy"
            :problem="problemAt(`env-${i}`)"
            @update="patchEnvironment(i, $event)"
            @remove="removeEnvironment(i)"
          />
        </ul>
        <div>
          <button type="button" class="btn" :disabled="!canWrite || busy" data-testid="policy-add-env" @click="addEnvironment">{{ s.addEnvironment }}</button>
        </div>
      </section>

      <!-- ── the preview, and only then the save ──────────────────── -->
      <section v-if="canWrite" class="stack" aria-labelledby="save-h">
        <div class="stack-sm">
          <h2 id="save-h">{{ s.saveTitle }}</h2>
          <p v-if="!dirty" class="muted" data-testid="policy-unchanged">{{ s.previewUnchanged }}</p>
          <p v-else-if="!fresh" class="muted" data-testid="policy-preview-needed">{{ s.previewNeeded }}</p>
        </div>

        <div class="field narrow">
          <label for="p-grace">{{ s.graceDays }}</label>
          <div class="row">
            <input
              id="p-grace"
              v-model.number="graceDays"
              type="number"
              min="0"
              max="90"
              step="1"
              class="grace"
              :disabled="busy"
              aria-describedby="p-grace-hint p-grace-error"
              data-testid="policy-grace-days"
            />
            <span aria-hidden="true">{{ s.graceDaysUnit }}</span>
          </div>
          <span id="p-grace-hint" class="hint">{{ s.graceDaysHint }}</span>
          <span id="p-grace-error" class="field-error" role="alert">{{ graceValid ? "" : s.graceDaysRange }}</span>
        </div>

        <div class="row">
          <button type="button" class="btn" :disabled="busy || !dirty || blocked || !graceValid" data-testid="policy-preview" @click="preview">
            {{ busy && !fresh ? s.previewing : s.preview }}
          </button>
          <!--
            The save only exists once a preview of *this* document does.
            It is not a disabled button with an explanation somewhere
            else: until the numbers are on screen there is nothing to
            press.
          -->
          <button v-if="fresh" type="button" class="btn btn-primary" :disabled="busy || blocked || !graceValid" data-testid="policy-save" @click="save">
            {{ busy ? s.saving : s.save(nextVersion) }}
          </button>
          <button v-if="dirty" type="button" class="btn btn-ghost" :disabled="busy" data-testid="policy-discard" @click="discard">{{ s.discard }}</button>
        </div>

        <PolicyImpact v-if="previewed" :impact="previewed.saved.impact" :stale="!fresh" />
      </section>

      <!-- ── how it got to be what it is ─────────────────────────── -->
      <section class="stack-sm" aria-labelledby="history-h">
        <h2 id="history-h">{{ s.historyTitle }}</h2>
        <p class="muted">{{ s.historyLead }}</p>
        <p v-if="!versions.length" class="muted" data-testid="policy-no-history">{{ s.noHistory }}</p>
        <div v-else class="scroll">
          <table class="table" data-testid="policy-history">
            <thead>
              <tr>
                <th scope="col">{{ s.historyColumns.version }}</th>
                <th scope="col">{{ s.historyColumns.document }}</th>
                <th scope="col">{{ s.historyColumns.by }}</th>
                <th scope="col">{{ s.historyColumns.when }}</th>
                <th scope="col"><span class="visually-hidden">{{ s.historyColumns.actions }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="v in versions" :key="v.version" :data-version="v.version" data-testid="policy-version-row">
                <th scope="row">{{ v.version }}</th>
                <td>
                  {{ versionSummary(v) }}
                  <span v-if="v.grace_until" class="muted"> · {{ s.historyGrace(absoluteTime(v.grace_until)) }}</span>
                </td>
                <td>{{ person(v.created_by) }}</td>
                <td><time :datetime="v.created_at" :title="absoluteTime(v.created_at)">{{ relativeTime(v.created_at) }}</time></td>
                <td class="actions">
                  <button type="button" class="btn btn-sm" :disabled="!canWrite || busy" data-testid="policy-load-version" @click="loadVersion(v)">
                    {{ s.historyLoad }}<span class="visually-hidden">{{ s.historyLoadFor(v.version) }}</span>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: var(--kl-content-md);
}
fieldset {
  border: none;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.narrow {
  max-inline-size: 26rem;
}
.grace {
  inline-size: 6rem;
}
.rules,
.envs {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.unsaved {
  align-self: flex-start;
}
.scroll {
  overflow-x: auto;
}
.actions {
  text-align: end;
}
.field-error:empty {
  display: none;
}
</style>
