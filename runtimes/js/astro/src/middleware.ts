/**
 * The integration's middleware (registered with `order: "pre"`, so it sees
 * the final HTML). Each request renders in its page locale's runtime, which
 * islands rendered on the server pick up too. Prerendered pages get their
 * `<glossa-*>` elements rendered with the release and the inline release
 * slice for hydration. On-demand pages keep streaming and only get the inline
 * slice, unless `prerender: "all"` buffers them to render elements as well.
 */
import type { MiddlewareHandler } from "astro";
import config from "virtual:glossa/config";
import release from "virtual:glossa/release";

import { inlineScript, inlineStream, renderPage } from "./page.js";
import { pageLocale } from "./routing.js";
import { requestContext, runtimeFor } from "./server.js";

const isHtml = (res: Response) => /^text\/html\b/i.test(res.headers.get("content-type") ?? "");

const withBody = (res: Response, body: BodyInit) => {
  const headers = new Headers(res.headers);
  headers.delete("content-length");
  return new Response(body, { status: res.status, statusText: res.statusText, headers });
};

export const onRequest: MiddlewareHandler = (context, next) => {
  const locale = pageLocale(context.currentLocale, config.routing);
  const runtime = runtimeFor(locale);
  return requestContext.run({ locale, runtime }, async () => {
    const res = await next();
    if (!isHtml(res) || !res.body) return res;
    const buffered = config.prerender === "all" || context.isPrerendered;
    if (buffered) {
      const html = renderPage(await res.text(), runtime, release ?? undefined, locale, {
        prerender: config.prerender !== false,
        inline: config.inline,
      });
      return withBody(res, html);
    }
    const rel = release;
    if (!rel || config.inline === "never") return res;
    return withBody(
      res,
      inlineStream(res.body, () => inlineScript(rel, locale), config.inline),
    );
  });
};
