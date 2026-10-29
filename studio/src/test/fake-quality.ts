/**
 * An in-memory QualityPort for component tests, with the Quality reads
 * in miniature: findings belong to a run, waivers are applied on read
 * (a waived finding is still returned, at severity `waived`, naming its
 * waiver), the counts are the whole run's whatever the filter selects,
 * and a waiver with no reason is refused exactly as the API refuses it.
 * Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { FindingFilter, Findings, QualityPort, WaiverFilter } from "../api/quality";
import type { CheckRun, CheckRunCounts, Finding, FindingLayer, Waiver } from "../api/quality-schemas";
import type { Page, ProjectRef } from "../api/releases";

const NOW = "2026-09-19T08:00:00Z";

export interface FakeQualityState {
  runs: CheckRun[];
  /** The findings of a run, by run id; a waiver turns one `waived` on read. */
  findings: Record<string, Finding[]>;
  waivers: Waiver[];
}

export interface FakeQuality extends QualityPort {
  readonly calls: Array<[string, ...unknown[]]>;
  readonly state: FakeQualityState;
}

let seq = 0;

export function checkRun(over: Partial<CheckRun> = {}): CheckRun {
  return {
    id: over.id ?? "run1",
    ref: "main",
    commit: "9f2c1e7a".repeat(5),
    trigger: "cli",
    policy_version: 3,
    layers: ["structure", "parity", "completeness", "terminology", "length", "visual"],
    counts: { errors: 0, warnings: 0, waived: 0 },
    conclusion: "failure",
    created_by: "person:me",
    started_at: NOW,
    completed_at: NOW,
    ...over,
  };
}

/** A finding with the shape the API sends; `fingerprint` is generated when it isn't given. */
export function finding(over: Partial<Finding> & { layer: FindingLayer; code: string }): Finding {
  const n = (seq += 1);
  return {
    schema: "glossa.finding/v1",
    fingerprint: `f_${n.toString(16).padStart(16, "0")}`,
    severity: "warning",
    locus: {},
    message: `${over.code} in ${over.layer}`,
    source_revision: 1,
    ...over,
  };
}

export function waiver(over: Partial<Waiver> & { fingerprint: string; reason: string }): Waiver {
  const n = (seq += 1);
  return {
    id: `w${n}`,
    scope: "project",
    source_revision: 1,
    active: true,
    created_by: "person:me",
    created_at: NOW,
    ...over,
  };
}

const matches = (f: Finding, q: FindingFilter): boolean =>
  (!q.layer || f.layer === q.layer) &&
  (!q.severity || f.severity === q.severity) &&
  (!q.code || f.code === q.code) &&
  (!q.locale || f.locus.locale === q.locale) &&
  (!q.namespace || f.locus.namespace === q.namespace) &&
  (!q.message || f.locus.key === q.message) &&
  (q.waived === undefined || (f.severity === "waived") === q.waived);

export function createFakeQuality(state: Partial<FakeQualityState> = {}): FakeQuality {
  const s: FakeQualityState = { runs: [], findings: {}, waivers: [], ...state };
  const calls: Array<[string, ...unknown[]]> = [];
  const live = (w: Waiver) => w.active && !w.revoked_at;

  /** Waivers on read (RFC 0005 §2.3): the finding stays, at `waived`, naming the waiver. */
  const applied = (f: Finding): Finding => {
    const w = s.waivers.find((x) => x.fingerprint === f.fingerprint && live(x) && x.source_revision === (f.source_revision ?? 0));
    return w ? { ...f, severity: "waived", waiver: w.id } : f;
  };
  const order = (f: Finding) => (f.severity === "error" ? 0 : f.severity === "warning" ? 1 : 2);
  const runFor = (q: FindingFilter) => (q.run ? s.runs.find((r) => r.id === q.run) : s.runs[0]);

  return {
    calls,
    state: s,
    async findings(_p: ProjectRef, q: FindingFilter = {}): Promise<Findings> {
      calls.push(["findings", q]);
      const run = runFor(q);
      if (!run) return { items: [], run: undefined, counts: { errors: 0, warnings: 0, waived: 0 }, truncated: false };
      const all = (s.findings[run.id] ?? []).map(applied);
      const counts: CheckRunCounts = {
        errors: all.filter((f) => f.severity === "error").length,
        warnings: all.filter((f) => f.severity === "warning").length,
        waived: all.filter((f) => f.severity === "waived").length,
      };
      const items = all.filter((f) => matches(f, q)).sort((a, b) => order(a) - order(b));
      return { items, run, counts, truncated: false };
    },
    async checkRuns(): Promise<Page<CheckRun>> {
      calls.push(["checkRuns"]);
      return { items: s.runs, next: undefined };
    },
    async waivers(_p: ProjectRef, q: WaiverFilter = {}): Promise<Waiver[]> {
      calls.push(["waivers", q]);
      return s.waivers.filter((w) => (q.active === undefined || live(w) === q.active) && (!q.fingerprint || w.fingerprint === q.fingerprint));
    },
    async createWaiver(_p, input) {
      calls.push(["createWaiver", input]);
      if (!input.reason.trim()) throw new ApiError(400, "waiver_reason_required", "A waiver needs a reason.");
      if (input.scope === "branch" && !input.ref?.trim()) throw new ApiError(400, "waiver_branch_required", "Name the branch.");
      const found = Object.values(s.findings).flat().find((f) => f.fingerprint === input.fingerprint);
      const made = waiver({
        fingerprint: input.fingerprint,
        reason: input.reason,
        scope: input.scope ?? "project",
        ...(input.ref ? { ref: input.ref } : {}),
        source_revision: input.source_revision ?? found?.source_revision ?? 1,
        ...(input.expires_at ? { expires_at: input.expires_at } : {}),
        ...(found ? { accepts: { layer: found.layer, code: found.code, ...(found.locus.key ? { key: found.locus.key } : {}), ...(found.locus.locale ? { locale: found.locus.locale } : {}) } } : {}),
      });
      s.waivers.unshift(made);
      return made;
    },
    async revokeWaiver(_p, id) {
      calls.push(["revokeWaiver", id]);
      const w = s.waivers.find((x) => x.id === id);
      if (!w) throw new ApiError(404, "not_found", "No such waiver.");
      s.waivers = s.waivers.map((x) => (x.id === id ? { ...x, active: false, revoked_at: NOW } : x));
      return undefined;
    },
  };
}
