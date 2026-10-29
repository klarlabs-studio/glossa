import { defineConfig } from "vitest/config";

import { glossaAstroTesting } from "./src/test-support.js";

export default defineConfig({
  // Serves virtual:glossa/config for the specs that import `src/client.ts`, as a product's config would.
  plugins: [glossaAstroTesting()],
  test: {
    include: ["src/**/*.test.ts", "test/**/*.test.ts"],
    environment: "node",
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      exclude: ["src/**/*.test.ts", "src/testing/**", "src/index.ts"],
    },
  },
});
