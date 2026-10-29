<script setup lang="ts">
/**
 * The per-locale quality row (RFC 0005 §8): coverage, open errors and
 * queue age for each language, and — the part intent §41 asks for —
 * which checks can run for it at all.
 *
 * Each number is missing rather than zero when nobody measured it, and
 * the layer chips below the numbers keep "cannot run here" and "ran,
 * found nothing" apart. The legend that explains the three non-clean
 * states is printed once, above the locales, rather than repeated under
 * each of them.
 */
import { computed } from "vue";
import type { LocaleHealth } from "../../api/quality-summary-schemas";
import { healthLocales, localeLayers } from "../../lib/health";
import { strings } from "../../strings";
import HealthStat from "./HealthStat.vue";
import LayerAvailability from "./LayerAvailability.vue";
import { localeStats } from "./locale-stats";

const props = defineProps<{
  locales: LocaleHealth[];
  /** A summary was read at all. False: the numbers are unknown, not zero. */
  measured: boolean;
}>();
const s = strings.health;

const rows = computed(() =>
  healthLocales(props.locales).map((l) => ({
    locale: l,
    layers: localeLayers(l),
    stats: localeStats(l, props.measured),
  })),
);
</script>

<template>
  <section class="stack-sm" aria-labelledby="locale-health-h" data-testid="locale-health">
    <h2 id="locale-health-h">{{ s.localesTitle }}</h2>
    <p class="muted">{{ s.localesLead }}</p>
    <p v-if="!rows.length" class="muted" data-testid="locale-health-empty">{{ s.localesNone }}</p>
    <template v-else>
      <p class="hint legend">{{ s.layersLegend }}</p>
      <ul class="locales" role="list">
        <li v-for="r in rows" :key="r.locale.code" class="card stack-sm locale" :data-locale="r.locale.code" data-testid="locale-row">
          <h3 class="locale-head">
            <code>{{ r.locale.code }}</code>
            <span v-if="r.locale.is_source" class="pill pill-neutral">{{ s.localeSource }}</span>
          </h3>
          <dl class="stats">
            <HealthStat
              v-for="stat in r.stats"
              :key="stat.key"
              :label="stat.label"
              :value="stat.value"
              :detail="stat.detail"
              :tone="stat.tone"
              :testid="`locale-${stat.key}`"
            />
          </dl>
          <LayerAvailability :locale="r.locale.code" :layers="r.layers" />
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.legend {
  max-inline-size: var(--kl-content-md);
}
.locales {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-4);
}
.locale-head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
  font-size: var(--kl-text-md);
}
.stats {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr));
  gap: var(--kl-space-4);
}
</style>
