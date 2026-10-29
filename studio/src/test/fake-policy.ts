/**
 * An in-memory CheckPolicyPort for component tests, with the part of
 * the server the editor actually depends on in miniature: a monotonic
 * version, a version history, and — the reason this fake exists — a
 * **real** impact preview, computed by deciding each seeded finding
 * under the stored policy and under the candidate.
 *
 * Precedence here mirrors checkpolicy.Decide: only rules whose every
 * named field matches are candidates, the one naming the most fields
 * wins, and ties go to the later rule. A fake that returned a canned
 * impact would let the editor claim a preview it never really has, so
 * this one earns the numbers.
 *
 * Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { CheckPolicyPort, SaveOptions } from "../api/policy";
import type { PolicyDocument, PolicyImpact, PolicyRule, PolicySaved, PolicyState, PolicyVersion } from "../api/policy-schemas";
import type { Page, ProjectRef } from "../api/releases";

const NOW = "2026-09-19T08:00:00Z";

/** One stored finding the preview is measured against, and the ref whose run it belongs to. */
export interface FakeTarget {
  layer: string;
  code: string;
  locale?: string;
  namespace?: string;
  /** What the layer emitted. A rule may change it. */
  severity: "error" | "warning";
  ref: string;
  /** The ref is an open pull request, which is what makes it something a policy change can break. */
  open?: boolean;
  /** The open pull request's number, and where it is, when the server would know. */
  pullRequest?: { number: number; url?: string };
}

export interface FakePolicyState {
  state: PolicyState;
  versions: PolicyVersion[];
  targets: FakeTarget[];
}

export interface FakeCheckPolicy extends CheckPolicyPort {
  readonly calls: Array<[string, ...unknown[]]>;
  readonly state: FakePolicyState;
}

export const DEFAULT_DOCUMENT: PolicyDocument = {
  schema: "glossa.check-policy/v1",
  require_complete: "all",
  locales: [],
  fail_on: "error",
  missing_translations: "error",
};

export function policyState(over: Partial<PolicyState> = {}): PolicyState {
  return { version: 0, document: DEFAULT_DOCUMENT, ...over };
}

const FIELDS = ["layer", "code", "locale", "namespace", "environment"] as const;

const selectorOf = (r: PolicyRule) => [r.layer ?? "", r.code ?? "", r.locale ?? "", r.namespace ?? "", r.environment ?? ""];
const targetOf = (t: FakeTarget) => [t.layer, t.code, t.locale ?? "", t.namespace ?? "", ""];

const matches = (r: PolicyRule, t: FakeTarget): boolean => {
  const sel = selectorOf(r);
  const tgt = targetOf(t);
  return FIELDS.every((_, i) => !sel[i] || sel[i] === tgt[i]);
};
const specificity = (r: PolicyRule) => selectorOf(r).filter(Boolean).length;

interface Decision {
  severity: "error" | "warning" | "off";
  enforced: boolean;
  rule: number;
}

/** checkpolicy.Decide in miniature: most fields wins, ties go to the later rule. */
function decide(doc: PolicyDocument, t: FakeTarget): Decision {
  let best = -1;
  (doc.rules ?? []).forEach((r, i) => {
    if (!matches(r, t)) return;
    if (best < 0 || specificity(r) >= specificity((doc.rules ?? [])[best] as PolicyRule)) best = i;
  });
  if (best < 0) return { severity: t.severity, enforced: true, rule: -1 };
  const r = (doc.rules ?? [])[best] as PolicyRule;
  return { severity: r.severity, enforced: r.mode !== "warn", rule: best };
}

const weight = (s: Decision["severity"]) => (s === "error" ? 2 : s === "warning" ? 1 : 0);
const fails = (d: Decision, doc: PolicyDocument) =>
  d.enforced && d.severity !== "off" && (doc.fail_on === "warning" ? weight(d.severity) >= 1 : doc.fail_on === "error" ? d.severity === "error" : false);

export function impactOf(current: PolicyDocument, candidate: PolicyDocument, targets: readonly FakeTarget[]): PolicyImpact {
  const refs = new Map<string, { was: boolean; now: boolean; open: boolean; pr?: FakeTarget["pullRequest"] }>();
  const rules = (candidate.rules ?? []).map((_, i) => ({ rule: i, selector: (candidate.rules ?? [])[i], matched: 0, changed: 0, newly_failing: 0 }));
  let raised = 0;
  let lowered = 0;
  let silenced = 0;
  let newly = 0;
  let noLonger = 0;
  for (const t of targets) {
    const was = decide(current, t);
    const now = decide(candidate, t);
    if (weight(now.severity) > weight(was.severity)) raised += 1;
    if (now.severity !== "off" && weight(now.severity) < weight(was.severity)) lowered += 1;
    if (now.severity === "off" && was.severity !== "off") silenced += 1;
    const failedBefore = fails(was, current);
    const failsNow = fails(now, candidate);
    if (failsNow && !failedBefore) newly += 1;
    if (!failsNow && failedBefore) noLonger += 1;
    if (now.rule >= 0) {
      const r = rules[now.rule];
      if (r) {
        r.matched += 1;
        if (now.severity !== was.severity || now.enforced !== was.enforced) r.changed += 1;
        if (failsNow && !failedBefore) r.newly_failing += 1;
      }
    }
    const ref = refs.get(t.ref) ?? { was: false, now: false, open: !!t.open };
    ref.was ||= failedBefore;
    ref.now ||= failsNow;
    ref.open ||= !!t.open;
    ref.pr ??= t.pullRequest;
    refs.set(t.ref, ref);
  }
  const newlyRefs = [...refs.entries()].filter(([, r]) => !r.was && r.now).map(([ref]) => ref);
  const fixedRefs = [...refs.entries()].filter(([, r]) => r.was && !r.now).map(([ref]) => ref);
  const out: PolicyImpact = {
    findings: targets.length,
    runs: refs.size,
    raised,
    lowered,
    silenced,
    newly_failing: newly,
    no_longer_failing: noLonger,
    open_pull_requests: newlyRefs.filter((ref) => refs.get(ref)?.open).length,
    rules,
  };
  if (newlyRefs.length) out.newly_failing_refs = newlyRefs;
  const prs = newlyRefs.flatMap((ref) => {
    const r = refs.get(ref);
    if (!r?.open || !r.pr) return [];
    return [{ ref, number: r.pr.number, ...(r.pr.url ? { url: r.pr.url } : {}) }];
  });
  if (prs.length) out.newly_failing_pull_requests = prs;
  if (fixedRefs.length) out.no_longer_failing_refs = fixedRefs;
  return out;
}

export function createFakeCheckPolicy(over: Partial<FakePolicyState> = {}): FakeCheckPolicy {
  const s: FakePolicyState = { state: policyState(), versions: [], targets: [], ...over };
  const calls: Array<[string, ...unknown[]]> = [];
  return {
    calls,
    state: s,
    async policy(_p: ProjectRef): Promise<PolicyState> {
      calls.push(["policy"]);
      return s.state;
    },
    async versions(_p: ProjectRef): Promise<Page<PolicyVersion>> {
      calls.push(["versions"]);
      return { items: s.versions, next: undefined };
    },
    async save(_p: ProjectRef, document: PolicyDocument, options: SaveOptions = {}): Promise<PolicySaved> {
      calls.push(["save", document, options]);
      const rule = (document.rules ?? []).find((r) => r.layer === "linguistic" && r.severity === "error");
      if (rule) throw new ApiError(400, "invalid_check_policy", "A rule may not raise the linguistic layer to an error.");
      const impact = impactOf(s.state.document, document, s.targets);
      if (options.dryRun) return { dry_run: true, policy: { ...s.state, document }, impact };
      const version = s.state.version + 1;
      const grace = options.graceDays ?? 14;
      const state: PolicyState = {
        version,
        document: { schema: "glossa.check-policy/v1", ...document },
        effective_from: NOW,
        created_by: "person:me",
        created_at: NOW,
        ...(grace > 0 ? { grace_until: NOW, pinned_version: s.state.version } : {}),
      };
      s.state = state;
      s.versions = [{ version, document: state.document, created_by: "person:me", created_at: NOW }, ...s.versions];
      return { dry_run: false, policy: state, impact };
    },
  };
}
