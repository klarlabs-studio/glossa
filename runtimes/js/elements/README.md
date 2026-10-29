# @klarlabs-studio/glossa/elements

Glossa's web components, built on [`@klarlabs-studio/glossa`](../runtime) and
[Lit](https://lit.dev). They keep the v0.3 element API (`<glossa-text key>`
with its slot as the inline default, `<glossa-rich>`, `<glossa-plural>`,
`<glossa-select>`, `<glossa-selector>`), so moving from v0.3 changes the
provider's attributes and nothing else. See [MIGRATION.md](./MIGRATION.md).

```html
<script type="module">
  import "@klarlabs-studio/glossa/elements";
</script>

<glossa-provider edge="https://edge.example.com" delivery-key="pk_7Hc2…" locale="de">
  <glossa-text key="cart.checkout">Zur Kasse</glossa-text>
  <glossa-rich key="athlete.greeting" vars='{"name":"Sophia"}'>Hallo!</glossa-rich>
  <glossa-plural key="athlete.session_count" count="3">Einheiten</glossa-plural>
  <glossa-select key="user.gender" value="female">Sie</glossa-select>
  <glossa-selector></glossa-selector>
</glossa-provider>
```

## Elements

| Element | |
|---|---|
| `<glossa-provider>` | Owns one runtime and shares it with its subtree through a Lit context. Sets `lang` and `dir` on itself after every activation. |
| `<glossa-text key>` | Renders message `key`. The slot content is the inline default: it shows while the first load is pending and whenever no locale of the active fallback chain has the message. `message="…"` is an alias for templates that reserve `key` (Vue, see below). |
| `<glossa-rich key vars>` | With values: `vars` is a JSON object in markup, or any object through the `.vars` property. |
| `<glossa-plural key count>` | Passes `count` (and `vars`) for messages that select on `$count`. |
| `<glossa-select key value name?>` | Passes `value` as `$value`, or under the variable `name` names. |
| `<glossa-selector>` | Language picker over the release's locales, labelled in their own language (`Intl.DisplayNames`). |

### `<glossa-provider>`

| Attribute | |
|---|---|
| `edge`, `delivery-key` | Edge origin and the project's publishable key. Without them nothing is fetched and the elements render their inline defaults (or a `bundled` release). |
| `environment` | Default `production`. |
| `locale` | Requested locale, or a comma-separated preference list (`de-AT,de`). Default: the browser's languages. Changing it switches every element once the new locale's artifacts are loaded. |
| `public-keys` | Trusted signing keys: `keyId:base64url` pairs, comma-separated, or a JSON array of `{ keyId, key }`. When set, unsigned manifests are rejected. |
| `strict` | `console.warn` for missing messages, load and format errors, and a provider still configured with v0.3 attributes. |
| `inspect` | Alt+click on a string dispatches `glossa-inspect` (see below). |

| Property | |
|---|---|
| `runtime` | Read: the runtime in use. Write: use this runtime instead of creating one, e.g. to share one runtime between elements and Vue islands. A runtime you pass in is never disposed by the provider. |
| `bundled` | A release shipped with the build (`{ manifest, artifacts }`), rendered synchronously on first paint. |
| `options` | Any other [`createRuntime`](../runtime/README.md) option (`storage`, `refreshInterval`, `transport`, `onError`, …). |
| `GlossaProvider.defaultRuntime` (static) | `() => Runtime`: used by providers with neither `edge`, `bundled` nor `runtime`. Integrations set it to share one page-wide runtime (`@klarlabs-studio/glossa/astro/elements` does). |

Events, all bubbling and composed:

- `glossa-change`: `{ locale, dir, release }` after every activation (a new release or locale).
- `glossa-error`: each entry of the runtime's error channel (`{ type, detail, messageId?, locale?, releaseId? }`). Nothing is sent anywhere.
- `glossa-inspect`: `{ id, element, explanation }` on Alt+click with `inspect` on. This is the hook the in-product editor (M3) attaches to; `explanation` is `runtime.explain(id)`.

### Rendering

- A translation renders into the element's shadow root, which inherits the
  surrounding text styles (the host is `display: contents`). The authored
  default stays in the light DOM.
- MF2 markup becomes an element only when it's a safe inline tag (`b strong
  i em u s small mark sub sup code kbd samp var abbr cite dfn q del ins bdi
  span br wbr`), and never with attributes. Other markup keeps its content.
  Translation text is never parsed as HTML.
- State attributes for styling: `data-glossa-pending` and `aria-busy` until
  the first load settles, then `data-glossa-missing` when the inline default
  renders.
- Capture mode (RFC 0004 §3.1): while a capture or editor session has an
  `onRender` hook installed on the runtime (see
  [`@klarlabs-studio/glossa/capture`](../capture/README.md)), the host also carries
  `data-glossa-id` and `data-glossa-locale` (the locale the message resolved
  from; absent while the inline default shows). Never in a normal page view,
  and `@klarlabs-studio/glossa/elements/ssr` never adds them.
- Formatting never throws: a missing value renders as its MF2 fallback
  (`{$name}`) and is reported as a `format` error.

### Inside Vue templates

Vue reserves `key` for its own diffing and never renders it as an attribute,
so `<glossa-text key="…">` inside a `.vue` template reaches the DOM without
its message ID and always shows the inline default (this was already true
with v0.3). Write `message="…"` there, or use `<GlossaText id>` from
`@klarlabs-studio/glossa/vue`, and tell Vue the tags are custom elements
(`compilerOptions.isCustomElement: (tag) => tag.startsWith("glossa-")`).
`key` keeps working in `.astro` files and plain HTML.

### `<glossa-selector>`

Offers the active release's locales (`runtime.availableLocales`), each named in
its own language. `locales="en,de"` and `labels="English,Deutsch"` restrict and
rename them as in v0.3; `label` is the accessible name (default "Language").
A pick dispatches a cancelable `glossa-locale-change`
(`{ locale, source: "manual" }`) and then switches the provider unless a
listener called `preventDefault()`. Persisting the choice stays with the app.
`auto-detect` suggests the browser language once (`source: "auto"`) without
switching.

## Server and build-time rendering

`@klarlabs-studio/glossa/elements/ssr` is pure string processing (no DOM, no Lit):

```ts
import { prerender } from "@klarlabs-studio/glossa/elements/ssr";

const html = prerender(pageHtml, runtime); // runtime: a @klarlabs-studio/glossa for the page's locale
```

Every `<glossa-text|rich|plural|select>` whose message resolves gets the
translation (escaped text and safe elements) in place of its inline default,
and `<glossa-provider>` tags get `lang`/`dir`. Static pages then ship
translated HTML that reads correctly without JavaScript, and when the elements
load they render the same text, so nothing flickers. `@klarlabs-studio/glossa/astro` does this
for every page at build time.

`@klarlabs-studio/glossa/elements/parts` holds the rendering rules every adapter shares
(`resolveParts`, `partsToTree`, `treeToHtml`, `SAFE_TAGS`), so `@klarlabs-studio/glossa/vue`
and the elements render markup the same way. It has no DOM or Lit dependency.

## Size

Minified and brotli-compressed (`pnpm size`):

| Import | Size | Budget |
|---|---|---|
| `@klarlabs-studio/glossa/elements`, everything included (Lit, `@lit/context`, `@klarlabs-studio/glossa`) | 15.21 kB | 15.3 kB (raised from 15 kB for staged rollout, RFC 0006 §5.2) |
| `@klarlabs-studio/glossa/elements` own code | 3.2 kB | 3.5 kB |
| `@klarlabs-studio/glossa/elements/ssr` (without the runtime) | 1.5 kB | 1.75 kB |

Lit and `@lit/context` are about 5.5 kB of the total and are shared with any
other Lit components on the page; the runtime is 6 kB. The own-code budget
leaves ~0.3 kB for the in-product editor's hook to grow. v0.3 was 10 kB
without Lit, because it bundled its own ICU formatter and API client.
