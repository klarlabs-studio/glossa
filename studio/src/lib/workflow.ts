/**
 * What the workflow editor needs to know about a `glossa.workflow/v1`
 * document without owning its meaning (RFC 0006 §2.3, §14 decision 12):
 * parse the author's text and say where it breaks, read the chart's
 * states and transitions for the rendered diagram and its text
 * alternative, place the server's lint findings on lines of the text,
 * and order a project's bindings the way the server resolves them.
 *
 * The server compiles and lints; nothing here decides whether a document
 * is valid. A document that parses but that the server would refuse
 * still renders, so the author sees what they wrote.
 */
import type { WorkflowBinding, WorkflowDocument, WorkflowFinding } from "../api/workflows-schemas";

// ── parsing ────────────────────────────────────────────────────────────

export type Parsed = { ok: true; doc: WorkflowDocument } | { ok: false; message: string; line: number | undefined };

/** The author's text as a document, or where and why it is not JSON (or not an object). */
export function parseDocument(text: string): Parsed {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    return { ok: false, message, line: errorLine(text, message) };
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) return { ok: false, message: "not-an-object", line: 1 };
  return { ok: true, doc: value as WorkflowDocument };
}

/** The line a JSON.parse error points at, from "position N" or "line N column M" in its message. */
function errorLine(text: string, message: string): number | undefined {
  const lc = /line (\d+) column \d+/.exec(message);
  if (lc) return Number(lc[1]);
  const pos = /position (\d+)/.exec(message);
  if (pos) return text.slice(0, Number(pos[1])).split("\n").length;
  // V8 sometimes quotes the text around the error instead: `Unexpected token '}', ..."  "b": \n}" is not valid JSON`.
  const quoted = /Unexpected token '(.)', (?:\.\.\.)?"([\s\S]*?)"(?:\.\.\.)? is not valid JSON/.exec(message);
  if (quoted) {
    const [, token, snippet] = quoted as unknown as [string, string, string];
    const at = text.indexOf(snippet);
    if (at >= 0) return text.slice(0, at + Math.max(0, snippet.lastIndexOf(token))).split("\n").length;
  }
  return undefined;
}

/** Two-space JSON, as the editor shows a stored document. */
export const formatDocument = (doc: WorkflowDocument): string => `${JSON.stringify(doc, null, 2)}\n`;

/** Whether two texts are the same document, ignoring formatting. */
export function sameDocument(a: string, b: WorkflowDocument): boolean {
  const p = parseDocument(a);
  return p.ok && JSON.stringify(p.doc) === JSON.stringify(b);
}

// ── the chart ──────────────────────────────────────────────────────────

export interface ChartTransition {
  from: string;
  event: string;
  target: string;
  guard: string | undefined;
  actions: string[];
}

export interface ChartState {
  id: string;
  final: boolean;
  entry: string[];
}

export interface Chart {
  initial: string | undefined;
  states: ChartState[];
  transitions: ChartTransition[];
}

const obj = (v: unknown): Record<string, unknown> | undefined => (v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined);
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : typeof v === "string" ? [v] : []);
const str = (v: unknown): string | undefined => (typeof v === "string" && v ? v : undefined);

/**
 * The chart's top-level states and transitions, read leniently: whatever
 * is missing or malformed is left out (the server's lint says why), so a
 * half-written document still draws what it has.
 */
export function chartOf(doc: WorkflowDocument): Chart {
  const chart = obj(doc.chart);
  const states = obj(chart?.states) ?? {};
  const out: Chart = { initial: str(chart?.initial), states: [], transitions: [] };
  for (const [key, raw] of Object.entries(states)) {
    const st = obj(raw) ?? {};
    out.states.push({ id: key, final: st.type === "final", entry: strs(st.entry) });
    const list = Array.isArray(st.transitions) ? st.transitions : [];
    for (const t of list) {
      const tr = obj(t);
      const event = str(tr?.event);
      if (!tr || !event) continue;
      out.transitions.push({ from: key, event, target: str(tr.target) ?? key, guard: str(tr.guard), actions: strs(tr.actions) });
    }
  }
  return out;
}

/** A Mermaid-safe state id: the chart's ids may hold dashes and dots. */
const mid = (id: string) => `s_${id.replace(/[^A-Za-z0-9_]/g, (c) => `_${c.charCodeAt(0).toString(16)}_`)}`;
/** Label text: Mermaid ends a label at a newline and reads `;` and `#` specially. */
const label = (s: string) => s.replace(/[\n;#:]/g, " ");

/**
 * The chart as a Mermaid `stateDiagram-v2`: every state under its own
 * name, the initial and final pseudo-states, and each transition labelled
 * `event [guard] / actions` — the notation statecharts use.
 */
export function toMermaid(chart: Chart): string {
  const lines = ["stateDiagram-v2"];
  for (const s of chart.states) lines.push(`  state "${label(s.id)}" as ${mid(s.id)}`);
  if (chart.initial && chart.states.some((s) => s.id === chart.initial)) lines.push(`  [*] --> ${mid(chart.initial)}`);
  const known = new Set(chart.states.map((s) => s.id));
  for (const t of chart.transitions) {
    if (!known.has(t.target)) continue;
    const text = [t.event, t.guard ? `[${t.guard}]` : "", t.actions.length ? `/ ${t.actions.join(", ")}` : ""].filter(Boolean).join(" ");
    lines.push(`  ${mid(t.from)} --> ${mid(t.target)} : ${label(text)}`);
  }
  for (const s of chart.states) if (s.final) lines.push(`  ${mid(s.id)} --> [*]`);
  return lines.join("\n");
}

// ── findings on lines ──────────────────────────────────────────────────

/**
 * The line of each key path in a JSON text (`chart.states.reviewing` →
 * the line where `"reviewing":` is written), from a small scanner that
 * follows objects and arrays. Array elements are addressed by index
 * (`chart.states.reviewing.transitions.0`).
 */
export function keyLines(text: string): Map<string, number> {
  const out = new Map<string, number>();
  const stack: Array<{ kind: "obj" | "arr"; path: string; index: number; key: string | undefined }> = [];
  let line = 1;
  let i = 0;
  const join = (base: string, part: string) => (base ? `${base}.${part}` : part);
  const here = (): string => {
    const top = stack[stack.length - 1];
    if (!top) return "";
    return top.kind === "obj" ? join(top.path, top.key ?? "") : join(top.path, String(top.index));
  };
  while (i < text.length) {
    const c = text[i]!;
    if (c === "\n") {
      line++;
      i++;
      continue;
    }
    if (c === '"') {
      let j = i + 1;
      let s = "";
      while (j < text.length && text[j] !== '"') {
        if (text[j] === "\\") {
          s += text[j + 1] ?? "";
          j += 2;
          continue;
        }
        if (text[j] === "\n") line++;
        s += text[j];
        j++;
      }
      i = j + 1;
      // A string followed by `:` is a key of the object on top.
      let k = i;
      while (k < text.length && /[ \t\r]/.test(text[k]!)) k++;
      const top = stack[stack.length - 1];
      if (text[k] === ":" && top?.kind === "obj") {
        top.key = s;
        out.set(join(top.path, s), line);
      }
      continue;
    }
    if (c === "{" || c === "[") {
      const path = here();
      const top = stack[stack.length - 1];
      if (top?.kind === "arr" && !out.has(path)) out.set(path, line);
      stack.push({ kind: c === "{" ? "obj" : "arr", path, index: 0, key: undefined });
    } else if (c === "}" || c === "]") {
      stack.pop();
    } else if (c === ",") {
      const top = stack[stack.length - 1];
      if (top?.kind === "arr") top.index++;
      else if (top) top.key = undefined;
    }
    i++;
  }
  return out;
}

/**
 * The line a finding belongs on: its `path` if the text has it (or the
 * nearest ancestor that it has), else the state it names, else none —
 * a finding about the whole document stays above the editor.
 */
export function findingLine(f: WorkflowFinding, lines: Map<string, number>): number | undefined {
  const tries: string[] = [];
  if (f.path) {
    const parts = f.path.split(".");
    for (let n = parts.length; n > 0; n--) tries.push(parts.slice(0, n).join("."));
  }
  if (f.state) tries.push(`chart.states.${f.state}`);
  for (const p of tries) {
    const l = lines.get(p);
    if (l !== undefined) return l;
  }
  return undefined;
}

export const SEVERITY_ORDER: Record<WorkflowFinding["severity"], number> = { error: 0, warning: 1, info: 2 };

/** Errors first, then warnings, then notes; within each, top of the document first. */
export function sortFindings(list: readonly WorkflowFinding[], lines: Map<string, number>): Array<WorkflowFinding & { line: number | undefined }> {
  return list
    .map((f) => ({ ...f, line: findingLine(f, lines) }))
    .sort((a, b) => SEVERITY_ORDER[a.severity] - SEVERITY_ORDER[b.severity] || (a.line ?? 0) - (b.line ?? 0));
}

/** Whether a set of findings would refuse a save: errors and warnings do, notes don't. */
export const refuses = (list: readonly WorkflowFinding[]): boolean => list.some((f) => f.severity !== "info");

// ── a new definition ───────────────────────────────────────────────────

/**
 * What "New workflow" starts from: one review by one person, four-eyes,
 * written out so every part of the format is visible and editable. It
 * is a starting point the author changes, not a default the platform
 * applies — the tenant's own `review` definition is that (§2.3).
 */
export function starterDocument(name: string, subject: "translation" | "release_request" = "translation"): WorkflowDocument {
  if (subject === "release_request") {
    return {
      schema: "glossa.workflow/v1",
      name,
      subject,
      chart: {
        id: name,
        initial: "pending",
        states: {
          pending: {
            id: "pending",
            type: "atomic",
            entry: ["ask_approvers"],
            transitions: [
              { event: "approval.granted", target: "approved", guard: "enough_approvals", actions: ["deploy"] },
              { event: "approval.denied", target: "denied", actions: ["deny"] },
              { event: "release_request.withdrawn", target: "withdrawn" },
            ],
          },
          approved: {
            id: "approved",
            type: "atomic",
            transitions: [
              { event: "release_request.deployed", target: "deployed" },
              { event: "release_request.refused", target: "refused" },
            ],
          },
          deployed: { id: "deployed", type: "final" },
          denied: { id: "denied", type: "final" },
          withdrawn: { id: "withdrawn", type: "final" },
          refused: { id: "refused", type: "final" },
        },
      },
      guards: { enough_approvals: { use: "approvals_as_required" } },
      actions: {
        ask_approvers: { use: "request_approval_as_required" },
        deploy: { use: "deploy_release" },
        deny: { use: "deny_release" },
      },
    };
  }
  return {
    schema: "glossa.workflow/v1",
    name,
    subject,
    chart: {
      id: name,
      initial: "reviewing",
      states: {
        reviewing: {
          id: "reviewing",
          type: "atomic",
          entry: ["ask_a_reviewer"],
          transitions: [
            { event: "approval.granted", target: "done", guard: "one_approval", actions: ["approve"] },
            { event: "approval.denied", target: "done", actions: ["reject"] },
          ],
        },
        done: { id: "done", type: "final" },
      },
    },
    guards: { one_approval: { use: "approvals_at_least", n: 1, distinct_from_author: true } },
    actions: {
      ask_a_reviewer: { use: "request_approval", n: 1, from: { role: "reviewer" } },
      approve: { use: "set_review_state", state: "approved" },
      reject: { use: "set_review_state", state: "rejected" },
    },
  };
}

/** A definition's name as the API requires it (`^[a-z0-9][a-z0-9-]{0,63}$`). */
export const NAME_PATTERN = "[a-z0-9][a-z0-9\\-]{0,63}";
export const validName = (name: string): boolean => /^[a-z0-9][a-z0-9-]{0,63}$/.test(name);

// ── bindings ───────────────────────────────────────────────────────────

/** How many fields a binding names beyond the project: locales, namespace. */
export const specificity = (b: Pick<WorkflowBinding, "locales" | "namespace">): number => (b.locales.length ? 1 : 0) + (b.namespace ? 1 : 0);

/**
 * Bindings in the order they are tried (RFC 0005 §4.1's rule, shared):
 * the one naming more fields first, and of two equally specific ones the
 * later. The first that matches a subject is the one it runs under.
 */
export function precedence<B extends Pick<WorkflowBinding, "locales" | "namespace" | "position">>(list: readonly B[]): B[] {
  return [...list].sort((a, b) => specificity(b) - specificity(a) || b.position - a.position);
}
