<script setup lang="ts">
/**
 * The translator workspace: a filterable, virtualised message list on the
 * left, the editor for the selected message in the target locale on the
 * right. Filters, the locale and the selection live in the URL, so a view
 * can be bookmarked or shared.
 *
 * With `?assignment=<id>` it is scoped to one assignment's units (RFC 0006
 * §3.1, opened from My work): only those messages, only its locales.
 */
import { computed, onBeforeUnmount, ref, shallowRef, triggerRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { applications as applicationsApi, messages as messagesApi, translations as translationsApi, type MessageFilters } from "../../api/endpoints";
import type { Application, LocaleStats, Message, Syntax, Translation } from "../../api/schemas";
import { useWork } from "../../api/work";
import type { Assignment } from "../../api/work-schemas";
import AssistPanel from "../../components/assist/AssistPanel.vue";
import FillDialog from "../../components/assist/FillDialog.vue";
import type { MatchText } from "../../components/assist/TmMatches.vue";
import { useTerminology } from "../../components/assist/useTerminology";
import ContextFilters from "../../components/context/ContextFilters.vue";
import { isContextFiltered, useContextFilter, type ContextFilterState } from "../../components/context/useContextFilter";
import ErrorAlert from "../../components/ErrorAlert.vue";
import MessageList from "../../components/MessageList.vue";
import TranslationEditor from "../../components/TranslationEditor.vue";
import { localeName } from "../../lib/bcp47";
import { getPref, setPref } from "../../lib/prefs";
import { matchNumber, useShortcuts } from "../../lib/shortcuts";
import { unitLocales } from "../../lib/work";
import { coverageOf, matches, namespacesOf, statusOf, type Coverage, type CoverageFilter, type MessageRow } from "../../lib/workspace";
import { allows, allowsFor } from "../../session/permissions";
import { useSession } from "../../session/session";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const route = useRoute();
const router = useRouter();
const { tenant, projectId, project, locales, targets, grant } = useProject();
const { person } = useSession();
const s = strings.workspace;

// ── URL state ───────────────────────────────────────────────────────
const q = (name: string) => (typeof route.query[name] === "string" ? (route.query[name] as string) : "");
function setQuery(patch: Record<string, string | undefined>): void {
  const query = { ...route.query, ...patch };
  for (const [k, v] of Object.entries(query)) if (v === undefined || v === "") delete query[k];
  void router.replace({ query });
}

// ── assignment scope (RFC 0006 §3.1) ──────────────────────────────────
const work = useWork();
const assignmentId = computed(() => q("assignment"));
type Scope = { phase: "none" } | { phase: "loading" } | { phase: "ready"; assignment: Assignment } | { phase: "failed"; error: unknown };
const scope = shallowRef<Scope>({ phase: "none" });
watch(
  assignmentId,
  async (id) => {
    if (!id) {
      scope.value = { phase: "none" };
      return;
    }
    scope.value = { phase: "loading" };
    try {
      const a = await work.assignment(tenant.value, id);
      if (id === assignmentId.value) scope.value = { phase: "ready", assignment: a };
    } catch (error) {
      if (id === assignmentId.value) scope.value = { phase: "failed", error };
    }
  },
  { immediate: true },
);
const scoped = computed(() => scope.value.phase !== "none");
const scopeAssignment = computed(() => (scope.value.phase === "ready" ? scope.value.assignment : undefined));
const scopeError = computed(() => (scope.value.phase === "failed" ? problemText(scope.value.error) : ""));
/** The target locales on offer: an assignment's own, when scoped to one. */
const localeTargets = computed(() => {
  const a = scopeAssignment.value;
  if (!a) return targets.value;
  const mine = new Set(unitLocales(a));
  return targets.value.filter((l) => mine.has(l.code));
});

const localeCode = computed(() => {
  const want = q("locale") || getPref(`locale:${projectId.value}`);
  return localeTargets.value.find((l) => l.code === want)?.code ?? localeTargets.value[0]?.code;
});
/** The assignment's messages in the target locale; `undefined` when not scoped. */
const scopeMessages = computed(() => {
  const a = scopeAssignment.value;
  if (!a) return undefined;
  return new Set(a.units.filter((u) => u.locale === localeCode.value).map((u) => u.message_id));
});
watch(localeCode, (code) => code && setPref(`locale:${projectId.value}`, code), { immediate: true });
const target = computed(() => locales.value.find((l) => l.code === localeCode.value));
const source = computed(() => locales.value.find((l) => l.is_source));
const namespace = computed(() => q("ns"));
const coverage = computed<CoverageFilter>(() => (["missing", "outdated"].includes(q("show")) ? (q("show") as CoverageFilter) : "all"));
const state = computed(() => (["obsolete", "proposed"].includes(q("state")) ? (q("state") as "obsolete" | "proposed") : q("state") === "all" ? "all" : "active"));
/**
 * Where a message appears (RFC 0004 §3.4). The branch filter rides on
 * the `proposed` state until the branch switcher arrives; `ctx` carries
 * the coverage filters.
 */
const contextFilter = computed<ContextFilterState>(() => ({
  route: q("route"),
  component: q("component"),
  file: q("file"),
  only: q("ctx") === "unused" ? "unused" : q("ctx") === "uncaptured" ? "uncaptured" : "",
}));
function setContextFilter(f: ContextFilterState): void {
  setQuery({ route: f.route || undefined, component: f.component || undefined, file: f.file || undefined, ctx: f.only || undefined });
}
const search = ref(q("q"));
watch(search, (v) => setQuery({ q: v || undefined }));

// ── messages ────────────────────────────────────────────────────────
const loaded = shallowRef<Message[]>([]);
const done = ref(false);
const loadError = ref<unknown>(null);
/** The target locale's coverage by message ID, for the row badges. */
const localeCoverage = shallowRef<Coverage>();
/** The target locale's counts (translation-stats). */
const localeStats = shallowRef<LocaleStats>();
const seenNamespaces = ref<string[]>([]);
let controller: AbortController | undefined;

const filters = computed<MessageFilters>(() => ({
  namespace: namespace.value || undefined,
  state: state.value === "all" ? undefined : state.value,
  missing_in: coverage.value === "missing" ? localeCode.value : undefined,
  outdated_in: coverage.value === "outdated" ? localeCode.value : undefined,
}));

async function pages(f: MessageFilters, signal: AbortSignal, onPage: (items: Message[]) => void): Promise<void> {
  let token: string | undefined;
  do {
    const page = await messagesApi.page({ tenant: tenant.value, project: projectId.value }, f, token, signal);
    if (signal.aborted) return;
    onPage(page.items);
    token = page.next_page_token;
  } while (token);
}

async function loadMessages(): Promise<void> {
  controller?.abort();
  const c = (controller = new AbortController());
  loaded.value = [];
  done.value = false;
  loadError.value = null;
  try {
    await pages(filters.value, c.signal, (items) => {
      loaded.value.push(...items);
      triggerRef(loaded);
    });
    if (!c.signal.aborted) done.value = true;
  } catch (e) {
    if (!c.signal.aborted) loadError.value = e;
  }
}

let statusController: AbortController | undefined;

/**
 * The row badges and counts for the target locale: its translations in
 * one bulk listing (a page per request, whatever the list's filters) and
 * the server's per-locale stats.
 */
async function loadStatus(): Promise<void> {
  statusController?.abort();
  const c = (statusController = new AbortController());
  localeCoverage.value = undefined;
  localeStats.value = undefined;
  const code = localeCode.value;
  if (!code) return;
  const p = { tenant: tenant.value, project: projectId.value };
  const translations = async () => {
    const items = [];
    let token: string | undefined;
    do {
      const page = await translationsApi.projectPage(p, code, token, c.signal);
      items.push(...page.items);
      token = page.next_page_token;
    } while (token && !c.signal.aborted);
    return coverageOf(items);
  };
  const [cov, stats] = await Promise.allSettled([translations(), loadStats(c.signal)]);
  if (c.signal.aborted || code !== localeCode.value) return;
  // Badges and counts are a convenience; the list works without them.
  if (cov.status === "fulfilled") localeCoverage.value = cov.value;
  if (stats.status === "fulfilled") localeStats.value = stats.value;
}

async function loadStats(signal?: AbortSignal): Promise<LocaleStats | undefined> {
  const code = localeCode.value;
  const st = await translationsApi.stats({ tenant: tenant.value, project: projectId.value }, signal);
  return st.locales.find((l) => l.code === code);
}

watch(
  [filters, projectId],
  async () => {
    await loadMessages();
  },
  { immediate: true },
);
watch([localeCode, projectId], () => void loadStatus(), { immediate: true });
// The applications name themselves in "Where it appears"; their IDs do until they load.
const apps = shallowRef<Application[]>([]);
watch(
  projectId,
  async (id) => {
    if (!id) return;
    try {
      apps.value = await applicationsApi.list({ tenant: tenant.value, project: id });
    } catch {
      apps.value = [];
    }
  },
  { immediate: true },
);
watch(loaded, (list) => {
  const merged = namespacesOf(list, seenNamespaces.value);
  if (merged.length !== seenNamespaces.value.length) seenNamespaces.value = merged;
});
onBeforeUnmount(() => {
  controller?.abort();
  statusController?.abort();
});

const context = useContextFilter({ tenant, projectId, filter: contextFilter });
/** Whether anything narrows the list: an empty list then means "nothing matches", not "no messages". */
const narrowed = computed(
  () => scoped.value || !!search.value.trim() || !!namespace.value || coverage.value !== "all" || state.value !== "active" || isContextFiltered(contextFilter.value),
);
/** A context filter is on, but which messages it allows isn't known yet. */
const contextPending = computed(() => isContextFiltered(contextFilter.value) && context.allowed.value === undefined);
const visible = computed(() => {
  // Scoped, but the assignment is not known (yet, or at all): show none rather than every message.
  if (contextPending.value || (scoped.value && !scopeMessages.value)) return [];
  const units = scopeMessages.value;
  return loaded.value.filter(
    (m) => (!units || units.has(m.id)) && matches(m, search.value) && (!context.allowed.value || context.allowed.value.has(m.id)),
  );
});
const rows = computed<MessageRow[]>(() =>
  visible.value.map((m) => ({ id: m.id, key: m.key, text: m.source.text, namespace: m.namespace, status: statusOf(m.id, localeCoverage.value) })),
);

// ── selection ───────────────────────────────────────────────────────
const selectedKey = computed(() => q("key"));
const activeIndex = computed(() => visible.value.findIndex((m) => m.key === selectedKey.value));
const selected = computed(() => visible.value[activeIndex.value]);
watch(
  [visible, selectedKey],
  ([list]) => {
    // Keep something selected so the editor is never empty while there are messages.
    if (list.length && activeIndex.value < 0) setQuery({ key: list[0]!.key });
  },
  { immediate: true },
);

function select(index: number): void {
  const m = visible.value[Math.max(0, Math.min(index, visible.value.length - 1))];
  if (m) setQuery({ key: m.key });
}

function onChanged(t: Translation): void {
  const cov = localeCoverage.value;
  if (cov) {
    if (t.state === "rejected") cov.translated.delete(t.message_id);
    else cov.translated.add(t.message_id);
    if (t.outdated && t.state !== "rejected") cov.outdated.add(t.message_id);
    else cov.outdated.delete(t.message_id);
    triggerRef(localeCoverage);
  }
  void loadStats()
    .then((st) => (localeStats.value = st))
    .catch(() => undefined);
}

// ── knowledge & AI ──────────────────────────────────────────────────
const draft = ref<{ text: string; syntax: Syntax }>({ text: "", syntax: "mf1" });
const terminology = useTerminology({
  tenant,
  projectId,
  message: selected,
  sourceLocale: computed(() => source.value?.code),
  targetLocale: localeCode,
  draft,
});
const assist = useTemplateRef<InstanceType<typeof AssistPanel>>("assist");
/** The unit's workflow instances (RFC 0006 §8) are offered to whoever may read workflows. */
const canReadWorkflows = computed(() => allows(grant.value, "workflows.read"));
const canFill = computed(() => !!localeCode.value && allowsFor(grant.value, "intelligence.translate", localeCode.value));
const fillOpen = ref(false);
/** A search narrows the fill to the keys it shows (the server takes at most 500). */
const fillKeys = computed(() => (search.value.trim() ? visible.value.slice(0, 500).map((m) => m.key) : undefined));

function insert(m: MatchText): void {
  editor.value?.setDraft(m.text, m.syntax);
}
function onAccepted(): void {
  void editor.value?.reload();
}
function onFillSettled(): void {
  void loadStatus();
  assist.value?.refresh();
}

// ── keyboard ────────────────────────────────────────────────────────
const list = useTemplateRef<InstanceType<typeof MessageList>>("list");
const editor = useTemplateRef<InstanceType<typeof TranslationEditor>>("editor");
const searchBox = useTemplateRef<HTMLInputElement>("searchBox");

/** j/k move through the list and put focus there, so Enter then edits. */
function step(by: 1 | -1): void {
  select(activeIndex.value + by);
  list.value?.focus();
}

useShortcuts({
  next: () => step(1),
  prev: () => step(-1),
  search: () => searchBox.value?.focus(),
  edit: () => editor.value?.focusEditor(),
  leave: () => {
    if (!editor.value?.isEditing()) return false;
    editor.value.blurEditor();
    list.value?.focus();
  },
  save: () => void editor.value?.save(false),
  saveApprove: () => void editor.value?.save(true),
  insertMatch: (e) => {
    const m = assist.value?.matchTarget(matchNumber(e) ?? 0);
    if (m === undefined) return false;
    insert(m);
  },
  editSuggestion: () => void assist.value?.editSuggestion(),
  acceptSuggestion: () => void assist.value?.acceptSuggestion(),
});

function onSearchKey(e: KeyboardEvent): void {
  if (e.key === "Enter" || e.key === "ArrowDown") {
    e.preventDefault();
    list.value?.focus();
  }
  if (e.key === "Escape" && search.value) {
    e.preventDefault();
    search.value = "";
  }
}
</script>

<template>
  <div v-if="!targets.length" class="page">
    <div class="card stack">
      <p>{{ s.noTargets }}</p>
      <div>
        <RouterLink class="btn btn-primary" :to="{ name: 'locales', params: { tenant, project: projectId } }">{{ s.addLocales }}</RouterLink>
      </div>
    </div>
  </div>
  <div v-else class="workspace">
    <aside class="side" :aria-label="s.messages">
      <div v-if="scoped" class="scope" data-testid="assignment-scope" :data-phase="scope.phase">
        <p v-if="scope.phase === 'loading'" class="muted">{{ strings.work.scopeLoading }}</p>
        <div v-else-if="scope.phase === 'failed'" class="alert alert-error" role="alert">
          <p>{{ strings.work.scopeFailed }}</p>
          <p>{{ scopeError }}</p>
        </div>
        <p v-else-if="scopeAssignment" class="scope-title">
          {{ strings.work.scoped(scopeAssignment.units.length, unitLocales(scopeAssignment).join(", ")) }}
          · <span data-testid="assignment-scope-state">{{ strings.work.state[scopeAssignment.state] }}</span>
        </p>
        <div class="row">
          <RouterLink :to="{ name: 'my-work', params: { tenant } }">{{ strings.work.back }}</RouterLink>
          <RouterLink :to="{ query: { ...route.query, assignment: undefined, key: undefined } }">{{ strings.work.showAll }}</RouterLink>
        </div>
      </div>
      <div class="filters" role="search">
        <div class="field">
          <label for="ws-locale">{{ s.targetLocale }}</label>
          <select id="ws-locale" :value="localeCode" @change="setQuery({ locale: ($event.target as HTMLSelectElement).value })">
            <option v-for="l in localeTargets" :key="l.code" :value="l.code">{{ l.code }} — {{ localeName(l.code) }}</option>
          </select>
        </div>
        <div class="field">
          <label for="ws-search">{{ s.search }} <kbd aria-hidden="true">/</kbd></label>
          <input id="ws-search" ref="searchBox" v-model="search" type="search" autocomplete="off" spellcheck="false" @keydown="onSearchKey" />
        </div>
        <div class="filter-row">
          <div class="field">
            <label for="ws-show">{{ s.coverage }}</label>
            <select id="ws-show" :value="coverage" @change="setQuery({ show: ($event.target as HTMLSelectElement).value === 'all' ? undefined : ($event.target as HTMLSelectElement).value })">
              <option value="all">{{ s.coverageAll }}</option>
              <option value="missing">{{ s.coverageMissing(localeCode ?? "") }}{{ localeStats ? ` (${localeStats.missing.toLocaleString()})` : "" }}</option>
              <option value="outdated">{{ s.coverageOutdated(localeCode ?? "") }}{{ localeStats ? ` (${localeStats.outdated.toLocaleString()})` : "" }}</option>
            </select>
          </div>
          <div class="field">
            <label for="ws-ns">{{ s.namespace }}</label>
            <select id="ws-ns" :value="namespace" @change="setQuery({ ns: ($event.target as HTMLSelectElement).value || undefined })">
              <option value="">{{ s.allNamespaces }}</option>
              <option v-for="n in seenNamespaces" :key="n" :value="n">{{ n }}</option>
            </select>
          </div>
          <div class="field">
            <label for="ws-state">{{ s.state }}</label>
            <select id="ws-state" :value="state" @change="setQuery({ state: ($event.target as HTMLSelectElement).value === 'active' ? undefined : ($event.target as HTMLSelectElement).value })">
              <option value="active">{{ s.stateActive }}</option>
              <option value="proposed">{{ s.stateProposedFilter }}</option>
              <option value="obsolete">{{ s.stateObsolete }}</option>
              <option value="all">{{ s.stateAll }}</option>
            </select>
          </div>
        </div>
        <ContextFilters
          :filter="contextFilter"
          :index="context.index.value"
          :loading="context.loadingIndex.value"
          :index-error="context.indexError.value"
          :error="context.error.value"
          :probe="context.probe.value"
          :matched="contextPending ? undefined : visible.length"
          @update:filter="setContextFilter"
          @open="context.loadIndex()"
        />
        <p class="count" role="status">{{ s.count(visible.length, loaded.length, done) }}</p>
        <p v-if="localeStats" class="count" data-testid="locale-stats">
          {{ s.localeStats(localeStats.code, localeStats.translated, localeStats.translated + localeStats.missing, localeStats.outdated, localeStats.states.needs_review) }}
        </p>
        <div v-if="canFill" class="row">
          <button type="button" class="btn btn-sm" @click="fillOpen = true">{{ strings.fill.button }}</button>
          <RouterLink class="hint" :to="{ name: 'review', params: { tenant, project: projectId }, query: localeCode ? { locale: localeCode } : {} }">{{ strings.fill.queueLink }}</RouterLink>
        </div>
      </div>
      <ErrorAlert :error="loadError" />
      <MessageList
        v-if="rows.length"
        ref="list"
        class="messages"
        :rows="rows"
        :active="activeIndex"
        :label="s.messages"
        :source-lang="source?.code ?? 'und'"
        :source-dir="source?.direction ?? 'auto'"
        @select="select"
      />
      <p v-else-if="scope.phase === 'loading'" class="empty muted">{{ strings.app.loading }}</p>
      <!-- A failed scope says so above; the list is not "empty", it is unknown. -->
      <template v-else-if="scope.phase === 'failed'" />
      <p v-else-if="contextPending" class="empty muted">{{ strings.app.loading }}</p>
      <p v-else-if="done" class="empty muted">{{ loaded.length || narrowed ? s.empty : s.noMessages }}</p>
      <p v-else class="empty muted">{{ strings.app.loading }}</p>
    </aside>

    <div class="work">
      <section class="main" :aria-label="s.target">
        <TranslationEditor
          v-if="selected && target && source && project"
          ref="editor"
          :key="`${selected.key}:${target.code}`"
          :tenant="tenant"
          :project="project"
          :message="selected"
          :locale="target"
          :source="source"
          :grant="grant"
          :self-id="person?.id"
          :term-hits="terminology.recognition.value"
          :term-findings="terminology.findings.value"
          @changed="onChanged"
          @draft="draft = $event"
        >
          <template v-if="canReadWorkflows" #history-extra>
            <p>
              <RouterLink
                :to="{ name: 'workflow-instances', params: { tenant, project: projectId }, query: { message: selected.key, locale: target.code } }"
                data-testid="workflow-instances-link"
              >
                {{ strings.instances.fromTranslation }}
              </RouterLink>
            </p>
          </template>
        </TranslationEditor>
        <p v-else class="page muted">{{ s.selectMessage }}</p>
      </section>
      <aside v-if="selected && target && source" class="assist-col" :aria-label="strings.assist.label">
        <AssistPanel
          ref="assist"
          :tenant="tenant"
          :project-id="projectId"
          :message="selected"
          :source="source"
          :target="target"
          :grant="grant"
          :recognition="terminology.recognition.value"
          :recognition-error="terminology.recognitionError.value"
          :findings="terminology.findings.value"
          :check-state="terminology.checkState.value"
          :has-draft="draft.text.trim() !== ''"
          :target-syntax="draft.syntax"
          :applications="apps"
          @insert="insert"
          @accepted="onAccepted"
        />
      </aside>
    </div>
    <FillDialog
      v-if="localeCode"
      :open="fillOpen"
      :tenant="tenant"
      :project-id="projectId"
      :locale="localeCode"
      :namespace="namespace || undefined"
      :select="coverage === 'outdated' ? 'outdated' : 'missing'"
      :keys="fillKeys"
      @close="fillOpen = false"
      @settled="onFillSettled"
    />
  </div>
</template>

<style scoped>
.workspace {
  flex: 1;
  display: grid;
  grid-template-columns: minmax(20rem, 26rem) 1fr;
  min-block-size: 0;
  block-size: calc(100vh - var(--gs-topbar-h) - 3.1rem);
}
.side {
  display: flex;
  flex-direction: column;
  border-inline-end: 1px solid var(--kl-border);
  min-block-size: 0;
  background: var(--kl-surface-raised);
}
.scope {
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-2);
  padding: var(--kl-space-3) var(--kl-space-4);
  border-block-end: 1px solid var(--kl-border);
  background: var(--kl-accent-dim);
  font-size: var(--kl-text-sm);
}
.scope-title {
  font-weight: var(--kl-weight-medium);
}
.filters {
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
  padding: var(--kl-space-4);
  border-block-end: 1px solid var(--kl-border);
}
.filter-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--kl-space-2);
}
.filter-row > :first-child {
  grid-column: 1 / -1;
}
.filter-row select {
  min-inline-size: 0;
  inline-size: 100%;
}
.count {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
}
.messages {
  flex: 1;
  min-block-size: 0;
}
.empty {
  padding: var(--kl-space-4);
}
.work {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(18rem, 24rem);
  grid-template-rows: minmax(0, 1fr);
  min-block-size: 0;
  min-inline-size: 0;
}
.main {
  overflow-y: auto;
  min-block-size: 0;
}
.assist-col {
  overflow-y: auto;
  min-block-size: 0;
  border-inline-start: 1px solid var(--kl-border);
  background: var(--kl-surface-raised);
}
@media (max-width: 80rem) {
  /* Narrower: the knowledge panes follow the editor in one scrolling column. */
  .work {
    display: block;
    overflow-y: auto;
  }
  .main {
    overflow-y: visible;
  }
  .assist-col {
    overflow-y: visible;
    border-inline-start: none;
    border-block-start: 1px solid var(--kl-border);
  }
}
@media (max-width: 60rem) {
  .workspace {
    grid-template-columns: 18rem 1fr;
  }
  .filter-row {
    grid-template-columns: 1fr;
  }
}
</style>
