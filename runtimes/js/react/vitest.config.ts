import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
    // Server rendering runs in plain Node, so a browser global touched at
    // import or render time fails the test; hydration tests opt into jsdom.
    environment: "node",
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      exclude: ["src/**/*.test.ts", "src/testing/**", "src/index.ts"],
    },
  },
});
