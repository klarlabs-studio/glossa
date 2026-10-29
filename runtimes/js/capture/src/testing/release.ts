/**
 * The bundled release the capture tests render (jsdom and the browser
 * fixture), built the way the publisher builds releases (runtimes/SPEC.md §1).
 * Test-only.
 */
import { createHash } from "node:crypto";
import type { Artifact, BundledRelease, Manifest, Message, Pattern } from "@klarlabs-studio/glossa-runtime";

type El = string | { $: string; fn?: string };

const pattern = (els: El[]): Pattern =>
  els.map((p) =>
    typeof p === "string"
      ? p
      : {
          type: "expression",
          arg: { type: "variable", name: p.$ },
          ...(p.fn ? { function: { type: "function", name: p.fn } } : {}),
        },
  );

/** A pattern message from strings and `{ $: "name", fn? }` placeholders. */
export const msg = (...els: El[]): Message => ({
  type: "message",
  declarations: [],
  pattern: pattern(els),
});

export function release(
  catalogs: Record<string, Record<string, Message>>,
  directions: Record<string, "ltr" | "rtl"> = {},
): BundledRelease {
  const artifacts: Record<string, Artifact> = {};
  const refs: Manifest["artifacts"] = {};
  for (const [locale, messages] of Object.entries(catalogs)) {
    const a: Artifact = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    const bytes = JSON.stringify(a);
    const sha = createHash("sha256").update(bytes, "utf8").digest("hex");
    artifacts[sha] = a;
    refs[locale] = { default: { sha256: sha, size: Buffer.byteLength(bytes) } };
  }
  const locales = Object.keys(catalogs);
  const manifest: Manifest = {
    schema: "glossa.manifest/v1",
    project: "prj_test",
    environment: "preview",
    release: { id: "rel_1", version: 1, createdAt: "2026-09-19T08:00:00Z" },
    sourceLocale: locales[0]!,
    locales: locales.map((code) => ({ code, direction: directions[code] ?? "ltr" })),
    fallback: { "*": [locales[0]!] },
    artifacts: refs,
  };
  return { manifest, artifacts };
}

/** The fixture catalog: three different messages that all read "Save", values, attributes, RTL. */
export const fixture = release(
  {
    de: {
      "profile.save": msg("Speichern"),
      "settings.save": msg("Speichern"),
      "draft.save": msg("Speichern"),
      "cart.total": msg("Summe: ", { $: "amount", fn: "number" }),
      "search.placeholder": msg("Suchen …"),
      "search.hint": msg("Stichwort eingeben"),
      "logo.alt": msg("Glossa-Logo"),
      "form.submit": msg("Absenden"),
      "hidden.note": msg("Unsichtbar"),
      "offscreen.note": msg("Außerhalb"),
      "user.name": msg("Lina"),
      "greeting": msg("Hallo, ", { $: "name" }, "!"),
      "long.text": msg(
        "Ein ziemlich langer Satz, der in einer schmalen Spalte über mehrere Zeilen umbricht.",
      ),
      "cart.checkout": msg("Zur Kasse"),
    },
    ar: {
      "profile.save": msg("حفظ"),
      "cart.checkout": msg("الدفع"),
      "long.text": msg("جملة طويلة إلى حد ما تلتف على عدة أسطر في عمود ضيق."),
    },
  },
  { ar: "rtl" },
);
