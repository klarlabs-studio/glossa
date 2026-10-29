<script setup lang="ts">
/**
 * The project health header (RFC 0005 §8): the seven numbers, and no
 * more, because a dashboard nobody reads is worse than a check that
 * fails.
 *
 * It consumes the quality summary and computes nothing of its own. Two
 * rules it keeps everywhere:
 *
 * - Nothing here falls back to zero. When the summary does not carry a
 *   number — because nothing has been checked, no build was uploaded,
 *   no pull request has been graded, or this server does not report the
 *   summary at all — the stat says "Not measured" and, when we know it,
 *   why.
 * - Every state is a word before it is a colour. The tone only repeats
 *   what the value already says.
 */
import { computed } from "vue";
import type { QualitySummary } from "../../api/quality-summary-schemas";
import type { HealthState } from "../../views/project/context";
import { coverageShare, duration, percent, percentiles, share } from "../../lib/health";
import { absoluteTime, relativeTime } from "../../lib/time";
import { strings } from "../../strings";
import HealthStat from "./HealthStat.vue";

type Tone = "ok" | "warn" | "err" | "neutral";
interface Stat {
  value: string | undefined;
  detail: string | undefined;
  tone: Tone;
}

const props = defineProps<{
  /** The summary, or nothing when it is still loading or this server does not report it. */
  summary: QualitySummary | undefined;
  state: HealthState;
}>();
const s = strings.health;

const health = computed(() => props.summary?.project);
/** A stat only carries an explanation when we actually read a summary; otherwise it is simply unknown. */
const none = (detail: string): Stat => ({ value: undefined, detail: props.summary ? detail : undefined, tone: "neutral" });
const ok = (value: string, detail: string | undefined, tone: Tone = "neutral"): Stat => ({ value, detail, tone });

const DAY = 86_400;

const coverage = computed<Stat>(() => {
  const c = health.value?.coverage;
  const part = coverageShare(c);
  if (!c || part === undefined) return none(s.coverageNone);
  const clean = c.missing === 0 && c.outdated === 0;
  return ok(percent(part) ?? "", s.coverageDetail(c.translated, c.outdated, c.missing), clean ? "ok" : "warn");
});

const findings = computed<Stat>(() => {
  const f = health.value?.findings;
  if (!f) return none(s.neverCheckedDetail);
  const run = health.value?.run;
  return ok(
    s.findingsValue(f.errors, f.warnings),
    run ? s.findingsDetail(f.waived, run.ref) : strings.quality.waivedCount(f.waived),
    f.errors > 0 ? "err" : f.warnings > 0 ? "warn" : "ok",
  );
});

const ai = computed<Stat>(() => {
  const a = health.value?.ai;
  if (!a || a.decisions === 0) return none(s.aiNone);
  return ok(percent(a.acceptance_rate) ?? "", s.aiDetail(a.decisions, a.mean_edit_distance.toFixed(1)));
});

const queue = computed<Stat>(() => {
  const q = health.value?.queue;
  if (!q) return none(s.queueAgeNone);
  const age = percentiles(q.age);
  if (q.depth === 0) return ok(s.queueEmpty, s.queueAgeNone, "ok");
  return ok(
    s.queueDepth(q.depth),
    age ? s.queueAge(duration(age.p50_seconds), duration(age.p90_seconds)) : s.queueAgeNone,
    age && age.p90_seconds > 7 * DAY ? "warn" : "neutral",
  );
});

const context = computed<Stat>(() => {
  const c = health.value?.context;
  const usage = share(c?.with_usage, c?.active_messages);
  if (!c || usage === undefined) return none(s.contextNone);
  const region = percent(share(c.with_region, c.active_messages));
  return ok(
    s.contextValue(percent(usage) ?? ""),
    region === undefined ? undefined : s.contextDetail(region, c.active_messages),
    usage >= 0.9 ? "ok" : "neutral",
  );
});

const leadTime = computed<Stat>(() => {
  const lt = percentiles(health.value?.lead_time);
  if (!lt) return none(s.leadTimeNone);
  return ok(s.leadTimeValue(duration(lt.p50_seconds)), s.leadTimeDetail(duration(lt.p90_seconds), lt.samples));
});

const checks = computed<Stat>(() => {
  const c = health.value?.checks;
  // A pass rate is absent when nothing concluded in the window. Asking
  // for it rather than inferring it from `runs` keeps this honest if the
  // two ever come apart: the rate is what this stat renders, so the rate
  // is what has to be there.
  if (!c || c.runs === 0 || c.pass_rate === undefined) return none(s.checksNone);
  return ok(
    s.checksValue(percent(c.pass_rate) ?? ""),
    c.median_seconds === undefined ? s.checksDetailNoMedian(c.runs) : s.checksDetail(c.runs, duration(c.median_seconds)),
    c.pass_rate >= 0.9 ? "ok" : c.pass_rate >= 0.7 ? "warn" : "err",
  );
});

const stats = computed(() => [
  { key: "coverage", label: s.coverage, ...coverage.value },
  { key: "findings", label: s.findings, ...findings.value },
  { key: "ai", label: s.ai, ...ai.value },
  { key: "queue", label: s.queue, ...queue.value },
  { key: "context", label: s.context, ...context.value },
  { key: "lead-time", label: s.leadTime, ...leadTime.value },
  { key: "checks", label: s.checks, ...checks.value },
]);
</script>

<template>
  <section class="card stack-sm" aria-labelledby="health-h" data-testid="health-header" :data-state="state">
    <div class="head">
      <h2 id="health-h">{{ s.title }}</h2>
      <p v-if="state === 'loading'" class="muted" role="status" data-testid="health-loading">{{ s.loading }}</p>
      <time
        v-else-if="summary"
        class="muted"
        :datetime="summary.computed_at"
        :title="absoluteTime(summary.computed_at)"
        data-testid="health-computed"
      >
        {{ s.computed(relativeTime(summary.computed_at)) }}
      </time>
    </div>
    <p class="muted lead">{{ s.lead }}</p>
    <!--
      The endpoint is not there. Saying so is the honest answer; showing
      seven zeroes would not be.
    -->
    <p v-if="state === 'unreported' || state === 'failed'" class="hint" data-testid="health-unavailable">
      {{ state === "unreported" ? s.unreported : s.unreadable }}
    </p>

    <dl class="stats">
      <HealthStat
        v-for="stat in stats"
        :key="stat.key"
        :label="stat.label"
        :value="stat.value"
        :detail="stat.detail"
        :tone="stat.tone"
        :testid="`health-${stat.key}`"
      />
    </dl>
  </section>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
  align-items: baseline;
  justify-content: space-between;
}
.lead {
  max-inline-size: var(--kl-content-md);
  font-size: var(--kl-text-sm);
}
.stats {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr));
  gap: var(--kl-space-4);
}
</style>
