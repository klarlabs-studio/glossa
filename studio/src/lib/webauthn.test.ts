import { describe, expect, it } from "vitest";
import { b64urlToBytes, bytesToB64url, creationOptions, requestOptions } from "./webauthn";

describe("base64url", () => {
  it("round-trips without padding", () => {
    const bytes = new Uint8Array([0, 255, 62, 63, 1, 2, 3]);
    const s = bytesToB64url(bytes);
    expect(s).toBe("AP8-PwECAw");
    expect([...b64urlToBytes(s)]).toEqual([...bytes]);
  });
});

describe("options", () => {
  it("decodes request options, with or without the publicKey wrapper", () => {
    for (const wrap of [(pk: Record<string, unknown>) => ({ publicKey: pk }), (pk: Record<string, unknown>) => pk]) {
      const o = requestOptions(wrap({ challenge: "AQID", rpId: "glossa.test", allowCredentials: [{ type: "public-key", id: "BAU" }] }));
      expect([...new Uint8Array(o.challenge as ArrayBuffer)]).toEqual([1, 2, 3]);
      expect(o.rpId).toBe("glossa.test");
      expect([...new Uint8Array(o.allowCredentials![0]!.id as ArrayBuffer)]).toEqual([4, 5]);
    }
  });

  it("decodes creation options", () => {
    const o = creationOptions({
      publicKey: {
        challenge: "AQID",
        rp: { name: "Glossa" },
        user: { id: "Bw", name: "ada@example.com", displayName: "Ada" },
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        excludeCredentials: [{ type: "public-key", id: "CA" }],
      },
    });
    expect([...new Uint8Array(o.user.id as ArrayBuffer)]).toEqual([7]);
    expect(o.user.name).toBe("ada@example.com");
    expect([...new Uint8Array(o.excludeCredentials![0]!.id as ArrayBuffer)]).toEqual([8]);
  });
});
