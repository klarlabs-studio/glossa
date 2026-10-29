import { defineConfig, devices } from "@playwright/test";

/**
 * The overlay in a real browser (`pnpm test:browser`): Alt+click targeting
 * with real layout, live override, keyboard use, axe, and the preview CSP.
 * The app page, the overlay script (from a Studio origin) and the fake API
 * are all served from Playwright route handlers, so no server is needed.
 * Needs the workspace built (`pnpm -r build`) and Chromium
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
