# @felixgeelhaar/glossa-react

## 0.4.0

Initial release under the `@felixgeelhaar/glossa-*` scope (RFC 0002 amendment, 2026-10-04).

New package: Glossa for React 18.3+ and 19 on `@felixgeelhaar/glossa-runtime`. `createGlossa(options)` creates the instance for `<GlossaProvider glossa>`; `useGlossa()` returns `t`, `parts`, `explain`, `setLocales`, `locale`, `dir`, `release` and `availableLocales` and re-renders on every activation; `useMessages<Messages>()` and the `GlossaRegister` augmentation (written by `glossa generate` with `generate.react`) type message IDs and values; `<T id values>{default}</T>` renders a message with safe, attribute-free markup and never renders translation text as HTML. SSR- and hydration-safe through `useSyncExternalStore`: nothing subscribes on the server, and hydration reads a bundled-only server snapshot, so server HTML hydrates without mismatches even when a newer persisted release is already active. 1.2 kB own code, 7.1 kB with the runtime (brotli, React excluded).
