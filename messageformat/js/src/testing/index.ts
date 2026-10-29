// Test-only helpers for the vendored Unicode MessageFormat suite, shared by
// every TypeScript implementation in the repo (`@klarlabs-studio/glossa-messageformat/testing`).
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

export const testdataDir = join(dirname(fileURLToPath(import.meta.url)), "../../../testdata");
export const unicodeTestsDir = join(testdataDir, "unicode/tests");
export const messageSchemaPath = join(testdataDir, "unicode/data-model/message.schema.json");

export interface SuiteParam {
  name: string;
  value: unknown;
  type?: "datetime";
}

export interface SuiteCase {
  file: string;
  description?: string;
  locale: string;
  src: string;
  bidiIsolation?: "default" | "none";
  params?: SuiteParam[];
  exp?: string;
  expParts?: unknown[];
  expErrors?: Array<{ type: string }>;
  tags?: string[];
}

interface SuiteFile {
  defaultTestProperties?: Partial<SuiteCase>;
  tests: Array<Partial<SuiteCase>>;
}

/** Every case of every upstream file, with defaults applied, in file order. */
export function suiteCases(): SuiteCase[] {
  const files = readdirSync(unicodeTestsDir, { recursive: true, encoding: "utf8" })
    .filter((f) => f.endsWith(".json"))
    .sort();
  const cases: SuiteCase[] = [];
  for (const f of files) {
    const path = join(unicodeTestsDir, f);
    const suite = JSON.parse(readFileSync(path, "utf8")) as SuiteFile;
    for (const t of suite.tests) {
      cases.push({
        ...suite.defaultTestProperties,
        ...t,
        file: relative(unicodeTestsDir, path),
      } as SuiteCase);
    }
  }
  return cases;
}

const dataModelErrors = new Set([
  "duplicate-attribute",
  "duplicate-declaration",
  "duplicate-option-name",
  "duplicate-variant",
  "missing-fallback-variant",
  "missing-selector-annotation",
  "variant-key-mismatch",
]);

/** How the upstream harness classifies a case (see the suite README). */
export function caseKind(tc: SuiteCase): "valid" | "syntax-error" | "data-model-error" | "error" {
  if (!tc.expErrors) return "valid";
  for (const e of tc.expErrors) {
    if (e.type === "syntax-error") return "syntax-error";
    if (dataModelErrors.has(e.type)) return "data-model-error";
  }
  return "error";
}

/** Upstream params → a values object; `type: "datetime"` values become `Date`s. */
export function paramValues(params: SuiteParam[] | undefined): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const p of params ?? []) {
    values[p.name] = p.type === "datetime" ? new Date(String(p.value)) : p.value;
  }
  return values;
}

export function caseName(tc: SuiteCase): string {
  const loc = tc.locale === "en-US" ? "" : ` [${tc.locale}]`;
  const params = tc.params ? ` / ${JSON.stringify(paramValues(tc.params))}` : "";
  return `${tc.src}${loc}${params}`.replace(/ *\n */g, " ");
}

export { testFunctions } from "./functions.js";
