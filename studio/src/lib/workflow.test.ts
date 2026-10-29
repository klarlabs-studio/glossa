import { describe, expect, it } from "vitest";
import { reviewDocument } from "../test/fake-workflows";
import { chartOf, findingLine, formatDocument, keyLines, parseDocument, precedence, refuses, sameDocument, sortFindings, specificity, starterDocument, toMermaid, validName } from "./workflow";

describe("parseDocument", () => {
  it("reads an object and says where text is not JSON", () => {
    expect(parseDocument('{"a": 1}')).toEqual({ ok: true, doc: { a: 1 } });
    const bad = parseDocument('{\n  "a": 1,\n  "b": \n}');
    expect(bad.ok).toBe(false);
    if (!bad.ok) expect(bad.line).toBeGreaterThanOrEqual(3);
  });

  it("refuses a value that is not an object", () => {
    expect(parseDocument("[1]")).toEqual({ ok: false, message: "not-an-object", line: 1 });
    expect(parseDocument("null")).toMatchObject({ ok: false });
  });

  it("compares documents, not formatting", () => {
    const doc = reviewDocument();
    expect(sameDocument(formatDocument(doc), doc)).toBe(true);
    expect(sameDocument(JSON.stringify(doc), doc)).toBe(true);
    expect(sameDocument(JSON.stringify({ ...doc, name: "x" }), doc)).toBe(false);
    expect(sameDocument("{", doc)).toBe(false);
  });
});

describe("chartOf and toMermaid", () => {
  it("reads states, finals and transitions with guard and actions", () => {
    const chart = chartOf(reviewDocument());
    expect(chart.initial).toBe("current");
    expect(chart.states.map((s) => [s.id, s.final])).toEqual([
      ["current", false],
      ["reviewing", false],
      ["done", true],
    ]);
    expect(chart.transitions).toContainEqual({ from: "reviewing", event: "approval.granted", target: "done", guard: "one_approval", actions: ["approve"] });
  });

  it("draws a stateDiagram with the initial and final pseudo-states and labelled edges", () => {
    const text = toMermaid(chartOf(reviewDocument()));
    expect(text.split("\n")[0]).toBe("stateDiagram-v2");
    expect(text).toContain('state "reviewing" as s_reviewing');
    expect(text).toContain("[*] --> s_current");
    expect(text).toContain("s_reviewing --> s_done : approval.granted [one_approval] / approve");
    expect(text).toContain("s_done --> [*]");
  });

  it("keeps ids Mermaid can't read apart, and labels free of its separators", () => {
    const text = toMermaid(
      chartOf({ chart: { initial: "a-b", states: { "a-b": { transitions: [{ event: "x;y", target: "a.b" }] }, "a.b": { type: "final" } } } }),
    );
    expect(text).toContain('state "a-b" as s_a_2d_b');
    expect(text).toContain('state "a.b" as s_a_2e_b');
    expect(text).toContain("s_a_2d_b --> s_a_2e_b : x y");
  });

  it("draws what a half-written document has and skips what it can't place", () => {
    const chart = chartOf({ chart: { states: { a: { transitions: [{ event: "go", target: "nowhere" }, { target: "a" }, "junk"] } } } });
    expect(chart.transitions).toHaveLength(1);
    expect(toMermaid(chart)).not.toContain("nowhere");
    expect(chartOf({}).states).toEqual([]);
  });
});

describe("findings on lines", () => {
  const text = formatDocument(reviewDocument());
  const lines = keyLines(text);
  const lineOf = (needle: string) => text.split("\n").findIndex((l) => l.includes(needle)) + 1;

  it("maps key paths and array elements to the lines they are written on", () => {
    expect(lines.get("chart.states.reviewing")).toBe(lineOf('"reviewing": {'));
    expect(lines.get("guards.one_approval")).toBe(lineOf('"one_approval": {'));
    expect(lines.get("chart.states.reviewing.transitions.1")).toBe(lineOf('"event": "approval.denied"') - 1);
  });

  it("places a finding on its path, its nearest ancestor, or its state", () => {
    expect(findingLine({ rule: "r", severity: "error", message: "m", path: "guards.one_approval.n" }, lines)).toBe(lineOf('"n": 1,'));
    expect(findingLine({ rule: "r", severity: "error", message: "m", path: "guards.one_approval.missing" }, lines)).toBe(lineOf('"one_approval": {'));
    expect(findingLine({ rule: "r", severity: "error", message: "m", state: "done" }, lines)).toBe(lineOf('"done": {'));
    expect(findingLine({ rule: "r", severity: "error", message: "m" }, lines)).toBeUndefined();
  });

  it("orders errors before warnings before notes, and says which refuse a save", () => {
    const list = [
      { rule: "a", severity: "info" as const, message: "note" },
      { rule: "b", severity: "warning" as const, message: "warn", state: "done" },
      { rule: "c", severity: "error" as const, message: "err", state: "reviewing" },
    ];
    expect(sortFindings(list, lines).map((f) => f.rule)).toEqual(["c", "b", "a"]);
    expect(refuses(list)).toBe(true);
    expect(refuses([list[0]!])).toBe(false);
  });
});

describe("starter documents and names", () => {
  it("starts a translation or a release-request workflow under the name given", () => {
    expect(starterDocument("legal")).toMatchObject({ schema: "glossa.workflow/v1", name: "legal", subject: "translation" });
    expect(starterDocument("ship", "release_request")).toMatchObject({ subject: "release_request", guards: { enough_approvals: { use: "approvals_as_required" } } });
  });

  it("checks names as the API does", () => {
    expect(validName("vendor-then-four-eyes")).toBe(true);
    expect(validName("Legal")).toBe(false);
    expect(validName("-x")).toBe(false);
    expect(validName("")).toBe(false);
  });
});

describe("binding precedence", () => {
  const b = (id: string, position: number, locales: string[] = [], namespace?: string) => ({ id, position, locales, ...(namespace ? { namespace } : {}) });

  it("tries the binding naming more fields first, and the later of two equally specific ones", () => {
    const list = [b("all", 1), b("de", 2, ["de"]), b("de-checkout", 3, ["de"], "checkout"), b("fr", 4, ["fr"]), b("all-later", 5)];
    expect(precedence(list).map((x) => x.id)).toEqual(["de-checkout", "fr", "de", "all-later", "all"]);
    expect(specificity(list[2]!)).toBe(2);
  });
});
