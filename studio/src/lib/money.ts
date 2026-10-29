/**
 * Money in the API is integer micro-USD (1 USD = 1 000 000). Studio
 * converts only at the edges: formatting for people and parsing what
 * they type, never floating-point arithmetic on stored amounts.
 */
export const MICRO = 1_000_000;

/** "$12.34"; amounts under a cent keep enough digits to be non-zero ("$0.0042"). */
export function formatUSD(micro: number): string {
  const usd = micro / MICRO;
  const digits = micro !== 0 && Math.abs(usd) < 0.01 ? 4 : 2;
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: digits, maximumFractionDigits: digits }).format(usd);
}

/** Dollars as typed ("12.5", "12,50", "$3") → micro-USD; undefined when it isn't an amount. */
export function parseUSD(text: string): number | undefined {
  const t = text.trim().replace(/^\$/, "").replace(",", ".");
  if (!/^\d+(\.\d{0,6})?$/.test(t)) return undefined;
  const [whole, frac = ""] = t.split(".");
  return Number(whole) * MICRO + Number(frac.padEnd(6, "0"));
}

/** micro-USD → the dollars field's text ("12.5"). */
export function toUSDText(micro: number): string {
  const whole = Math.floor(micro / MICRO);
  const frac = String(micro % MICRO).padStart(6, "0").replace(/0+$/, "");
  return frac ? `${whole}.${frac}` : String(whole);
}

/** Share of the budget spent, clamped to [0, 1]; a zero budget is fully spent. */
export function spentShare(spent: number, budget: number): number {
  if (budget <= 0) return 1;
  return Math.min(1, Math.max(0, spent / budget));
}
