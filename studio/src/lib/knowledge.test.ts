import { describe, expect, it } from "vitest";
import type { TermHit } from "../api/knowledge-schemas";
import { strings } from "../strings";
import { batchAcceptable, bandOf, byImpact, formatContribution, formatScore } from "./confidence";
import { fillPlan, fillProgress } from "./fill";
import { formatUSD, parseUSD, spentShare, toUSDText } from "./money";
import { dailySpend } from "./spend";
import { fieldsOf, formOf, ruleIdFrom, ruleOf, scopeLabel, styleSummary } from "./style";
import { byStatus, cpSlice, highlight } from "./terms";

const hit = (text: string, start: number, over: Partial<TermHit> = {}): TermHit => ({
  concept_id: `c-${text}`,
  definition: "",
  term: { id: `t-${text}`, locale: "en", text, status: "preferred", case_sensitive: false },
  start,
  end: start + [...text].length,
  text,
  ...over,
});

describe("highlight", () => {
  it("marks terms in the source as written, found through the visible text", () => {
    // MF1 source; the server analyzed its visible text with the placeholder as U+FFFC.
    const raw = "Invite {name} to the workspace";
    const analyzed = "Invite ￼ to the workspace";
    const segs = highlight(raw, analyzed, [hit("workspace", 21)]);
    expect(segs.map((s) => [s.text, !!s.hit])).toEqual([
      ["Invite {name} to the ", false],
      ["workspace", true],
    ]);
  });

  it("maps the n-th occurrence in the visible text to the n-th in the source, across plural variants", () => {
    const raw = "{count, plural, one {# workspace} other {# workspaces, one workspace each}}";
    const analyzed = "￼ workspace\n￼ workspaces, one workspace each";
    const second = analyzed.lastIndexOf("workspace each");
    const segs = highlight(raw, analyzed, [hit("workspace", 2), hit("workspace", [...analyzed.slice(0, second)].length)]);
    const marked = segs.filter((s) => s.hit).map((s) => s.text);
    expect(marked).toEqual(["workspace", "workspace"]);
    expect(segs.map((s) => s.text).join("")).toBe(raw);
    // The first in "one {# workspace}", the second in "one workspace each" (the 3rd occurrence in both texts).
    const offsets = segs.reduce<{ at: number; marks: number[] }>((acc, s) => ({ at: acc.at + s.text.length, marks: s.hit ? [...acc.marks, acc.at] : acc.marks }), { at: 0, marks: [] }).marks;
    expect(offsets).toEqual([raw.indexOf("workspace"), raw.indexOf("workspace each")]);
  });

  it("counts offsets in code points and skips words it can't find", () => {
    expect(cpSlice("🎉 workspace", 2)).toBe("workspace");
    const segs = highlight("It's a café", "It's a café", [hit("cafe", 7)]);
    expect(segs).toEqual([{ text: "It's a café" }]);
  });

  it("orders terms by status, allowed ones first", () => {
    const t = (text: string, status: "preferred" | "admitted" | "deprecated" | "forbidden") => ({ text, status });
    expect(byStatus([t("b", "forbidden"), t("a", "admitted"), t("c", "preferred")]).map((x) => x.text)).toEqual(["c", "a", "b"]);
  });
});

describe("confidence", () => {
  it("bands scores and never renders them as percentages", () => {
    expect([0.95, 0.9, 0.8, 0.75, 0.6, 0.5, 0.2].map(bandOf)).toEqual(["very_high", "very_high", "high", "high", "medium", "medium", "low"]);
    expect(formatScore(0.623)).toBe("0.62");
  });

  it("signs contributions and sorts factors by impact", () => {
    expect(formatContribution(-0.349)).toBe("−0.35");
    expect(formatContribution(0.03)).toBe("+0.03");
    expect(formatContribution(0.001)).toBe("±0.00");
    const f = (factor: string, contribution: number) => ({ factor, value: 0, contribution, reason: "" });
    expect(byImpact([f("a", 0.1), f("b", -0.35), f("c", 0.2)]).map((x) => x.factor)).toEqual(["b", "c", "a"]);
  });

  it("only batch-accepts pending suggestions the policy recommends", () => {
    expect(batchAcceptable({ status: "pending", action: "approve_recommended" })).toBe(true);
    expect(batchAcceptable({ status: "pending", action: "review_required" })).toBe(false);
    expect(batchAcceptable({ status: "accepted", action: "approve_recommended" })).toBe(false);
  });
});

describe("money", () => {
  it("keeps micro-USD integers and converts only at the edges", () => {
    expect(parseUSD("12.5")).toBe(12_500_000);
    expect(parseUSD("$3")).toBe(3_000_000);
    expect(parseUSD("0,000001")).toBe(1);
    expect(parseUSD("1.0000001")).toBeUndefined();
    expect(parseUSD("-2")).toBeUndefined();
    expect(parseUSD("abc")).toBeUndefined();
    expect(toUSDText(12_500_000)).toBe("12.5");
    expect(toUSDText(5_000_000)).toBe("5");
    expect(formatUSD(8_480)).toBe("$0.0085");
    expect(formatUSD(5_000_000)).toBe("$5.00");
    expect(formatUSD(0)).toBe("$0.00");
  });

  it("shares of a budget are clamped, and a zero budget is spent", () => {
    expect(spentShare(1, 4)).toBe(0.25);
    expect(spentShare(9, 4)).toBe(1);
    expect(spentShare(0, 0)).toBe(1);
  });

  it("buckets spend by UTC day from the month's start", () => {
    const days = dailySpend(
      [
        { occurred_at: "2026-09-02T10:00:00Z", cost_micro_usd: 100 },
        { occurred_at: "2026-09-02T23:30:00-02:00", cost_micro_usd: 5 },
        { occurred_at: "2026-09-01T00:00:00Z", cost_micro_usd: 7 },
      ],
      "2026-09-01T00:00:00Z",
      new Date("2026-09-03T12:00:00Z"),
    );
    expect(days).toEqual([
      { day: "2026-09-01", micro: 7, calls: 1 },
      { day: "2026-09-02", micro: 100, calls: 1 },
      { day: "2026-09-03", micro: 5, calls: 1 },
    ]);
  });
});

describe("fill progress", () => {
  it("counts settled jobs and why they failed", () => {
    const p = fillProgress([
      { state: "succeeded" },
      { state: "running" },
      { state: "queued" },
      { state: "failed", failure_code: "budget_exceeded" },
      { state: "failed", failure_code: "budget_exceeded" },
      { state: "dead" },
    ]);
    expect(p).toMatchObject({ total: 6, active: 2, succeeded: 1, failed: 3 });
    expect(p.failures).toEqual([
      ["budget_exceeded", 2],
      ["dead", 1],
    ]);
  });
});

describe("fill plan", () => {
  it("sums a preview's locales, reasons most first", () => {
    const cost = { estimated_micro_usd: 0, max_micro_usd: 0, unpriced: false };
    const plan = fillPlan({
      locales: [
        { locale: "de", keys: ["a", "b", "c"], existing: 1, tm_exact: 1, provider: 1, refused: { sensitive: 2 }, skipped: { up_to_date: 4 }, cost },
        { locale: "fr", keys: ["a"], existing: 0, tm_exact: 0, provider: 0, refused: { budget_exceeded: 1, sensitive: 1 }, skipped: { limit: 0 }, cost },
      ],
    });
    expect(plan).toEqual({
      messages: 4,
      existing: 1,
      tmExact: 1,
      provider: 1,
      refused: [
        ["sensitive", 3],
        ["budget_exceeded", 1],
      ],
      skipped: [["up_to_date", 4]],
    });
  });
});

describe("style", () => {
  it("round-trips fields through the form, leaving unset leaves out", () => {
    const fields = { formality: { register: "informal" as const, pronoun: "du" }, tone: ["friendly", "concise"], punctuation: { quotes: "„“", serial_comma: false } };
    const form = formOf(fields);
    expect(form.tone).toBe("friendly, concise");
    expect(form.serial_comma).toBe("no");
    expect(form.dash).toBe("");
    expect(fieldsOf(form)).toEqual(fields);
    expect(fieldsOf(formOf({}))).toEqual({});
  });

  it("summarizes fields in words", () => {
    const lines = styleSummary({ formality: { register: "informal", pronoun: "du" }, punctuation: { dash: "en", serial_comma: false } }, strings.style.summary);
    expect(lines).toEqual([
      { label: "Address", value: "informal “du”" },
      { label: "Dash", value: "en dash (–)" },
      { label: "Serial comma", value: "no" },
    ]);
  });

  it("builds rules, ids and scope labels", () => {
    expect(ruleOf({ id: "cta", title: " Imperative CTAs ", rationale: "", good: "Speichern\n\n", bad: "", disabled: false })).toEqual({ id: "cta", title: "Imperative CTAs", good: ["Speichern"] });
    expect(ruleOf({ id: "x", title: "t", rationale: "r", good: "g", bad: "b", disabled: true })).toEqual({ id: "x", disabled: true });
    expect(ruleIdFrom("Use du, not Sie!")).toBe("use-du-not-sie");
    expect(ruleIdFrom("¿?")).toBe("rule");
    expect(scopeLabel({ project_id: "p", locale: "de", namespace: "checkout" }, { tenant: "Workspace", project: "Demo" })).toBe("Demo · de · checkout");
    expect(scopeLabel({}, { tenant: "Workspace", project: "Demo" })).toBe("Workspace");
  });
});
