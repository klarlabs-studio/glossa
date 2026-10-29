/**
 * The three numbers a locale row carries (RFC 0005 §8): coverage, open
 * errors, queue age — worded once, so the quality view's per-locale
 * cards and the projects list's per-locale rows cannot drift apart.
 *
 * `value: undefined` is the load-bearing case: it means nobody measured
 * this, and every renderer must print words rather than a zero.
 * `measured` false (no summary was read at all) additionally drops the
 * explanation, because with no summary we do not know *why* either.
 */
import type { LocaleHealth } from "../../api/quality-summary-schemas";
import { coverageShare, duration, percent, percentiles } from "../../lib/health";
import { strings } from "../../strings";

export type Tone = "ok" | "warn" | "err" | "neutral";

export interface Stat {
  key: "coverage" | "errors" | "queue";
  label: string;
  /** What it stands at, or `undefined` when nobody measured it. Never `0` as a stand-in. */
  value: string | undefined;
  detail: string | undefined;
  tone: Tone;
}

const WEEK = 7 * 86_400;

export function localeStats(l: LocaleHealth, measured: boolean): Stat[] {
  const s = strings.health;
  const none = (key: Stat["key"], label: string, detail: string): Stat => ({
    key,
    label,
    value: undefined,
    detail: measured ? detail : undefined,
    tone: "neutral",
  });

  const c = l.coverage;
  const part = coverageShare(c);
  const coverage: Stat =
    !c || part === undefined
      ? none("coverage", s.coverage, s.coverageNone)
      : {
          key: "coverage",
          label: s.coverage,
          value: percent(part) ?? "",
          detail: s.coverageDetail(c.translated, c.outdated, c.missing),
          tone: c.missing === 0 && c.outdated === 0 ? "ok" : "warn",
        };

  const f = l.findings;
  const errors: Stat = !f
    ? none("errors", s.findings, s.neverCheckedDetail)
    : {
        key: "errors",
        label: s.findings,
        value: f.errors === 0 ? s.errorsNone : s.errorsValue(f.errors),
        detail: `${s.warningsValue(f.warnings)} · ${strings.quality.waivedCount(f.waived)}`,
        tone: f.errors > 0 ? "err" : f.warnings > 0 ? "warn" : "ok",
      };

  const q = l.queue;
  const age = percentiles(q?.age);
  let queue: Stat;
  if (!q) queue = none("queue", s.queueAgeLabel, s.queueAgeNone);
  else if (q.depth === 0) queue = { key: "queue", label: s.queueAgeLabel, value: s.queueEmpty, detail: s.queueAgeNone, tone: "ok" };
  else if (!age) queue = { key: "queue", label: s.queueAgeLabel, value: s.queueDepth(q.depth), detail: s.queueAgeNone, tone: "neutral" };
  else
    queue = {
      key: "queue",
      label: s.queueAgeLabel,
      value: s.queueAgeValue(duration(age.p50_seconds)),
      detail: `${s.queueDepth(q.depth)} · ${s.queueAge(duration(age.p50_seconds), duration(age.p90_seconds))}`,
      tone: age.p90_seconds > WEEK ? "warn" : "neutral",
    };

  return [coverage, errors, queue];
}
