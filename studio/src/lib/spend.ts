/** The spend ledger as days of the month (UTC), for the budget chart. */
import type { AISpendEntry } from "../api/intelligence-schemas";

export interface DaySpend {
  /** YYYY-MM-DD (UTC). */
  day: string;
  micro: number;
  calls: number;
}

/** One entry per day from the month's start through `now`, zero-filled. */
export function dailySpend(entries: readonly Pick<AISpendEntry, "occurred_at" | "cost_micro_usd">[], monthStart: string, now: Date = new Date()): DaySpend[] {
  const start = new Date(monthStart);
  const days: DaySpend[] = [];
  const index = new Map<string, DaySpend>();
  for (let d = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth(), start.getUTCDate())); d <= now; d.setUTCDate(d.getUTCDate() + 1)) {
    const day = d.toISOString().slice(0, 10);
    const entry = { day, micro: 0, calls: 0 };
    days.push(entry);
    index.set(day, entry);
  }
  for (const e of entries) {
    const at = new Date(e.occurred_at);
    const slot = Number.isNaN(at.getTime()) ? undefined : index.get(at.toISOString().slice(0, 10));
    if (!slot) continue;
    slot.micro += e.cost_micro_usd;
    slot.calls++;
  }
  return days;
}
