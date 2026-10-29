/**
 * The Lit context `<glossa-provider>` shares with every descendant element.
 * The provider publishes a new object on every change (activation, first
 * load settled, runtime swapped), since `@lit/context` notifies consumers on
 * reference change only.
 */
import { createContext } from "@lit/context";
import type { Runtime } from "@klarlabs-studio/glossa-runtime";

export interface GlossaContextValue {
  /** The provider's runtime; undefined while the provider isn't connected. */
  runtime: Runtime | undefined;
  /** The active locale, or `""` before a release is active. */
  locale: string;
  /** The active locale's direction. */
  dir: "ltr" | "rtl";
  /** The provider's `strict` flag. */
  strict: boolean;
  /** Whether the runtime's first load has settled. Before that, missing text is "pending". */
  ready: boolean;
  /** Ask the provider to switch locales (what `<glossa-selector>` does on a pick). */
  setLocale(locale: string): void;
}

export const glossaContext = createContext<GlossaContextValue>(Symbol("glossa"));
