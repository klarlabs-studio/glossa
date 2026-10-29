import { afterEach, describe, expect, it, vi } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { RuntimeError, RuntimeOptions } from "@klarlabs-studio/glossa-runtime";

import "./index.js";
import { GlossaProvider } from "./glossa-provider.js";
import { DELIVERY_KEY, EDGE, fakeEdge, match, msg, release, text } from "./testing/release.js";
import type { TestRelease } from "./testing/release.js";

const r1 = release(
  "rel_1",
  1,
  {
    de: {
      "cart.checkout": text("Zur Kasse"),
      "athlete.greeting": msg("Hallo, ", { $: "name" }, "!"),
      "athlete.session_count": match("count", "number", {
        "0": ["keine Einheiten"],
        one: ["eine Einheit"],
        "*": [{ $: "count" }, " Einheiten"],
      }),
      "user.gender": match("value", "string", { male: ["Er"], "*": ["Sie"] }),
      "user.role": match("role", "string", { coach: ["Trainerin"], "*": ["Mitglied"] }),
      "terms.hint": msg("Lies die ", { open: "b" }, "AGB", { close: "b" }, "."),
      "evil.html": text('<img src=x onerror="alert(1)">'),
    },
    en: { "cart.checkout": text("Checkout") },
    ar: { "cart.checkout": text("الدفع") },
  },
  { directions: { ar: "rtl" }, fallback: { "*": ["de"] } },
);

const edgeAttrs = `edge="${EDGE}" delivery-key="${DELIVERY_KEY}"`;

/** Options every test provider gets: the fake edge, no persistence, no timers, no isolates. */
const testOptions = (serve: () => TestRelease | undefined = () => r1): RuntimeOptions => ({
  transport: fakeEdge(serve).transport,
  storage: null,
  refreshInterval: 0,
  bidiIsolation: "none",
});

/**
 * Let the runtime load (WebCrypto hashing is async) and Lit re-render.
 *
 * The five event-loop turns below are a floor for the runtime's async
 * load, not the whole wait. Lit renders on its own queue, so a fixed
 * number of turns is enough on an idle machine and not always enough on
 * a loaded CI runner — which is what made this suite flake twice, once
 * on a teardown assertion and once reading a component's shadow root
 * before it had rendered. So after the floor we wait for the condition
 * itself: every component's own `updateComplete`, repeated until a round
 * reports that nothing re-rendered. `updateComplete` resolves false when
 * the update queued another one, which is exactly the signal to wait
 * again.
 */
async function settle(firstLoad = true): Promise<void> {
  const rendering = () =>
    Array.from(document.querySelectorAll("*")).filter(
      (e): e is Element & { updateComplete: Promise<boolean> } => "updateComplete" in e,
    );
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  // Every provider's first load, too: what a text renders after it — a
  // translation, or in strict mode the missing-key warning — comes from
  // the provider publishing once its runtime is ready, which is a fetch
  // from the test edge. Waiting only for the components to stop
  // re-rendering returned early whenever their first render, still
  // pending, finished before that fetch did; CI's "warns about missing
  // keys in strict mode" failed that way while the same code passed the
  // run before.
  if (firstLoad) {
    const providers = Array.from(document.querySelectorAll("glossa-provider")) as GlossaProvider[];
    await Promise.all(providers.map((p) => p.runtime?.ready.catch(() => {})));
  }
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  for (let round = 0; round < 100; round++) {
    if ((await Promise.all(rendering().map((e) => e.updateComplete))).every(Boolean)) return;
    await new Promise((r) => setTimeout(r, 0));
  }
  throw new Error("settle: the components never stopped re-rendering");
}

async function mount(
  markup: string,
  configure: (p: GlossaProvider) => void = (p) => (p.options = testOptions()),
  // false for a test about the state before the first load settles,
  // whose transport never answers: it says so rather than racing a
  // timeout, which is how this suite became flaky in the first place.
  firstLoad = true,
): Promise<GlossaProvider> {
  const container = document.createElement("div");
  container.innerHTML = markup;
  const provider = container.querySelector("glossa-provider")!;
  configure(provider);
  document.body.append(container);
  await settle(firstLoad);
  return provider;
}

/** Shadow-DOM text of `el`, without Lit's comment markers and the <style> jsdom inlines. */
function rendered(el: Element): string {
  let out = "";
  for (const node of Array.from(el.shadowRoot?.childNodes ?? [])) {
    if (node.nodeType === Node.COMMENT_NODE || (node as Element).tagName === "STYLE") continue;
    out += node.textContent ?? "";
  }
  return out.trim();
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("<glossa-provider> + <glossa-text>", () => {
  it("renders the translation from the edge configured on the provider", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="cart.checkout">Approve</glossa-text></glossa-provider>`,
    );
    expect(rendered(p.querySelector("glossa-text")!)).toBe("Zur Kasse");
    expect(p.runtime?.release?.id).toBe("rel_1");
  });

  it("renders the slot content, the inline default, when the key is missing", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="no.such.key">Default <b>Label</b></glossa-text></glossa-provider>`,
    );
    const el = p.querySelector("glossa-text")!;
    expect(el.shadowRoot!.querySelector("slot")).not.toBeNull();
    expect(el.textContent).toBe("Default Label");
    expect(el.hasAttribute("data-glossa-missing")).toBe(true);
  });

  it("marks elements pending until the first load settles", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="cart.checkout">Zur Kasse</glossa-text></glossa-provider>`,
      (p) => (p.options = { ...testOptions(), transport: () => new Promise(() => {}) }),
      false,
    );
    const el = p.querySelector("glossa-text")!;
    expect(el.getAttribute("data-glossa-pending")).toBe("");
    expect(el.getAttribute("aria-busy")).toBe("true");
    expect(el.shadowRoot!.querySelector("slot")).not.toBeNull();
  });

  it("takes the message ID from message= where a template reserves key (Vue)", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text message="cart.checkout">…</glossa-text></glossa-provider>`,
    );
    expect(rendered(p.querySelector("glossa-text")!)).toBe("Zur Kasse");
  });

  it("renders a bundled release synchronously, before any request settles", async () => {
    const p = await mount(
      `<glossa-provider locale="de"><glossa-text key="cart.checkout">…</glossa-text></glossa-provider>`,
      (p) => (p.bundled = r1),
    );
    const el = p.querySelector("glossa-text")!;
    expect(rendered(el)).toBe("Zur Kasse");
    expect(el.hasAttribute("data-glossa-pending")).toBe(false);
  });

  it("switches every string when the locale changes, and sets lang and dir on the host", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="cart.checkout">…</glossa-text></glossa-provider>`,
    );
    const el = p.querySelector("glossa-text")!;
    expect([rendered(el), p.lang, p.dir]).toEqual(["Zur Kasse", "de", "ltr"]);
    p.setAttribute("locale", "en");
    await settle();
    expect([rendered(el), p.lang, p.dir]).toEqual(["Checkout", "en", "ltr"]);
    p.locale = "ar-EG";
    await settle();
    expect([rendered(el), p.lang, p.dir]).toEqual(["الدفع", "ar", "rtl"]);
  });

  it("picks up a new release when the runtime refreshes", async () => {
    let current = r1;
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="cart.checkout">…</glossa-text></glossa-provider>`,
      (p) => (p.options = testOptions(() => current)),
    );
    current = release("rel_2", 2, { de: { "cart.checkout": text("Zur Kasse (neu)") } });
    await p.runtime!.refresh();
    await settle();
    expect(rendered(p.querySelector("glossa-text")!)).toBe("Zur Kasse (neu)");
  });

  it("renders safe markup as elements and never parses translations as HTML", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de">
         <glossa-text key="terms.hint">…</glossa-text>
         <glossa-text key="evil.html">…</glossa-text>
       </glossa-provider>`,
    );
    const [hint, evil] = Array.from(p.querySelectorAll("glossa-text"));
    expect(hint!.shadowRoot!.querySelector("b")?.textContent).toBe("AGB");
    expect(rendered(hint!)).toBe("Lies die AGB.");
    expect(evil!.shadowRoot!.querySelector("img")).toBeNull();
    expect(rendered(evil!)).toBe('<img src=x onerror="alert(1)">');
  });

  it("warns about missing keys in strict mode", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await mount(
      `<glossa-provider ${edgeAttrs} locale="de" strict><glossa-text key="no.such.key">Fallback</glossa-text></glossa-provider>`,
    );
    expect(warn).toHaveBeenCalledWith(expect.stringContaining("no.such.key"));
  });

  it("is quiet about missing keys without strict mode", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="no.such.key">Fallback</glossa-text></glossa-provider>`,
    );
    expect(warn).not.toHaveBeenCalled();
  });

  it("warns in strict mode when it still has v0.3 attributes and no edge", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await mount(
      `<glossa-provider project="demo" api-url="https://glossa.test" api-key="glossa_x" locale="de" strict><glossa-text key="a">A</glossa-text></glossa-provider>`,
      () => {},
    );
    expect(warn).toHaveBeenCalledWith(expect.stringContaining("delivery-key"));
  });

  it("reports runtime errors as glossa-error events", async () => {
    const errors: RuntimeError[] = [];
    document.addEventListener("glossa-error", (e) => errors.push((e as CustomEvent).detail), {
      once: true,
    });
    await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="no.such.key">x</glossa-text></glossa-provider>`,
    );
    expect(errors).toEqual([
      expect.objectContaining({ type: "missing-message", messageId: "no.such.key" }),
    ]);
  });

  it("announces activations with glossa-change", async () => {
    const seen: unknown[] = [];
    document.addEventListener("glossa-change", (e) => seen.push((e as CustomEvent).detail));
    const p = await mount(`<glossa-provider ${edgeAttrs} locale="en"></glossa-provider>`);
    p.locale = "ar";
    await settle();
    expect(seen).toEqual([
      { locale: "en", dir: "ltr", release: { id: "rel_1", version: 1 } },
      { locale: "ar", dir: "rtl", release: { id: "rel_1", version: 1 } },
    ]);
  });

  it("uses GlossaProvider.defaultRuntime when it has no edge, bundle or runtime", async () => {
    const shared = createRuntime({ bundled: r1, locales: "en", storage: null });
    const dispose = vi.spyOn(shared, "dispose");
    GlossaProvider.defaultRuntime = () => shared;
    try {
      const p = await mount(
        `<glossa-provider><glossa-text key="cart.checkout">…</glossa-text></glossa-provider>`,
        () => {},
      );
      expect(p.runtime).toBe(shared);
      expect(rendered(p.querySelector("glossa-text")!)).toBe("Checkout");
      const own = await mount(`<glossa-provider ${edgeAttrs} locale="de"></glossa-provider>`);
      expect(own.runtime).not.toBe(shared);
      p.remove();
      expect(dispose).not.toHaveBeenCalled();
    } finally {
      GlossaProvider.defaultRuntime = undefined;
    }
  });

  it("uses a runtime it is given, and leaves it running when disconnected", async () => {
    const runtime = createRuntime({ bundled: r1, locales: "en", storage: null });
    const dispose = vi.spyOn(runtime, "dispose");
    const p = await mount(
      `<glossa-provider><glossa-text key="cart.checkout">…</glossa-text></glossa-provider>`,
      (p) => (p.runtime = runtime),
    );
    expect(rendered(p.querySelector("glossa-text")!)).toBe("Checkout");
    p.remove();
    expect(dispose).not.toHaveBeenCalled();
  });

  it("disposes the runtime it created when disconnected, and boots again on reconnect", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-text key="cart.checkout">…</glossa-text></glossa-provider>`,
    );
    const first = p.runtime!;
    const dispose = vi.spyOn(first, "dispose");
    const parent = p.parentElement!;
    p.remove();
    expect(dispose).toHaveBeenCalledOnce();
    parent.append(p);
    await settle();
    expect(p.runtime).not.toBe(first);
    expect(rendered(p.querySelector("glossa-text")!)).toBe("Zur Kasse");
  });

  it("dispatches glossa-inspect on Alt+click when inspect is on (the in-product editor's hook)", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de" inspect><p><glossa-text key="cart.checkout">…</glossa-text></p></glossa-provider>`,
    );
    const seen: Array<{ id: string; resolvedFrom: string | null }> = [];
    p.addEventListener("glossa-inspect", (e) => {
      const d = (e as CustomEvent).detail;
      seen.push({ id: d.id, resolvedFrom: d.explanation.resolvedFrom });
    });
    const el = p.querySelector("glossa-text")!;
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, composed: true }));
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, composed: true, altKey: true }));
    expect(seen).toEqual([{ id: "cart.checkout", resolvedFrom: "de" }]);
  });

  it("carries data-glossa-id and -locale only while a session hook is installed (RFC 0004 §3.1)", async () => {
    const runtime = createRuntime({ bundled: r1, locales: "en", storage: null });
    const p = await mount(
      `<glossa-provider><glossa-text key="cart.checkout">…</glossa-text><glossa-plural key="athlete.session_count" count="2">…</glossa-plural><glossa-text key="no.such.key">Default</glossa-text></glossa-provider>`,
      (p) => (p.runtime = runtime),
    );
    const [text, plural, missing] = Array.from(p.querySelectorAll("glossa-text, glossa-plural"));
    const marks = () =>
      [text!, plural!, missing!].map((el) => [
        el.getAttribute("data-glossa-id"),
        el.getAttribute("data-glossa-locale"),
      ]);
    expect(marks()).toEqual([
      [null, null],
      [null, null],
      [null, null],
    ]);

    const off = runtime.onRender(() => undefined);
    await settle();
    expect(marks()).toEqual([
      ["cart.checkout", "en"],
      ["athlete.session_count", "de"],
      ["no.such.key", null],
    ]);
    expect(rendered(text!)).toBe("Checkout");

    off();
    await settle();
    expect(marks()).toEqual([
      [null, null],
      [null, null],
      [null, null],
    ]);
  });
});

describe("<glossa-rich>", () => {
  it("formats with vars from the attribute (JSON) or the property", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-rich key="athlete.greeting" vars='{"name":"Sophia"}'>Hi</glossa-rich></glossa-provider>`,
    );
    const rich = p.querySelector("glossa-rich")!;
    expect(rendered(rich)).toBe("Hallo, Sophia!");
    rich.vars = { name: "Lina" };
    await rich.updateComplete;
    expect(rendered(rich)).toBe("Hallo, Lina!");
  });

  it("renders the MF2 fallback for a missing var instead of throwing", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de"><glossa-rich key="athlete.greeting">Hi</glossa-rich></glossa-provider>`,
    );
    expect(rendered(p.querySelector("glossa-rich")!)).toBe("Hallo, {$name}!");
  });
});

describe("<glossa-plural>", () => {
  it("selects the plural variant by count and locale", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de">
         <glossa-plural key="athlete.session_count" count="3">no sessions</glossa-plural>
         <glossa-plural key="athlete.session_count" count="1">no sessions</glossa-plural>
         <glossa-plural key="athlete.session_count" count="0">no sessions</glossa-plural>
       </glossa-provider>`,
    );
    expect(Array.from(p.querySelectorAll("glossa-plural")).map(rendered)).toEqual([
      "3 Einheiten",
      "eine Einheit",
      "keine Einheiten",
    ]);
  });
});

describe("<glossa-select>", () => {
  it("selects the variant by value, as $value or under the name attribute", async () => {
    const p = await mount(
      `<glossa-provider ${edgeAttrs} locale="de">
         <glossa-select key="user.gender" value="male">they</glossa-select>
         <glossa-select key="user.role" name="role" value="coach">member</glossa-select>
       </glossa-provider>`,
    );
    expect(Array.from(p.querySelectorAll("glossa-select")).map(rendered)).toEqual([
      "Er",
      "Trainerin",
    ]);
  });
});
