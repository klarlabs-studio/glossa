# @klarlabs-studio/glossa

## 0.5.0

New API from the Pet Medical / KraftSport migration wave. Nothing changes for existing code.

- **`@klarlabs-studio/glossa/apierr`** (new subpath, 0.27 kB): `resolveApiError(runtime, body)` turns a Go `apierr` error envelope into localized text, falling back to the server's English `message`; `apiErrorMessage(body)` gives the message key and arguments, `parseApiError(body)` the normalised payload. Replaces the v0.2 SDK's `resolveApiError`; see `elements/MIGRATION.md`. (#78)
- **`runtime.has(id)`**: whether the active release has a message on its fallback chain, without `explain()`. Tells a missing message from one whose text equals its id. (#79)
- **`createRuntime({ parseDefault })`**: opt-in formatting of the inline `default` of `t()`/`parts()` with the call's values, e.g. `parseDefault: parseMF2` from `/messageformat`. Unset, defaults stay literal as before. There is no ICU MF1 parser in the JS packages, so a default is written in MF2 syntax. (#79)
- **`@klarlabs-studio/glossa/astro/testing`** (new subpath): `glossaAstroTesting()`, a Vitest/Vite plugin that serves `virtual:glossa/config` and `virtual:glossa/release` stubs, so code that imports `/astro/client` or `/astro/server` unit-tests with no manual stubbing. (#81)
- **`@klarlabs-studio/glossa/astro/translate`** (new subpath): `t(id, fallback, values?)`, `has`, `currentRuntime` and `provideRuntime`, with no dependency on a virtual module, for shared helpers and libraries. `getRuntime()` from `/astro/client` now works without a `document` and publishes the page runtime for it. (#81)
- **Astro `requireRelease`** option (and `GLOSSA_REQUIRE_RELEASE=1`): fails the build instead of warning when no release could be loaded, e.g. an image built without its delivery key. Off by default. (#82)

Size budgets: the runtime plus resolver chain (`{ createRuntime, resolveLocales, acceptLanguage }`) moves from 6.8 kB to 6.9 kB (6.75 kB to 6.81 kB measured) for `has()` and `parseDefault`; `{ createRuntime }` is 6.69 kB against 6.8 kB, and `/astro/client` 0.60 kB against 0.75 kB.

## 0.4.0

The nine JS packages shipped as one npm package.
