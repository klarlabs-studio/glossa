/**
 * Style guides in Studio: a readable summary of structured style fields,
 * the scope a guide applies to, and the editor's flat form ↔ the API's
 * nested fields (an unset leaf inherits from a broader guide, so empty
 * inputs are left out, never sent as "").
 */
import type { StyleFields, StyleGuide, StyleRule } from "../api/knowledge-schemas";

export interface SummaryLine {
  label: string;
  value: string;
}

/** The words a summary uses (from strings.ts, so they localize with the rest of Studio). */
export interface SummaryLabels {
  address: string;
  tone: string;
  quotes: string;
  nested: (q: string) => string;
  dash: string;
  dashes: Record<"hyphen" | "en" | "em", string>;
  ellipsis: string;
  spaceBeforeUnit: string;
  spaceBeforePunctuation: string;
  serialComma: string;
  yes: string;
  no: string;
  numbers: string;
  decimal: (s: string) => string;
  grouping: (s: string) => string;
  numberNotes: string;
  dates: string;
  dateNotes: string;
}

export function styleSummary(f: StyleFields, L: SummaryLabels): SummaryLine[] {
  const out: SummaryLine[] = [];
  const yesNo = (b: boolean) => (b ? L.yes : L.no);
  const reg = f.formality?.register;
  const pronoun = f.formality?.pronoun;
  if (reg || pronoun) out.push({ label: L.address, value: [reg, pronoun ? `“${pronoun}”` : ""].filter(Boolean).join(" ") });
  if (f.tone?.length) out.push({ label: L.tone, value: f.tone.join(", ") });
  const p = f.punctuation;
  if (p?.quotes) out.push({ label: L.quotes, value: p.nested_quotes ? `${p.quotes} ${L.nested(p.nested_quotes)}` : p.quotes });
  if (p?.dash) out.push({ label: L.dash, value: L.dashes[p.dash] });
  if (p?.ellipsis) out.push({ label: L.ellipsis, value: p.ellipsis });
  if (p?.space_before_unit !== undefined) out.push({ label: L.spaceBeforeUnit, value: yesNo(p.space_before_unit) });
  if (p?.space_before_punctuation !== undefined) out.push({ label: L.spaceBeforePunctuation, value: yesNo(p.space_before_punctuation) });
  if (p?.serial_comma !== undefined) out.push({ label: L.serialComma, value: yesNo(p.serial_comma) });
  const n = f.numbers;
  if (n?.decimal_separator || n?.grouping_separator) {
    const parts = [n.decimal_separator ? L.decimal(n.decimal_separator) : "", n.grouping_separator ? L.grouping(n.grouping_separator) : ""];
    out.push({ label: L.numbers, value: parts.filter(Boolean).join(", ") });
  }
  if (n?.notes) out.push({ label: L.numberNotes, value: n.notes });
  if (f.dates?.format) out.push({ label: L.dates, value: f.dates.format });
  if (f.dates?.notes) out.push({ label: L.dateNotes, value: f.dates.notes });
  return out;
}

/** Where a guide applies, most general first: "Tenant", "Demo · de · checkout". */
export function scopeLabel(g: Pick<StyleGuide, "project_id" | "locale" | "namespace">, names: { tenant: string; project: string }): string {
  const parts = [g.project_id ? names.project : names.tenant, g.locale, g.namespace].filter(Boolean);
  return parts.join(" · ");
}

/** A tri-state for optional booleans: inherit, yes or no. */
export type Tri = "" | "yes" | "no";
export const toTri = (b: boolean | undefined): Tri => (b === undefined ? "" : b ? "yes" : "no");
const fromTri = (t: Tri): boolean | undefined => (t === "" ? undefined : t === "yes");

export interface StyleForm {
  register: "" | "formal" | "informal" | "neutral";
  pronoun: string;
  tone: string;
  quotes: string;
  nested_quotes: string;
  dash: "" | "hyphen" | "en" | "em";
  ellipsis: string;
  space_before_unit: Tri;
  space_before_punctuation: Tri;
  serial_comma: Tri;
  decimal_separator: string;
  grouping_separator: string;
  number_notes: string;
  date_format: string;
  date_notes: string;
}

export function formOf(f: StyleFields): StyleForm {
  return {
    register: f.formality?.register ?? "",
    pronoun: f.formality?.pronoun ?? "",
    tone: (f.tone ?? []).join(", "),
    quotes: f.punctuation?.quotes ?? "",
    nested_quotes: f.punctuation?.nested_quotes ?? "",
    dash: f.punctuation?.dash ?? "",
    ellipsis: f.punctuation?.ellipsis ?? "",
    space_before_unit: toTri(f.punctuation?.space_before_unit),
    space_before_punctuation: toTri(f.punctuation?.space_before_punctuation),
    serial_comma: toTri(f.punctuation?.serial_comma),
    decimal_separator: f.numbers?.decimal_separator ?? "",
    grouping_separator: f.numbers?.grouping_separator ?? "",
    number_notes: f.numbers?.notes ?? "",
    date_format: f.dates?.format ?? "",
    date_notes: f.dates?.notes ?? "",
  };
}

/** Drop unset leaves; undefined when nothing is left. */
function prune<T extends Record<string, unknown>>(o: T): T | undefined {
  const entries = Object.entries(o).filter(([, v]) => v !== undefined && v !== "");
  return entries.length ? (Object.fromEntries(entries) as T) : undefined;
}

export function fieldsOf(form: StyleForm): StyleFields {
  const tone = form.tone
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);
  const fields: StyleFields = {};
  const formality = prune({ register: form.register || undefined, pronoun: form.pronoun.trim() });
  if (formality) fields.formality = formality;
  if (tone.length) fields.tone = tone;
  const punctuation = prune({
    quotes: form.quotes.trim(),
    nested_quotes: form.nested_quotes.trim(),
    dash: form.dash || undefined,
    ellipsis: form.ellipsis.trim(),
    space_before_unit: fromTri(form.space_before_unit),
    space_before_punctuation: fromTri(form.space_before_punctuation),
    serial_comma: fromTri(form.serial_comma),
  });
  if (punctuation) fields.punctuation = punctuation;
  const numbers = prune({ decimal_separator: form.decimal_separator, grouping_separator: form.grouping_separator, notes: form.number_notes.trim() });
  if (numbers) fields.numbers = numbers;
  const dates = prune({ format: form.date_format.trim(), notes: form.date_notes.trim() });
  if (dates) fields.dates = dates;
  return fields;
}

export interface RuleForm {
  id: string;
  title: string;
  rationale: string;
  good: string;
  bad: string;
  disabled: boolean;
}

const lines = (t: string) =>
  t
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

export const ruleFormOf = (r: StyleRule): RuleForm => ({
  id: r.id,
  title: r.title ?? "",
  rationale: r.rationale ?? "",
  good: (r.good ?? []).join("\n"),
  bad: (r.bad ?? []).join("\n"),
  disabled: r.disabled ?? false,
});

/** A rule for the API; a disabled rule only switches off a broader one with its id. */
export function ruleOf(f: RuleForm): StyleRule {
  const r: StyleRule = { id: f.id.trim() };
  if (f.disabled) return { ...r, disabled: true };
  if (f.title.trim()) r.title = f.title.trim();
  if (f.rationale.trim()) r.rationale = f.rationale.trim();
  const good = lines(f.good);
  const bad = lines(f.bad);
  if (good.length) r.good = good;
  if (bad.length) r.bad = bad;
  return r;
}

export const RULE_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/;

/** A rule id from its title: "Use du, not Sie" → "use-du-not-sie". */
export function ruleIdFrom(title: string): string {
  return (
    title
      .toLowerCase()
      .normalize("NFKD")
      .replace(/[̀-ͯ]/g, "")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 64)
      .replace(/-+$/, "") || "rule"
  );
}
