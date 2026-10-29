import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Hit-testing with real layout, CSP and axe: that's e2e/ (Playwright, `pnpm test:browser`).
    include: ["src/**/*.test.ts"],
    environment: "jsdom",
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      exclude: ["src/**/*.test.ts", "src/testing/**", "src/index.ts"],
    },
  },
});
