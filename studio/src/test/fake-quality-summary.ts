/**
 * An in-memory QualitySummaryPort for component tests, plus builders for
 * the summary shape wave 5's API slice will send (RFC 0005 §8).
 *
 * The builders deliberately leave every optional number *out* by
 * default, so a test that wants a measured number has to say so. That is
 * the shape the screens are most likely to get wrong: it is far easier
 * to render a zero than to notice a missing field.
 *
 * Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { QualitySummaryPort } from "../api/quality-summary";
import type { FindingLayer } from "../api/quality-schemas";
import {
  SUMMARY_SCHEMA,
  type LocaleHealth,
  type ProjectHealth,
  type QualitySummary,
  type SummaryLayer,
} from "../api/quality-summary-schemas";
import type { ProjectRef } from "../api/releases";

const NOW = "2026-09-19T08:00:00Z";

export interface FakeQualitySummary extends QualitySummaryPort {
  readonly calls: ProjectRef[];
}

/** A layer that ran here and found nothing: the only green answer. */
export function cleanLayer(layer: FindingLayer): SummaryLayer {
  return { layer, available: true, checked: true, findings: { errors: 0, warnings: 0, waived: 0 } };
}

/** A layer that cannot run for this locale at all (intent §41). */
export function unavailableLayer(layer: FindingLayer, why: SummaryLayer["unavailable"] = "unsupported_locale"): SummaryLayer {
  return { layer, available: false, unavailable: why, checked: false };
}

/** A layer that could have run here and did not: no counts, because there are none. */
export function uncheckedLayer(layer: FindingLayer): SummaryLayer {
  return { layer, available: true, checked: false };
}

const SINCE = "2026-09-01T00:00:00Z";
const EXPIRES = "2026-09-30T12:01:00Z";

export function gradedLayer(layer: FindingLayer, errors: number, warnings = 0, waived = 0): SummaryLayer {
  return { layer, available: true, checked: true, findings: { errors, warnings, waived } };
}

export function localeHealth(code: string, over: Partial<LocaleHealth> = {}): LocaleHealth {
  return { code, direction: "ltr", is_source: false, layers: [], ...over };
}

export function projectHealth(over: Partial<ProjectHealth> = {}): ProjectHealth {
  return { ...over };
}

export function qualitySummary(over: Partial<Omit<QualitySummary, "schema">> = {}): QualitySummary {
  return {
    schema: SUMMARY_SCHEMA,
    project_id: "prj_1",
    environment: "production",
    since: SINCE,
    computed_at: NOW,
    expires_at: EXPIRES,
    cached: false,
    project: {},
    locales: [],
    unmeasured: [],
    ...over,
  };
}

/**
 * The port. Given a summary it answers with it; given nothing it fails
 * the way a server without the endpoint does, which is the state every
 * screen must survive until the sibling slice lands.
 */
export function createFakeQualitySummary(summary?: QualitySummary, failWith?: ApiError): FakeQualitySummary {
  const calls: ProjectRef[] = [];
  return {
    calls,
    async summary(p: ProjectRef): Promise<QualitySummary> {
      calls.push(p);
      if (failWith) throw failWith;
      if (!summary) throw new ApiError(404, "not_found", "No such operation.");
      return summary;
    },
  };
}
