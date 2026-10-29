import { fileURLToPath } from "node:url";
import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vitest/config";

import { overlayDelivery } from "./build/overlay";

// The API is proxied, so the session cookie (`__Host-`, SameSite=Lax) is
// first-party and requests are same-origin — as in production, where
// Studio and /v1 share an origin.
const api = process.env.GLOSSA_API_URL ?? "http://localhost:8080";
const proxy = { "/v1": { target: api, changeOrigin: false } };

export default defineConfig({
  plugins: [
    vue({ template: { compilerOptions: { isCustomElement: (tag) => tag.startsWith("kl-") } } }),
    // The in-product editor, served at /overlay/v1/overlay.js (RFC 0004 §5.1).
    overlayDelivery(),
  ],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    // One Lit for the design system's elements.
    dedupe: ["lit"],
  },
  server: { port: 5173, strictPort: true, proxy },
  preview: { port: Number(process.env.STUDIO_PORT ?? 4173), strictPort: true, proxy },
  build: { target: "es2022", manifest: true, sourcemap: true },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.test.ts", "build/**/*.test.ts"],
    restoreMocks: true,
    unstubGlobals: true,
  },
});
