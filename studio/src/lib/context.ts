/**
 * Pure helpers for "Where it appears" (RFC 0004 §3.4): usages grouped
 * application → route → component, the repository link a Git connection
 * would give them, the screens a message's captures make, and the crop
 * around a region — boxes are CSS pixels from the page's top left, the
 * image is `image.width / viewport.width` times larger.
 */
import type { CaptureBox, CaptureRegion, ContextUsage, MessageCapture } from "../api/context-schemas";

// ── usages ──────────────────────────────────────────────────────────
export interface ComponentGroup {
  /** The component the call is in; absent where the collector couldn't know it. */
  component: string | undefined;
  usages: ContextUsage[];
}
export interface RouteGroup {
  /** The route pattern; absent where the collector couldn't know it. */
  route: string | undefined;
  components: ComponentGroup[];
}
export interface ApplicationGroup {
  applicationId: string;
  /** The application's name, or its ID while the names aren't loaded. */
  application: string;
  routes: RouteGroup[];
}

/**
 * Usages grouped application → route → component, each level keeping the
 * order the API sent (the default branch first, then application, file
 * and line).
 */
export function groupUsages(usages: readonly ContextUsage[], nameOf: (applicationId: string) => string = (id) => id): ApplicationGroup[] {
  const apps: ApplicationGroup[] = [];
  for (const u of usages) {
    let app = apps.find((a) => a.applicationId === u.application_id);
    if (!app) apps.push((app = { applicationId: u.application_id, application: nameOf(u.application_id), routes: [] }));
    let route = app.routes.find((r) => r.route === u.route);
    if (!route) app.routes.push((route = { route: u.route, components: [] }));
    let component = route.components.find((c) => c.component === u.component);
    if (!component) route.components.push((component = { component: u.component, usages: [] }));
    component.usages.push(u);
  }
  return apps;
}

/** `file:line`, the way a stack trace names a place. */
export const usageLocation = (u: Pick<ContextUsage, "file" | "line">): string => `${u.file}:${u.line}`;

/**
 * A Git connection: the repository a project's code lives in. RFC 0004
 * §6 adds them; until then nothing provides one, so `repositoryLink`
 * returns undefined and a usage renders as plain text.
 */
export interface GitConnection {
  /** The repository's web URL, without a trailing slash: `https://github.com/acme/shop`. */
  webUrl: string;
}

/** `…/blob/<commit>/<file>#L<line>` on the connected repository; undefined while there is no connection. */
export function repositoryLink(u: Pick<ContextUsage, "commit" | "file" | "line">, connection?: GitConnection): string | undefined {
  if (!connection?.webUrl || !u.commit) return undefined;
  const base = connection.webUrl.replace(/\/+$/, "");
  const path = u.file.split("/").map(encodeURIComponent).join("/");
  return `${base}/blob/${u.commit}/${path}#L${u.line}`;
}

// ── captures ────────────────────────────────────────────────────────
/** One screen — an application's route at one viewport — with the capture of it per locale. */
export interface CaptureScreen {
  id: string;
  applicationId: string;
  route: string;
  viewport: { width: number; height: number };
  /** The captures of this screen by locale, in the order the API sent them. */
  byLocale: Map<string, MessageCapture>;
}

/** The captures grouped into screens (application, route, viewport), keeping the API's order. */
export function screensOf(captures: readonly MessageCapture[]): CaptureScreen[] {
  const screens: CaptureScreen[] = [];
  for (const c of captures) {
    const id = `${c.application_id}|${c.route}|${c.viewport.width}x${c.viewport.height}`;
    let screen = screens.find((s) => s.id === id);
    if (!screen) screens.push((screen = { id, applicationId: c.application_id, route: c.route, viewport: c.viewport, byLocale: new Map() }));
    if (!screen.byLocale.has(c.locale)) screen.byLocale.set(c.locale, c);
  }
  return screens;
}

/** Every locale any capture was taken in, in the API's order. */
export function localesOf(captures: readonly MessageCapture[]): string[] {
  return [...new Set(captures.map((c) => c.locale))];
}

/** The screen's capture in the wanted locale, else the source locale's, else the first one it has. */
export function captureFor(screen: CaptureScreen, wanted: string | undefined, sourceLocale: string | undefined): MessageCapture | undefined {
  return (
    (wanted ? screen.byLocale.get(wanted) : undefined) ??
    (sourceLocale ? screen.byLocale.get(sourceLocale) : undefined) ??
    screen.byLocale.values().next().value
  );
}

// ── geometry ────────────────────────────────────────────────────────
/** A rectangle in CSS pixels from the page's top left. */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** The page's size in CSS pixels: as wide as the viewport, as tall as the image scaled back. */
export function pageSize(capture: Pick<MessageCapture, "viewport" | "image">): Rect {
  const scale = capture.image.width / capture.viewport.width || 1;
  return { x: 0, y: 0, width: capture.viewport.width, height: Math.round(capture.image.height / scale) };
}

/** The regions that have a box on the page (a zero-size or off-screen one has none to show). */
export const visibleRegions = (regions: readonly CaptureRegion[]): CaptureRegion[] => regions.filter((r) => r.visible && r.box.width > 0 && r.box.height > 0);

const union = (boxes: readonly CaptureBox[]): Rect | undefined => {
  if (!boxes.length) return undefined;
  const x = Math.min(...boxes.map((b) => b.x));
  const y = Math.min(...boxes.map((b) => b.y));
  const right = Math.max(...boxes.map((b) => b.x + b.width));
  const bottom = Math.max(...boxes.map((b) => b.y + b.height));
  return { x, y, width: right - x, height: bottom - y };
};

/** The smallest crop shown around a region, in CSS pixels. */
export const MIN_CROP = { width: 360, height: 200 };
/** How much of the surroundings a crop keeps around the message, in CSS pixels. */
export const CROP_PADDING = { x: 120, y: 72 };

/**
 * The window to show around a message's visible regions: their union,
 * padded, at least MIN_CROP, and never outside the page. Undefined when
 * no region is visible — then there is nothing to outline.
 */
export function cropFor(capture: Pick<MessageCapture, "viewport" | "image" | "regions">): Rect | undefined {
  const boxes = visibleRegions(capture.regions).map((r) => r.box);
  const around = union(boxes);
  if (!around) return undefined;
  const page = pageSize(capture);
  const width = Math.min(page.width, Math.max(MIN_CROP.width, around.width + 2 * CROP_PADDING.x));
  const height = Math.min(page.height, Math.max(MIN_CROP.height, around.height + 2 * CROP_PADDING.y));
  const clamp = (v: number, max: number) => Math.max(0, Math.min(v, max));
  return {
    x: clamp(around.x + around.width / 2 - width / 2, page.width - width),
    y: clamp(around.y + around.height / 2 - height / 2, page.height - height),
    width,
    height,
  };
}

const pct = (n: number) => `${(n * 100).toFixed(4)}%`;

/**
 * Where the full-page image sits inside a crop window: its width and
 * offsets as percentages of the window, so the box scales with whatever
 * width the pane gives it.
 */
export function imageStyle(crop: Rect, capture: Pick<MessageCapture, "viewport" | "image">): Record<string, string> {
  const page = pageSize(capture);
  return { width: pct(page.width / crop.width), left: pct(-crop.x / crop.width), top: pct(-crop.y / crop.height) };
}

/** Where a region's box sits inside a crop window, as percentages of it. */
export function boxStyle(box: CaptureBox, crop: Rect): Record<string, string> {
  return {
    left: pct((box.x - crop.x) / crop.width),
    top: pct((box.y - crop.y) / crop.height),
    width: pct(box.width / crop.width),
    height: pct(box.height / crop.height),
  };
}

/** The crop's aspect ratio, for a box that keeps it at any width. */
export const aspectRatio = (crop: Rect): string => `${crop.width} / ${crop.height}`;
