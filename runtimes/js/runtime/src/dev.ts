/**
 * `@klarlabs-studio/glossa-runtime/dev`: the in-product editor's loader (RFC 0004 §5.1).
 *
 * Applications never import this. `@klarlabs-studio/glossa-unplugin` injects it into builds
 * for a Glossa environment that isn't `production`, and it does nothing
 * until someone asks for the editor: `?glossa=edit` in the URL, or
 * Alt+Shift+E. Then it checks the page's runtimes (the ones `createRuntime`
 * lists for it outside production), refuses unless every one has loaded a
 * release whose manifest `environment` isn't `production`, and only then
 * adds the overlay from Studio with a pinned SRI hash and starts a session.
 * It adds no inline script and evaluates no strings, so a strict CSP that
 * allows the Studio origin in `script-src` is enough.
 *
 * The session's credential comes from Studio's authorization popup
 * (./grant.ts, RFC 0004 §5.2): a fifteen-minute grant held in memory,
 * renewed through the popup, and dropped when the API says it expired.
 * No frame is ever created, so the CSP needs no `frame-src` either.
 */
import { popupGrant, type GrantProvider } from "./grant.js";
import type { Runtime } from "./runtime.js";

/** Where Studio serves the overlay, below its origin. */
export const OVERLAY_PATH = "/overlay/v1/overlay.js";

/** Set on the `<script>` the loader adds; also what bundle scans look for. */
export const LOADER_ATTRIBUTE = "data-glossa-overlay-loader";

export interface OverlayLoaderConfig {
  /** Studio's origin, which serves the overlay at `/overlay/v1/overlay.js`. */
  studio: string;
  /** The script's SRI hash, `sha384-…`, as Studio's `/overlay/v1/overlay.json` publishes it. */
  integrity: string;
  tenant: string;
  project: string;
  /** The API origin. Default: `studio`, which serves `/v1`. */
  api?: string;
}

/** What the overlay script registers under `Symbol.for("glossa.overlay")` when it runs. */
export interface OverlayModule {
  version: string;
  activate(options: {
    apiBase: string;
    studioBase: string;
    token: () => Promise<string>;
    /** Called on a 401, so the loader drops the grant and asks again. */
    onAuthFailure?: () => void;
    tenant: string;
    project: string;
    locale: string;
    runtimes: readonly Runtime[];
  }): { deactivate(): void };
}

export type LoaderState = "idle" | "checking" | "refused" | "loading" | "active" | "failed";

export interface OverlayLoader {
  readonly state: LoaderState;
  /** Why the last attempt was refused or failed. */
  readonly reason: string | undefined;
  /** What the gesture does: check the runtimes, load the overlay, start a session. */
  start(): Promise<boolean>;
  /** End the session (the overlay script stays loaded for the next one). */
  stop(): void;
  /** Stop, and remove the gesture listeners. */
  dispose(): void;
}

/** A Glossa environment name that means production, however it's spelled. */
export const isProduction = (environment: string): boolean =>
  environment.trim().toLowerCase() === "production";

/** The runtimes `createRuntime` listed on this page (non-production ones only). */
export const pageRuntimes = (): readonly Runtime[] =>
  (globalThis as Record<symbol, Runtime[] | undefined>)[Symbol.for("glossa.runtimes")] ?? [];

/** Why the page's runtimes rule out an editor session, or undefined when they don't. */
export function refusal(runtimes: readonly Runtime[]): string | undefined {
  if (runtimes.length === 0) return "no Glossa runtime outside production on this page";
  for (const rt of runtimes) {
    const env = rt.environment;
    if (env === undefined) return "a runtime has no release loaded";
    if (isProduction(env)) return "a runtime serves a production release";
  }
  return undefined;
}

const warn = (message: string) => console.warn(`[glossa] ${message}`);

const inert: OverlayLoader = {
  state: "idle",
  reason: undefined,
  start: () => Promise.resolve(false),
  stop() {},
  dispose() {},
};

export function loadOverlay(config: OverlayLoaderConfig): OverlayLoader {
  const doc = (globalThis as { document?: Document }).document;
  const win = doc?.defaultView;
  if (!doc || !win) return inert; // server rendering: nothing to load

  const src = new URL(OVERLAY_PATH, config.studio).href;
  const studio = new URL(config.studio).origin;
  let state: LoaderState = "idle";
  let reason: string | undefined;
  let session: { deactivate(): void } | undefined;
  // One provider per loader, so a session that stops and starts again
  // reuses the grant it already holds instead of opening a second popup.
  let grant: GrantProvider | undefined;
  let script: Promise<OverlayModule> | undefined;
  let pending: Promise<boolean> | undefined;

  const load = () =>
    (script ??= new Promise<OverlayModule>((resolve, reject) => {
      const el = doc.createElement("script");
      el.type = "module";
      el.src = src;
      el.setAttribute("integrity", config.integrity);
      el.setAttribute("crossorigin", "anonymous"); // SRI on a cross-origin script needs CORS
      el.setAttribute(LOADER_ATTRIBUTE, "");
      el.addEventListener("load", () => {
        const mod = (win as unknown as Record<symbol, OverlayModule | undefined>)[
          Symbol.for("glossa.overlay")
        ];
        if (mod) resolve(mod);
        else reject(new Error(`${src} ran but registered no overlay`));
      });
      el.addEventListener("error", () =>
        reject(new Error(`${src} didn't load, or doesn't match its integrity hash`)),
      );
      doc.head.append(el);
    }).catch((e: Error) => {
      script = undefined; // a later gesture tries again
      throw e;
    }));

  const attempt = async () => {
    state = "checking";
    const runtimes = [...pageRuntimes()];
    await Promise.all(runtimes.map((rt) => rt.ready));
    reason = refusal(runtimes);
    if (reason) {
      state = "refused";
      warn(`the in-product editor stays off: ${reason}`);
      return false;
    }
    state = "loading";
    try {
      const mod = await load();
      grant ??= popupGrant({ studio, tenant: config.tenant, project: config.project, window: win });
      session = mod.activate({
        apiBase: config.api ?? studio,
        studioBase: studio,
        token: grant,
        onAuthFailure: () => grant?.invalidate(),
        tenant: config.tenant,
        project: config.project,
        locale: runtimes[0]!.locale!,
        runtimes,
      });
    } catch (e) {
      state = "failed";
      reason = (e as Error).message;
      warn(`the in-product editor didn't start: ${reason}`);
      return false;
    }
    state = "active";
    return true;
  };

  const start = () =>
    session
      ? Promise.resolve(true)
      : (pending ??= attempt().finally(() => (pending = undefined)));

  const stop = () => {
    session?.deactivate();
    session = undefined;
    if (state === "active") state = "idle";
  };

  const onKey = (e: KeyboardEvent) => {
    if (!e.altKey || !e.shiftKey || e.ctrlKey || e.metaKey || e.code !== "KeyE") return;
    e.preventDefault();
    if (session) stop();
    else void start();
  };
  win.addEventListener("keydown", onKey, true);

  // `?glossa=edit`: once the page has loaded, so its runtimes exist.
  const onLoad = () => void start();
  const asked = new URLSearchParams(win.location.search).get("glossa") === "edit";
  if (asked) {
    if (doc.readyState === "complete") win.setTimeout(onLoad);
    else win.addEventListener("load", onLoad, { once: true });
  }

  return {
    get state() {
      return state;
    },
    get reason() {
      return reason;
    },
    start,
    stop,
    dispose() {
      stop();
      win.removeEventListener("keydown", onKey, true);
      win.removeEventListener("load", onLoad);
    },
  };
}
