import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Geometry needs a real layout engine: that's e2e/ (Playwright, `pnpm test:browser`).
    include: ["src/**/*.test.ts"],
    environment: "jsdom",
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      exclude: ["src/**/*.test.ts", "src/testing/**", "src/index.ts"],
    },
  },
});
