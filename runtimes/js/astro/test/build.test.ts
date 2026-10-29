/**
 * Builds test/fixture with `astro build` (static output, Vue islands,
 * i18n routing) and checks what ships: translated HTML from the build-time
 * release, islands rendered on the server with the page's runtime, and the
 * inline release slice they hydrate from. The same site built for a
 * preview environment gets the overlay loader; the production build has
 * none of it (RFC 0004 §5.1). Needs `pnpm build` first (the fixture uses
 * ../../dist, like an installed package).
 */
import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { beforeAll, describe, expect, it } from "vitest";

import { LOADER_ATTRIBUTE, OVERLAY_PATH } from "@klarlabs-studio/glossa-runtime/dev";

import { INLINE_ID } from "../src/page.js";
import type { InlineRelease } from "../src/page.js";
import { match, msg, release, text } from "../src/testing/release.js";

const fixture = fileURLToPath(new URL("./fixture/", import.meta.url));
// Resolve the CLI through the package's own `bin` entry: the file moved
// between majors (astro.js in 5–6, bin/astro.mjs in 7).
const astroPkg = createRequire(import.meta.url).resolve("astro/package.json");
const astroBin = (JSON.parse(readFileSync(astroPkg, "utf8")) as { bin: { astro: string } }).bin
  .astro;
const astro = join(dirname(astroPkg), astroBin);

const r = release("rel_42", 42, {
  de: {
    "home.title": text("Willkommen"),
    "cart.items": match("count", "number", {
      one: ["ein Artikel"],
      "*": [{ $: "count" }, " Artikel"],
    }),
    greeting: msg("Hallo, ", { $: "name" }, "!"),
    terms: msg("Lies die ", { open: "b" }, "AGB", { close: "b" }, "."),
  },
  en: {
    "home.title": text("Welcome"),
    "cart.items": match("count", "number", { one: ["one item"], "*": [{ $: "count" }, " items"] }),
    greeting: msg("Hello, ", { $: "name" }, "!"),
    terms: msg("Read the ", { open: "b" }, "terms", { close: "b" }, "."),
  },
});

const page = (path: string) => readFile(join(fixture, "dist", path), "utf8");

async function files(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map((e) => (e.isDirectory() ? files(join(dir, e.name)) : [join(dir, e.name)])),
  );
  return nested.flat();
}

const inlined = (html: string): InlineRelease | undefined => {
  const m = new RegExp(`<script type="application/json" id="${INLINE_ID}">(.*?)</script>`).exec(
    html,
  );
  return m ? (JSON.parse(m[1]!) as InlineRelease) : undefined;
};

async function writeRelease(dir: string, rel: ReturnType<typeof release>) {
  await rm(dir, { recursive: true, force: true });
  await mkdir(join(dir, "a"), { recursive: true });
  await writeFile(join(dir, "manifest.json"), JSON.stringify(rel.manifest));
  for (const [sha, bytes] of Object.entries(rel.bytes)) {
    await writeFile(join(dir, "a", `${sha}.json`), bytes);
  }
}

const build = (environment?: string) =>
  promisify(execFile)(process.execPath, [astro, "build", "--root", fixture], {
    env: {
      ...process.env,
      ASTRO_TELEMETRY_DISABLED: "1",
      // What names the build in CI (@klarlabs-studio/glossa-unplugin reads them).
      GITHUB_SHA: COMMIT,
      GITHUB_HEAD_REF: "feat/astro-usages",
      ...(environment ? { GLOSSA_FIXTURE_ENVIRONMENT: environment } : {}),
    },
  });

beforeAll(async () => {
  await writeRelease(join(fixture, ".glossa-release"), r);
  await writeRelease(join(fixture, ".glossa-release-preview"), {
    ...r,
    manifest: { ...r.manifest, environment: "preview" },
  });
  await rm(join(fixture, ".glossa"), { recursive: true, force: true });
  // One after the other: both builds share the fixture's caches.
  await build();
  await build("preview");
}, 120_000);

const COMMIT = "0123456789abcdef0123456789abcdef01234567";

describe("astro build with glossa()", () => {
  it("renders .astro pages and <glossa-*> elements with the release, per locale", async () => {
    const de = await page("index.html");
    expect(de).toContain('<html lang="de" dir="ltr"');
    expect(de).toContain("<title>Willkommen</title>");
    expect(de).toMatch(/<glossa-provider lang="de" dir="ltr"/);
    expect(de).toMatch(
      /<glossa-text key="home.title" data-allow-mismatch="">Willkommen<\/glossa-text>/,
    );
    expect(de).toMatch(/<glossa-plural key="cart.items" count="3"[^>]*>3 Artikel</);
    expect(de).toMatch(/<glossa-text key="only.inline">Nur inline<\/glossa-text>/);
    const en = await page("en/index.html");
    expect(en).toContain('<html lang="en" dir="ltr"');
    expect(en).toContain("<title>Welcome</title>");
    expect(en).toMatch(/>Welcome<\/glossa-text>/);
    expect(en).toContain('<link rel="alternate" hreflang="de" href="/">');
    expect(en).toContain('<link rel="alternate" hreflang="en" href="/en/">');
  });

  it("renders Vue islands on the server with the page's locale", async () => {
    const de = await page("index.html");
    expect(de).toContain('<section class="island" lang="de">');
    expect(de).toContain("Hallo, ⁨Lina⁩!");
    expect(de).toContain("Lies die <b>AGB</b>.");
    const en = await page("en/index.html");
    expect(en).toContain("Hello, ⁨Lina⁩!");
    expect(en).toContain("Read the <b>terms</b>.");
    // Vue drops `key`, so elements inside .vue files use message=.
    expect(en).toContain(
      '<p class="e"><glossa-text message="home.title" data-allow-mismatch="">Welcome</glossa-text>',
    );
  });

  it("inlines the page locale's release slice for hydration, and only where something hydrates", async () => {
    const slice = inlined(await page("en/index.html"))!;
    expect(slice.locale).toBe("en");
    expect(slice.release.manifest.release.id).toBe("rel_42");
    expect(Object.keys(slice.release.artifacts).sort()).toEqual(
      [r.manifest.artifacts.en!.default!.sha256, r.manifest.artifacts.de!.default!.sha256].sort(),
    );
    const plain = await page("plain/index.html");
    expect(plain).toContain('<p id="t">Willkommen</p>');
    expect(plain).toContain(">Willkommen</glossa-text>");
    expect(inlined(plain)).toBeUndefined();
  });

  it("writes .glossa/usages.json beside dist with @klarlabs-studio/glossa-unplugin, across the server and island builds", async () => {
    const doc = JSON.parse(await readFile(join(fixture, ".glossa", "usages.json"), "utf8"));
    expect(doc).toMatchObject({
      schema: "glossa.usages/v1",
      application: "astro-fixture",
      commit: COMMIT,
      branch: "feat/astro-usages",
      tool: { name: "@klarlabs-studio/glossa" },
    });
    const at = (u: { key: string; file: string; line: number; column: number; kind: string; route?: string; component?: string }) =>
      `${u.key} ${u.file}:${u.line}:${u.column} ${u.kind} ${u.component ?? "-"} ${u.route ?? "-"}`;
    expect(doc.usages.map(at)).toEqual([
      "cart.items src/components/Page.astro:17:42 element Page -",
      "greeting src/components/Greeting.vue:10:24 t Greeting -",
      "home.title src/components/Greeting.vue:12:40 element Greeting -",
      "home.title src/components/Page.astro:11:16 t Page -",
      "home.title src/components/Page.astro:16:41 element Page -",
      "home.title src/pages/plain.astro:10:19 t plain /plain",
      "home.title src/pages/plain.astro:11:33 element plain /plain",
      "only.inline src/components/Page.astro:18:40 element Page -",
      "terms src/components/Greeting.vue:11:34 component Greeting -",
    ]);
    expect(await readdir(join(fixture, "dist"))).not.toContain(".glossa");
  });

  it("the production build has no overlay loader and no overlay URL", async () => {
    for (const f of await files(join(fixture, "dist"))) {
      const code = await readFile(f, "utf8");
      expect(code).not.toContain(LOADER_ATTRIBUTE);
      expect(code).not.toContain(OVERLAY_PATH);
    }
  });

  it("a preview build loads the overlay loader on every page, from a module script", async () => {
    const out = join(fixture, "dist-preview");
    const assets = await files(join(out, "_astro"));
    const loaders = [];
    for (const f of assets.filter((a) => a.endsWith(".js"))) {
      if ((await readFile(f, "utf8")).includes(LOADER_ATTRIBUTE)) loaders.push(f.slice(out.length));
    }
    expect(loaders).toHaveLength(1);
    for (const html of ["index.html", "en/index.html", "plain/index.html"]) {
      const doc = await readFile(join(out, html), "utf8");
      expect(doc).toContain(`<script type="module" src="${loaders[0]}"></script>`);
      expect(doc).not.toContain(OVERLAY_PATH);
    }
  });

  it("keeps the release out of client JavaScript", async () => {
    const assets = join(fixture, "dist", "_astro");
    const js = (await readdir(assets)).filter((f) => f.endsWith(".js"));
    expect(js.length).toBeGreaterThan(0);
    for (const f of js) {
      const code = await readFile(join(assets, f), "utf8");
      expect(code).not.toContain("Willkommen");
      expect(code).not.toContain("Welcome");
    }
  });
});
