/**
 * Pure helpers for the check-policy editor (RFC 0005 §4).
 *
 * The thing this file exists to make legible is **precedence**. The
 * schema states it in one sentence — the rule naming more fields wins,
 * and ties go to the later rule — and an editor that hides the order
 * turns that sentence into a guess. So:
 *
 * - `specificity()` is the number the first half of precedence is about,
 *   and the editor prints it beside every rule.
 * - `overlap()` answers whether two rules can ever decide the same
 *   finding, which is the only case where their *order* matters at all.
 * - `ties()` names, per rule, exactly which other rules its position
 *   decides against. A rule with no ties can be moved freely, and the
 *   editor says so rather than implying that every drag changes the
 *   policy.
 *
 * Nothing here talks to the API. The draft shapes use "" for "not
 * named", because that is what a native form control holds; `toDocument`
 * is the one place that turns a draft back into the wire document.
 */
import type {
  LocaleRequirement,
  PolicyDocument,
  PolicyEnvironment,
  PolicyRule,
  RuleMode,
  RuleSeverity,
} from "../api/policy-schemas";
import type { FindingLayer } from "../api/quality-schemas";
import { LAYERS } from "./quality";

/**
 * The selector's fields in the order the kernel counts and prints them
 * (checkpolicy.Selector.fields). Order matters here only for the
 * reading: specificity counts them, it does not weigh them.
 */
export const SELECTOR_FIELDS = ["layer", "code", "locale", "namespace", "environment"] as const;
export type SelectorField = (typeof SELECTOR_FIELDS)[number];

export const RULE_SEVERITIES: readonly RuleSeverity[] = ["error", "warning", "off"] as const;
export const RULE_MODES: readonly RuleMode[] = ["enforce", "warn"] as const;
export const REQUIREMENTS: readonly LocaleRequirement[] = ["all", "listed", "none"] as const;
export const FAIL_ON = ["error", "warning", "never"] as const;
export const MISSING_TRANSLATIONS = ["error", "warning"] as const;

/**
 * The layers a model decides (checkpolicy.AdvisoryLayers). A rule may
 * not raise one to `error`: a build never fails on an opinion
 * (RFC 0005 §14 decision 10). The server refuses such a rule with
 * `invalid_check_policy`; the editor says so before anyone waits for it.
 */
export const ADVISORY_LAYERS: readonly FindingLayer[] = ["linguistic"] as const;

// ── the draft the editor holds ──────────────────────────────────────
//
// Every selector field is a string, "" meaning "not named", because
// that is what a `<select>` and an `<input>` hold. `key` is a client-side
// identity so a Vue key survives a reorder — the rules have no ids of
// their own, and using the index as the key would make a move animate
// the wrong row.

export interface DraftRule {
  key: string;
  layer: string;
  code: string;
  locale: string;
  namespace: string;
  environment: string;
  severity: RuleSeverity;
  mode: RuleMode;
}

export interface DraftEnvironment {
  key: string;
  name: string;
  /** "" is "inherit the document's requirement", which is not the same as "all". */
  requireComplete: "" | LocaleRequirement;
  locales: string[];
  requireReview: boolean;
}

export interface DraftPolicy {
  requireComplete: LocaleRequirement;
  locales: string[];
  failOn: (typeof FAIL_ON)[number];
  missingTranslations: (typeof MISSING_TRANSLATIONS)[number];
  rules: DraftRule[];
  environments: DraftEnvironment[];
}

let keys = 0;
/** A fresh client-side identity for a rule or an environment block. */
export const draftKey = (prefix = "r"): string => `${prefix}${(keys += 1)}`;

export const emptyRule = (): DraftRule => ({
  key: draftKey("r"),
  layer: "",
  code: "",
  locale: "",
  namespace: "",
  environment: "",
  severity: "warning",
  mode: "enforce",
});

export const emptyEnvironment = (name = ""): DraftEnvironment => ({
  key: draftKey("e"),
  name,
  requireComplete: "",
  locales: [],
  requireReview: false,
});

/** The document as a draft the form can hold. */
export function toDraft(doc: PolicyDocument): DraftPolicy {
  return {
    requireComplete: doc.require_complete,
    locales: [...(doc.locales ?? [])],
    failOn: doc.fail_on,
    missingTranslations: doc.missing_translations,
    rules: (doc.rules ?? []).map((r) => ({
      key: draftKey("r"),
      layer: r.layer ?? "",
      code: r.code ?? "",
      locale: r.locale ?? "",
      namespace: r.namespace ?? "",
      environment: r.environment ?? "",
      severity: r.severity,
      mode: r.mode ?? "enforce",
    })),
    // Object key order is not meaning here, but a stable reading is: the
    // editor lists environments by name so two people editing the same
    // policy see the same page.
    environments: Object.entries(doc.environments ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, e]) => ({
        key: draftKey("e"),
        name,
        requireComplete: e.require_complete ?? "",
        locales: [...(e.locales ?? [])],
        requireReview: e.require_review === "approved",
      })),
  };
}

const trimmed = (v: string): string => v.trim();

/** One draft rule as the wire spells it: a field nobody named is left out, never sent as "". */
export function toRule(d: DraftRule): PolicyRule {
  const out: PolicyRule = { severity: d.severity };
  if (trimmed(d.layer)) out.layer = d.layer as FindingLayer;
  if (trimmed(d.code)) out.code = trimmed(d.code);
  if (trimmed(d.locale)) out.locale = trimmed(d.locale);
  if (trimmed(d.namespace)) out.namespace = trimmed(d.namespace);
  if (trimmed(d.environment)) out.environment = trimmed(d.environment);
  // `enforce` is the default and the wire's absent value; sending it
  // changes nothing, and leaving it out keeps an exported document
  // readable.
  if (d.mode === "warn") out.mode = "warn";
  return out;
}

function toEnvironment(d: DraftEnvironment): PolicyEnvironment {
  const out: PolicyEnvironment = {};
  if (d.requireComplete) {
    out.require_complete = d.requireComplete;
    out.locales = d.requireComplete === "listed" ? [...d.locales] : [];
  }
  if (d.requireReview) out.require_review = "approved";
  return out;
}

/**
 * The draft as the document to save. `schema` is left out: the server
 * writes it, and a request that set the bookkeeping is refused.
 */
export function toDocument(d: DraftPolicy): PolicyDocument {
  const out: PolicyDocument = {
    require_complete: d.requireComplete,
    locales: d.requireComplete === "listed" ? [...d.locales] : [],
    fail_on: d.failOn,
    missing_translations: d.missingTranslations,
  };
  if (d.rules.length) out.rules = d.rules.map(toRule);
  const named = d.environments.filter((e) => trimmed(e.name));
  if (named.length) out.environments = Object.fromEntries(named.map((e) => [trimmed(e.name), toEnvironment(e)]));
  return out;
}

/**
 * A stable string for a document, so "has this changed since the
 * preview?" is one comparison. Key order is fixed by construction, and
 * environments are sorted, so two equal documents always stringify the
 * same way.
 */
export function canonical(doc: PolicyDocument): string {
  const rule = (r: PolicyRule) => [r.layer ?? "", r.code ?? "", r.locale ?? "", r.namespace ?? "", r.environment ?? "", r.severity, r.mode ?? "enforce"];
  const env = (e: PolicyEnvironment) => [e.require_complete ?? "", (e.locales ?? []).join(","), e.require_review ?? ""];
  return JSON.stringify([
    doc.require_complete,
    doc.locales ?? [],
    doc.fail_on,
    doc.missing_translations,
    (doc.rules ?? []).map(rule),
    Object.entries(doc.environments ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, e]) => [name, env(e)]),
  ]);
}

// ── precedence ──────────────────────────────────────────────────────

const fieldsOf = (r: PolicyRule | DraftRule): Record<SelectorField, string> => ({
  layer: trimmed(r.layer ?? ""),
  code: trimmed(r.code ?? ""),
  locale: trimmed(r.locale ?? ""),
  namespace: trimmed(r.namespace ?? ""),
  environment: trimmed(r.environment ?? ""),
});

/** How many fields the selector names. The rule naming more wins. */
export function specificity(r: PolicyRule | DraftRule): number {
  return Object.values(fieldsOf(r)).filter(Boolean).length;
}

/** The fields the selector names, in the kernel's order, as `[field, value]`. */
export function namedFields(r: PolicyRule | DraftRule): Array<[SelectorField, string]> {
  const f = fieldsOf(r);
  return SELECTOR_FIELDS.filter((k) => f[k]).map((k) => [k, f[k]]);
}

/**
 * Whether two selectors can ever decide the same finding. They cannot
 * when they name the same field with different values; anything else
 * overlaps, because a field a selector leaves out matches everything.
 */
export function overlap(a: PolicyRule | DraftRule, b: PolicyRule | DraftRule): boolean {
  const x = fieldsOf(a);
  const y = fieldsOf(b);
  return SELECTOR_FIELDS.every((k) => !x[k] || !y[k] || x[k] === y[k]);
}

/** Two selectors name exactly the same fields with the same values. */
export function sameSelector(a: PolicyRule | DraftRule, b: PolicyRule | DraftRule): boolean {
  const x = fieldsOf(a);
  const y = fieldsOf(b);
  return SELECTOR_FIELDS.every((k) => x[k] === y[k]);
}

/** What a rule's *position* in the document decides, and what it does not. */
export interface RuleOrder {
  /** Its index now. */
  index: number;
  specificity: number;
  /**
   * The rules this one's position decides against: equally specific and
   * able to match the same finding, so the later of the two wins. Empty
   * means moving this rule cannot change what the policy decides.
   */
  ties: number[];
  /** A tie that comes later in the document, and therefore beats it. */
  beatenBy: number[];
  /** A tie that comes earlier, and that it therefore beats. */
  beats: number[];
  /** A later rule with an identical selector: this one decides nothing at all. */
  shadowedBy: number | undefined;
  /** A rule asking for `error` on a layer a model decides; the server refuses it. */
  advisoryError: boolean;
}

/**
 * Every rule's standing, in document order. This is the whole of what
 * the editor needs to tell the truth about order: a rule with no ties
 * can be moved anywhere without changing a verdict, and one with ties
 * cannot.
 */
export function ruleOrder(rules: readonly (PolicyRule | DraftRule)[]): RuleOrder[] {
  return rules.map((r, i) => {
    const spec = specificity(r);
    const ties: number[] = [];
    const beatenBy: number[] = [];
    const beats: number[] = [];
    let shadowedBy: number | undefined;
    rules.forEach((other, j) => {
      if (i === j || specificity(other) !== spec || !overlap(r, other)) return;
      ties.push(j);
      if (j > i) {
        beatenBy.push(j);
        if (shadowedBy === undefined && sameSelector(r, other)) shadowedBy = j;
      } else {
        beats.push(j);
      }
    });
    const layer = trimmed((r as PolicyRule).layer ?? "");
    return {
      index: i,
      specificity: spec,
      ties,
      beatenBy,
      beats,
      shadowedBy,
      advisoryError: r.severity === "error" && (ADVISORY_LAYERS as readonly string[]).includes(layer),
    };
  });
}

/** `rules` with the one at `from` moved to `to`. Out-of-range moves return the list unchanged. */
export function moveRule<T>(rules: readonly T[], from: number, to: number): T[] {
  if (from < 0 || from >= rules.length || to < 0 || to >= rules.length || from === to) return [...rules];
  const out = [...rules];
  const [moved] = out.splice(from, 1);
  out.splice(to, 0, moved as T);
  return out;
}

// ── validation the server would do anyway, said sooner ──────────────

export interface PolicyProblem {
  /** The rule or environment it is about, for the message and the focus. */
  where: string;
  detail: string;
}

/**
 * What the server would refuse with `invalid_check_policy`, answered
 * here so nobody waits for a round trip to be told. It is deliberately
 * not exhaustive — the server is the authority — but these three are
 * the ones a person hits while typing.
 */
export function problems(d: DraftPolicy, projectLocales: readonly string[] = []): PolicyProblem[] {
  const out: PolicyProblem[] = [];
  const known = new Set(projectLocales);
  if (d.requireComplete === "listed" && !d.locales.length) {
    out.push({ where: "require_complete", detail: "listed-empty" });
  }
  d.rules.forEach((r, i) => {
    if ((ADVISORY_LAYERS as readonly string[]).includes(r.layer) && r.severity === "error") {
      out.push({ where: `rule-${i}`, detail: "advisory-error" });
    }
    if (trimmed(r.locale) && known.size && !known.has(trimmed(r.locale))) {
      out.push({ where: `rule-${i}`, detail: "unknown-locale" });
    }
  });
  const names = new Set<string>();
  d.environments.forEach((e, i) => {
    const name = trimmed(e.name);
    if (!name) out.push({ where: `env-${i}`, detail: "unnamed" });
    else if (names.has(name)) out.push({ where: `env-${i}`, detail: "duplicate" });
    names.add(name);
  });
  return out;
}

/** The layers a rule may select, in RFC 0005 §3's order. */
export const RULE_LAYERS = LAYERS;
