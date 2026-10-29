/**
 * The fake API's contract check: validates bodies against the components
 * schemas of platform/api/openapi.yaml (OpenAPI 3.1, so JSON Schema
 * 2020-12), so the fake can't drift from the API the overlay will really
 * call. Test-only, Node-only.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Ajv2020 } from "ajv/dist/2020.js";
import type { ValidateFunction } from "ajv/dist/2020.js";
import { parse } from "yaml";

import type { Validator } from "./fake-api.js";

const spec = join(import.meta.dirname, "../../../../../platform/api/openapi.yaml");

/** A validator over the spec's `components.schemas`. */
export function openapiValidator(): Validator {
  const doc = parse(readFileSync(spec, "utf8")) as Record<string, unknown>;
  const ajv = new Ajv2020({ allErrors: true, strict: false, validateFormats: false });
  ajv.addSchema({ ...doc, $id: "openapi" });
  const cache = new Map<string, ValidateFunction>();
  return (schema, value) => {
    let v = cache.get(schema);
    if (!v) {
      v = ajv.getSchema(`openapi#/components/schemas/${schema}`);
      if (!v) return [`no schema ${schema} in openapi.yaml`];
      cache.set(schema, v);
    }
    if (v(value)) return [];
    return (v.errors ?? []).map((e) => `${e.instancePath || "/"} ${e.message ?? ""}`.trim());
  };
}
