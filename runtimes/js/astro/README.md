# @klarlabs-studio/glossa/astro

Glossa for Astro 5. Static pages are rendered with a release at build time, so
they ship translated HTML rather than client JavaScript. Vue islands and
`<glossa-*>` elements share one runtime per page, which starts from the same
release as the server. Locale routing follows Astro's i18n routing.

```js
// astro.config.mjs
import vue from "@astrojs/vue";
import { defineConfig } from "astro/config";
import glossa from "@klarlabs-studio/glossa/astro";

export default defineConfig({
  i18n: { locales: ["de", "en"], defaultLocale: "de" },
  integrations: [
    vue({
      appEntrypoint: "@klarlabs-studio/glossa/astro/vue",
      template: { compilerOptions: { isCustomElement: (tag) => tag.startsWith("glossa-") } },
    }),
    glossa({ edge: "https://edge.example.com", deliveryKey: "pk_7Hc2…", elements: true }),
  ],
});
```

## Options

| Option | Default | |
|---|---|---|
| `edge`, `deliveryKey` | none | The build fetches the edge's current release once at build start. Islands and elements refresh from the edge in the browser. |
| `environment` | `production` | The release has to be for this environment. |
| `publicKeys` | none | Trusted signing keys, `[{ keyId, key }]`. |
| `release` | the edge's | A `glossa pull --release` directory (relative to the project root), or a `{ manifest, artifacts }` object. A directory holds `manifest.json` and `a/<sha256>.json`, which mirror the edge's paths. |
| `requireRelease` | off; `GLOSSA_REQUIRE_RELEASE=1` turns it on when unset | Fails the build, instead of warning, when no release could be loaded (no `release`, and no `edge` + `deliveryKey`). See [Production builds](#production-builds). |
| `locales`, `defaultLocale` | Astro's `i18n`, then the release | The locales the site renders. |
| `prerender` | `"static"` | Controls rendering of `<glossa-*>` elements into the HTML: `"static"` does it on prerendered pages; `"all"` also does it on on-demand pages, which buffers them instead of streaming; `false` turns it off. |
| `inline` | `"auto"` | Controls inlining of the page locale's release slice, the manifest plus that locale's fallback-chain artifacts, as `<script type="application/json" id="glossa-release">`: `"auto"` inlines it on pages that have islands or providers, `"always"` on every page, `"never"` nowhere. |
| `elements` | `false` | Defines the elements on every page and makes `<glossa-provider>` without `edge` use the page runtime. |
| `usages` | on | Where messages are used. `astro build` runs [`@klarlabs-studio/glossa/unplugin`](../unplugin), which writes `.glossa/usages.json` beside `outDir` (not inside it, so it's never deployed) for `glossa context push`. The object goes to the plugin (`application`, `commit`, `branch`, `routes`, `keys`, which defaults to the release's message keys); `false` turns it off. Components are the `.astro`/`.vue` file names; routes come from `src/pages/**`. |
| `overlay` | on when `environment` isn't `production` | The in-product editor's loader ([RFC 0004 §5.1](../../../docs/rfcs/0004-context.md)), imported by a page script on every page. `true` with `environment: "production"` fails the build; `false` leaves it out. See [`@klarlabs-studio/glossa/unplugin`](../unplugin/README.md#the-in-product-editors-loader). |
| `studio` | none; required while `overlay` is on | `{ origin, integrity, tenant, project, api? }`: where the overlay comes from (Studio's origin and the script's SRI hash from its `/overlay/v1/overlay.json`) and what it edits. |

The release is loaded through `@klarlabs-studio/glossa`, so the build verifies it the
same way a browser would: schema, environment, the signature when keys are
set, and every artifact's SHA-256. If the release can't be loaded completely,
**the build fails**, because a static site must not quietly ship its inline
defaults. With no release configured, the build logs a warning and pages
render their inline defaults.

### Production builds

With no key, `glossa()` warns and builds with the inline defaults. That is right for local
development, and wrong for an image built in CI whose `GLOSSA_DELIVERY_KEY` didn't arrive: it would
ship its fallbacks in every locale with only a log line to show for it. Make the build fail instead:

```js
glossa({ edge: process.env.GLOSSA_EDGE, deliveryKey: process.env.GLOSSA_DELIVERY_KEY, requireRelease: true })
```

or leave the config alone and set the variable where the production build runs:

```dockerfile
ARG GLOSSA_DELIVERY_KEY
ENV GLOSSA_REQUIRE_RELEASE=1
RUN pnpm build   # fails here, not in production, if GLOSSA_DELIVERY_KEY is empty
```

An explicit `requireRelease: false` wins over the variable. A release that is configured but can't be
loaded completely always fails the build, with or without this option.

## In `.astro` components: `@klarlabs-studio/glossa/astro/server`

```astro
---
import { alternates, getGlossa, localizePath } from "@klarlabs-studio/glossa/astro/server";
const { t, locale, dir } = getGlossa(Astro);
---
<html lang={locale} dir={dir}>
  <head>
    <title>{t("home.title", {}, { default: "Willkommen" })}</title>
    {alternates(Astro).map((a) => <link rel="alternate" hreflang={a.hreflang} href={a.href} />)}
  </head>
  <a href={localizePath("/pricing", "en")}>English</a>
</html>
```

| | |
|---|---|
| `getGlossa(Astro)` | Returns `{ t, parts, explain, locale, dir, release, runtime }` for the page's locale. The page locale is `Astro.currentLocale` when the site renders it, otherwise the default locale. |
| `localizePath(path, locale)` | Gives `path` in `locale` under Astro's routing: the default locale is unprefixed unless `prefixDefaultLocale` is set, and other locales get their `/{path}/` prefix, with `base` respected. |
| `alternates(Astro)` | Returns hreflang targets for the current page, including `x-default`. |
| `localeParams()` | Returns `getStaticPaths()` entries for a `[...locale]` route. |
| `siteRouting` | The resolved locales, default locale and prefix setting. |

## Elements, islands and hydration

- **`<glossa-*>` elements** in `.astro` files, `.vue` templates or anywhere
  else in the page are rendered at build time by the middleware, which uses
  `prerender` from `@klarlabs-studio/glossa/elements/ssr`. The HTML is translated before any
  JavaScript runs. Elements whose message is missing keep their inline
  default. Inside `.vue` files, write `message="…"` in place of `key="…"`,
  because Vue never renders `key` (see the elements' MIGRATION.md).
- **Vue islands** get the page's runtime through the app entrypoint
  `@klarlabs-studio/glossa/astro/vue` (with your own entrypoint, call it from there). On the
  server that runtime is the request's, in the page's locale. In the browser
  it's one runtime per page (`getRuntime()` from `@klarlabs-studio/glossa/astro/client`),
  created from the inlined slice. The first render therefore matches the
  server HTML, and a newer release from the edge activates afterwards.
  Prerendered elements carry `data-allow-mismatch`, so Vue doesn't report
  their translated text as a hydration mismatch.
- **Client JavaScript** never contains the release. It lives in the server
  bundle and in the per-page inline JSON.
- **On-demand (SSR) pages** keep streaming. The inline slice is inserted
  before `</body>` as the page streams through. Elements on those pages are
  only prerendered with `prerender: "all"`. For streamed pages, use
  `getGlossa()` and Vue components instead.

## Unit tests, and libraries outside Astro

`/astro/client` and `/astro/server` import `virtual:glossa/config`, which only exists inside an Astro
build. Two ways to keep that out of your way:

**Test the code that imports them** with `@klarlabs-studio/glossa/astro/testing`. Add its Vite plugin
to `vitest.config.ts`; no `vi.mock`, no alias, no `server.deps.inline` by hand:

```ts
import { defineConfig } from "vitest/config";
import { glossaAstroTesting } from "@klarlabs-studio/glossa/astro/testing";

export default defineConfig({ plugins: [glossaAstroTesting()] });
```

The plugin serves `virtual:glossa/config` (an inert stub: no edge, no key, English, `environment:
"test"`) and `virtual:glossa/release` (`null`), and has Vitest process the package instead of loading
it as an external. `getRuntime()` then works under Node (no `document`) and renders inline defaults.
`glossaAstroTesting(overrides, release)` changes the stub config, or gives server-side code a
`BundledRelease`; `stubConfig` is exported too. The package's own `test/unit-fixture.test.ts` is a
helper that calls `getRuntime()`, tested this way.

**Write shared helpers and libraries without `/astro/client`** with
`@klarlabs-studio/glossa/astro/translate`. It imports no virtual module and no Astro code:

```ts
import { t } from "@klarlabs-studio/glossa/astro/translate";

export const saveLabel = () => t("form.save", "Speichern");
```

`t(id, fallback, values?)` renders with the request's runtime on the server, or the page's in the
browser (`getRuntime()` publishes it, which every island, provider and elements page already does),
and returns `fallback` as written when there is none (tests, scripts). `has(id)`, `currentRuntime()`
and `provideRuntime(runtime)` (for tests and hosts with their own runtime) are there too. A library
that must not depend on globals at all takes a `Runtime` (or `{ t }`) as a parameter, which needs no
Astro anywhere.

## Tests

`pnpm test` runs the pure parts (routing, page rendering, streaming inline,
release loading, the integration hooks) and one real `astro build` of
`test/fixture`: a static site with i18n routing, a Vue island, elements, and a
page without islands. The builds take about 25 s. It checks the translated HTML
per locale, the islands' server rendering, the inline slice, that no
client chunk contains the catalog, and the usages `@klarlabs-studio/glossa/unplugin` wrote. A second build of the same site for a
`preview` environment checks that every page loads the overlay loader from a
module script, and the production build that none of it is there. Run `pnpm
build` first, because the fixture uses `dist/` the way an installed package
would.

## Size

`@klarlabs-studio/glossa/astro/client`, the only part of this package that ships to
browsers, is 0.60 kB brotli without the runtime (budget 0.75 kB). Islands also
load `@klarlabs-studio/glossa/vue` (1.2 kB) and the runtime (6 kB); elements load
`@klarlabs-studio/glossa/elements`.
