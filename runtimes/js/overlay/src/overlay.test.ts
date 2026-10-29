/**
 * The overlay against a fake API (src/testing/fake-api.ts), whose requests
 * and answers are checked against platform/api/openapi.yaml: opening on an
 * Alt+click, editing with server validation and live preview, saving as a
 * revision with in-context provenance, conflicts, QA failures, history,
 * terminology, AI suggestions, keyboard use and ending the session.
 */
import { afterEach, describe, expect, it } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { Runtime } from "@klarlabs-studio/glossa-runtime";
import { strip } from "@klarlabs-studio/glossa-capture";
import "@klarlabs-studio/glossa-elements";

import { activate } from "./activate.js";
import type { Overlay } from "./activate.js";
import { openapiValidator } from "./testing/contract.js";
import { FakeApi, PROJECT, TENANT, TOKEN } from "./testing/fake-api.js";
import { PAGE_HTML, renderPage } from "./testing/page.js";
import { fixture, seed } from "./testing/release.js";

const validator = openapiValidator();
let current: { fake: FakeApi; overlay: Overlay } | undefined;

afterEach(() => {
  const c = current;
  current = undefined;
  c?.overlay.deactivate();
  document.body.innerHTML = "";
  expect(c?.fake.violations ?? []).toEqual([]);
});

function setup(o: { token?: () => string } = {}) {
  const fake = new FakeApi(validator);
  seed(fake);
  const rt: Runtime = createRuntime({
    bundled: fixture,
    locales: "de",
    environment: "preview",
    storage: null,
  });
  document.body.innerHTML = PAGE_HTML;
  rt.subscribe(() => renderPage(document, rt));
  renderPage(document, rt);
  const overlay = activate({
    apiBase: "https://api.glossa.test/",
    token: o.token ?? (() => TOKEN),
    tenant: TENANT,
    project: PROJECT,
    locale: "de",
    runtimes: rt,
    fetch: fake.fetch,
    route: "/checkout",
    studioBase: "https://studio.glossa.test",
  });
  overlay.element.host!.wait = async () => {};
  current = { fake, overlay };
  const root = () => overlay.element.shadowRoot!;
  const q = <T extends Element = HTMLElement>(sel: string) => root().querySelector<T>(sel);
  return { fake, rt, overlay, root, q };
}

/** Poll until `check` holds (Lit renders and the fake answers asynchronously). */
async function until(check: () => unknown, what = "condition"): Promise<void> {
  for (let i = 0; i < 200; i++) {
    try {
      if (check()) return;
    } catch {
      // not yet
    }
    await new Promise((r) => setTimeout(r, 10));
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** A recorded request's body, which the fake keeps as it was sent. */
const sent = (r: { body?: string }): Record<string, unknown> => JSON.parse(r.body ?? "{}");

const altClick = (el: Element, init: MouseEventInit = {}) =>
  el.dispatchEvent(
    new MouseEvent("click", {
      altKey: true,
      bubbles: true,
      composed: true,
      cancelable: true,
      ...init,
    }),
  );

/** What an element shows, without markers and bidi isolates. */
const text = (id: string) =>
  strip(document.getElementById(id)!.textContent ?? "").replace(/[\u2066-\u2069]/g, "");

function type(area: HTMLTextAreaElement, value: string): void {
  area.value = value;
  area.dispatchEvent(new Event("input", { bubbles: true, composed: true }));
}

const buttonNamed = (root: ShadowRoot, name: string) =>
  Array.from(root.querySelectorAll("button")).find((b) => b.textContent?.trim().startsWith(name));

async function openOn(t: ReturnType<typeof setup>, id: string): Promise<HTMLTextAreaElement> {
  altClick(document.getElementById(id)!);
  await until(() => t.q("#draft"), "the editor");
  return t.q<HTMLTextAreaElement>("#draft")!;
}

describe("opening", () => {
  it("Alt+click on a marked t() string opens its source and translation", async () => {
    const t = setup();
    const pay = document.getElementById("pay")!;
    pay.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(t.overlay.element.isOpen).toBe(false);

    expect(altClick(pay)).toBe(false); // default prevented: a link wouldn't navigate
    const draft = await openOn(t, "pay");
    expect(draft.value).toBe("Jetzt zahlen");
    expect(t.q("h2")!.textContent).toContain("checkout.pay");
    expect(t.q(".text")!.textContent).toBe("Pay now");
    expect(t.q("[role=dialog]")!.getAttribute("aria-modal")).toBe("true");

    const req = t.fake.requests.find((r) => r.url.includes("/translations/de"))!;
    expect(req.url).toBe(
      `https://api.glossa.test/v1/tenants/${TENANT}/projects/${PROJECT}/messages/checkout.pay/translations/de`,
    );
    expect(req.headers.authorization ?? req.headers.Authorization).toBe(`Bearer ${TOKEN}`);
  });

  it("finds a marked attribute, a component host, and a message the locale lacks", async () => {
    const t = setup();
    altClick(document.getElementById("search")!);
    await until(() => t.q<HTMLTextAreaElement>("#draft")?.value === "Suchen …", "the placeholder");

    const host = document.querySelector("glossa-text")!;
    await until(() => host.getAttribute("data-glossa-id") === "cart.checkout", "host marks");
    altClick(host);
    await until(() => t.q<HTMLTextAreaElement>("#draft")?.value === "Zur Kasse", "the component");

    altClick(document.getElementById("promo")!);
    await until(() => t.q("h2")?.textContent?.includes("promo.banner"), "the fallback message");
    await until(() => t.q("#draft"));
    expect(t.q<HTMLTextAreaElement>("#draft")!.value).toBe("");
    expect(t.q("#where")!.textContent).toContain("In de. The page shows the en fallback");
    expect(t.root().textContent).toContain("Not translated yet.");
  });

  it("links to the message in Studio", async () => {
    const t = setup();
    await openOn(t, "pay");
    const link = t.q<HTMLAnchorElement>("a.button")!;
    expect(link.href).toBe(
      `https://studio.glossa.test/t/${TENANT}/p/${PROJECT}/translate?locale=de&key=checkout.pay`,
    );
    expect(link.rel).toContain("noopener");
  });

  it("says when the session has expired", async () => {
    const t = setup({ token: () => "expired" });
    altClick(document.getElementById("pay")!);
    await until(() => t.q("[role=alert]"), "an alert");
    expect(t.q("[role=alert]")!.textContent).toContain("session has expired");
  });
});

describe("editing", () => {
  it("validates on the server, previews live, and saves a human revision with in-context provenance", async () => {
    const t = setup();
    const draft = await openOn(t, "pay");
    type(draft, "Sofort bezahlen");
    await until(() => t.q("#check .ok"), "a valid parse");
    expect(text("pay")).toBe("Sofort bezahlen"); // live preview through override
    expect(text("save-1")).toBe("Speichern");

    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.root().textContent?.includes("Saved as revision 2"), "the save");
    const put = t.fake.requests.find((r) => r.method === "PUT")!;
    expect(put.headers["if-match"] ?? put.headers["If-Match"]).toBe('"r1"');
    const rev = t.fake.revisions("checkout.pay", "de").at(-1)!;
    expect(rev.origin).toBe("human");
    expect(rev.origin_detail).toEqual({
      in_context: { route: "/checkout", viewport: { width: innerWidth, height: innerHeight } },
    });

    // Closing keeps what was saved on the page.
    t.q("[role=dialog]")!.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    );
    await until(() => !t.q("[role=dialog]"), "closing");
    expect(text("pay")).toBe("Sofort bezahlen");
  });

  it("closing without saving drops the live preview", async () => {
    const t = setup();
    const draft = await openOn(t, "pay");
    type(draft, "Vorschau");
    await until(() => text("pay") === "Vorschau", "the preview");
    t.overlay.element.close();
    expect(text("pay")).toBe("Jetzt zahlen");
  });

  it("shows parse errors inline and doesn't save an invalid draft", async () => {
    const t = setup();
    const draft = await openOn(t, "greeting");
    expect(draft.value).toBe("Hallo {$name}!");
    type(draft, "Hallo {$name");
    await until(() => t.q("#check .error"), "the parse error");
    expect(t.q("#check")!.textContent).toContain("syntax-error");
    expect(draft.getAttribute("aria-invalid")).toBe("true");
    expect(t.q<HTMLButtonElement>("button[type=submit]")!.disabled).toBe(true);
    expect(text("greeting")).toBe("Hallo Lina!");
    expect(t.fake.requests.some((r) => r.method === "PUT")).toBe(false);
  });

  it("keeps the draft on a conflict (412) and saves over the latest version after loading it", async () => {
    const t = setup();
    const draft = await openOn(t, "pay");
    t.fake.translate("checkout.pay", "de", "Jetzt sofort zahlen"); // someone else, meanwhile
    type(draft, "Bezahlen");
    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.q("[role=alert]"), "the conflict");
    expect(t.q("[role=alert]")!.textContent).toContain("Someone changed this translation");
    expect(t.q<HTMLTextAreaElement>("#draft")!.value).toBe("Bezahlen");

    buttonNamed(t.root(), "Load latest version")!.click();
    await until(() => t.root().textContent?.includes("Loaded revision 2"), "the latest version");
    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.root().textContent?.includes("Saved as revision 3"), "the second save");
    expect(t.fake.translation("checkout.pay", "de")!.text).toBe("Bezahlen");
  });

  it("lists structural QA findings when the server rejects the text (422)", async () => {
    const t = setup();
    const draft = await openOn(t, "greeting");
    type(draft, "Hallo!");
    await until(() => t.q("#check .ok"));
    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.root().textContent?.includes("missing-argument"), "the findings");
    expect(t.q("[role=alert]")!.textContent).toContain("doesn't fit its source");
  });

  it("creates the first translation without If-Match", async () => {
    const t = setup();
    const draft = await openOn(t, "promo");
    type(draft, "Diese Woche versandkostenfrei");
    const submit = t.q<HTMLButtonElement>("button[type=submit]")!;
    await until(() => !submit.disabled, "an enabled Save");
    submit.click();
    await until(() => t.root().textContent?.includes("Saved as revision 1"), "the save");
    const put = t.fake.requests.find((r) => r.method === "PUT")!;
    expect(put.headers["if-match"] ?? put.headers["If-Match"]).toBeUndefined();
    expect(text("promo")).toBe("Diese Woche versandkostenfrei");
  });
});

describe("history and terminology", () => {
  it("shows the revision history, in-context edits included", async () => {
    const t = setup();
    const draft = await openOn(t, "pay");
    type(draft, "Zahlen");
    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.root().textContent?.includes("Saved as revision 2"));
    const toggle = buttonNamed(t.root(), "History")!;
    toggle.click();
    await until(() => t.q("#section-history li"), "the history");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const items = t.root().querySelectorAll("#section-history li");
    expect(items).toHaveLength(2);
    expect(items[0]!.textContent).toContain("edited in context on /checkout");
    expect(items[1]!.textContent).toContain("Jetzt zahlen");
  });

  it("shows terminology findings for this translation", async () => {
    const t = setup();
    t.fake.termFindings("checkout.pay", "de", [
      {
        code: "term_forbidden",
        severity: "warning",
        concept_id: "c1",
        term_id: "t1",
        side: "target",
        start: 6,
        end: 12,
        text: "zahlen",
        suggestions: ["bezahlen"],
        message: "Use “bezahlen”, not “zahlen”.",
      },
    ]);
    await openOn(t, "pay");
    buttonNamed(t.root(), "Terminology")!.click();
    await until(() => t.q("#section-terms li"), "the findings");
    expect(t.q("#section-terms")!.textContent).toContain("Use: bezahlen");
  });
});

describe("AI suggestions", () => {
  it("requests a suggestion, puts it in the editor and accepts it with the edits", async () => {
    const t = setup();
    t.fake.aiDraft("promo.banner", "de", "Kostenloser Versand diese Woche");
    const draft = await openOn(t, "promo");
    buttonNamed(t.root(), "Ask AI")!.click();
    await until(() => buttonNamed(t.root(), "Request a suggestion"), "the fill preview");
    expect(t.q("#section-ai")!.textContent).toContain("AI provider");
    buttonNamed(t.root(), "Request a suggestion")!.click();
    await until(() => buttonNamed(t.root(), "Use in editor"), "the suggestion");
    expect(t.q("#section-ai")!.textContent).toContain("Confidence 82%");

    buttonNamed(t.root(), "Use in editor")!.click();
    await until(() => draft.value === "Kostenloser Versand diese Woche");
    expect(text("promo")).toBe("Kostenloser Versand diese Woche");
    type(draft, "Kostenloser Versand – nur diese Woche");
    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.root().textContent?.includes("Saved as revision 1"), "the acceptance");
    const tr = t.fake.translation("promo.banner", "de")!;
    expect(tr.origin).toBe("ai");
    expect(tr.text).toBe("Kostenloser Versand – nur diese Woche");
    expect(t.fake.revisions("promo.banner", "de")[0]!.origin_detail).toMatchObject({
      edited: true,
    });
    expect(text("promo")).toBe("Kostenloser Versand – nur diese Woche");
  });

  it("shows a pending suggestion right away", async () => {
    const t = setup();
    t.fake.suggestion("checkout.pay", "de", "Jetzt bezahlen");
    await openOn(t, "pay");
    buttonNamed(t.root(), "Ask AI")!.click();
    await until(() => t.q("#section-ai")?.textContent?.includes("Jetzt bezahlen"));
    expect(t.fake.requests.some((r) => r.url.includes("ai-fill"))).toBe(false);
  });

  // The message someone is reading is current by definition, so asking
  // about it is the editor's normal case, not its refused one
  // (RFC 0004 5.3). Both the preview and the fill say `force`.
  it("asks for a second opinion on a translation that is already current", async () => {
    const t = setup();
    t.fake.aiDraft("checkout.pay", "de", "Jetzt kaufen");
    const draft = await openOn(t, "pay");
    buttonNamed(t.root(), "Ask AI")!.click();
    await until(() => buttonNamed(t.root(), "Request a suggestion"), "the fill preview");

    const preview = t.fake.requests.find((r) => r.url.includes("/ai-fill-previews"))!;
    expect(sent(preview)).toMatchObject({ force: true });

    buttonNamed(t.root(), "Request a suggestion")!.click();
    await until(() => buttonNamed(t.root(), "Use in editor"), "the suggestion");
    const fill = t.fake.requests.find((r) => r.url.endsWith("/ai-fills"))!;
    expect(sent(fill)).toMatchObject({ force: true });

    buttonNamed(t.root(), "Use in editor")!.click();
    await until(() => draft.value === "Jetzt kaufen");
  });

  // Accepting in the running product records where the person was, as a
  // plain edit already did (RFC 0004 5.3).
  it("records the route and viewport when a suggestion is accepted", async () => {
    const t = setup();
    t.fake.suggestion("checkout.pay", "de", "Jetzt bezahlen!");
    await openOn(t, "pay");
    buttonNamed(t.root(), "Ask AI")!.click();
    await until(() => buttonNamed(t.root(), "Use in editor"), "the pending suggestion");
    buttonNamed(t.root(), "Use in editor")!.click();
    await until(() => t.q<HTMLTextAreaElement>("#draft")!.value === "Jetzt bezahlen!");
    t.q<HTMLButtonElement>("button[type=submit]")!.click();
    await until(() => t.root().textContent?.includes("Saved as revision"), "the acceptance");

    const accepted = t.fake.requests.find((r) => r.url.includes("/acceptance"))!;
    expect(sent(accepted)).toMatchObject({
      in_context: { route: "/checkout", viewport: { width: expect.any(Number), height: expect.any(Number) } },
    });
    // checkout.pay already had a translation, so the acceptance is the
    // newest revision, not the first.
    const revisions = t.fake.revisions("checkout.pay", "de");
    expect(revisions.at(-1)!.origin_detail).toMatchObject({ in_context: { route: "/checkout" } });
  });

  // The fill names its job, so a job that ends without a suggestion says
  // why instead of the panel timing out into "no suggestion yet".
  it("says why when the fill's job ends without a suggestion", async () => {
    const t = setup();
    t.fake.jobEnding = "failed";
    await openOn(t, "pay");
    buttonNamed(t.root(), "Ask AI")!.click();
    await until(() => buttonNamed(t.root(), "Request a suggestion"), "the fill preview");
    buttonNamed(t.root(), "Request a suggestion")!.click();
    await until(
      () => t.q("#section-ai")?.textContent?.includes("provider didn't answer"),
      "the job's ending",
    );
    expect(t.fake.requests.some((r) => r.url.includes("/ai-jobs/"))).toBe(true);
  });
});

describe("keyboard", () => {
  it("traps Tab inside the panel, closes on Esc and returns focus", async () => {
    const t = setup();
    const pay = document.getElementById("pay")!;
    pay.focus();
    await openOn(t, "pay");
    await until(() => t.root().activeElement?.id === "draft", "focus in the editor");

    const dialog = t.q("[role=dialog]")!;
    const focusables = Array.from(
      t.root().querySelectorAll<HTMLElement>("a[href], button:not([disabled]), textarea, select"),
    );
    focusables.at(-1)!.focus();
    dialog.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }),
    );
    expect(t.root().activeElement).toBe(focusables[0]);
    dialog.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }),
    );
    expect(t.root().activeElement).toBe(focusables.at(-1));

    dialog.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await until(() => !t.q("[role=dialog]"));
    expect(document.activeElement).toBe(pay);
  });

  it("opens with Alt+Enter on a focused element that shows a message", async () => {
    const t = setup();
    const search = document.getElementById("search")!;
    search.focus();
    search.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", altKey: true, bubbles: true }),
    );
    await until(() => t.q<HTMLTextAreaElement>("#draft")?.value === "Suchen …");
  });

  it("labels every control", async () => {
    const t = setup();
    await openOn(t, "pay");
    for (const el of Array.from(t.root().querySelectorAll("textarea, select"))) {
      expect(t.q(`label[for="${el.id}"]`), el.id).not.toBeNull();
    }
    expect(t.q("button.close")!.getAttribute("aria-label")).toBe("Close editor");
  });
});

describe("ending the session", () => {
  it("removes the panel, the listeners, the markers and the previews", async () => {
    const t = setup();
    const draft = await openOn(t, "pay");
    type(draft, "Vorschau");
    await until(() => text("pay") === "Vorschau");
    t.overlay.deactivate();
    expect(document.querySelector("glossa-overlay")).toBeNull();
    expect(document.getElementById("pay")!.textContent).toBe("Jetzt zahlen");
    expect(t.rt.hooked).toBe(false);
    altClick(document.getElementById("pay")!);
    expect(t.overlay.element.isOpen).toBe(false);
  });
});
