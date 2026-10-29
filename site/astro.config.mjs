// @ts-check
import { defineConfig } from "astro/config";

// The landing page of glossa.klarlabs.de. Studio (studio/) serves every
// other path on that host; the ingress sends only "/" and /_astro/* here
// (deploy/charts/glossa-platform). Plain static HTML and CSS: no client
// JavaScript, and nothing inline, because the CSP is `default-src 'none'`
// with self-only scripts and styles.
export default defineConfig({
  site: "https://glossa.klarlabs.de",
  output: "static",
  build: { inlineStylesheets: "never" },
  server: { port: 4321, host: true },
  devToolbar: { enabled: false },
});
