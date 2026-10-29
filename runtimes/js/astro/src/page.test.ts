import { describe, expect, it } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";

import { INLINE_ID, inlineRelease, inlineScript, inlineStream, renderPage } from "./page.js";
import type { InlineRelease } from "./page.js";
import { release, text } from "./testing/release.js";

const r = release(
  "rel_1",
  1,
  {
    de: { "cart.checkout": text("Zur Kasse"), evil: text("</script><script>alert(1)</script>") },
    "de-AT": { "cart.checkout": text("Zur Kassa") },
    en: { "cart.checkout": text("Checkout") },
  },
  { fallback: { "de-AT": ["de"] } },
);

const rt = (locale: string) =>
  createRuntime({ bundled: r, locales: locale, storage: null, bidiIsolation: "none" });

const inlined = (html: string): InlineRelease | undefined => {
  const m = new RegExp(`<script type="application/json" id="${INLINE_ID}">(.*?)</script>`).exec(
    html,
  );
  return m ? (JSON.parse(m[1]!) as InlineRelease) : undefined;
};

const page = (body: string) => `<html><head><title>x</title></head><body>${body}</body></html>`;

describe("inlineRelease", () => {
  it("keeps the manifest and only the artifacts of the locale's fallback chain", () => {
    const slice = inlineRelease(r, "de-AT");
    expect(slice.locale).toBe("de-AT");
    expect(slice.release.manifest).toBe(r.manifest);
    expect(Object.keys(slice.release.artifacts).sort()).toEqual(
      [
        r.manifest.artifacts["de-AT"]!.default!.sha256,
        r.manifest.artifacts.de!.default!.sha256,
      ].sort(),
    );
  });
});

describe("renderPage", () => {
  it("prerenders elements, marks them for Vue hydration, and inlines the release for islands", () => {
    const html = page(
      `<astro-island uid="1"><p><glossa-text key="cart.checkout">Checkout</glossa-text></p></astro-island>`,
    );
    const out = renderPage(html, rt("de-AT"), r, "de-AT", { prerender: true, inline: "auto" });
    expect(out).toContain(
      `<glossa-text key="cart.checkout" data-allow-mismatch="">Zur Kassa</glossa-text>`,
    );
    expect(out.indexOf(INLINE_ID)).toBeLessThan(out.indexOf("</body>"));
    expect(out.indexOf(INLINE_ID)).toBeGreaterThan(out.indexOf("</astro-island>"));
    const slice = inlined(out)!;
    expect(slice.locale).toBe("de-AT");
    const client = createRuntime({ bundled: slice.release, locales: slice.locale, storage: null });
    expect(client.t("cart.checkout")).toBe("Zur Kassa");
  });

  it("can't be broken out of by a translation containing </script>", () => {
    const out = renderPage(page("<glossa-provider></glossa-provider>"), rt("de"), r, "de", {
      prerender: true,
      inline: "auto",
    });
    expect(out.match(/<\/script>/g)).toHaveLength(1);
    expect(inlined(out)!.release.artifacts).toBeDefined();
  });

  it("inlines only on pages with islands or providers unless told otherwise", () => {
    const plain = page(`<p><glossa-text key="cart.checkout">Checkout</glossa-text></p>`);
    const o = { prerender: true, inline: "auto" } as const;
    expect(inlined(renderPage(plain, rt("de"), r, "de", o))).toBeUndefined();
    expect(inlined(renderPage(plain, rt("de"), r, "de", { ...o, inline: "always" }))).toBeDefined();
    const island = page("<astro-island></astro-island>");
    expect(
      inlined(renderPage(island, rt("de"), r, "de", { ...o, inline: "never" })),
    ).toBeUndefined();
    expect(inlined(renderPage(island, rt("de"), undefined, "de", o))).toBeUndefined();
  });

  it("leaves elements alone when prerendering is off", () => {
    const html = page(`<glossa-text key="cart.checkout">Checkout</glossa-text>`);
    expect(renderPage(html, rt("de"), r, "de", { prerender: false, inline: "never" })).toBe(html);
  });

  it("inlines before </body>, or at the end when there's none", () => {
    const o = { prerender: false, inline: "always" } as const;
    expect(renderPage("<body>x</body>", rt("de"), r, "de", o)).toMatch(
      /^<body>x<script .*<\/script><\/body>$/,
    );
    expect(renderPage("x", rt("de"), r, "de", o)).toMatch(/^x<script /);
  });
});

describe("inlineStream", () => {
  const stream = (chunks: string[]) =>
    new ReadableStream<Uint8Array>({
      start(c) {
        for (const x of chunks) c.enqueue(new TextEncoder().encode(x));
        c.close();
      },
    });
  const read = (s: ReadableStream<Uint8Array>) => new Response(s).text();
  const script = () => inlineScript(r, "de");

  it("inserts before </body> on pages with islands, even with tags split across chunks", async () => {
    const chunks = ["<html><body><astro-", "island></astro-island><p>x</p></bo", "dy></html>"];
    const out = await read(inlineStream(stream(chunks), script, "auto"));
    expect(out).toBe(`<html><body><astro-island></astro-island><p>x</p>${script()}</body></html>`);
  });

  it("passes pages without islands or providers through unchanged", async () => {
    const chunks = ["<html><body><p>", "hello</p></body>", "</html>"];
    expect(await read(inlineStream(stream(chunks), script, "auto"))).toBe(chunks.join(""));
  });

  it("appends when there's no </body>, and always inlines with inline: always", async () => {
    const out = await read(inlineStream(stream(["<p>a</p>", "<p>b</p>"]), script, "always"));
    expect(out).toBe(`<p>a</p><p>b</p>${script()}`);
    const short = await read(inlineStream(stream(["<glossa-provider>"]), script, "auto"));
    expect(short).toBe(`<glossa-provider>${script()}`);
  });

  it("streams: early chunks come out before the page is finished", async () => {
    let push!: (s: string) => void;
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        push = (s) => c.enqueue(new TextEncoder().encode(s));
      },
    });
    const reader = inlineStream(body, script, "auto").getReader();
    push("<html><head><title>a long enough head</title></head>");
    const first = await reader.read();
    expect(new TextDecoder().decode(first.value)).toMatch(/^<html><head>/);
  });
});
