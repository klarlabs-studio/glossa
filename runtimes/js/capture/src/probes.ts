/**
 * The visual probe pass (RFC 0005 §5): semantic assertions about known
 * regions, measured live in the page while the capture session is open,
 * because `scrollWidth`, `getComputedStyle`, `document.fonts.check()` and
 * `explain()` exist only while the page is open. No pixel diffing, no OCR, no
 * contrast, no screenshot ever read back (RFC 0005 §5.3).
 *
 * Each probe emits the one finding shape (RFC 0005 §2.1,
 * runtimes/testdata/schemas/finding.v1.schema.json) at layer `visual`, always
 * at severity `warning`: promotion to `error` needs the same fingerprint in
 * two consecutive captures, which only the server can see (§5.2). Two fields
 * of that shape are deliberately absent, because the page cannot know them
 * and a guess would be worse than a gap:
 *
 * - `fingerprint` hashes the **catalog message ID** where the caller has one
 *   (`platform/internal/quality/domain.Fingerprint`), and a browser never has
 *   one — it has the key. A fingerprint computed here would not be the one
 *   the server computes for the same finding, so waivers would stop matching.
 * - `locus.capture` is the capture's ID, minted on ingest. A probe names the
 *   region within this capture (`r_<index into regions>`) and the ingest
 *   fills the capture in — exactly as RFC 0005 §2.1 has Context fill a locus
 *   at report time.
 *
 * Everything else — `schema`, `layer`, `code`, `severity`, `locus.key`,
 * `locus.locale`, `locus.region`, `message`, `subject`, `evidence` — is the
 * wire shape verbatim.
 */
import type { Runtime, RuntimeError } from "@klarlabs-studio/glossa-runtime";

import { flatParent, validKey, validLocale } from "./regions.js";
import type { Capture, Host } from "./regions.js";

/** The schema every finding names. */
export const FINDING = "glossa.finding/v1";

/** Where a probe finding is, as much of it as the page knows. */
export interface ProbeLocus {
  /** The message key. */
  key?: string;
  /** The locale the finding is about (BCP 47). */
  locale?: string;
  /** `r_<index into the capture's regions>`; the ingest adds `capture`. */
  region?: string;
}

/** One `glossa.finding/v1` finding, as a probe can write it in the page. */
export interface ProbeFinding {
  schema: typeof FINDING;
  layer: "visual";
  code: string;
  severity: "warning";
  locus: ProbeLocus;
  message: string;
  subject?: string;
  evidence?: Record<string, unknown>;
}

/**
 * The line boxes each message key covered on one (route, viewport): what the
 * next locale's `collect()` compares itself against. `glossa capture`
 * captures the source locale first and hands its `metrics` on as the target's
 * `baseline`.
 */
export type Baseline = Record<string, number>;

/**
 * What a probe run measures against. Every threshold is optional and every
 * default below is the check policy's, because the policy is the source: the
 * driver (`glossa capture`) passes the project's thresholds
 * (`checkpolicy.VisualThresholds`, Go) with the capture's options, and these
 * constants are what a probe called without a driver falls back to. The rule
 * lives in one place; only its default is written twice.
 *
 * Every length is in CSS pixels, never device pixels (RFC 0005 §5.2).
 */
export interface ProbeOptions {
  /** The source capture's `metrics` for the same route and viewport. */
  baseline?: Baseline;
  /** Line boxes a translation may gain before `line-growth` is reported. Default 0. */
  tolerance?: number;
  /** CSS pixels of content over box before `text-clipped` is reported. Default 1. */
  slack?: number;
  /** Per cent of the smaller region two must share before `region-overlap`. Default 25. */
  overlap?: number;
  /** The most findings one capture reports. Default 500. */
  max?: number;
}

/** What the session knows and the regions don't. */
export interface ProbeContext {
  /** Every runtime in the session, for the regions no marker indexes. */
  runtimes: readonly Runtime[];
  /** The runtime behind each render-log entry, indexed like the log. */
  owners: readonly (Runtime | undefined)[];
  /** What the runtimes put on their error channels during the session (SPEC §6). */
  errors: readonly RuntimeError[];
}

export interface ProbeResult {
  probes: ProbeFinding[];
  metrics: Baseline;
}

/**
 * `probe`'s type. A session takes the pass rather than importing it
 * (`startCapture(runtimes, { probe })`), so a bundle that never probes — the
 * in-product editor — doesn't carry this module.
 */
export type ProbePass = typeof probe;

/**
 * The defaults for RFC 0005 §5.2's thresholds, in CSS pixels and never device
 * pixels. The policy owns the numbers (see ProbeOptions); these stand only
 * when a probe runs without a driver to hand them down.
 */
const SLACK = 1;
/** Per cent of the smaller region, so the overlap share stays an integer. */
const OVERLAP = 25;
/** RFC 0005 §10: a capture carries at most 500 visual findings. */
const MAX = 500;

const CLIPS = /^(hidden|clip)$/;

/**
 * Probe the regions `collectRegions` just found. `hosts` is parallel to
 * `c.regions`: the `onHost` callback pushes one per region.
 *
 * A probe never throws into a capture: a page that breaks `getComputedStyle`
 * or `fonts.check()` loses that probe, not the capture.
 */
export function probe(
  c: Capture,
  hosts: readonly Host[],
  ctx: ProbeContext,
  o: ProbeOptions = {},
): ProbeResult {
  const probes: ProbeFinding[] = [];
  const metrics: Baseline = {};
  // The policy's thresholds where the driver handed them down, the defaults where it did not.
  const slack = o.slack ?? SLACK;
  const overlap = o.overlap ?? OVERLAP;
  const max = o.max ?? MAX;
  const seen = new Set<string>();
  const keys: string[] = [];
  for (const r of c.renders) keys[r.index] = r.key;
  /** Resolved-from locale → the keys that resolved from it, for `mixed-locale`. */
  const locales = new Map<string, Set<string>>();

  const css = (el: Element) => el.ownerDocument.defaultView?.getComputedStyle(el);
  const keyOf = (r: { key?: string; index?: number }) => r.key ?? keys[r.index ?? -1];

  /**
   * Add a finding, unless the same (code, key, locale, subject) is already
   * in: that is what the server's fingerprint hashes, and a message wrapping
   * to three lines is three regions and one finding.
   */
  const push = (
    code: string,
    locus: ProbeLocus,
    message: string,
    evidence?: Record<string, unknown>,
    subject?: string,
  ) => {
    const id = `${code}|${locus.key}|${locus.locale}|${subject}`;
    if (seen.has(id) || probes.length >= max) return;
    seen.add(id);
    probes.push({
      schema: FINDING,
      layer: "visual",
      code,
      severity: "warning",
      locus,
      message,
      subject,
      evidence,
    });
  };

  /**
   * `text-clipped`: the nearest clipping container above the region cuts the
   * text off — `overflow: hidden|clip` or an ellipsizing `text-overflow`, and
   * more content than the box that shows it.
   */
  const clipped = (from: Element, locus: ProbeLocus) => {
    for (let el: Element | null = from; el; el = flatParent(el)) {
      const s = css(el);
      if (!s) return;
      if (!CLIPS.test(s.overflowX) && !CLIPS.test(s.overflowY) && s.textOverflow !== "ellipsis") {
        continue;
      }
      const box = [el.clientWidth, el.clientHeight];
      const content = [el.scrollWidth, el.scrollHeight];
      if (content[0]! - box[0]! > slack || content[1]! - box[1]! > slack) {
        const by = "×";
        push("text-clipped", locus, `Clipped: ${content.join(by)} px of text in ${box.join(by)} px.`, {
          box,
          content,
        });
      }
      return;
    }
  };

  /**
   * `rtl-not-mirrored`: the runtime that rendered the region reports an RTL
   * locale and the region still lays out left to right — the layout took the
   * text and not the direction. The other half of RFC 0005 §5.2's rule, an
   * inline start that sits on the same physical side as in the LTR capture,
   * is not computed here: it would cost more of the 4 kB budget (§5.1) than
   * it is worth next to the direction, which is the decisive signal.
   */
  const mirroring = (s: CSSStyleDeclaration, locus: ProbeLocus) => {
    const dir = s.direction;
    if (dir && dir !== "rtl") {
      push("rtl-not-mirrored", locus, `Not mirrored: direction ${dir}.`);
    }
  };

  /**
   * `missing-glyph`: the fonts the region computes to have no glyph for some
   * of its text — tofu for the locale's script, before anyone reads the
   * screenshot.
   */
  const glyphs = (el: Element, s: CSSStyleDeclaration, text: string, locus: ProbeLocus) => {
    const fonts = el.ownerDocument.fonts;
    if (!fonts?.check || !text.trim()) return;
    const font = `${s.fontStyle} ${s.fontWeight} ${s.fontSize} ${s.fontFamily}`;
    if (!fonts.check(font, text)) {
      push("missing-glyph", locus, "No glyph for some of this text.", { font });
    }
  };

  /**
   * `untranslated-on-screen`, from `explain()` (SPEC §6) and never from a
   * heuristic on the text: the region resolved from a fallback locale or from
   * the inline default, in a locale the manifest lists as translated. Files
   * the region under the locale it resolved from, for `mixed-locale`.
   */
  const resolution = (rt: Runtime, key: string, locus: ProbeLocus) => {
    const e = rt.explain(key);
    const from = e.resolvedFrom;
    const on = validLocale(e.locale) ? e.locale : undefined;
    if (!on) return;
    if (validLocale(from)) {
      if (!locales.has(from)) locales.set(from, new Set());
      locales.get(from)!.add(key);
    }
    if (from === on || !rt.availableLocales.some((l) => l.code === on)) return;
    push(
      "untranslated-on-screen",
      { ...locus, locale: on },
      `Rendered from ${from ?? "the inline default"} on a ${on} screen.`,
      { resolved_from: from },
    );
  };

  /**
   * `region-overlap`: the region at `i` and a later one of another message
   * cover more than a quarter of the smaller, and neither element contains
   * the other — which separates a translation that grew into its neighbour
   * from a caption deliberately laid over an image.
   */
  const overlaps = (i: number, key: string, el: Element) => {
    const a = c.regions[i]!.box;
    for (let j = i + 1; j < c.regions.length; j++) {
      const r = c.regions[j]!;
      const b = r.box;
      const kb = keyOf(r);
      const eb = hosts[j]?.el;
      if (!r.visible || !validKey(kb) || kb === key) continue;
      if (eb && (eb === el || el.contains(eb) || eb.contains(el))) continue;
      const w = Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x);
      const h = Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y);
      const small = Math.min(a.width * a.height, b.width * b.height);
      const share = w > 0 && h > 0 && small > 0 ? Math.round((w * h * 100) / small) : 0;
      if (share > overlap) {
        push("region-overlap", { key, region: `r_${i}` }, `Overlaps ${kb} by ${share} %.`, { share }, kb);
      }
    }
  };

  c.regions.forEach((r, i) => {
    const h = hosts[i];
    const key = keyOf(r);
    if (!h?.el || !validKey(key)) return;
    const locus: ProbeLocus = { key, region: `r_${i}` };
    const rt = ctx.owners[r.index ?? -1] ?? ctx.runtimes[0];
    if (r.kind === "text") metrics[key] = (metrics[key] ?? 0) + 1;
    // A probe's failure is not a capture's failure, and the cheap, certain
    // ones run before the ones a page can break.
    try {
      if (rt) resolution(rt, key, locus);
      if (r.visible) overlaps(i, key, h.el);
      clipped(h.el, locus);
      const s = css(h.el);
      if (s) {
        if (rt?.dir === "rtl") mirroring(s, locus);
        glyphs(h.el, s, h.text, locus);
      }
    } catch {
      // Nothing: the region keeps the probes that did run.
    }
  });

  /**
   * `line-growth`: the translation wraps to materially more line boxes than
   * the source capture of the same route and viewport did. Without a baseline
   * nothing is decided — a line count on its own says nothing.
   */
  if (o.baseline) {
    for (const key of Object.keys(metrics)) {
      const now = metrics[key]!;
      const was = o.baseline[key];
      if (was === undefined || now <= was + (o.tolerance ?? 0)) continue;
      push("line-growth", { key }, `Wraps to ${now} lines, up from ${was}.`, {
        lines: now,
        source_lines: was,
      });
    }
  }

  /**
   * `mixed-locale`: the screen shows regions from two locales that aren't in
   * one fallback chain. The locale most regions resolved from is the
   * screen's; every other one that can't reach it, or be reached from it, is
   * reported.
   */
  const any = ctx.runtimes[0];
  if (locales.size > 1 && any) {
    const ranked = [...locales].sort((a, b) => b[1].size - a[1].size);
    const screen = ranked[0]![0];
    // One release, one manifest: any runtime on the page chains a locale the same way.
    const chain = (l: string) => any.explain("", l).chain;
    for (const [locale, keyed] of ranked.slice(1)) {
      if (chain(locale).includes(screen) || chain(screen).includes(locale)) continue;
      for (const key of keyed) {
        push("mixed-locale", { key, locale }, `Rendered from ${locale} on a ${screen} screen.`, undefined, screen);
      }
    }
  }

  /**
   * The error-channel drain (RFC 0005 §3.7): whatever the session's runtimes
   * reported while the page rendered becomes a `runtime-<type>` finding. It
   * is runtime evidence from an environment we control, and it opens no
   * telemetry path — the channel is already there and already suppresses
   * repeats (SPEC §6).
   */
  for (const e of ctx.errors) {
    push(
      `runtime-${e.type}`,
      {
        key: validKey(e.messageId) ? e.messageId : undefined,
        locale: validLocale(e.locale) ? e.locale : undefined,
      },
      `The runtime reported ${e.type}: ${e.detail}`,
    );
  }

  return { probes, metrics };
}
