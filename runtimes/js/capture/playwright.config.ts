import { defineConfig, devices } from "@playwright/test";

/**
 * Capture-mode geometry in a real browser (`pnpm test:browser`). The fixture
 * page is built in the test with esbuild and loaded with setContent, so no
 * server is needed. Needs the workspace built (`pnpm -r build`) and Chromium
 * (`pnpm exec playwright install chromium`).
 */
export default defineConfig({
  testDir: "e2e",
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: "list",
  use: { trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
