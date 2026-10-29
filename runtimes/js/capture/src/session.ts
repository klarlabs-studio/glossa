/**
 * A capture or editor session (RFC 0004 §3.1): installs an `onRender` hook on
 * the page's runtimes that marks every `t()` string, keeps the render log the
 * markers point into, listens on their error channels, collects regions, and
 * ends by removing the hooks and the markers it left behind.
 *
 * The visual probe pass (RFC 0005 §5) is **given** to a session, not imported
 * by it: `startCapture(runtimes, { probe })`. `glossa capture` passes it
 * (`src/agent.ts`) and pays for it; the in-product editor, which measures
 * nothing, doesn't, and `./probes.js` never enters its bundle. A static import
 * here would put it there, because `collect()` is always reachable.
 */
import type { Render, Runtime, RuntimeError } from "@klarlabs-studio/glossa-runtime";

import { hasMarkers, mark, strip } from "./markers.js";
// Types only: erased at compile time, so this file's module graph stops here.
import type { Baseline, ProbeFinding, ProbeOptions, ProbePass } from "./probes.js";
import { collectRegions } from "./regions.js";
import type { Capture, Host, LogEntry } from "./regions.js";

/** One entry of the session's render log. */
export interface LoggedRender extends LogEntry {
  /** FNV-1a (32-bit, hex) of the values' JSON: equal values, equal digest. Values never enter the log. */
  digest: string;
}

/** A capture with the visual probe pass over it (RFC 0005 §5.1). */
export interface SessionCapture extends Capture {
  /** What the probes found, in the `glossa.finding/v1` shape. Empty without a probe pass. */
  probes: ProbeFinding[];
  /** What this capture measured, to hand to the next locale's `collect()` as its `baseline`. */
  metrics: Baseline;
}

export interface SessionOptions {
  /**
   * The visual probe pass (RFC 0005 §5). Import it as
   * `import { probe } from "@klarlabs-studio/glossa-capture/probes"` and pass it here; a
   * session without one collects regions and reports no findings.
   */
  probe?: ProbePass;
}

export interface CaptureSession {
  /** The render log: a marker's index is a position in it. Equal renders share an entry. */
  readonly renders: readonly LoggedRender[];
  /** What the session's runtimes put on their error channels (SPEC §6), in order. */
  readonly errors: readonly RuntimeError[];
  /**
   * Regions under `root` (default: the document), with the log entries they
   * refer to, and the probe pass over them (RFC 0005 §5.2) — computed after
   * the regions and before the screenshot, while the page still has layout.
   */
  collect(root?: Document | Element | ShadowRoot, options?: ProbeOptions): SessionCapture;
  /** Hook one more runtime into this session's log (one created after the session started). */
  add(runtime: Runtime): void;
  /**
   * End the session: remove the hook (so the page re-renders without
   * markers) and strip the markers still in the document.
   */
  stop(): void;
}

/** FNV-1a over the values' JSON. BigInts count as their digits; unserializable values digest as `!`. */
export function digest(values: Record<string, unknown> | undefined): string {
  let json: string;
  try {
    json = JSON.stringify(values ?? {}, (_, v) => (typeof v === "bigint" ? `${v}n` : v));
  } catch {
    return "!";
  }
  let h = 0x811c9dc5;
  for (let i = 0; i < json.length; i++) h = Math.imul(h ^ json.charCodeAt(i), 0x01000193);
  return (h >>> 0).toString(16).padStart(8, "0");
}

/**
 * Start a session on the page's runtimes (several for independent islands;
 * they share one log). Their components re-render with host attributes and
 * their `t()` strings with markers.
 */
export function startCapture(
  runtimes: Runtime | readonly Runtime[],
  { probe }: SessionOptions = {},
): CaptureSession {
  const renders: LoggedRender[] = [];
  const seen = new Map<string, number>();
  // The runtime behind each log entry: `explain()` has to be asked of the
  // runtime that actually rendered the message, not of whichever island came
  // first (RFC 0005 §5.2).
  const owners: Array<Runtime | undefined> = [];
  const errors: RuntimeError[] = [];
  const rts: Runtime[] = [];
  const offs: Array<() => void> = [];
  const hook = (r: Render, rt: Runtime) => {
    const d = digest(r.values);
    const key = JSON.stringify([r.id, r.locale ?? null, d, r.output]);
    let index = seen.get(key);
    if (index === undefined) {
      index = renders.push({ id: r.id, locale: r.locale, digest: d }) - 1;
      owners[index] = rt;
      seen.set(key, index);
    }
    return mark(index, r.output);
  };
  const attach = (rt: Runtime) => {
    rts.push(rt);
    offs.push(
      rt.onRender((r) => hook(r, rt)),
      // The error-channel drain (RFC 0005 §3.7): the channel is already
      // there and already suppresses repeats, so listening adds no
      // telemetry path — nothing leaves the page but the capture.
      rt.onError((e) => void (errors.length < 500 && errors.push(e))),
    );
  };
  (Array.isArray(runtimes) ? runtimes : [runtimes as Runtime]).forEach(attach);
  let active = true;
  return {
    renders,
    errors,
    collect(root, options) {
      const hosts: Host[] = [];
      const capture = collectRegions(renders, root, probe && ((h) => void hosts.push(h)));
      const found = probe?.(capture, hosts, { runtimes: rts, owners, errors }, options);
      return { ...capture, probes: found?.probes ?? [], metrics: found?.metrics ?? {} };
    },
    add(rt) {
      if (active) attach(rt);
    },
    stop() {
      if (!active) return;
      active = false;
      for (const off of offs) off();
      if (typeof document !== "undefined") stripMarkers(document);
    },
  };
}

/**
 * Remove every marker under `root` (default: the document): text, attribute
 * values, input values and the title, open shadow roots included.
 * Frameworks re-render without markers once the hook is gone; this cleans up
 * what they don't own.
 */
export function stripMarkers(root: Document | Element | ShadowRoot = document): void {
  const doc = root.nodeType === Node.DOCUMENT_NODE ? (root as Document) : root.ownerDocument!;
  const clean = (tree: Node) => {
    const walker = doc.createTreeWalker(tree, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (let n: Node | null = walker.currentNode; n; n = walker.nextNode()) {
      if (n.nodeType === Node.TEXT_NODE) {
        const t = n as Text;
        if (hasMarkers(t.data)) t.data = strip(t.data);
        continue;
      }
      if (n.nodeType !== Node.ELEMENT_NODE) continue;
      const el = n as Element;
      for (const a of Array.from(el.attributes)) {
        if (hasMarkers(a.value)) a.value = strip(a.value);
      }
      if (
        (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) &&
        hasMarkers(el.value)
      ) {
        el.value = strip(el.value);
      }
      if (el.shadowRoot) clean(el.shadowRoot);
    }
  };
  clean(root);
}
