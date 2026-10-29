/**
 * Pure helpers for the quality view (RFC 0005 §2, §8): findings grouped
 * by the layer that found them, the filter state the URL carries, and
 * what a waived finding needs to stay visible — the waiver that accepts
 * it, and the crop a visual finding points at.
 *
 * Nothing here hides a finding. A waiver changes how one reads, never
 * whether it is there (RFC 0005 §14 decision 5).
 */
import type { Finding, FindingLayer, FindingLocus, FindingSeverity, Waiver } from "../api/quality-schemas";
import type { FindingFilter } from "../api/quality";

/** The layers in the order RFC 0005 §3 lists them, which is the order the view groups them in. */
export const LAYERS: readonly FindingLayer[] = [
  "structure",
  "parity",
  "completeness",
  "terminology",
  "style",
  "length",
  "locale",
  "source",
  "visual",
  "linguistic",
] as const;

export const SEVERITIES: readonly FindingSeverity[] = ["error", "warning", "waived"] as const;

/** One layer's findings, with what they add up to. `checked` false: the run never looked. */
export interface LayerGroup {
  layer: FindingLayer;
  findings: Finding[];
  errors: number;
  warnings: number;
  waived: number;
  /** The run computed this layer, so an empty group means "clean" and not "not looked at". */
  checked: boolean;
}

/**
 * The findings grouped by layer, in RFC 0005 §3's order, keeping the
 * API's order inside a group (errors, then warnings, then the waived).
 * Every layer the run computed gets a group even when it found nothing,
 * because "clean" and "not looked at" are different answers; a layer
 * with findings always gets one, computed or not.
 */
export function groupByLayer(findings: readonly Finding[], checked: readonly FindingLayer[] = []): LayerGroup[] {
  const groups = new Map<FindingLayer, LayerGroup>();
  const group = (layer: FindingLayer): LayerGroup => {
    let g = groups.get(layer);
    if (!g) groups.set(layer, (g = { layer, findings: [], errors: 0, warnings: 0, waived: 0, checked: checked.includes(layer) }));
    return g;
  };
  for (const layer of checked) group(layer);
  for (const f of findings) {
    const g = group(f.layer);
    g.findings.push(f);
    if (f.severity === "error") g.errors += 1;
    else if (f.severity === "warning") g.warnings += 1;
    else g.waived += 1;
  }
  return LAYERS.filter((l) => groups.has(l)).map((l) => groups.get(l) as LayerGroup);
}

/** The layers this run never computed, in RFC 0005 §3's order: nothing is known about them. */
export function notChecked(checked: readonly FindingLayer[]): FindingLayer[] {
  return LAYERS.filter((l) => !checked.includes(l));
}

/** The waivers a list of findings names, by id, so a waived finding can show its reason. */
export function waiversById(waivers: readonly Waiver[]): Map<string, Waiver> {
  return new Map(waivers.map((w) => [w.id, w]));
}

/** `src/checkout/PaymentFooter.vue:42`, the way a stack trace names a place. */
export function findingPlace(locus: FindingLocus): string | undefined {
  if (!locus.file) return undefined;
  return locus.line === undefined ? locus.file : `${locus.file}:${locus.line}`;
}

/**
 * A visual finding shows the crop its capture and region point at
 * (RFC 0005 §5.2). It needs the message's key to read the capture back
 * through Context, so a finding with no key has no crop to show.
 */
export function cropTarget(f: Finding): { key: string; capture: string } | undefined {
  const { capture, key } = f.locus;
  return capture && key ? { key, capture } : undefined;
}

// ── filters ─────────────────────────────────────────────────────────
/** What the URL carries, so a filtered view is bookmarkable. Empty means "any". */
export interface QualityFilterState {
  run: string;
  layer: string;
  severity: string;
  locale: string;
  code: string;
}

export const EMPTY_FILTER: QualityFilterState = { run: "", layer: "", severity: "", locale: "", code: "" };

export const isFiltered = (f: QualityFilterState): boolean =>
  !!(f.layer || f.severity || f.locale || f.code);

const isLayer = (v: string): v is FindingLayer => (LAYERS as readonly string[]).includes(v);
const isSeverity = (v: string): v is FindingSeverity => (SEVERITIES as readonly string[]).includes(v);

/**
 * The filter state as the API takes it. An unknown layer or severity is
 * dropped rather than sent: the server answers `invalid_query` for one,
 * and a stale bookmark should show the findings, not an error.
 */
export function toFindingFilter(f: QualityFilterState): FindingFilter {
  return {
    run: f.run || undefined,
    layer: isLayer(f.layer) ? f.layer : undefined,
    severity: isSeverity(f.severity) ? f.severity : undefined,
    locale: f.locale || undefined,
    code: f.code.trim() || undefined,
  };
}
