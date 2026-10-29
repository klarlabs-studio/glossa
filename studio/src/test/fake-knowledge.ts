/**
 * An in-memory KnowledgePort for component tests, with Knowledge's rules
 * in miniature: terms are recognized case-insensitively as whole words
 * (the real matcher also tolerates inflection), terminology QA reports
 * forbidden target terms and missing preferred ones, translation memory
 * matches exact text at 100 and shared words as fuzzy, concepts and
 * guides are versioned and need the current ETag to change.
 * Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { KnowledgePort } from "../api/knowledge";
import type { StyleGuide, StyleGuideVersion, Term, TermConcept, TermConceptRevision, TermFinding, TermHit, TMUnit } from "../api/knowledge-schemas";

export interface FakeKnowledge extends KnowledgePort {
  readonly calls: Array<[string, ...unknown[]]>;
  readonly concepts_: TermConcept[];
  readonly guides: StyleGuide[];
  readonly units: TMUnit[];
  /** The source of each message key, which the server reads from Catalog. */
  readonly sources: Map<string, string>;
  /** Behave as for an assigned member: matches carry no `message_key`. */
  hideKeys: boolean;
  /**
   * Approve a translation into memory. `target` is MF2; `targetMf1` is the
   * same target in MF1, when MF1 can express it.
   */
  remember(source: string, target: string, over?: Partial<TMUnit>, targetMf1?: string): TMUnit;
}

const NOW = "2026-09-19T08:00:00Z";
const words = (s: string) => s.toLowerCase().match(/\p{L}+/gu) ?? [];

function findWord(text: string, word: string, caseSensitive: boolean): number {
  const hay = caseSensitive ? text : text.toLowerCase();
  const needle = caseSensitive ? word : word.toLowerCase();
  for (let at = hay.indexOf(needle); at >= 0; at = hay.indexOf(needle, at + 1)) {
    const before = at === 0 || !/\p{L}/u.test(hay[at - 1]!);
    const after = at + needle.length >= hay.length || !/\p{L}/u.test(hay[at + needle.length]!);
    if (before && after) return at;
  }
  return -1;
}

export function createFakeKnowledge(): FakeKnowledge {
  let seq = 0;
  const id = (p: string) => `${p}_${++seq}`;
  const calls: Array<[string, ...unknown[]]> = [];
  const concepts: TermConcept[] = [];
  const revisions = new Map<string, TermConceptRevision[]>();
  const guides: StyleGuide[] = [];
  const versions = new Map<string, StyleGuideVersion[]>();
  const units: TMUnit[] = [];
  const mf1 = new Map<string, string>();
  const tag = (v: number) => `"${v}"`;
  const need = (etag: string | undefined, v: number) => {
    if (etag !== undefined && etag !== tag(v)) throw new ApiError(412, "precondition_failed", "stale");
  };
  const conceptOf = (cid: string) => {
    const c = concepts.find((x) => x.id === cid);
    if (!c) throw new ApiError(404, "not_found", "No such concept.");
    return c;
  };
  const guideOf = (gid: string) => {
    const g = guides.find((x) => x.id === gid);
    if (!g) throw new ApiError(404, "not_found", "No such guide.");
    return g;
  };
  const termsFrom = (input: Array<Omit<Term, "id" | "status" | "case_sensitive"> & Partial<Pick<Term, "status" | "case_sensitive">>>, old: Term[] = []): Term[] =>
    input.map((t) => ({
      id: old.find((o) => o.locale === t.locale && o.text === t.text)?.id ?? id("term"),
      ...t,
      status: t.status ?? "preferred",
      case_sensitive: t.case_sensitive ?? false,
    }));
  const hits = (text: string, locale: string, target?: string): TermHit[] => {
    const out: TermHit[] = [];
    for (const c of concepts) {
      for (const t of c.terms.filter((x) => x.locale === locale)) {
        const at = findWord(text, t.text, t.case_sensitive);
        if (at < 0) continue;
        out.push({
          concept_id: c.id,
          definition: c.definition,
          term: t,
          start: [...text.slice(0, at)].length,
          end: [...text.slice(0, at + t.text.length)].length,
          text: text.slice(at, at + t.text.length),
          ...(target ? { targets: c.terms.filter((x) => x.locale === target) } : {}),
        });
      }
    }
    return out.sort((a, b) => a.start - b.start);
  };

  const port: FakeKnowledge = {
    calls,
    concepts_: concepts,
    guides,
    units,
    sources: new Map(),
    hideKeys: false,
    remember(source, target, over = {}, targetMf1) {
      const u: TMUnit = {
        id: id("unit"),
        origin: "translation",
        source_locale: "en",
        target_locale: "de",
        source,
        target,
        target_model: { type: "message", declarations: [], pattern: [target] },
        source_normalized: source,
        signature: "",
        state: "active",
        hit_count: 0,
        created_by: "person:me",
        created_at: NOW,
        updated_at: NOW,
        ...over,
      };
      units.push(u);
      if (targetMf1 !== undefined) mf1.set(u.id, targetMf1);
      return u;
    },
    async lookupTM(_tenant, q) {
      calls.push(["lookupTM", q]);
      const matches = units
        .filter((u) => u.source_locale === q.source_locale && u.target_locale === q.target_locale && u.state === "active")
        .map((u) => {
          if (u.source_normalized === q.source) return { u, score: u.message_key === q.message_key ? 101 : 100 };
          const a = new Set(words(u.source_normalized));
          const b = words(q.source);
          const shared = b.filter((w) => a.has(w)).length / Math.max(a.size, b.length);
          return { u, score: Math.round(50 + shared * 49) };
        })
        .filter((m) => m.score >= (q.min_score ?? 50) && m.score > 50)
        .sort((a, b) => b.score - a.score)
        .slice(0, q.limit ?? 5);
      // The target in the syntax asked for; MF2 when MF1 can't express it.
      const want = q.target_syntax ?? q.syntax ?? "mf2";
      const inSyntax = (u: TMUnit) => {
        const text = want === "mf1" ? mf1.get(u.id) : undefined;
        return text !== undefined
          ? { target_text: text, target_syntax: "mf1" as const, target_syntax_fallback: false }
          : { target_text: u.target, target_syntax: "mf2" as const, target_syntax_fallback: want === "mf1" };
      };
      return {
        source_normalized: q.source,
        matches: matches.map(({ u, score }) => ({
          score,
          kind: score === 101 ? ("context" as const) : score === 100 ? ("exact" as const) : ("fuzzy" as const),
          target: u.target,
          ...inSyntax(u),
          target_model: u.target_model,
          variables_adapted: true,
          unit: u,
        })),
      };
    },
    async unitTMMatches(_tenant, _project, key, locale, q = {}) {
      calls.push(["unitTMMatches", key, locale, q]);
      const source = port.sources.get(key);
      if (source === undefined) throw new ApiError(404, "not_found", "No such unit.");
      const r = await port.lookupTM("t", {
        source,
        syntax: "mf1",
        source_locale: "en",
        target_locale: locale,
        message_key: key,
        limit: q.limit,
        min_score: q.min_score,
        target_syntax: q.target_syntax,
      } as never);
      return {
        source_normalized: r.source_normalized,
        items: r.matches.map((m) => ({
          score: m.score,
          kind: m.kind,
          source_normalized: m.unit.source_normalized,
          target: m.target,
          target_text: m.target_text,
          target_syntax: m.target_syntax,
          target_syntax_fallback: m.target_syntax_fallback,
          variables_adapted: m.variables_adapted,
          project_scoped: Boolean(m.unit.project_id),
          ...(port.hideKeys || !m.unit.message_key ? {} : { message_key: m.unit.message_key }),
        })),
      };
    },
    async concordance(_tenant, q) {
      calls.push(["concordance", q]);
      const side = q.side ?? "source";
      return {
        matches: units
          .filter((u) => (side === "source" ? u.source : u.target).toLowerCase().includes(q.q.toLowerCase()))
          .map((unit) => ({ similarity: 0.5, unit })),
      };
    },
    async recognizeTerms(_tenant, body) {
      calls.push(["recognizeTerms", body]);
      return { analyzed_text: body.text, hits: hits(body.text, body.locale, body.target_locale) };
    },
    async checkTerminology(_tenant, body) {
      calls.push(["checkTerminology", body]);
      const findings: TermFinding[] = [];
      for (const h of hits(body.source, body.source_locale, body.target_locale)) {
        const allowed = (h.targets ?? []).filter((t) => t.status === "preferred" || t.status === "admitted");
        if (allowed.length && !allowed.some((t) => findWord(body.target, t.text, t.case_sensitive) >= 0)) {
          findings.push({ code: "term_missing", severity: "warning", concept_id: h.concept_id, term_id: h.term.id, side: "source", start: h.start, end: h.end, text: h.text, suggestions: allowed.map((t) => t.text), message: "missing" });
        }
      }
      for (const h of hits(body.target, body.target_locale)) {
        if (h.term.status !== "forbidden" && h.term.status !== "deprecated") continue;
        const c = conceptOf(h.concept_id);
        findings.push({
          code: "term_forbidden",
          severity: h.term.status === "forbidden" ? "error" : "warning",
          concept_id: h.concept_id,
          term_id: h.term.id,
          side: "target",
          start: h.start,
          end: h.end,
          text: h.text,
          suggestions: c.terms.filter((t) => t.locale === body.target_locale && t.status === "preferred").map((t) => t.text),
          message: "forbidden",
        });
      }
      return { source_text: body.source, target_text: body.target, findings };
    },

    async concepts(_tenant, q) {
      calls.push(["concepts", q]);
      return concepts.filter((c) => !q.q || c.terms.some((t) => t.text.toLowerCase().includes(q.q!.toLowerCase())) || c.definition.includes(q.q));
    },
    async concept(_tenant, cid) {
      const c = conceptOf(cid);
      return { value: structuredClone(c), etag: tag(c.version) };
    },
    async createConcept(_tenant, body, key) {
      calls.push(["createConcept", body, key]);
      const c: TermConcept = {
        id: id("concept"),
        ...(body.project_id ? { project_id: body.project_id } : {}),
        definition: body.definition ?? "",
        domain: body.domain ?? "",
        note: body.note ?? "",
        product_ref: body.product_ref ?? "",
        terms: termsFrom(body.terms),
        version: 1,
        created_by: "person:me",
        created_at: NOW,
        updated_by: "person:me",
        updated_at: NOW,
      };
      concepts.push(c);
      revisions.set(c.id, [{ version: 1, action: "created", author: "person:me", created_at: NOW, concept: structuredClone(c) }]);
      return { value: structuredClone(c), etag: tag(1) };
    },
    async replaceConcept(_tenant, cid, body, etag) {
      calls.push(["replaceConcept", cid, body, etag]);
      const c = conceptOf(cid);
      need(etag, c.version);
      Object.assign(c, { definition: body.definition ?? "", domain: body.domain ?? "", note: body.note ?? "", terms: termsFrom(body.terms, c.terms), version: c.version + 1 });
      revisions.get(cid)!.unshift({ version: c.version, action: "updated", author: "person:me", created_at: NOW, concept: structuredClone(c) });
      return { value: structuredClone(c), etag: tag(c.version) };
    },
    async deleteConcept(_tenant, cid) {
      calls.push(["deleteConcept", cid]);
      const c = conceptOf(cid);
      concepts.splice(concepts.indexOf(c), 1);
    },
    async conceptRevisions(_tenant, cid) {
      return structuredClone(revisions.get(cid) ?? []);
    },

    async styleGuides(_tenant, q) {
      calls.push(["styleGuides", q]);
      return guides.filter((g) => (q.tenant_only ? !g.project_id : q.project ? g.project_id === q.project : true));
    },
    async styleGuide(_tenant, gid) {
      const g = guideOf(gid);
      return { value: structuredClone(g), etag: tag(g.version) };
    },
    async createStyleGuide(_tenant, body, key) {
      calls.push(["createStyleGuide", body, key]);
      const scope = (g: { project_id?: string | undefined; locale?: string | undefined; namespace?: string | undefined }) => `${g.project_id}|${g.locale}|${g.namespace}`;
      if (guides.some((g) => scope(g) === scope(body))) throw new ApiError(409, "style_guide_exists", "exists");
      const g: StyleGuide = {
        id: id("guide"),
        ...(body.project_id ? { project_id: body.project_id } : {}),
        ...(body.locale ? { locale: body.locale } : {}),
        ...(body.namespace ? { namespace: body.namespace } : {}),
        name: body.name ?? "",
        fields: body.fields ?? {},
        rules: body.rules ?? [],
        version: 1,
        created_by: "person:me",
        created_at: NOW,
        updated_by: "person:me",
        updated_at: NOW,
      };
      guides.push(g);
      versions.set(g.id, [{ version: 1, action: "created", author: "person:me", created_at: NOW, style_guide: structuredClone(g) }]);
      return { value: structuredClone(g), etag: tag(1) };
    },
    async replaceStyleGuide(_tenant, gid, body, etag) {
      calls.push(["replaceStyleGuide", gid, body, etag]);
      const g = guideOf(gid);
      need(etag, g.version);
      Object.assign(g, { name: body.name ?? "", fields: body.fields ?? {}, rules: body.rules ?? [], version: g.version + 1 });
      versions.get(gid)!.unshift({ version: g.version, action: "updated", author: "person:me", created_at: NOW, style_guide: structuredClone(g) });
      return { value: structuredClone(g), etag: tag(g.version) };
    },
    async deleteStyleGuide(_tenant, gid) {
      calls.push(["deleteStyleGuide", gid]);
      guides.splice(guides.indexOf(guideOf(gid)), 1);
    },
    async styleGuideVersions(_tenant, gid) {
      return structuredClone(versions.get(gid) ?? []);
    },
    async effectiveStyle(_tenant, q) {
      calls.push(["effectiveStyle", q]);
      const applies = guides.filter(
        (g) => (!g.project_id || g.project_id === q.project) && (!g.locale || q.locale?.startsWith(g.locale)) && (!g.namespace || g.namespace === q.namespace),
      );
      const fields = Object.assign({}, ...applies.map((g) => g.fields));
      const rules = new Map(applies.flatMap((g) => g.rules).map((r) => [r.id, r]));
      return { fields, rules: [...rules.values()], sources: applies.map((g) => ({ style_guide_id: g.id, version: g.version })) };
    },
  };
  return port;
}
