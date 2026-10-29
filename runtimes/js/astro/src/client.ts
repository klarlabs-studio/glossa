/**
 * `@klarlabs-studio/glossa-astro/client`: the page's one runtime, shared by every island and
 * `<glossa-provider>` on it. It starts from the release slice the page
 * inlined (so the first render matches the server's HTML) and then refreshes
 * from the edge like any browser runtime. On the server, during an island's
 * render, it's the request's runtime instead.
 */
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { Runtime } from "@klarlabs-studio/glossa-runtime";
import config from "virtual:glossa/config";

import type { InlineRelease } from "./page.js";
import { provideRuntime } from "./translate.js";

let page: Runtime | undefined;

/** The runtime for this page (browser) or this request (server). */
export function getRuntime(): Runtime {
  const server = (globalThis as Record<symbol, (() => Runtime) | undefined>)[
    Symbol.for("glossa.astro.server")
  ];
  if (server) return server();
  if (page) return provideRuntime(page), page;
  // No document under Node (unit tests): the runtime then renders inline defaults.
  const doc = (globalThis as { document?: Document }).document;
  let inline: Partial<InlineRelease> = {};
  try {
    inline = JSON.parse(doc?.getElementById("glossa-release")?.textContent ?? "{}");
  } catch {
    // No inline release: the runtime starts from persisted storage or the edge.
  }
  page = createRuntime({
    edge: config.edge,
    deliveryKey: config.deliveryKey,
    environment: config.environment,
    publicKeys: config.publicKeys,
    locales: inline.locale ?? (doc?.documentElement.lang || undefined),
    bundled: inline.release,
  });
  provideRuntime(page); // for `/astro/translate`
  return page;
}
