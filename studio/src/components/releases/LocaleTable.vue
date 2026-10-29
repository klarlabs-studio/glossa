<script setup lang="ts">
/**
 * Per-locale figures of a release: what it ships (counts) and/or what
 * changed against another release (diff), one row per locale.
 */
import { computed } from "vue";
import type { ReleaseCounts, ReleaseDiff } from "../../api/schemas";
import { localeTotals } from "../../lib/releases";
import { strings } from "../../strings";

const props = defineProps<{
  locales: string[];
  sourceLocale?: string | undefined;
  counts?: ReleaseCounts["locales"] | undefined;
  diff?: ReleaseDiff | undefined;
  caption: string;
}>();
const s = strings.releases;
const c = s.localeColumns;

const rows = computed(() => {
  const codes = [...props.locales];
  for (const d of props.diff?.locales ?? []) if (!codes.includes(d.locale)) codes.push(d.locale);
  return codes.map((code) => {
    const d = props.diff?.locales.find((x) => x.locale === code);
    return { code, counts: props.counts?.[code], diff: d, totals: d ? localeTotals(d) : undefined };
  });
});
const withIds = computed(() => rows.value.filter((r) => r.totals && r.totals.added + r.totals.changed + r.totals.removed > 0));
const n = (v: number | undefined) => (v === undefined ? "—" : v.toLocaleString());
</script>

<template>
  <div class="stack-sm">
    <div class="scroll">
      <table class="table">
        <caption class="visually-hidden">{{ caption }}</caption>
        <thead>
          <tr>
            <th scope="col">{{ c.locale }}</th>
            <template v-if="counts">
              <th scope="col" class="num">{{ c.messages }}</th>
              <th scope="col" class="num">{{ c.outdated }}</th>
            </template>
            <template v-if="diff">
              <th scope="col" class="num">{{ c.added }}</th>
              <th scope="col" class="num">{{ c.changed }}</th>
              <th scope="col" class="num">{{ c.removed }}</th>
            </template>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="r.code" :data-locale="r.code">
            <th scope="row">
              <code>{{ r.code }}</code>
              <span v-if="r.code === sourceLocale" class="muted"> · {{ s.sourceLocale }}</span>
            </th>
            <template v-if="counts">
              <td class="num">{{ r.counts ? n(r.counts.messages) : s.notInRelease }}</td>
              <td class="num">{{ n(r.counts?.outdated) }}</td>
            </template>
            <template v-if="diff">
              <td class="num" :class="{ add: r.totals?.added }">{{ r.totals?.added ? `+${r.totals.added}` : n(r.totals?.added) }}</td>
              <td class="num">{{ n(r.totals?.changed) }}</td>
              <td class="num" :class="{ del: r.totals?.removed }">{{ r.totals?.removed ? `−${r.totals.removed}` : n(r.totals?.removed) }}</td>
            </template>
          </tr>
        </tbody>
      </table>
    </div>
    <details v-for="r in withIds" :key="r.code" class="ids">
      <summary>
        <code>{{ r.code }}</code>: {{ s.showIds((r.totals?.added ?? 0) + (r.totals?.changed ?? 0) + (r.totals?.removed ?? 0)) }}
      </summary>
      <dl>
        <template v-for="kind in (['added', 'changed', 'removed'] as const)" :key="kind">
          <template v-if="r.diff?.[kind].length">
            <dt>{{ c[kind] }}</dt>
            <dd><code v-for="id in r.diff[kind]" :key="id" class="id">{{ id }}</code></dd>
          </template>
        </template>
      </dl>
    </details>
  </div>
</template>

<style scoped>
.scroll {
  overflow-x: auto;
}
.num {
  text-align: end;
  font-variant-numeric: tabular-nums;
}
.table th[scope="row"] {
  font-weight: var(--kl-weight-medium);
}
.add {
  color: var(--gs-ok);
}
.del {
  color: var(--gs-err);
}
.ids summary {
  cursor: pointer;
}
.ids dl {
  margin: var(--kl-space-2) 0 0;
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-2) var(--kl-space-4);
}
.ids dd {
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
}
</style>
