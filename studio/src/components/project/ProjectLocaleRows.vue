<script setup lang="ts">
/**
 * A project card's per-locale row (RFC 0005 §8): coverage, open errors
 * and queue age, one line per language, so the projects list answers
 * "where is the work" without opening anything.
 *
 * It shares `localeStats` with the quality view's per-locale cards, so
 * the two surfaces can never word the same number differently. A cell
 * with nothing behind it reads "Not measured" — the projects list is
 * exactly where a confident zero would do the most damage, because it
 * is the screen people scan rather than read.
 */
import { computed } from "vue";
import type { QualitySummary } from "../../api/quality-summary-schemas";
import { healthLocales } from "../../lib/health";
import { localeStats } from "../quality/locale-stats";
import { strings } from "../../strings";

const props = defineProps<{
  project: string;
  /** The project's summary, or nothing when it is still loading or could not be read. */
  summary: QualitySummary | undefined;
  state: "loading" | "ready" | "unavailable";
}>();
const s = strings.health;

const TONE: Record<string, string> = { ok: "pill-ok", warn: "pill-warn", err: "pill-err", neutral: "pill-neutral" };

const rows = computed(() =>
  healthLocales(props.summary?.locales ?? []).map((l) => ({
    code: l.code,
    isSource: l.is_source,
    stats: localeStats(l, true),
  })),
);
</script>

<template>
  <div :data-testid="`project-locales-${project}`" :data-state="state">
    <p v-if="state === 'loading'" class="hint" role="status">{{ s.loading }}</p>
    <p v-else-if="state === 'unavailable'" class="hint" data-testid="project-locales-unavailable">{{ s.notMeasured }}</p>
    <p v-else-if="!rows.length" class="hint">{{ s.localesNone }}</p>
    <!--
      The table scrolls sideways on a narrow card, so it is focusable and
      named: a scrollable region nobody can reach with a keyboard is a
      WCAG 2.1.1 failure, and axe fails the build over it.
    -->
    <div v-else class="locale-rows" role="group" :aria-label="s.localesTitle" tabindex="0">
      <table class="table small" data-testid="project-locale-table">
        <caption class="visually-hidden">{{ s.localesLead }}</caption>
        <thead>
          <tr>
            <th scope="col">{{ strings.quality.locale }}</th>
            <th scope="col">{{ s.coverage }}</th>
            <th scope="col">{{ s.findings }}</th>
            <th scope="col">{{ s.queueAgeLabel }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="r.code" :data-locale="r.code" data-testid="project-locale-row">
            <th scope="row" class="mono">
              {{ r.code }}<span v-if="r.isSource" class="visually-hidden">{{ " " + s.localeSource }}</span>
            </th>
            <td v-for="stat in r.stats" :key="stat.key" :data-stat="stat.key">
              <span class="pill" :class="TONE[stat.tone]">{{ stat.value ?? s.notMeasured }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.locale-rows {
  inline-size: 100%;
  overflow-x: auto;
}
.locale-rows:focus-visible {
  outline: 2px solid var(--kl-accent);
  outline-offset: 2px;
}
.table.small th,
.table.small td {
  padding: var(--kl-space-2);
  font-size: var(--kl-text-sm);
}
.table tr:last-child th,
.table tr:last-child td {
  border-block-end: 0;
}
</style>
