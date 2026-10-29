# @klarlabs-studio/glossa/vue

Glossa for Vue 3.5+, on [`@klarlabs-studio/glossa`](../runtime): a plugin, reactive
composables, `$t` in templates and a `<GlossaText>` component. SSR-safe and
hydration-safe.

```ts
import { createApp } from "vue";
import { createGlossa } from "@klarlabs-studio/glossa/vue";

createApp(App)
  .use(createGlossa({ edge: "https://edge.example.com", deliveryKey: "pk_7Hc2…", locales: "de" }))
  .mount("#app");
```

```vue
<script setup lang="ts">
import { useGlossa } from "@klarlabs-studio/glossa/vue";
const { t, locale, dir, setLocale, availableLocales } = useGlossa();
</script>

<template>
  <main :lang="locale" :dir="dir">
    <h1>{{ t("cart.checkout", {}, { default: "Zur Kasse" }) }}</h1>
    <p>{{ $t("cart.items", { count: 3 }) }}</p>
    <GlossaText id="terms.hint">Lies die AGB.</GlossaText>
    <select :value="locale" @change="setLocale(($event.target as HTMLSelectElement).value)">
      <option v-for="l in availableLocales" :key="l.code" :value="l.code">{{ l.code }}</option>
    </select>
  </main>
</template>
```

## API

**`createGlossa(options) → { runtime, install }`** takes every
[`createRuntime`](../runtime/README.md) option (`edge`, `deliveryKey`,
`environment`, `locales`, `bundled`, `publicKeys`, `storage`, …), or
`runtime` to share an existing runtime between apps (Astro islands). A runtime
the plugin created is disposed when the app unmounts; a given one is left
running. `install` provides the runtime, registers `<GlossaText>` and `$t`.

**`useGlossa()`** (in `setup()`) returns:

| | |
|---|---|
| `t(id, values?, { default? }) → string` | Reactive: re-renders when a release or locale activates. Never throws, never empty. |
| `parts(id, values?, { default? }) → Part[]` | The same as parts, for custom rendering. |
| `explain(id, locales?)` | Why a message renders the way it does (SPEC §6). The in-product editor (M3) builds on it. |
| `setLocale(locales) → Promise` | Switches once the new locale's artifacts are loaded. |
| `locale`, `dir`, `release`, `availableLocales` | Computed refs. |
| `runtime` | The runtime itself. |

Without `app.use(createGlossa(…))` it warns once and renders inline defaults.

**`<GlossaText id values?>`** renders a message without a wrapper element.
The default slot is the inline default, then the message ID. Safe MF2 markup
(`{#b}…{/b}`) becomes elements by the same rules as `@klarlabs-studio/glossa/elements`
(attribute-free inline tags only); translation text is never rendered as HTML.

**Capture mode** (RFC 0004 §3.1). While a capture or editor session has an
`onRender` hook installed on the runtime (see
[`@klarlabs-studio/glossa/capture`](../capture/README.md)), `<GlossaText>` wraps its content
in a `<span style="display: contents">` carrying `data-glossa-id` and
`data-glossa-locale`, and `t()` strings carry the session's invisible markers.
Both go away when the session ends. A normal page view, and server rendering,
never has them.

## Typed messages

`useMessages<M>()` is `useGlossa()` typed by a message map, so IDs and values
are checked at compile time:

```ts
// messages.d.ts — what `glossa generate` will emit (message ID → values)
export interface Messages {
  "cart.checkout": {};
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

To type every `useGlossa()` and `$t` call instead, register the map once:

```ts
declare module "@klarlabs-studio/glossa/vue" {
  interface GlossaRegister {
    messages: Messages;
  }
}
```

Code generation is a later task (`glossa generate`); the intended output is a
`Messages` interface like the one above, plus the registration.

## SSR and hydration

- Importing the package and rendering on the server touch no browser globals.
  On the server nothing subscribes to the runtime, so one runtime can be shared
  across requests without collecting listeners. Create the app per request
  with `createSSRApp` and one plugin per request, with the request's locale
  (`resolveLocales(…, acceptLanguage(req.headers["accept-language"]))`).
- To hydrate without mismatches, the client's first render must see the same
  release and locale as the server: pass the same `bundled` release (from
  `glossa pull --release`, or the release `@klarlabs-studio/glossa/astro` inlines) and the same
  `locales`, and mount right after `createGlossa()`. The runtime renders a
  bundled release synchronously; a newer persisted or network release
  activates afterwards and re-renders.
- Tested with `vue/server-renderer`: a Node-only render, per-request locales,
  and hydration of server HTML with no Vue hydration warnings while a newer
  persisted release is waiting.

## Size

Minified and brotli-compressed (`pnpm size`), Vue excluded:

| Import | Size | Budget |
|---|---|---|
| `@klarlabs-studio/glossa/vue` own code (incl. the shared markup rules) | 1.2 kB | 1.5 kB |
| `@klarlabs-studio/glossa/vue` with `@klarlabs-studio/glossa` | 7.1 kB | 8 kB |
