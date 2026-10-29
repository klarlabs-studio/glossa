import { defineConfig, devices } from "@playwright/test";
import { apiURL, ports, studioURL } from "./e2e/harness";

/**
 * End-to-end against a real glossa-server (built from ../platform) on a
 * testcontainers Postgres, with Studio's production build served by
 * `vite preview`, which proxies /v1 to the server like production does.
 * Needs Go, Docker and `pnpm build` first. See README.md.
 */
export default defineConfig({
  testDir: "e2e",
  // e2e/m4 is the M4 exit test's own spec (RFC 0005 §12.8). It needs a
  // project that test seeded, so it runs only when that test drives the
  // suite and is otherwise not collected at all.
  testIgnore: process.env.GLOSSA_M4_PROJECT ? [] : ["m4/**"],
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  globalSetup: "./e2e/global-setup.ts",
  use: {
    baseURL: studioURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } }],
  webServer: {
    command: `pnpm exec vite preview --port ${ports.studio} --strictPort`,
    url: studioURL,
    env: { GLOSSA_API_URL: apiURL, STUDIO_PORT: String(ports.studio) },
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
