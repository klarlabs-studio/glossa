import { afterEach, describe, expect, it, vi } from "vitest";
import { configuredEdge, edgeOrigin, maskKey, resetEdgeOrigin, snippets } from "./snippets";

const key = "glossa_pk_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345";
const base = { edge: "https://edge.example.com", deliveryKey: key, environment: "production", publicKeys: [] };

describe("snippets", () => {
  it("covers every runtime with the key, edge and environment", () => {
    const list = snippets(base);
    expect(list.map((s) => s.id)).toEqual(["runtime", "vue", "elements", "go"]);
    for (const s of list) {
      expect(s.code).toContain(key);
      expect(s.code).toContain("https://edge.example.com");
      expect(s.code).toContain("production");
    }
  });

  it("uses each runtime's own API", () => {
    const [runtime, vue, elements, go] = snippets(base);
    expect(runtime!.code).toContain('import { createRuntime } from "@klarlabs-studio/glossa";');
    expect(vue!.code).toContain(".use(\n    createGlossa({");
    expect(elements!.code).toContain(`<glossa-provider\n  edge="https://edge.example.com"\n  delivery-key="${key}"`);
    expect(go!.code).toContain("glossa.New(glossa.Config{");
    expect(go!.code).toContain(`DeliveryKey: "${key}",`);
    expect(go!.code).not.toContain("PublicKeys");
  });

  it("pins signing keys when there are any", () => {
    const [runtime, , elements, go] = snippets({ ...base, environment: "staging", publicKeys: [{ keyId: "k1", key: "pub1" }] });
    expect(runtime!.code).toContain('publicKeys: [{ keyId: "k1", key: "pub1" }],');
    expect(runtime!.code).toContain('environment: "staging",');
    expect(elements!.code).toContain('public-keys="k1:pub1"');
    expect(go!.code).toContain('key1, err := glossa.ParsePublicKey("k1", "pub1")');
    expect(go!.code).toContain("PublicKeys:  []glossa.PublicKey{key1},");
  });

  it("escapes attribute values in markup", () => {
    const [, , elements] = snippets({ ...base, edge: 'https://e.example/"x' });
    expect(elements!.code).toContain('edge="https://e.example/&quot;x"');
  });
});

describe("the edge origin", () => {
  afterEach(resetEdgeOrigin);
  const config = (body: unknown, status = 200) => async () => new Response(typeof body === "string" ? body : JSON.stringify(body), { status });

  it("comes from the server first", async () => {
    const fetchConfig = vi.fn(config({ edgeUrl: "https://edge.image.dev" }));
    expect(await edgeOrigin({ announced: async () => "https://edge.server.dev/", config: fetchConfig })).toBe("https://edge.server.dev");
    expect(fetchConfig).not.toHaveBeenCalled();
  });

  it("falls back to the image's /config.json when the server announces none", async () => {
    expect(await edgeOrigin({ announced: async () => undefined, config: config({ apiBaseUrl: "", edgeUrl: "https://edge.acme.dev/", environment: "production" }) })).toBe(
      "https://edge.acme.dev",
    );
    resetEdgeOrigin();
    expect(await edgeOrigin({ announced: () => Promise.reject(new Error("offline")), config: config({ edgeUrl: "https://edge.acme.dev" }) })).toBe(
      "https://edge.acme.dev",
    );
  });

  it("falls back to the build when the runtime configuration has none", async () => {
    vi.stubEnv("VITE_GLOSSA_EDGE_URL", "https://edge.build.dev");
    expect(await edgeOrigin({ config: config({ edgeUrl: "" }) })).toBe("https://edge.build.dev");
    resetEdgeOrigin();
    // A dev server answers /config.json with the SPA's HTML.
    expect(await edgeOrigin({ config: config("<!doctype html>") })).toBe("https://edge.build.dev");
    resetEdgeOrigin();
    expect(await edgeOrigin({ config: config({}, 404) })).toBe("https://edge.build.dev");
    vi.unstubAllEnvs();
  });

  it("accepts only absolute http(s) origins", () => {
    expect(configuredEdge({ VITE_GLOSSA_EDGE_URL: "https://edge.acme.dev/ " })).toBe("https://edge.acme.dev");
    expect(configuredEdge({ VITE_GLOSSA_EDGE_URL: "javascript:alert(1)" })).toBeUndefined();
    expect(configuredEdge({ VITE_GLOSSA_EDGE_URL: "edge.acme.dev" })).toBeUndefined();
    expect(configuredEdge({})).toBeUndefined();
  });
});

it("masks a key to its recognisable start", () => {
  expect(maskKey(key)).toBe("glossa_pk_ABCD…");
});
