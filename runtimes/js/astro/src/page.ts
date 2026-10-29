/**
 * What the middleware does to a rendered page (pure, so it's unit-tested
 * without Astro): prerender `<glossa-*>` elements with the page's runtime,
 * and inline the page locale's slice of the release for islands and elements
 * to hydrate from, so the client renders exactly what the server did.
 */
import { fallbackChain } from "@klarlabs-studio/glossa-runtime";
import type { Artifact, BundledRelease, Runtime } from "@klarlabs-studio/glossa-runtime";
import { prerender } from "@klarlabs-studio/glossa-elements/ssr";

/** The `id` of the inline `<script type="application/json">`. */
export const INLINE_ID = "glossa-release";

/** What a page inlines: its locale and the artifacts of that locale's fallback chain. */
export interface InlineRelease {
  locale: string;
  release: BundledRelease;
}

export interface PageOptions {
  /** Replace inline defaults in `<glossa-*>` elements with translations. */
  prerender: boolean;
  /** Inline the release slice: on pages with islands or providers (`auto`), always or never. */
  inline: "auto" | "always" | "never";
}

/** The manifest plus only the artifacts `locale`'s fallback chain needs. */
export function inlineRelease(release: BundledRelease, locale: string): InlineRelease {
  const { manifest } = release;
  const artifacts: Record<string, Artifact> = {};
  for (const l of fallbackChain(locale, manifest)) {
    for (const ref of Object.values(manifest.artifacts[l] ?? {})) {
      const a = release.artifacts[ref.sha256];
      if (a) artifacts[ref.sha256] = a;
    }
  }
  return { locale, release: { manifest, artifacts } };
}

/** JSON that's safe inside `<script>`: no `</script>` or `<!--` can appear. */
const scriptJson = (value: unknown) => JSON.stringify(value).replace(/</g, "\\u003c");

/** The inline `<script>` for `locale`'s slice of `release`. */
export const inlineScript = (release: BundledRelease, locale: string) =>
  `<script type="application/json" id="${INLINE_ID}">${scriptJson(inlineRelease(release, locale))}</script>`;

const BODY_END = /<\/body\s*>/i;
/** Pages that need the inline release: ones with islands or providers. */
const HYDRATES = /<(astro-island|glossa-provider)\b/i;

/**
 * Insert `tag` before `</body>`, else at the end. Island and element scripts
 * are modules, which run after the whole document is parsed, so the inline
 * release is there when they hydrate.
 */
function insert(html: string, tag: string): string {
  const m = BODY_END.exec(html);
  return m ? html.slice(0, m.index) + tag + html.slice(m.index) : html + tag;
}

/** Render a page's HTML for `locale` with `runtime` (an Astro page, any HTML string). */
export function renderPage(
  html: string,
  runtime: Pick<Runtime, "parts" | "locale" | "dir">,
  release: BundledRelease | undefined,
  locale: string,
  o: PageOptions,
): string {
  // Vue hydrates islands against their server HTML; the prerendered text
  // differs from the element's slot on purpose, so tell Vue not to report it.
  let out = o.prerender
    ? prerender(html, runtime, { attributes: { "data-allow-mismatch": "" } })
    : html;
  const wanted = o.inline === "always" || (o.inline === "auto" && HYDRATES.test(out));
  if (release && wanted && !out.includes(`id="${INLINE_ID}"`)) {
    out = insert(out, inlineScript(release, locale));
  }
  return out;
}

/**
 * The streaming variant for on-demand pages: passes the body through as it's
 * rendered and inserts `script` before `</body>` if the page has islands or
 * providers (`inline: "auto"`) or always. Only a short tail is held back, so
 * a tag split across chunks is still found.
 */
export function inlineStream(
  body: ReadableStream<Uint8Array>,
  script: () => string,
  inline: "auto" | "always",
): ReadableStream<Uint8Array> {
  const decoder = new TextDecoder();
  const encoder = new TextEncoder();
  const HOLD = 16; // longer than "<glossa-provider" and "</body >"
  let tail = "";
  let wanted = inline === "always";
  let done = false;
  return body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, out) {
        let text = tail + decoder.decode(chunk, { stream: true });
        tail = "";
        if (!done) {
          wanted ||= HYDRATES.test(text);
          const end = BODY_END.exec(text);
          if (end) {
            if (wanted) text = text.slice(0, end.index) + script() + text.slice(end.index);
            done = true;
          } else {
            tail = text.slice(-HOLD);
            text = text.slice(0, text.length - tail.length);
          }
        }
        if (text) out.enqueue(encoder.encode(text));
      },
      flush(out) {
        let text = tail + decoder.decode();
        if (!done && (wanted ||= HYDRATES.test(text))) text += script();
        if (text) out.enqueue(encoder.encode(text));
      },
    }),
  );
}
