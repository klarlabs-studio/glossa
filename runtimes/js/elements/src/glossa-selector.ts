/**
 * `<glossa-selector>`: a language picker for the nearest `<glossa-provider>`.
 *
 * It offers the active release's locales (the manifest's `locales`), each
 * labelled in its own language via `Intl.DisplayNames` ("Deutsch",
 * "English"). `locales` (comma-separated) and `labels` restrict or rename
 * them, as in v0.3. With no locales known yet it shows the current locale
 * read-only.
 *
 * A pick dispatches `glossa-locale-change` (`{ locale, source: "manual" }`,
 * bubbling, composed, cancelable) and then switches the provider, unless a
 * listener called `preventDefault()`. Persisting the choice (cookie, profile)
 * stays in application code. With `auto-detect`, a browser language that is
 * offered and differs from the current one is suggested once with
 * `source: "auto"`; suggestions never switch on their own.
 *
 * Attributes: `locales`, `labels`, `label` (accessible name, default
 * "Language"), `auto-detect`, `disabled`. The default slot replaces the
 * built-in `<select>`; slotted UIs dispatch their own `glossa-locale-change`.
 */
import { ContextConsumer } from "@lit/context";
import { LitElement, css, html, nothing } from "lit";

import { glossaContext } from "./context.js";

export interface GlossaLocaleChangeDetail {
  locale: string;
  source: "manual" | "auto";
}

const csv = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

/** A locale's name in its own language, e.g. "Deutsch" for `de`; the code if Intl can't name it. */
export function autonym(code: string): string {
  try {
    const name = new Intl.DisplayNames([code], { type: "language" }).of(code);
    return name ? name.charAt(0).toLocaleUpperCase(code) + name.slice(1) : code;
  } catch {
    return code;
  }
}

/**
 * The offered locale that best matches a browser language: an exact match,
 * else the first with the same primary language (`en-GB` → `en`).
 */
export function pickBestMatch(browser: string, available: string[]): string | undefined {
  const b = browser.toLowerCase();
  const primary = b.split("-")[0];
  return (
    available.find((c) => c.toLowerCase() === b) ??
    available.find((c) => c.toLowerCase().split("-")[0] === primary)
  );
}

export class GlossaSelector extends LitElement {
  static override styles = css`
    :host {
      display: inline-block;
    }
    select {
      font: inherit;
      color: inherit;
      background: inherit;
      padding: 0.25em 0.5em;
      border: 1px solid currentColor;
      border-radius: 0.25em;
    }
    select:focus-visible {
      outline: 2px solid currentColor;
      outline-offset: 2px;
    }
  `;

  static override properties = {
    locales: { type: String },
    labels: { type: String },
    label: { type: String },
    autoDetect: { type: Boolean, attribute: "auto-detect" },
    disabled: { type: Boolean },
  };

  public locales = "";
  public labels = "";
  public label = "Language";
  public autoDetect = false;
  public disabled = false;

  /** Where the browser language comes from; replaceable for tests. */
  public detectImpl: () => string | undefined = () =>
    typeof navigator === "undefined" ? undefined : navigator.languages?.[0] || navigator.language;

  private ctx = new ContextConsumer(this, { context: glossaContext, subscribe: true });
  private autoDetected = false;

  /** The offered locale codes: the `locales` attribute, else the active release's. */
  public get offered(): string[] {
    const explicit = csv(this.locales);
    if (explicit.length) return explicit;
    return (this.ctx.value?.runtime?.availableLocales ?? []).map((l) => l.code);
  }

  protected override updated(): void {
    this.maybeAutoDetect();
  }

  protected override render() {
    const current = this.ctx.value?.locale ?? "";
    const offered = this.offered;
    if (!offered.length) return html`<span aria-label=${this.label}>${current || nothing}</span>`;
    const labels = csv(this.labels);
    return html`<slot>
      <select
        aria-label=${this.label}
        ?disabled=${this.disabled}
        @change=${(e: Event) => this.pick((e.target as HTMLSelectElement).value, "manual")}
      >
        ${offered.map(
          (code, i) =>
            html`<option value=${code} lang=${code} .selected=${code === current}>
              ${labels[i] ?? autonym(code)}
            </option>`,
        )}
      </select>
    </slot>`;
  }

  /** Suggest the browser's language once, if it's offered and not already active. */
  public maybeAutoDetect(): void {
    const ctx = this.ctx.value;
    if (!this.autoDetect || this.autoDetected || !ctx?.ready || !ctx.locale) return;
    const browser = this.detectImpl();
    const offered = this.offered;
    if (!browser || !offered.length) return;
    this.autoDetected = true;
    const matched = pickBestMatch(browser, offered);
    if (matched && matched !== pickBestMatch(ctx.locale, offered)) this.pick(matched, "auto");
  }

  private pick(locale: string, source: GlossaLocaleChangeDetail["source"]): void {
    const go = this.dispatchEvent(
      new CustomEvent<GlossaLocaleChangeDetail>("glossa-locale-change", {
        detail: { locale, source },
        bubbles: true,
        composed: true,
        cancelable: true,
      }),
    );
    if (go && source === "manual") this.ctx.value?.setLocale(locale);
  }
}

if (!customElements.get("glossa-selector"))
  customElements.define("glossa-selector", GlossaSelector);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-selector": GlossaSelector;
  }
}
