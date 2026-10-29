<script setup lang="ts">
/**
 * One AI suggestion: its text, confidence as a band and a number (never
 * as certainty), the routed action, risks, and "Why?" — each confidence
 * factor with its contribution, and the provenance: provider, model,
 * prompt version, the TM units, terms and style version it used.
 * Shared by the editor's suggestion panel and the review queue.
 */
import { computed } from "vue";
import type { AISuggestion, UnitAISuggestion } from "../../api/intelligence-schemas";
import { actionTone, bandOf, bandTone, byImpact, formatContribution, formatScore } from "../../lib/confidence";
import { formatUSD } from "../../lib/money";
import { strings } from "../../strings";

/**
 * A unit's suggestion (an assigned member's view) has no provenance or
 * cost to show: the "Why?" holds the factors alone.
 */
const props = defineProps<{ suggestion: AISuggestion | UnitAISuggestion; lang: string; dir: string; headingLevel?: 3 | 4 }>();
const s = strings.ai;

const band = computed(() => bandOf(props.suggestion.score));
const factors = computed(() => byImpact(props.suggestion.explanation));
const full = computed(() => ("provenance" in props.suggestion ? props.suggestion : undefined));
const tokens = computed(() => (full.value ? full.value.usage.input_tokens + full.value.usage.output_tokens : 0));
</script>

<template>
  <div class="stack-sm suggestion" data-testid="suggestion">
    <div class="row">
      <span class="pill" :class="`pill-${bandTone(band)}`" data-testid="confidence-band">{{ s.band[band] }}</span>
      <span class="score" data-testid="confidence-score">{{ s.score(formatScore(suggestion.score)) }}</span>
      <span class="pill" :class="`pill-${actionTone(suggestion.action)}`" data-testid="suggestion-action">{{ s.action[suggestion.action] }}</span>
    </div>
    <p class="hint">{{ s.notCertainty }}</p>
    <p class="text" :lang="lang" :dir="dir" data-testid="suggestion-text">{{ suggestion.message }}</p>
    <p v-if="suggestion.action_note" class="alert alert-warn">{{ suggestion.action_note }}</p>
    <ul v-if="suggestion.risk_tags.length" class="tags" :aria-label="s.risks">
      <li v-for="t in suggestion.risk_tags" :key="t" class="pill pill-warn">{{ s.riskTag(t) }}</li>
    </ul>
    <ul v-if="suggestion.term_findings.length || suggestion.findings.length" class="findings" :aria-label="s.findings">
      <li v-for="(f, i) in suggestion.term_findings" :key="`t${i}`"><code>{{ f.code }}</code> {{ f.message }}</li>
      <li v-for="(f, i) in suggestion.findings" :key="`q${i}`"><code>{{ f.code }}</code> {{ f.message }}</li>
    </ul>
    <details class="why" data-testid="why">
      <summary>{{ s.why }}</summary>
      <div class="stack-sm why-body">
        <table class="table factors">
          <caption class="visually-hidden">{{ s.factorsCaption }}</caption>
          <thead>
            <tr>
              <th scope="col">{{ s.factor }}</th>
              <th scope="col">{{ s.effect }}</th>
              <th scope="col">{{ s.reason }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="f in factors" :key="f.factor" :data-factor="f.factor">
              <th scope="row">{{ s.factorName(f.factor) }}</th>
              <td class="mono" :class="f.contribution < 0 ? 'neg' : f.contribution > 0 ? 'pos' : ''">{{ formatContribution(f.contribution) }}</td>
              <td>{{ f.reason }}</td>
            </tr>
          </tbody>
        </table>
        <component :is="headingLevel === 4 ? 'h5' : 'h4'" v-if="full" class="sub">{{ s.provenance }}</component>
        <dl v-if="full" class="prov" data-testid="provenance">
          <dt>{{ s.origin }}</dt>
          <dd>{{ s.originName[full.provenance.origin] }}</dd>
          <template v-if="full.provenance.provider">
            <dt>{{ s.model }}</dt>
            <dd class="mono">{{ full.provenance.provider }} / {{ full.provenance.model }}</dd>
          </template>
          <template v-if="full.provenance.prompt_version">
            <dt>{{ s.promptVersion }}</dt>
            <dd class="mono">{{ full.provenance.prompt_version }}</dd>
          </template>
          <dt>{{ s.tmUnits }}</dt>
          <dd>{{ full.provenance.tm_unit_ids?.length ? s.count(full.provenance.tm_unit_ids.length) : s.noneUsed }}</dd>
          <dt>{{ s.terms }}</dt>
          <dd>{{ full.provenance.term_ids?.length ? s.count(full.provenance.term_ids.length) : s.noneUsed }}</dd>
          <dt>{{ s.styleVersion }}</dt>
          <dd class="mono">{{ full.provenance.style_version || s.noneUsed }}</dd>
          <dt>{{ s.repairs }}</dt>
          <dd>{{ full.provenance.repairs }}</dd>
          <dt>{{ s.cost }}</dt>
          <dd>{{ formatUSD(full.cost_micro_usd) }} · {{ s.tokens(tokens) }}</dd>
        </dl>
      </div>
    </details>
  </div>
</template>

<style scoped>
.score {
  font-variant-numeric: tabular-nums;
  font-weight: var(--kl-weight-medium);
}
.text {
  white-space: pre-wrap;
  font-family: var(--kl-font-mono);
  font-size: var(--kl-text-sm);
  padding: var(--kl-space-3);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
}
.tags {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-1);
}
.findings {
  margin: 0;
  padding-inline-start: var(--kl-space-5);
  font-size: var(--kl-text-sm);
}
.why summary {
  cursor: pointer;
  font-weight: var(--kl-weight-medium);
  color: var(--gs-accent-ink);
}
.why-body {
  padding-block-start: var(--kl-space-2);
}
.factors {
  font-size: var(--kl-text-sm);
}
.factors th,
.factors td {
  padding: var(--kl-space-1) var(--kl-space-2);
  vertical-align: top;
}
.factors tbody th {
  font-weight: var(--kl-weight-medium);
  color: var(--kl-ink);
}
.neg {
  color: var(--gs-err);
}
.pos {
  color: var(--gs-ok);
}
.sub {
  font-size: var(--kl-text-sm);
  margin: 0;
}
.prov {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-1) var(--kl-space-3);
  margin: 0;
  font-size: var(--kl-text-sm);
}
.prov dt {
  color: var(--kl-ink-secondary);
}
.prov dd {
  margin: 0;
  word-break: break-all;
}
</style>
