/**
 * `<glossa-select key="…" value="female">`: a message selecting on a string
 * value, passed as `$value` or under the variable named by `name`
 * (`.input {$value :string} .match $value female {{…}} * {{…}}`). Extra
 * values go in `vars`.
 */
import type { PropertyDeclarations } from "lit";

import { varsProperty } from "./glossa-rich.js";
import { GlossaText } from "./glossa-text.js";

export class GlossaSelect extends GlossaText {
  static override properties: PropertyDeclarations = {
    value: { type: String },
    name: { type: String },
    vars: varsProperty,
  };

  public value = "";
  /** The variable the message selects on. */
  public name = "value";
  public vars: Record<string, unknown> = {};

  protected override values(): Record<string, unknown> {
    return { ...this.vars, [this.name || "value"]: this.value };
  }
}

if (!customElements.get("glossa-select")) customElements.define("glossa-select", GlossaSelect);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-select": GlossaSelect;
  }
}
