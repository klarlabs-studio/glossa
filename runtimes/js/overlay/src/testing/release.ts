/**
 * The page the overlay tests edit: a bundled preview release (en source, de
 * target) and the same messages seeded into the fake API. Test-only.
 */
import { createHash } from "node:crypto";
import type { Artifact, BundledRelease, Manifest, Message } from "@klarlabs-studio/glossa-runtime";

import type { FakeApi } from "./fake-api.js";
import { parse } from "./fake-api.js";

/** Source (en) and target (de) text per key, in MF2. */
export const TEXTS: Record<string, { en: string; de?: string }> = {
  "checkout.pay": { en: "Pay now", de: "Jetzt zahlen" },
  "cart.greeting": { en: "Hello {$name}!", de: "Hallo {$name}!" },
  "user.name": { en: "Lina", de: "Lina" },
  "search.placeholder": { en: "Search…", de: "Suchen …" },
  "cart.checkout": { en: "Checkout", de: "Zur Kasse" },
  "profile.save": { en: "Save", de: "Speichern" },
  "settings.save": { en: "Save", de: "Speichern" },
  "promo.banner": { en: "Free shipping this week" },
};

function release(catalogs: Record<string, Record<string, Message>>): BundledRelease {
  const artifacts: Record<string, Artifact> = {};
  const refs: Manifest["artifacts"] = {};
  for (const [locale, messages] of Object.entries(catalogs)) {
    const a: Artifact = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    const bytes = JSON.stringify(a);
    const sha = createHash("sha256").update(bytes, "utf8").digest("hex");
    artifacts[sha] = a;
    refs[locale] = { default: { sha256: sha, size: Buffer.byteLength(bytes) } };
  }
  const manifest: Manifest = {
    schema: "glossa.manifest/v1",
    project: "prj_1",
    environment: "preview",
    release: { id: "rel_1", version: 1, createdAt: "2026-09-19T08:00:00Z" },
    sourceLocale: "en",
    locales: Object.keys(catalogs).map((code) => ({ code, direction: "ltr" as const })),
    fallback: { de: ["en"] },
    artifacts: refs,
  };
  return { manifest, artifacts };
}

const catalog = (locale: "en" | "de") =>
  Object.fromEntries(
    Object.entries(TEXTS)
      .filter(([, t]) => t[locale] !== undefined)
      .map(([key, t]) => [key, parse(t[locale]!, "mf2")]),
  );

export const fixture: BundledRelease = release({ en: catalog("en"), de: catalog("de") });

/** Seed the fake API with the fixture's messages and de translations. */
export function seed(fake: FakeApi): void {
  for (const [key, t] of Object.entries(TEXTS)) {
    fake.message(key, t.en);
    if (t.de !== undefined) fake.translate(key, "de", t.de);
  }
}
