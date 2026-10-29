/**
 * The fixture page the overlay is tested on, in jsdom and in Chromium: `t()`
 * strings in text and an attribute, a marked value inside another message,
 * two messages that read the same, a message the target locale lacks (it
 * falls back to the source) and a `<glossa-text>` component. Test-only; no
 * Node APIs, so the browser fixture bundles it.
 */
import type { Runtime } from "@klarlabs-studio/glossa-runtime";

export const PAGE_HTML = `<main>
  <h1>Checkout</h1>
  <p id="greeting"></p>
  <p><button id="pay" type="button"></button> <button id="save-1" type="button"></button> <button id="save-2" type="button"></button></p>
  <p><label>Search <input id="search" type="search"></label></p>
  <p id="promo"></p>
  <glossa-provider id="provider"><p id="component"><glossa-text key="cart.checkout">Checkout</glossa-text></p></glossa-provider>
</main>`;

/** Render the page's `t()` strings; call it again whenever the runtime notifies. */
export function renderPage(doc: Document, rt: Runtime): void {
  const $ = (id: string) => doc.getElementById(id)!;
  $("pay").textContent = rt.t("checkout.pay");
  $("greeting").textContent = rt.t("cart.greeting", { name: rt.t("user.name") });
  $("save-1").textContent = rt.t("profile.save");
  $("save-2").textContent = rt.t("settings.save");
  ($("search") as HTMLInputElement).placeholder = rt.t("search.placeholder");
  $("promo").textContent = rt.t("promo.banner");
  const provider = doc.getElementById("provider") as (HTMLElement & { runtime?: Runtime }) | null;
  if (provider && provider.runtime !== rt) provider.runtime = rt;
}
