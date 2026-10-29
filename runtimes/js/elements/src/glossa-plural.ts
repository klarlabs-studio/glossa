/**
 * `<glossa-plural key="…" count="3">`: a message selecting on `$count`
 * (`.input {$count :number} .match $count one {{…}} * {{…}}`, or the MF2 form
 * of a v0.3 `{count, plural, …}` message). Extra values go in `vars`.
 */
import type { PropertyDeclarations } from "lit";

import { varsProperty } from "./glossa-rich.js";
import { GlossaText } from "./glossa-text.js";

export class GlossaPlural extends GlossaText {
  static override properties: PropertyDeclarations = {
    count: { type: Number },
    vars: varsProperty,
  };

  public count = 0;
  public vars: Record<string, unknown> = {};

  protected override values(): Record<string, unknown> {
    return { ...this.vars, count: this.count };
  }
}

if (!customElements.get("glossa-plural")) customElements.define("glossa-plural", GlossaPlural);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-plural": GlossaPlural;
  }
}
