<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { locales as localesApi } from "../../api/endpoints";
import ErrorAlert from "../../components/ErrorAlert.vue";
import LocaleInput from "../../components/LocaleInput.vue";
import { checkLocale, localeName } from "../../lib/bcp47";
import { toGraph, toRows, validateRows, WILDCARD, type FallbackIssue, type FallbackRow } from "../../lib/fallback";
import { allows } from "../../session/permissions";
import { strings } from "../../strings";
import { useProject } from "./context";

const { tenant, projectId, locales, grant, reloadLocales } = useProject();
const s = strings.locales;
const projectRef = () => ({ tenant: tenant.value, project: projectId.value });
const canManage = computed(() => allows(grant.value, "catalog.write"));

// ── add / remove ────────────────────────────────────────────────────
const code = ref("");
const adding = ref(false);
const listError = ref<unknown>(null);

async function add(): Promise<void> {
  const c = checkLocale(code.value);
  if (!c.ok) return;
  adding.value = true;
  listError.value = null;
  try {
    await localesApi.add(projectRef(), c.tag);
    code.value = "";
    await reloadLocales();
  } catch (e) {
    listError.value = e;
  } finally {
    adding.value = false;
  }
}

async function remove(tag: string): Promise<void> {
  if (!confirm(s.removeConfirm(tag))) return;
  listError.value = null;
  try {
    await localesApi.remove({ ...projectRef(), locale: tag });
    await reloadLocales();
  } catch (e) {
    listError.value = e;
  }
}

// ── fallback graph ──────────────────────────────────────────────────
const rows = ref<FallbackRow[]>([]);
const etag = ref<string>();
const graphError = ref<unknown>(null);
const graphSaved = ref(false);
const saving = ref(false);
const codes = computed(() => locales.value.map((l) => l.code));

async function loadGraph(): Promise<void> {
  try {
    const r = await localesApi.fallback(projectRef());
    rows.value = toRows(r.value.fallback);
    etag.value = r.etag;
  } catch (e) {
    graphError.value = e;
  }
}
watch(projectId, loadGraph, { immediate: true });

const issues = computed(() => validateRows(rows.value, codes.value));
const issuesFor = (row: number) => issues.value.filter((i) => i.row === row);
function issueText(i: FallbackIssue): string {
  return i.code === "fallback_cycle" ? s.issues.fallback_cycle(i.locale, i.path) : s.issues[i.code](i.locale);
}
const unused = (row: FallbackRow) => codes.value.filter((c) => c !== row.from && !row.chain.includes(c));
const fromOptions = (row: FallbackRow) => [WILDCARD, ...codes.value].filter((c) => c === row.from || !rows.value.some((r) => r.from === c));

function addRow(): void {
  const free = [...codes.value, WILDCARD].find((c) => !rows.value.some((r) => r.from === c));
  if (free) rows.value.push({ from: free, chain: [] });
  graphSaved.value = false;
}
function move(row: FallbackRow, i: number, by: -1 | 1): void {
  const j = i + by;
  if (j < 0 || j >= row.chain.length) return;
  [row.chain[i], row.chain[j]] = [row.chain[j]!, row.chain[i]!];
  graphSaved.value = false;
}
function appendTo(row: FallbackRow, e: Event): void {
  const sel = e.target as HTMLSelectElement;
  if (sel.value) row.chain.push(sel.value);
  sel.value = "";
  graphSaved.value = false;
}

async function saveGraph(): Promise<void> {
  if (issues.value.length) return;
  saving.value = true;
  graphError.value = null;
  graphSaved.value = false;
  try {
    const r = await localesApi.putFallback(projectRef(), toGraph(rows.value), etag.value);
    rows.value = toRows(r.value.fallback);
    etag.value = r.etag;
    graphSaved.value = true;
  } catch (e) {
    graphError.value = e;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="page stack">
    <div class="stack-sm">
      <h1>{{ s.title }}</h1>
      <p class="muted">{{ s.lead }}</p>
    </div>

    <section class="card stack">
      <ErrorAlert :error="listError" />
      <table class="table">
        <thead>
          <tr>
            <th scope="col">{{ s.code }}</th>
            <th scope="col">{{ s.name }}</th>
            <th scope="col">{{ s.direction }}</th>
            <th scope="col"><span class="visually-hidden">{{ strings.app.remove }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in locales" :key="l.code">
            <td>
              <code>{{ l.code }}</code>
              <span v-if="l.is_source" class="pill pill-accent src">{{ s.source }}</span>
            </td>
            <td>
              <span :lang="l.code" :dir="l.direction">{{ localeName(l.code, l.code) }}</span>
              <span class="muted"> · {{ localeName(l.code) }}</span>
            </td>
            <td>
              <span class="pill pill-neutral" :title="l.direction">{{ l.direction === "rtl" ? `⇠ ${s.rtl}` : `⇢ ${s.ltr}` }}</span>
            </td>
            <td class="actions">
              <button v-if="canManage && !l.is_source" type="button" class="btn btn-sm btn-ghost" :aria-label="s.removeLabel(l.code)" @click="remove(l.code)">
                {{ strings.app.remove }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <form v-if="canManage" class="add-form" @submit.prevent="add">
        <LocaleInput id="new-locale" v-model="code" :label="s.add" />
        <button type="submit" class="btn btn-primary" :disabled="adding || !checkLocale(code).ok">{{ s.add }}</button>
      </form>
    </section>

    <section class="card stack" aria-labelledby="fallback-h">
      <h2 id="fallback-h">{{ s.fallbackTitle }}</h2>
      <p class="muted">{{ s.fallbackLead }}</p>
      <ErrorAlert :error="graphError" />
      <ol class="rules" role="list">
        <li v-for="(row, ri) in rows" :key="ri" class="rule">
          <div class="rule-line">
            <div class="field">
              <label :for="`from-${ri}`">{{ s.from }}</label>
              <select :id="`from-${ri}`" v-model="row.from" :disabled="!canManage" @change="graphSaved = false">
                <option v-for="c in fromOptions(row)" :key="c" :value="c">{{ c === WILDCARD ? s.everyLocale : c }}</option>
              </select>
            </div>
            <div class="chain" role="group" :aria-label="`${s.chain} (${row.from})`">
              <span class="label">{{ s.chain }}</span>
              <ol class="chain-list">
                <li v-for="(l, li) in row.chain" :key="l" class="chip">
                  <code>{{ l }}</code>
                  <template v-if="canManage">
                    <button type="button" class="btn btn-ghost btn-sm" :aria-label="s.moveUp(l)" :disabled="li === 0" @click="move(row, li, -1)">↑</button>
                    <button type="button" class="btn btn-ghost btn-sm" :aria-label="s.moveDown(l)" :disabled="li === row.chain.length - 1" @click="move(row, li, 1)">↓</button>
                    <button type="button" class="btn btn-ghost btn-sm" :aria-label="s.removeFromChain(l)" @click="row.chain.splice(li, 1); graphSaved = false">×</button>
                  </template>
                </li>
              </ol>
              <select v-if="canManage && unused(row).length" :aria-label="`${s.addToChain} (${row.from})`" @change="appendTo(row, $event)">
                <option value="">{{ s.addToChain }}</option>
                <option v-for="c in unused(row)" :key="c" :value="c">{{ c }}</option>
              </select>
            </div>
            <button v-if="canManage" type="button" class="btn btn-ghost btn-sm" :aria-label="s.removeRow(row.from)" @click="rows.splice(ri, 1); graphSaved = false">
              {{ strings.app.remove }}
            </button>
          </div>
          <ul v-if="issuesFor(ri).length" class="issues">
            <li v-for="(i, ii) in issuesFor(ri)" :key="ii" class="field-error">{{ issueText(i) }}</li>
          </ul>
        </li>
      </ol>
      <div v-if="canManage" class="row">
        <button type="button" class="btn" :disabled="rows.length > codes.length" @click="addRow">{{ s.addRow }}</button>
        <button type="button" class="btn btn-primary" :disabled="saving || issues.length > 0" @click="saveGraph">{{ s.saveFallback }}</button>
        <span v-if="graphSaved" class="pill pill-ok" role="status">{{ s.fallbackSaved }}</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.src {
  margin-inline-start: var(--kl-space-2);
}
.actions {
  text-align: end;
}
.add-form {
  display: flex;
  gap: var(--kl-space-3);
  align-items: flex-start;
}
.add-form .field {
  flex: 1;
  max-inline-size: 28rem;
}
.add-form .btn {
  margin-block-start: 1.45rem;
}
.rules {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.rule {
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  padding: var(--kl-space-3);
}
.rule-line {
  display: flex;
  gap: var(--kl-space-4);
  align-items: flex-end;
  flex-wrap: wrap;
}
.chain {
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
  flex: 1;
}
.chain-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  gap: var(--kl-space-2);
  flex-wrap: wrap;
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding-inline-start: var(--kl-space-2);
  border: 1px solid var(--kl-border-strong);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
}
.issues {
  margin: var(--kl-space-2) 0 0;
  padding-inline-start: var(--kl-space-5);
}
</style>
