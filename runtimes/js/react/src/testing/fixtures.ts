/**
 * The runtime contract's conformance fixtures (runtimes/testdata), test-only:
 * the same shapes @klarlabs-studio/glossa-runtime's contract tests read.
 */
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { Manifest } from "@klarlabs-studio/glossa-runtime";

import type { EdgeManifest } from "./edge.js";

// A string, not `new URL(…)`: under jsdom, `URL` is jsdom's, which node:url rejects.
const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../testdata");

export interface ScenarioCase {
  requested: string[];
  id: string;
  values: Record<string, unknown>;
  default?: string;
  bidiIsolation: "none" | "default";
  exp: string;
  expLocale: string;
  expChain: string[];
  expResolvedFrom: string | null;
  expDirection?: "ltr" | "rtl";
}

export interface Scenario {
  description: string;
  manifest: Manifest;
  artifacts: Record<string, string>;
  cases: ScenarioCase[];
}

export interface LoadingStep {
  description: string;
  edge: { manifest: EdgeManifest; artifacts: Record<string, string> };
  read: { id: string; requested: string[] };
  expActiveRelease: string | null;
  expSource: string;
  expErrors: string[];
  exp: string;
  /** `explain().rollout` (SPEC §1.4); absent means unchecked. */
  expRollout?: { id: string; percent: number; cohort: number; side: string } | null;
}

export interface LoadingSequence {
  description: string;
  publicKeys: Array<{ keyId: string; key: string }>;
  steps: LoadingStep[];
  restartBefore?: number[];
  /** The installation id to run with (SPEC §1.4); absent = the runtime's own. */
  installationId?: string;
  /** `false` runs with rollout support off. */
  rolloutSupport?: boolean;
}

/** Every `*.json` in a fixture directory, by file name without extension. */
export function fixtures<T>(dir: "scenarios" | "loading"): Array<[string, T]> {
  const path = join(testdata, dir);
  return readdirSync(path)
    .filter((f) => f.endsWith(".json"))
    .sort()
    .map((f) => [f.slice(0, -5), JSON.parse(readFileSync(join(path, f), "utf8")) as T]);
}
