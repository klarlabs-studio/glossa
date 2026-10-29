/**
 * `<glossa-provider>`: owns one runtime (runtimes/SPEC.md) for its subtree and
 * shares it with every `<glossa-*>` descendant through a Lit context.
 *
 * Attributes: `edge`, `delivery-key`, `environment`, `locale` (one tag or a
 * comma-separated preference list; default: the browser's), `public-keys`
 * (`keyId:key` pairs, comma-separated, or a JSON array of `{ keyId, key }`),
 * `strict` (console warnings for missing messages and load problems) and
 * `inspect` (Alt+click on a string dispatches `glossa-inspect`, the hook the
 * in-product editor attaches to).
 *
 * Properties: `runtime` (use this runtime instead of creating one; it's left
 * running on disconnect), `bundled` (a release shipped with the build, rendered
 * synchronously) and `options` (any other `createRuntime` option). A provider
 * with neither `edge` nor `runtime` uses `GlossaProvider.defaultRuntime` when
 * an integration sets it (`@klarlabs-studio/glossa-astro` shares the page's runtime this way).
 *
 * Events (bubbling, composed): `glossa-change` after every activation
 * (`{ locale, dir, release }`), `glossa-error` for each runtime error, and
 * `glossa-inspect` (`{ id, element, explanation }`).
 *
 * After each activation the host gets `lang` and `dir`, so the subtree is
 * announced and laid out in the active language.
 */
import { ContextProvider } from "@lit/context";
import { LitElement, css, html } from "lit";
import { createRuntime, navigatorLanguages } from "@klarlabs-studio/glossa-runtime";
import type {
  BundledRelease,
  PublicKey,
  Runtime,
  RuntimeError,
  RuntimeOptions,
} from "@klarlabs-studio/glossa-runtime";

import { glossaContext } from "./context.js";

const list = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

/** `k_2026a:BASE64URL,k_2026b:BASE64URL` or `[{"keyId":…,"key":…}]`. */
export function parsePublicKeys(s: string): PublicKey[] {
  if (s.trim().startsWith("[")) {
    try {
      const keys = JSON.parse(s) as PublicKey[];
      return Array.isArray(keys) ? keys.filter((k) => k && k.keyId && k.key) : [];
    } catch {
      return [];
    }
  }
  return list(s).flatMap((pair) => {
    const i = pair.indexOf(":");
    return i > 0 ? [{ keyId: pair.slice(0, i), key: pair.slice(i + 1) }] : [];
  });
}

const V03 = ["project", "api-url", "api-key"];

const CONFIG = [
  "edge",
  "deliveryKey",
  "environment",
  "publicKeys",
  "bundled",
  "options",
  "runtime",
];

export class GlossaProvider extends LitElement {
  static override styles = css`
    :host {
      display: contents;
    }
  `;

  static override properties = {
    edge: { type: String },
    deliveryKey: { type: String, attribute: "delivery-key" },
    environment: { type: String },
    locale: { type: String },
    publicKeys: { type: String, attribute: "public-keys" },
    strict: { type: Boolean },
    inspect: { type: Boolean },
    bundled: { attribute: false },
    options: { attribute: false },
  };

  public edge = "";
  public deliveryKey = "";
  public environment = "";
  public locale = "";
  public publicKeys = "";
  public strict = false;
  public inspect = false;
  public bundled: BundledRelease | undefined;
  public options: RuntimeOptions | undefined;

  /**
   * The runtime for providers configured with neither `edge`, `bundled` nor
   * `runtime`, e.g. one page-wide runtime shared with framework islands. It's
   * never disposed by a provider.
   */
  public static defaultRuntime: (() => Runtime | undefined) | undefined;

  private external: Runtime | undefined;
  private active: Runtime | undefined;
  private owned = false;
  private booted = false;
  private isReady = false;
  private unsubscribe: Array<() => void> = [];

  private context = new ContextProvider(this, {
    context: glossaContext,
    initialValue: {
      runtime: undefined,
      locale: "",
      dir: "ltr",
      strict: false,
      ready: false,
      setLocale: (l) => (this.locale = l),
    },
  });

  constructor() {
    super();
    this.addEventListener("click", (e) => this.onClick(e));
  }

  /** The runtime this provider renders with. Set it to share a runtime created elsewhere. */
  public get runtime(): Runtime | undefined {
    return this.active;
  }

  public set runtime(rt: Runtime | undefined) {
    const old = this.external;
    this.external = rt;
    this.requestUpdate("runtime", old);
  }

  public override connectedCallback(): void {
    super.connectedCallback();
    if (!this.booted && this.hasUpdated) this.requestUpdate();
  }

  public override disconnectedCallback(): void {
    super.disconnectedCallback();
    this.teardown();
  }

  protected override willUpdate(changed: Map<PropertyKey, unknown>): void {
    if (!this.booted || CONFIG.some((k) => changed.has(k))) this.boot();
    else if (changed.has("locale")) {
      void this.active?.setLocales(this.locale ? list(this.locale) : (navigatorLanguages() ?? []));
    } else if (changed.has("strict")) this.publish();
  }

  protected override render() {
    return html`<slot></slot>`;
  }

  private boot(): void {
    this.teardown();
    this.booted = true;
    const o: RuntimeOptions = { ...this.options };
    if (this.edge) o.edge = this.edge;
    if (this.deliveryKey) o.deliveryKey = this.deliveryKey;
    if (this.environment) o.environment = this.environment;
    if (this.publicKeys) o.publicKeys = parsePublicKeys(this.publicKeys);
    if (this.locale) o.locales = list(this.locale);
    if (this.bundled) o.bundled = this.bundled;
    const shared =
      this.external ?? (o.edge || o.bundled ? undefined : GlossaProvider.defaultRuntime?.());
    const rt = shared ?? createRuntime(o);
    this.active = rt;
    this.owned = !shared;
    this.isReady = false;
    this.unsubscribe = [
      rt.subscribe(() => {
        this.publish();
        this.announce();
      }),
      rt.onError((e) => this.report(e)),
    ];
    if (this.strict && !o.edge && !shared && V03.some((a) => this.hasAttribute(a))) {
      console.warn(
        "[glossa] <glossa-provider> ignores the v0.3 attributes project, api-url and api-key; " +
          "set edge and delivery-key instead (see @klarlabs-studio/glossa-elements MIGRATION.md).",
      );
    }
    void rt.ready.then(() => {
      if (this.active !== rt) return;
      this.isReady = true;
      this.publish();
      if (this.strict && !rt.release) {
        console.warn("[glossa] no release is active; rendering inline defaults.");
      }
    });
    this.publish();
    if (rt.release) this.announce();
  }

  private teardown(): void {
    for (const off of this.unsubscribe) off();
    this.unsubscribe = [];
    if (this.owned) this.active?.dispose();
    this.active = undefined;
    this.owned = false;
    this.booted = false;
  }

  private publish(): void {
    const rt = this.active;
    if (rt?.locale) {
      this.lang = rt.locale;
      this.dir = rt.dir;
    }
    this.context.setValue({
      runtime: rt,
      locale: rt?.locale ?? "",
      dir: rt?.dir ?? "ltr",
      strict: this.strict,
      ready: this.isReady,
      setLocale: (l) => (this.locale = l),
    });
  }

  private emit(type: string, detail: unknown): void {
    this.dispatchEvent(new CustomEvent(type, { detail, bubbles: true, composed: true }));
  }

  private announce(): void {
    const rt = this.active;
    if (rt) this.emit("glossa-change", { locale: rt.locale, dir: rt.dir, release: rt.release });
  }

  private report(e: RuntimeError): void {
    this.emit("glossa-error", e);
    if (this.strict) {
      console.warn(`[glossa] ${e.type}${e.messageId ? ` "${e.messageId}"` : ""}: ${e.detail}`);
    }
  }

  /** Alt+click on a `<glossa-*>` string with `inspect` on: tell the in-product editor. */
  private onClick(e: MouseEvent): void {
    const rt = this.active;
    if (!this.inspect || !e.altKey || !rt) return;
    const el = e
      .composedPath()
      .find(
        (n): n is HTMLElement =>
          n instanceof HTMLElement &&
          n !== this &&
          n.localName.startsWith("glossa-") &&
          (n.hasAttribute("key") || n.hasAttribute("message")),
      );
    if (!el) return;
    e.preventDefault();
    const id = el.getAttribute("key") || el.getAttribute("message")!;
    this.emit("glossa-inspect", { id, element: el, explanation: rt.explain(id) });
  }
}

if (!customElements.get("glossa-provider"))
  customElements.define("glossa-provider", GlossaProvider);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-provider": GlossaProvider;
  }
}
