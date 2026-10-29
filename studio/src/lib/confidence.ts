/**
 * How Studio talks about AI confidence (intent §23): a band plus the
 * number, never as certainty. The score orders review; it doesn't
 * promise correctness. Bands follow the intent's four levels; the
 * project's routing policy — not these bands — decides what happens to
 * a suggestion (its `action`).
 */
import type { AIAction, AIConfidenceFactor, AISuggestion } from "../api/intelligence-schemas";

export type Band = "very_high" | "high" | "medium" | "low";

/** Band edges on the [0, 1] score, inclusive lower bounds. */
export const BAND_EDGES: Readonly<Record<Exclude<Band, "low">, number>> = { very_high: 0.9, high: 0.75, medium: 0.5 };

export function bandOf(score: number): Band {
  if (score >= BAND_EDGES.very_high) return "very_high";
  if (score >= BAND_EDGES.high) return "high";
  if (score >= BAND_EDGES.medium) return "medium";
  return "low";
}

export const bandTone = (b: Band): "ok" | "accent" | "warn" | "err" =>
  b === "very_high" ? "ok" : b === "high" ? "accent" : b === "medium" ? "warn" : "err";

export const actionTone = (a: AIAction): "ok" | "accent" | "warn" =>
  a === "auto_approve" ? "ok" : a === "approve_recommended" ? "accent" : "warn";

/** Two decimals, so a score never reads like a percentage of certainty. */
export const formatScore = (score: number): string => score.toFixed(2);

/** A factor's contribution, signed: "+0.10", "−0.25", "±0.00". */
export function formatContribution(c: number): string {
  const r = Math.round(c * 100) / 100;
  if (r === 0) return "±0.00";
  return `${r > 0 ? "+" : "−"}${Math.abs(r).toFixed(2)}`;
}

/** Factors that moved the score most first. */
export function byImpact(factors: readonly AIConfidenceFactor[]): AIConfidenceFactor[] {
  return [...factors].sort((a, b) => Math.abs(b.contribution) - Math.abs(a.contribution));
}

/** Only suggestions the routing policy recommends approving may be accepted in a batch. */
export const batchAcceptable = (s: Pick<AISuggestion, "action" | "status">): boolean =>
  s.status === "pending" && s.action === "approve_recommended";
