# @klarlabs-studio/glossa

Glossa for JavaScript, in one package: the runtime, the web components, the Vue, React and Astro
bindings, the usages bundler plugin, capture, the in-product editor and the MessageFormat kernel.
Each part is a subpath of this package.

```sh
npm install @klarlabs-studio/glossa
npm install vue      # only for /vue (and /astro/vue); react and astro likewise
```

### Registries

The package is published to two registries, with the same version and contents:

| Registry | Use when | Auth |
|---|---|---|
| npmjs.org (`https://registry.npmjs.org`), the default | Your repo has no `@klarlabs-studio` registry setting. | None. Published with provenance. |
| GitHub Packages (`https://npm.pkg.github.com`) | Your `.npmrc` already routes `@klarlabs-studio` there (for `@klarlabs-studio/ui`). | A token with `read:packages`. |

npm and pnpm send a whole scope to **one** registry, so a repo that sets
`@klarlabs-studio:registry=https://npm.pkg.github.com` for `@klarlabs-studio/ui`
gets `@klarlabs-studio/glossa` from there too. Per-repo `.npmrc` options:

```ini
# A. The scope already goes to GitHub Packages (the Klarlabs default): nothing to
#    change except authentication. Both packages install from one registry.
@klarlabs-studio:registry=https://npm.pkg.github.com
//npm.pkg.github.com/:_authToken=${NODE_AUTH_TOKEN}

# B. Everything from npmjs.org: drop the scope line (and publish
#    @klarlabs-studio/ui there too, or install it with option C).
#    No .npmrc entry is needed for @klarlabs-studio/glossa.

# C. Keep the scope on GitHub Packages and take this one package from npmjs.org:
#    one install with a flag, no .npmrc change (the lockfile then records npmjs).
#    npm install @klarlabs-studio/glossa --@klarlabs-studio:registry=https://registry.npmjs.org
#    This also redirects @klarlabs-studio/ui for that command, so install the two separately.
```

With option A a fresh `npm install` and `npm ci` both work. A `404` from
`npm.pkg.github.com` means the token is missing or lacks `read:packages`.

```ts
import { createRuntime } from "@klarlabs-studio/glossa";
import { createGlossa, GlossaText } from "@klarlabs-studio/glossa/vue";
import "@klarlabs-studio/glossa/elements";
import glossa from "@klarlabs-studio/glossa/unplugin/vite";
```

## Subpaths

| Subpath | What it is | Docs |
|---|---|---|
| `@klarlabs-studio/glossa` | The runtime: release loader, locale resolver, `explain`, a MessageFormat 2 interpreter over precompiled messages. Only `Intl` and WebCrypto. | [runtime](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/runtime/README.md) |
| `/idb` | IndexedDB storage for the persisted last-good release. | [runtime](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/runtime/README.md) |
| `/apierr` | `resolveApiError(runtime, body)`: an `apierr` error envelope (Go `apierr` module) into localized text; `apiErrorMessage` gives the key and arguments. | [runtime](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/runtime/README.md), [MIGRATION](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/elements/MIGRATION.md) |
| `/dev` | The in-product editor's loader; never in production builds. | [runtime](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/runtime/README.md) |
| `/elements` | `<glossa-provider>`, `<glossa-text\|rich\|plural\|select>`, `<glossa-selector>`. Also `/elements/ssr` and `/elements/parts`. | [elements](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/elements/README.md), [MIGRATION](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/elements/MIGRATION.md) |
| `/vue` | Vue 3.5+ plugin, composables and `<GlossaText>`. | [vue](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/vue/README.md) |
| `/react` | React 18.3+ and 19 provider, hooks and `<T>`. | [react](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/react/README.md) |
| `/astro` | Astro 6 and 7 integration. Also `/astro/server`, `/astro/client`, `/astro/vue`, `/astro/elements`, `/astro/middleware`, `/astro/translate` (`t(id, fallback)` without Astro) and `/astro/testing` (Vitest plugin). | [astro](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/astro/README.md) |
| `/unplugin` | The usages plugin for Vite, Rollup, webpack and esbuild: `/unplugin/vite`, `/unplugin/rollup`, `/unplugin/webpack`, `/unplugin/esbuild`. | [unplugin](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/unplugin/README.md) |
| `/capture` | Capture mode: markers and the capture script. `/capture/probes` is the visual probe pass. Never loaded by applications. | [capture](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/capture/README.md) |
| `/overlay` | The in-product editor (a Lit web component). `/overlay/bundle` is the file Studio serves at `/overlay/v1/overlay.js`. | [overlay](https://github.com/klarlabs-studio/glossa/blob/main/runtimes/js/overlay/README.md) |
| `/messageformat` | The MessageFormat kernel for tooling: MF2 and ICU MF1 into the canonical MF2 data model, stringify, format, validate. `/messageformat/testing` holds the shared conformance helpers. | [messageformat](https://github.com/klarlabs-studio/glossa/blob/main/messageformat/js/README.md) |

Every subpath keeps the specifier it had as a separate package, under its prefix
(`@klarlabs-studio/glossa-astro/vue` is `@klarlabs-studio/glossa/astro/vue`).

## One runtime, one set of elements

Subpaths import each other through the package's own name, never by relative path, so a bundler
resolves a single copy of the runtime (and of the custom elements and the Lit context) however
many subpaths an application uses. Importing `@klarlabs-studio/glossa` alone pulls in no Lit, Vue,
React or capture code.

## Dependencies

`lit`, `@lit/context`, `acorn`, `unplugin`, `@jridgewell/trace-mapping`,
`@messageformat/icu-messageformat-1`, `messageformat` and `zod` are dependencies, so one install
pulls them all even for an app that uses only the runtime; bundles contain only what is imported.
`vue`, `react` and `astro` are optional peer dependencies: install the one you use.

## How it is built

The sources live in the nine private workspace packages under `runtimes/js/` and `messageformat/js/`.
`pnpm build` here copies their compiled output to `dist/<subpath>/` and rewrites the references between
them to the specifiers above; `pnpm test` checks the exports map against the sources', the files, the
tree-shaking and every size budget (`.size-limit.mjs`); `pnpm lint` typechecks a consumer of every
subpath. See RFC 0002, Amendment 1.
