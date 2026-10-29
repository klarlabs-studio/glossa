/** An AI fill's plan (from its preview) and its progress (from its jobs' states). */
import type { AIFillPreview, AIJob } from "../api/intelligence-schemas";

export interface FillPlan {
  /** Messages a fill would queue (or reuse) a job for, across locales. */
  messages: number;
  /** Jobs that exist and would be reused. */
  existing: number;
  /** Covered by an exact translation-memory match: no provider call. */
  tmExact: number;
  /** Would call a provider. */
  provider: number;
  /** Wouldn't reach a provider, by reason, most first. */
  refused: Array<[reason: string, count: number]>;
  /** Left out, by reason, most first. */
  skipped: Array<[reason: string, count: number]>;
}

const byCount = (m: Map<string, number>) => [...m].filter(([, n]) => n > 0).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));

/** A preview's per-locale answers, summed. */
export function fillPlan(preview: Pick<AIFillPreview, "locales">): FillPlan {
  const refused = new Map<string, number>();
  const skipped = new Map<string, number>();
  const plan: FillPlan = { messages: 0, existing: 0, tmExact: 0, provider: 0, refused: [], skipped: [] };
  for (const l of preview.locales) {
    plan.messages += l.keys.length;
    plan.existing += l.existing;
    plan.tmExact += l.tm_exact;
    plan.provider += l.provider;
    for (const [why, n] of Object.entries(l.refused)) refused.set(why, (refused.get(why) ?? 0) + n);
    for (const [why, n] of Object.entries(l.skipped)) skipped.set(why, (skipped.get(why) ?? 0) + n);
  }
  plan.refused = byCount(refused);
  plan.skipped = byCount(skipped);
  return plan;
}

/** How often a running fill's jobs are polled. */
export const FILL_POLL_MS = 1000;

export interface FillProgress {
  total: number;
  queued: number;
  running: number;
  /** Queued or running: not settled yet. */
  active: number;
  succeeded: number;
  skipped: number;
  /** Failed for good, dead or cancelled. */
  failed: number;
  /** Why jobs failed, most common first. */
  failures: Array<[code: string, count: number]>;
}

export function fillProgress(jobs: readonly Pick<AIJob, "state" | "failure_code">[]): FillProgress {
  const p: FillProgress = { total: jobs.length, queued: 0, running: 0, active: 0, succeeded: 0, skipped: 0, failed: 0, failures: [] };
  const codes = new Map<string, number>();
  for (const j of jobs) {
    switch (j.state) {
      case "queued":
        p.queued++;
        break;
      case "running":
        p.running++;
        break;
      case "succeeded":
        p.succeeded++;
        break;
      case "skipped":
        p.skipped++;
        break;
      default: {
        p.failed++;
        const code = j.state === "failed" ? (j.failure_code ?? "unknown") : j.state;
        codes.set(code, (codes.get(code) ?? 0) + 1);
      }
    }
  }
  p.active = p.queued + p.running;
  p.failures = [...codes].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  return p;
}
