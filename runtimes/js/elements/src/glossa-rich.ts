/**
 * `<glossa-rich key="…" vars='{"name":"Sophia"}'>`: a message with values.
 * `vars` is a JSON object in markup, or any object through the `.vars`
 * property. A value the message needs but doesn't get renders as its MF2
 * fallback (`{$name}`); it never throws.
 */
import type { PropertyDeclarations } from "lit";

import { GlossaText } from "./glossa-text.js";
import { parseVars } from "./parts.js";

export const varsProperty = { attribute: "vars", converter: { fromAttribute: parseVars } };

export class GlossaRich extends GlossaText {
  static override properties: PropertyDeclarations = { vars: varsProperty };

  public vars: Record<string, unknown> = {};

  protected override values(): Record<string, unknown> {
    return this.vars;
  }
}

if (!customElements.get("glossa-rich")) customElements.define("glossa-rich", GlossaRich);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-rich": GlossaRich;
  }
}
