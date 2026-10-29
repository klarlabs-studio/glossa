import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { describe, expect, it } from "vitest";
import { loadingFixture as loading } from "./testing/fixtures.js";
import { checkManifest, jcs, sha256Hex, verifySignature } from "./verify.js";

describe("jcs (RFC 8785)", () => {
  it("sorts keys by UTF-16 code units at every level and emits no whitespace", () => {
    const value = { b: [1, { z: true, a: null }], a: "x", "\u20ac": 1, "\r": 2, "\ud83d\ude00": 3 };
    expect(jcs(value)).toBe(
      `{"\\r":2,"a":"x","b":[1,{"a":null,"z":true}],"\u20ac":1,"\ud83d\ude00":3}`,
    );
  });

  it("serializes strings and numbers as ECMAScript does", () => {
    expect(jcs({ s: "\u2028\u0007\"\\\u00e9", n: [1e21, 0.1, -0, 1.5e-7, 100] })).toBe(
      `{"n":[1e+21,0.1,0,1.5e-7,100],"s":"\u2028\\u0007\\"\\\\\u00e9"}`,
    );
  });
});

describe("sha256Hex", () => {
  it("hashes the UTF-8 bytes of a string", async () => {
    expect(await sha256Hex("abc")).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    );
    const text = '{"messages":{"a":"Zur Kasse – مرحبا 😀"}}';
    expect(await sha256Hex(text)).toBe(createHash("sha256").update(text, "utf8").digest("hex"));
  });
});

describe("checkManifest", () => {
  const base = loading("last-good").steps[0]!.edge.manifest.body!;

  it("accepts v1 and ignores unknown top-level fields", () => {
    expect(checkManifest(base)).toBeUndefined();
    expect(checkManifest({ ...base, future: { anything: 1 } })).toBeUndefined();
  });

  it("rejects another major schema version, and malformed manifests", () => {
    expect(checkManifest({ ...base, schema: "glossa.manifest/v2" })).toMatch(/schema/);
    expect(checkManifest({ ...base, schema: "glossa.manifest/v10" })).toMatch(/schema/);
    expect(checkManifest(null)).toBeDefined();
    expect(checkManifest({ ...base, locales: "en" })).toBeDefined();
    expect(checkManifest({ ...base, release: {} })).toBeDefined();
  });
});

describe("verifySignature (Ed25519 over JCS)", () => {
  const fixture = loading("signatures");
  const keys = fixture.publicKeys;
  const [signed, unsigned, wrongKey] = fixture.steps.map((s) => s.edge.manifest.body!);

  it("accepts a manifest signed by a configured key", async () => {
    expect(await verifySignature(signed!, keys)).toBeUndefined();
  });

  it("rejects an unsigned manifest and one signed by another key", async () => {
    expect(await verifySignature(unsigned!, keys)).toMatch(/not signed/);
    expect(await verifySignature(wrongKey!, keys)).toMatch(/no valid signature/);
  });

  it("rejects a manifest changed after signing", async () => {
    const tampered = { ...signed!, sourceLocale: "de" };
    expect(await verifySignature(tampered, keys)).toMatch(/no valid signature/);
  });

  it("tries every signature, matching keys by keyId", async () => {
    const { publicKey, privateKey } = generateKeyPairSync("ed25519");
    const raw = publicKey.export({ format: "jwk" }).x!;
    const { signatures: _drop, ...body } = signed!;
    const sig = sign(null, Buffer.from(jcs(body)), privateKey).toString("base64url");
    const manifest = {
      ...signed!,
      signatures: [
        { keyId: "k_old", alg: "Ed25519" as const, sig: "AAAA" },
        { keyId: "k_new", alg: "Ed25519" as const, sig },
      ],
    };
    expect(await verifySignature(manifest, [{ keyId: "k_new", key: raw }])).toBeUndefined();
    expect(await verifySignature(manifest, [{ keyId: "k_old", key: raw }])).toBeDefined();
  });

  it("treats a malformed key or signature as invalid, never throwing", async () => {
    const bad = {
      ...signed!,
      signatures: [{ keyId: "k_test", alg: "Ed25519" as const, sig: "!" }],
    };
    expect(await verifySignature(bad, keys)).toBeDefined();
    expect(await verifySignature(signed!, [{ keyId: "k_test", key: "short" }])).toBeDefined();
  });
});
