/**
 * `<glossa-text key="…">Inline default</glossa-text>`: renders message `key`
 * with the nearest `<glossa-provider>`'s runtime. Inside Vue templates, where
 * `key` is reserved and never reaches the DOM, use `message="…"` instead. The slot content is the
 * inline default (runtimes/SPEC.md §3, step 5): it shows while the first load
 * is pending and whenever no locale of the active chain has the message, so
 * the page never shows a blank.
 *
 * The translation renders into the shadow root, which inherits the host's
 * text styles (`display: contents`, so a `<button>` containing it still
 * announces as a button). Safe MF2 markup (`{#b}…{/b}`) becomes elements;
 * nothing from a translation is ever parsed as HTML.
 *
 * State attributes, for styling: `data-glossa-pending` (+ `aria-busy`) until
 * the first load settles, then `data-glossa-missing` when the inline default
 * renders. `<glossa-rich>`, `<glossa-plural>` and `<glossa-select>` extend
 * this element and only add values.
 *
 * Capture mode (RFC 0004 §3.1): while a capture or editor session has an
 * `onRender` hook installed on the runtime, the host also carries
 * `data-glossa-id` and `data-glossa-locale`; never in a normal page view.
 */
import { ContextConsumer } from "@lit/context";
import { LitElement, css, html } from "lit";
import type { PropertyDeclarations } from "lit";

import { glossaContext } from "./context.js";
import { markAttributes, partsToTree, resolveParts } from "./parts.js";
import type { TreeNode } from "./parts.js";

const toDom = (nodes: TreeNode[]): Array<Node | string> =>
  nodes.map((n) => {
    if (typeof n === "string") return n;
    const el = document.createElement(n.tag);
    el.append(...toDom(n.children));
    return el;
  });

const MARKS = ["data-glossa-id", "data-glossa-locale"];

export class GlossaText extends LitElement {
  static override styles = css`
    :host {
      display: contents;
    }
    :host([data-glossa-pending]) ::slotted(*) {
      opacity: 0.85;
    }
    :host([data-glossa-missing]) ::slotted(*) {
      outline: 1px dotted currentColor;
      outline-offset: 2px;
    }
  `;

  static override properties: PropertyDeclarations = {
    key: { type: String },
    message: { type: String },
  };

  /** The message ID. */
  public key = "";
  /** The message ID, for templates that reserve `key` (Vue). `key` wins when both are set. */
  public message = "";

  protected ctx = new ContextConsumer(this, { context: glossaContext, subscribe: true });

  /** The values the message is formatted with. */
  protected values(): Record<string, unknown> | undefined {
    return undefined;
  }

  protected override render() {
    const ctx = this.ctx.value;
    const id = this.key || this.message;
    const parts = ctx?.runtime && id ? resolveParts(ctx.runtime, id, this.values()) : undefined;
    const pending = !parts && !ctx?.ready;
    this.toggleAttribute("data-glossa-pending", pending);
    this.toggleAttribute("data-glossa-missing", !parts && !pending);
    if (pending) this.setAttribute("aria-busy", "true");
    else this.removeAttribute("aria-busy");
    const mark = ctx?.runtime && id ? markAttributes(ctx.runtime, id) : undefined;
    for (const name of MARKS) {
      const v = mark?.[name];
      if (v) this.setAttribute(name, v);
      else this.removeAttribute(name);
    }
    if (!parts) return html`<slot></slot>`;
    const tree = partsToTree(parts);
    return tree.every((n) => typeof n === "string") ? tree.join("") : toDom(tree);
  }
}

if (!customElements.get("glossa-text")) customElements.define("glossa-text", GlossaText);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-text": GlossaText;
  }
}
