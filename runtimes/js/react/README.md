# @klarlabs-studio/glossa/react

Glossa for React 18.3+ and 19, on [`@klarlabs-studio/glossa`](../runtime): a
provider, hooks and a `<T>` component. SSR-safe and hydration-safe through
`useSyncExternalStore`.

```tsx
import { createRoot } from "react-dom/client";
import { GlossaProvider, createGlossa } from "@klarlabs-studio/glossa/react";

const glossa = createGlossa({
  edge: "https://edge.example.com",
  deliveryKey: "pk_7Hc2…",
  locales: navigator.languages,
});

createRoot(document.getElementById("app")!).render(
  <GlossaProvider glossa={glossa}>
    <App />
  </GlossaProvider>,
);
```

```tsx
import { T, useGlossa } from "@klarlabs-studio/glossa/react";

export function App() {
  const { t, locale, dir, setLocales, availableLocales } = useGlossa();
  return (
    <main lang={locale} dir={dir}>
      <h1>{t("cart.checkout", {}, { default: "Zur Kasse" })}</h1>
      <p>{t("cart.items", { count: 3 })}</p>
      <T id="terms.hint">Lies die AGB.</T>
      <select value={locale} onChange={(e) => setLocales(e.target.value)}>
        {availableLocales.map((l) => (
          <option key={l.code} value={l.code}>{l.code}</option>
        ))}
      </select>
    </main>
  );
}
```

## API

**`createGlossa(options) → { runtime }`** takes every
[`createRuntime`](../runtime/README.md) option (`edge`, `deliveryKey`,
`environment`, `locales`, `bundled`, `publicKeys`, `storage`, …), or
`runtime` to share an existing runtime between React roots. Create it once,
outside components. The runtime lives as long as the instance; call
`glossa.runtime.dispose()` when you tear an app down for good (tests,
micro-frontends). Unmounting only unsubscribes.

**`<GlossaProvider glossa>`** makes the instance available below it.

**`useGlossa()`** returns:

| | |
|---|---|
| `t(id, values?, { default? }) → string` | Never throws, never empty. |
| `parts(id, values?, { default? }) → Part[]` | The same as parts, for custom rendering. |
| `explain(id, locales?)` | Why a message renders the way it does (SPEC §6). The in-product editor (M3) builds on it. |
| `setLocales(locales) → Promise` | Switches once the new locale's artifacts are loaded. |
| `locale`, `dir`, `release`, `availableLocales` | The active state. |
| `runtime` | The runtime itself. |

The component re-renders whenever a new release or locale activates. The
object (and `t`) is a new one after each activation, so `useMemo(…, [t])`
recomputes when the text can have changed. Outside a provider it warns once
and renders inline defaults.

**`<T id values?>{default}</T>`** renders a message without a wrapper
element. The children are the inline default, then the message ID. Safe MF2
markup (`{#b}…{/b}`) becomes elements by the same rules as `@klarlabs-studio/glossa/elements`
(attribute-free inline tags only); translation text is never rendered as HTML.

**Capture mode** (RFC 0004 §3.1). While a capture or editor session has an
`onRender` hook installed on the runtime (see
[`@klarlabs-studio/glossa/capture`](../capture/README.md)), `<T>` wraps its content in a
`<span style="display: contents">` carrying `data-glossa-id` and
`data-glossa-locale`, and `t()` strings carry the session's invisible markers.
Both go away when the session ends. A normal page view, and server rendering,
never has them; a session started before hydration still hydrates the server
HTML without mismatches, then marks.

## Typed messages

`useMessages<M>()` is `useGlossa()` typed by a message map, so IDs and values
are checked at compile time:

```ts
// messages.ts, written by `glossa generate` (message ID → values)
export interface Messages {
  "cart.checkout": Record<string, never>;
  "cart.items": { count: number };
  "athlete.greeting": { name: string };
}
```

```ts
const m = useMessages<Messages>();
m.t("cart.items", { count: 3 }); // ok
m.t("cart.items"); // error: values required
m.t("cart.itmes", { count: 3 }); // error: unknown ID
```

To type every `useGlossa()` call and `<T id>` instead, register the map once.
`glossa generate` writes this file when `generate.react` is set in
`glossa.yaml`, together with a `useTypedMessages()` hook for the generated
accessors (`m.cart.items({ count })`):

```ts
declare module "@klarlabs-studio/glossa/react" {
  interface GlossaRegister {
    messages: Messages;
  }
}
```

## SSR and hydration

- Importing the package and rendering on the server touch no browser globals.
  React never subscribes on the server, so one runtime can be shared across
  requests without collecting listeners. Create one instance per request with
  the request's locale
  (`resolveLocales(…, acceptLanguage(req.headers["accept-language"]))`), or
  one per locale around a shared runtime.
- To hydrate without mismatches, the client must be able to render what the
  server rendered: pass the same `bundled` release (from
  `glossa pull --release`) and the same `locales`. While hydrating, the hooks
  read a server snapshot: a second, bundled-only runtime, so the first client
  render matches the server HTML even when a newer persisted or network
  release activated before React got to hydrate. React then re-renders with
  the live runtime. (With `runtime` given, the snapshot is that runtime as it
  is; hydrate before it activates anything newer.)
- Tested with `react-dom/server` in plain Node (per-request locales, no
  listeners on a shared runtime), and by hydrating server HTML in jsdom, in
  `StrictMode`, with a newer persisted release already active: no hydration
  errors or warnings, and the server's DOM nodes are kept.
- The SPEC scenario and loading fixtures run through React too: every case
  is read from the DOM of a mounted component, as `t()` and as `<T>`.
- The whole suite runs on React 19 and again on React 18.3
  ([`react-18/`](./react-18), a test-only workspace package, since pnpm would
  otherwise link an aliased `react-dom@18` to React 19).

## Atlassian Forge and Tauri

- **Forge** (Custom UI): talking to the edge needs a declared egress
  (`permissions.external.fetch.client`). Ship a bundled release instead, so
  the app renders without egress. Over-the-air updates are optional: declare
  the edge's origin and pass `edge` and `deliveryKey` as well, and a newer
  release activates once it's published.

  ```ts
  // glossa pull --release latest --environment production --out src/glossa/release
  import { createGlossa, type GlossaOptions } from "@klarlabs-studio/glossa/react";
  import manifest from "./glossa/release/manifest.json";

  // Vite: every artifact of the bundle, keyed by its SHA-256 (the file name).
  const files = import.meta.glob("./glossa/release/a/*.json", { eager: true, import: "default" });
  const artifacts = Object.fromEntries(
    Object.entries(files).map(([path, artifact]) => [path.slice(-69, -5), artifact]),
  );
  const bundled = { manifest, artifacts } as GlossaOptions["bundled"];

  export const glossa = createGlossa({ bundled, locales: navigator.languages });
  ```
- **Tauri**: the webview has `localStorage`, which the runtime uses by default
  to persist the last good release, so the app starts with the last release
  it saw, then refreshes. Nothing to configure; pass `bundled` too for a first
  start offline. Catalogs too big for `localStorage` can persist in IndexedDB
  instead (`storage: indexedDbStorage()` from `@klarlabs-studio/glossa/idb`).

## Size

Minified and brotli-compressed (`pnpm size`), React excluded:

| Import | Size | Budget |
|---|---|---|
| `@klarlabs-studio/glossa/react` own code (incl. the shared markup rules) | 1.2 kB | 1.5 kB |
| `@klarlabs-studio/glossa/react` with `@klarlabs-studio/glossa` | 7.1 kB | 8 kB |
