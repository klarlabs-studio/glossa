/**
 * Client-side BCP 47 checks for the locale form. The server is the
 * authority (it canonicalizes with golang.org/x/text); this gives instant
 * feedback with the same rules: canonical form, no extensions or private
 * use, at most 35 characters, and a direction from the likely script.
 */

export type Direction = "ltr" | "rtl";

export type LocaleCheck =
  | { ok: true; tag: string; direction: Direction }
  | { ok: false; reason: "empty" | "invalid" | "extension" | "too_long" };

/** Scripts written right to left (ISO 15924). */
const RTL_SCRIPTS = new Set([
  "Adlm", "Arab", "Aran", "Hebr", "Mand", "Mend", "Nkoo", "Rohg", "Samr", "Syrc", "Thaa", "Yezi",
]);

export const MAX_LOCALE_LENGTH = 35;

/** Canonicalize and validate a locale code as the server would. */
export function checkLocale(input: string): LocaleCheck {
  const raw = input.trim().replace(/_/g, "-");
  if (raw === "") return { ok: false, reason: "empty" };
  let tag: string;
  try {
    [tag = ""] = Intl.getCanonicalLocales(raw);
  } catch {
    return { ok: false, reason: "invalid" };
  }
  if (tag === "" || tag.toLowerCase() === "und") return { ok: false, reason: "invalid" };
  // A singleton subtag starts an extension (-u-, -t-, …) or private use (-x-).
  if (tag.split("-").some((s) => s.length === 1)) return { ok: false, reason: "extension" };
  if (tag.length > MAX_LOCALE_LENGTH) return { ok: false, reason: "too_long" };
  return { ok: true, tag, direction: directionOf(tag) };
}

/** Text direction of a locale, from its (likely) script. */
export function directionOf(tag: string): Direction {
  try {
    const script = new Intl.Locale(tag).maximize().script;
    return script && RTL_SCRIPTS.has(script) ? "rtl" : "ltr";
  } catch {
    return "ltr";
  }
}

/** A human name for a locale in the UI's language, falling back to the tag. */
export function localeName(tag: string, uiLocale = "en"): string {
  try {
    return new Intl.DisplayNames([uiLocale], { type: "language" }).of(tag) ?? tag;
  } catch {
    return tag;
  }
}

/**
 * CLDR parent locales that aren't the truncation parent — enough for the
 * advisory permission check; the server applies the full CLDR data.
 */
const CLDR_PARENTS: Record<string, string> = {
  "es-AR": "es-419", "es-BO": "es-419", "es-CL": "es-419", "es-CO": "es-419", "es-CR": "es-419",
  "es-CU": "es-419", "es-DO": "es-419", "es-EC": "es-419", "es-GT": "es-419", "es-HN": "es-419",
  "es-MX": "es-419", "es-NI": "es-419", "es-PA": "es-419", "es-PE": "es-419", "es-PR": "es-419",
  "es-PY": "es-419", "es-SV": "es-419", "es-US": "es-419", "es-UY": "es-419", "es-VE": "es-419",
  "pt-AO": "pt-PT", "pt-CV": "pt-PT", "pt-GW": "pt-PT", "pt-MO": "pt-PT", "pt-MZ": "pt-PT",
  "pt-ST": "pt-PT", "pt-TL": "pt-PT",
  "en-AU": "en-001", "en-CA": "en-001", "en-GB": "en-001", "en-IE": "en-001", "en-IN": "en-001",
  "en-NZ": "en-001", "en-SG": "en-001", "en-ZA": "en-001",
  "es-419": "es", "pt-PT": "pt", "en-001": "en",
};

/** The locale and its ancestors, most specific first: de-AT → de. */
export function ancestry(tag: string): string[] {
  const out: string[] = [];
  let cur: string | undefined = tag;
  while (cur) {
    out.push(cur);
    const next: string | undefined = CLDR_PARENTS[cur] ?? (cur.includes("-") ? cur.slice(0, cur.lastIndexOf("-")) : undefined);
    cur = next;
  }
  return out;
}
