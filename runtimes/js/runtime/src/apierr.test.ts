import { describe, expect, it } from "vitest";
import { apiErrorMessage, parseApiError, resolveApiError } from "./apierr.js";
import { createRuntime } from "./runtime.js";
import { memoryStorage } from "./storage.js";
import { release, text } from "./testing/release.js";

const envelope = {
  error: {
    code: "validation_email_required",
    message: "Email address is required",
    key: "validation.email.required",
    params: { field: "email" },
    status: 400,
  },
};

const r = release("rel_1", 1, {
  de: {
    "validation.email.required": {
      type: "message",
      declarations: [],
      pattern: ["Das Feld ", { type: "expression", arg: { type: "variable", name: "field" } }, " fehlt"],
    },
  },
  en: { other: text("x") },
});
const runtime = createRuntime({
  locales: "de",
  bundled: { manifest: r.manifest, artifacts: r.parsed },
  storage: memoryStorage(),
  refreshInterval: 0,
  bidiIsolation: "none",
});

describe("parseApiError", () => {
  it("reads the envelope, the bare payload and the legacy string", () => {
    expect(parseApiError(envelope)?.key).toBe("validation.email.required");
    expect(parseApiError(envelope.error)?.params).toEqual({ field: "email" });
    expect(parseApiError({ error: "boom" })).toMatchObject({ code: "unknown_error", message: "boom", key: "" });
  });

  it("defaults a missing key and status, and drops non-object params", () => {
    expect(parseApiError({ error: { code: "c", message: "m", params: "x" } })).toEqual({
      code: "c",
      message: "m",
      key: "",
      params: undefined,
      status: 500,
    });
  });

  it.each([null, undefined, 1, "x", {}, { wrong: "shape" }, { error: { code: 1 } }])("is null for %j", (v) => {
    expect(parseApiError(v)).toBeNull();
  });
});

describe("apiErrorMessage", () => {
  it("is the key, its args and the English fallback", () => {
    expect(apiErrorMessage(envelope)).toEqual({
      id: "validation.email.required",
      args: { field: "email" },
      fallback: "Email address is required",
    });
    expect(apiErrorMessage({ error: "boom" })).toEqual({ id: "", args: {}, fallback: "boom" });
    expect(apiErrorMessage(null)).toBeNull();
  });
});

describe("resolveApiError", () => {
  it("formats the key with the params when the release has it", () => {
    expect(resolveApiError(runtime, envelope)).toBe("Das Feld email fehlt");
  });

  it("falls back to the server's message when the key is not in the release", () => {
    const unknown = { error: { ...envelope.error, key: "validation.nope" } };
    expect(resolveApiError(runtime, unknown)).toBe("Email address is required");
    expect(resolveApiError(runtime, { error: "boom" })).toBe("boom");
  });

  it("never throws on a body that is not an error", () => {
    expect(resolveApiError(runtime, null)).toBe("Unknown error");
    expect(resolveApiError(runtime, { wrong: "shape" }, { unknown: "Etwas ging schief" })).toBe("Etwas ging schief");
  });
});
