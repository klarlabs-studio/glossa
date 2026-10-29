# @felixgeelhaar/glossa-astro

## 0.4.0

Initial release under the `@felixgeelhaar/glossa-*` scope (RFC 0002 amendment, 2026-10-04).

`astro build` now records where messages are used: the integration adds `@felixgeelhaar/glossa-unplugin` to Vite, which writes `.glossa/usages.json` beside `outDir` for `glossa context push`, with `.astro`/`.vue` file names as components, `src/pages/**` routes, and the release's message keys for typed accessors. The new `usages` option passes plugin options (`application`, `commit`, `branch`, `routes`, `keys`); `usages: false` turns it off.
