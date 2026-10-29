# @klarlabs-studio/glossa

Glossa's JavaScript runtime. It implements the
[runtime and delivery contract](../../SPEC.md): it loads a signed release from
memory, persisted storage, the edge or the build's bundle, resolves the locale
and its fallback graph, formats with a **MessageFormat 2 interpreter over
precompiled messages**, and explains every decision. It runs on `Intl.*` and
WebCrypto only and has no dependencies. Framework adapters (Vue, React, web
components) build on it.

```ts
import { createRuntime, resolveLocales, navigatorLanguages } from "@klarlabs-studio/glossa";

const glossa = createRuntime({
  edge: "https://edge.example.com",
  deliveryKey: "pk_7Hc2…", // publishable, read-only
  locales: resolveLocales(user?.locale, org?.locale, navigatorLanguages),
});

glossa.t("cart.checkout", {}, { default: "Zur Kasse" }); // usable immediately, never throws
await glossa.ready; // first load settled (persisted, then network)
glossa.t("cart.items", { count: 3 }); // "3 Artikel"
document.documentElement.dir = glossa.dir;
glossa.subscribe(render); // re-render after a new release or locale activates
```

With nothing but `edge` and `deliveryKey` it persists the last good release in
`localStorage`, refreshes every 5 minutes and when the page becomes visible,
and falls back along the manifest's fallback graph to the source locale, then
to the inline default, then to the message ID. Everything else is optional.

## `createRuntime(options?) → Runtime`

| Option | Default | |
|---|---|---|
| `edge`, `deliveryKey` | none | Edge origin and publishable key. Without them the runtime makes no requests. |
| `environment` | `"production"` | |
| `locales` | `navigator.languages` | Requested locales, most preferred first. Canonicalized (`en_us` → `en-US`, `iw` → `he`). |
| `bundled` | none | `{ manifest, artifacts }` from `glossa pull --release`, artifacts keyed by SHA-256. Renders synchronously at construction and is the last resort offline. |
| `publicKeys` | none | `[{ keyId, key }]`, base64url raw Ed25519. When set, manifests without a valid signature are rejected. |
| `storage` | `webStorage()` in browsers | Where last-good persists. `memoryStorage()`, `indexedDbStorage()` from `@klarlabs-studio/glossa/idb`, your own `{ get, set }`, or `null` for none. |
| `transport` | `fetch` | Any `(url, { headers }) → Promise<{ status, headers.get, text() }>`. |
| `refreshInterval` | `300000` | Background manifest refresh in ms; `0` turns it off. The default timer is `unref`'d, so it never keeps a server process alive. |
| `timer` | `setInterval` | `(tick, ms) → cancel`, for tests or custom scheduling. |
| `bidiIsolation`, `functions` | MF2 defaults | Passed to the interpreter. |
| `parseDefault` | none | Opt-in: parses the inline `default` of `t()`/`parts()` as a message, so a fallback with placeholders is formatted with the call's values: `createRuntime({ parseDefault: parseMF2 })` (`parseMF2` from `@klarlabs-studio/glossa/messageformat`; the runtime itself carries no parser, which keeps it inside its budget). Unset, the default renders literally, as before. A default that fails to parse or format renders literally and is reported on the error channel (`format`). There is no ICU MF1 parser in the JS packages: write the default in MF2 syntax (`Hallo {$name}`). |
| `onError` | none | Error channel listener (more with `runtime.onError`). |
| `errorInterval` | `60000` | An identical error is reported at most once per interval. |
| `rollout` | `true` | Staged rollout support (SPEC §1.4). `false` ignores a manifest's `rollout`: always the stable release, never a candidate fetch, `explain().rollout` is `null`. |
| `installationId` | a random 128-bit id | This installation's cohort key under a staged rollout. By default it is created the first time a manifest with a `rollout` is read and kept in `storage` with the last-good release (in memory without storage). |

The `Runtime`:

| Member | |
|---|---|
| `t(id, values?, { default? }) → string` | Renders `id` along the active chain. |
| `parts(id, values?, { default? }) → Part[]` | The same as parts (text, markup, bidi isolates, fallbacks, values), for adapters and typed accessors. |
| `has(id) → boolean` | Whether the active release has `id` on its fallback chain. Cheap: no formatting, no error report, unlike `explain()`. Tells a missing message from one whose text equals its id (`t()` returns the id for both). `false` until a release is active. |
| `explain(id, locales?) → Explanation` | SPEC §6, without side effects: `{ id, requested, locale, chain, resolvedFrom, release, source, steps, rollout }`. `rollout` is `{ id, percent, cohort, side }` under a staged rollout, else `null`. With `locales`, explains those instead of the active ones and loads nothing. |
| `locale`, `dir`, `release` | The active locale, its direction from the manifest, and `{ id, version }`. |
| `environment` | The active release's environment, from its manifest (covered by its signature when `publicKeys` are set); `undefined` until a release is active. The overlay loader reads it. |
| `availableLocales` | The active release's `locales` (`{ code, direction }[]`, empty until one is active), e.g. for a locale picker. |
| `setLocales(locales) → Promise` | Switches once the new chain's artifacts are loaded. |
| `refresh() → Promise` | Revalidates now. Concurrent calls share one request. |
| `ready` | Settles after the first load. Never rejects. |
| `subscribe(fn)`, `onError(fn)` | Both return an unsubscribe function. Listener exceptions are contained. |
| `onRender(hook) → unsubscribe` | Capture and editor sessions only (RFC 0004 §3.1): `hook({ id, locale, values, output })` sees every `t()` render and may return a string that replaces the output. Adding or removing a hook notifies subscribers, so the page re-renders. See [`@klarlabs-studio/glossa/capture`](../capture/README.md). |
| `hooked` | Whether an `onRender` hook is installed. Components add `data-glossa-id`/`data-glossa-locale` to their host element only then. |
| `override(id, locale, model?) → boolean` | The in-product editor's live preview (RFC 0004 §5.3), never in production: renders `model` (an MF2 data-model message, as the API parsed it) for `id` in `locale` through `t()`, `parts()` and `explain()`, until it's called without `model`. The locale must be on the active fallback chain to show. Notifies subscribers, so the page re-renders. Returns `false` and changes nothing when the runtime's `environment` is `production`. See [`@klarlabs-studio/glossa/overlay`](../overlay/README.md). |
| `environment` | The active manifest's `environment` (`undefined` until a release is active). `glossa capture` refuses a page that reports `production`. |
| `dispose()` | Stops the timer and the visibility listener and drops listeners. |
| `dispose()` | Stops the timer and the visibility listener, drops listeners, and takes the runtime off the page's list (below). |

A runtime created in a browser adds itself to a page-wide list
(`globalThis[Symbol.for("glossa.runtimes")]`), which is how the overlay loader
and `glossa capture` find the page's runtimes whenever they run; `dispose()`
takes it off again. A runtime created without a `document` is never listed (a
server rendering per request keeps nothing). Production runtimes are listed
too — the loader checks every runtime's `environment` before it does anything,
and a capture session has to tell a production page from a page without
Glossa.

## `@klarlabs-studio/glossa/apierr`: API errors

The client side of the Go `apierr` module. A Glossa-aware backend answers a failed request with
`{ "error": { "code", "message", "key", "params", "status" } }`; `key` is a message id and `params`
its arguments.

```ts
import { resolveApiError, apiErrorMessage } from "@klarlabs-studio/glossa/apierr";

const res = await fetch("/api/signup", { method: "POST", body });
if (!res.ok) toast(resolveApiError(glossa, await res.json().catch(() => null)));
```

| Export | |
|---|---|
| `resolveApiError(runtime, body, { unknown? }) → string` | `runtime.t(key, params)` when the release has `key` (`runtime.has`), else the server's English `message`, else `unknown` (default `"Unknown error"`). Takes the envelope, the bare payload, or a pre-apierr `{ "error": "text" }`. Never throws. |
| `apiErrorMessage(body) → { id, args, fallback } or null` | The message key, its arguments and the English fallback, for callers that format themselves (a typed accessor, `<glossa-text>`, a toast component). |
| `parseApiError(body) → ApiErrorPayload or null` | The normalised `{ code, message, key, params?, status }`. |

Any `Runtime` works (`getRuntime()` from `/astro/client`, the Vue and React ones).

## `@klarlabs-studio/glossa/dev`: the overlay loader

The in-product editor's loader ([RFC 0004
§5.1](../../../docs/rfcs/0004-context.md)). **Applications don't import it**:
[`@klarlabs-studio/glossa/unplugin`](../unplugin/README.md) injects it into builds whose
Glossa `environment` isn't `production`, and a production build never
contains it. On the page it does nothing until someone asks for the editor
with `?glossa=edit` in the URL or Alt+Shift+E (which also ends the session).
Then it:

1. waits for the listed runtimes' first load, and **refuses** unless there is
   at least one and every one has an active release whose manifest
   `environment` isn't `production` (a warning on the console says why);
2. adds `<script type="module" src="{studio}/overlay/v1/overlay.js"
   integrity="sha384-…" crossorigin="anonymous">`, the hash pinned at build
   time, so a script that isn't the published one never runs;
3. calls the overlay's `activate` with the runtimes, the page's locale, the
   tenant and project, the API origin (Studio's by default), and a token
   provider backed by Studio's authorization popup.

**Signing in** (`src/grant.ts`, RFC 0004 §5.2). The editor runs on the
product's own page, so it has no Studio session to use. The first API call
opens a popup on Studio — `/in-context/authorize?tenant=…&project=…&origin=…&channel=…`
— and Studio posts a fifteen-minute grant back to that exact origin. The
token lives in a closure: never `localStorage`, never `sessionStorage`, never
a cookie, never the URL. It is renewed through the popup a minute before it
expires, and dropped as soon as the API answers `401`, so the next call asks
again rather than retrying a dead credential.

Both ends check each other. Studio posts to the origin that asked and no
other. This side believes a message only when it came from Studio's origin,
from the popup it opened, and carries the `channel` nonce that request
generated — so a stale answer, a second popup's answer, or any other
`postMessage` from the same origin is ignored.

It adds no inline script, evaluates no strings and creates no frame, so the
preview CSP in
[`@klarlabs-studio/glossa/overlay`](../overlay/README.md#csp-for-preview-deployments) is all
it needs — `frame-src` included: the flow is a popup, never an iframe.

`glossa capture` uses the same page-wide list: it puts the array in place
before the page's scripts run, with a `push` that hooks each new runtime into
its capture session from the first render (RFC 0004 §3.2).

Errors are `{ type, detail, messageId?, locale?, releaseId? }` with `type` one of
`network`, `integrity`, `signature`, `schema`, `format`, `missing-message`.
Nothing is sent anywhere; the application decides what to do with them.

### Locale resolution

`resolveLocales(...resolvers)` runs a resolver chain in order and returns the
canonicalized, de-duplicated requested locales. A resolver is a value or a
function; one that throws is skipped. The contract's default chain is explicit
→ user → organization → request metadata → `Accept-Language` /
`navigator.languages` → source locale:

```ts
resolveLocales(params.lang, user?.locale, org?.locale, acceptLanguage(req.headers["accept-language"]));
```

The source locale is always the implicit last step. `lookupLocale`
(RFC 4647 Lookup), `fallbackChain` (SPEC §4.2), `canonicalLocales` and
`acceptLanguage` are exported for servers and tools.

### How loading behaves

- **Order** (SPEC §3): the release in memory, then persisted last-good, then the
  edge, then the bundle, then the inline default or message ID. Artifacts are
  content-addressed, so each one is looked up by its hash in memory, the
  persisted release, the bundle and only then the edge: a partly cached or
  bundled release only fetches what it has nowhere else, and only for the
  active fallback chain.
- **Atomic activation.** A manifest is checked (schema major version and
  environment first, then the signature), every artifact of the chain is verified against its
  SHA-256, and only then does the release switch, in one step. Until then the
  previous release keeps serving. Activations are serialized, so a locale
  switch during a release update can't resurrect the old release.
- **`explain().source`** is where the rendered text came from: the source a
  release was activated from, or `memory` once a later refresh brings nothing
  new (a `304`, or a failure). It is `inline` whenever the inline default or
  the message ID renders.
- **Bundled vs. persisted.** Persisted last-good wins, unless the bundle is a
  newer release (higher `release.version`), which happens after an app update
  while the edge is unreachable. Bundled artifacts are part of the app build
  and are trusted like its code: they aren't re-hashed or re-verified.
- **Persistence** stores the manifest, its ETag and the verified bytes of every
  artifact of that release the runtime has loaded, across restarts. Storage
  failures only cost persistence.
- **Staged rollout** (SPEC §1.4). When the manifest carries a valid
  `rollout`, the installation's cohort — the first four bytes of
  SHA-256(salt ‖ installation id), big-endian, mod 10000 — decides its side:
  below `percent × 100` it activates the candidate's release, locales,
  fallback and artifacts, atomically as above; otherwise the stable release,
  without fetching any candidate artifact. A candidate that can't be
  activated falls back to the stable view of the same manifest, never to the
  previous release. The manifest is persisted as served, so a restart decides
  the same side, and an aborted rollout (no `rollout`, or another `id`)
  returns the installation to stable on the next manifest. An invalid
  `rollout` is ignored with a `schema` error.
- **Bad messages.** A message in an artifact that isn't an MF2 data-model
  message is dropped with a `schema` error (with its `messageId`) and resolves
  as missing, so the fallback chain covers it; it never blocks the release.
- **Never blank.** A message that formats to the empty string renders the
  inline default, or the message ID.

## `format` and `formatToParts`

The interpreter is usable on its own:

```ts
import { format, formatToParts } from "@klarlabs-studio/glossa";

format(message, "de", { count: 3 }); // "3 neue Nachrichten"
formatToParts(message, "de", { count: 3 }); // text, number, markup, bidiIsolation, fallback parts
format(message, "de", {}, { onError: (e) => log(e.type, e.source) }); // "Hallo {$name}!"
```

`format(message, locale, values?, opts?) → string` and
`formatToParts(…) → Part[]`. `locale` is a BCP 47 tag or a list of them.
Options:

| Option | Default | |
|---|---|---|
| `onError(error)` | none | Called with `{ type, source }` per error (`unresolved-variable`, `bad-operand`, `bad-option`, `bad-selector`, `unknown-function`, `not-formattable`, `unsupported-operation`, `bad-message`). |
| `bidiIsolation` | `"default"` | Wrap placeholders in Unicode isolates (U+2066–2069) as the spec requires; `"none"` turns that off. |
| `dir` | from the locale | The message's base direction. |
| `functions` | none | Custom functions (`MessageFunction`), merged over the built-ins. |

What it implements:

- Patterns, `.input` and `.local` declarations (resolved lazily, once),
  `.match` with the spec's pattern-selection algorithm (exact keys before
  plural categories, `*` fallback, any number of selectors).
- Functions `:string :number :integer :percent :currency :offset :date :time
  :datetime :unit` with the spec's (LDML 48) options, on `Intl.NumberFormat`,
  `Intl.PluralRules` and `Intl.DateTimeFormat`. Intl formatters are cached per
  locale and options.
- Markup as structured parts (`{ type: "markup", kind, name, options?, id? }`);
  it formats to nothing in `format()`. Attributes are ignored, as the spec says.
- `u:dir` and `u:id` on expressions; bidi isolation per the spec, with each
  value's direction taken from CLDR (`Intl.Locale` text info).
- **It never throws.** A failing placeholder renders as its fallback
  (`{$name}`, `{|literal|}`, `{:fn}`) and reports through `onError`. A message
  it can't interpret at all (malformed data) renders as `{�}` and reports
  `bad-message`.

## Conformance

`pnpm test` runs:

- the **runtime contract fixtures** (`runtimes/testdata`, SPEC §7): every
  resolution scenario (negotiation, fallback graph and cycles, formatting with
  the locale a message was found in, missing messages) and every loading
  sequence (persisted last-good, atomic activation, signatures, schema version,
  cold offline start), through a fake edge and in-memory storage. A restart is
  a new runtime sharing the storage. No skips.
- the **vendored Unicode MessageFormat suite** (`messageformat/testdata/unicode`):
  each `src` is parsed with the reference parser *in the test only*, then
  interpreted here and checked against `exp`, `expParts` and `expErrors`.
  Every runtime case passes; the skip list in `src/conformance.test.ts` is
  empty. Syntax and data model errors are compile-time, so for those the test
  only checks that the reference parser rejects them.
- Glossa's **`messageformat/testdata/glossa/runtime-format.json`**: German and
  English UI strings (plurals with exact keys, ordinals, EUR, dates, nested
  selectors, MF1 offsets, markup), Spanish, French and Japanese ones (`many`
  and `other`-only plurals, EUR and JPY, dates in a named time zone, percent,
  units, grouping, negative numbers) plus Arabic and Hebrew bidi cases, with
  expected output from the reference formatter. No skips, on the CLDR of every
  supported Node version.

The tests need `@klarlabs-studio/glossa/messageformat` built first
(`pnpm -r --filter "./messageformat/js" --filter "./runtimes/js/*" build`).

## Size

Minified and brotli-compressed, measured by `pnpm size` per import, so each
line is what an app that imports only that pays:

| Import | Size | Budget |
|---|---|---|
| `{ format, formatToParts }` (interpreter only) | 3.13 kB | 4 kB |
| `{ createRuntime }` (interpreter, loader, verification, resolver, `explain`, staged rollout) | 6.69 kB | 6.8 kB |
| `{ createRuntime, resolveLocales, acceptLanguage }` | 6.81 kB | 6.9 kB (was 6.8: `has()` and `parseDefault` cost ~110 B) |
| `@klarlabs-studio/glossa/idb` | 0.26 kB | 0.5 kB |
| `@klarlabs-studio/glossa/dev` (the overlay loader, never in production builds) | 0.98 kB | 1.25 kB |

RFC 0002 §8 set 4 kB for the whole JS core. The interpreter alone fits it; the
contract's loader, SHA-256 and Ed25519 verification, JCS, the fallback graph,
persistence, background refresh and `explain` add about 2.9 kB. The budget
was 6.5 kB, keeping ~0.5 kB for namespace-level lazy loading (bundle
splitting); staged rollout (SPEC §1.4) costs ~340 B that no trimming of
its own code recovers, and RFC 0006 §5.2 raised the budget to 6.8 kB for
it rather than make rollout opt-in, which would leave apps that don't opt
in outside every rollout.
Framework adapters are separate packages with their own budgets. If the budget
gets tight, trim here before dropping contract behaviour: the `Intl.Locale`
script fallback in `dirOf` (only needed where `textInfo` is missing) and the
date/time option validation are the candidates.

## Platform requirements

WebCrypto `SHA-256` everywhere, and `Ed25519` only when `publicKeys` are set
(Chrome 137+, Firefox 129+, Safari 17+, Node 22+). Where Ed25519 is missing,
signature checks fail closed: the manifest is rejected and the last good release
keeps serving.

## Deliberate differences from the reference implementation

- Dotted variable names aren't looked up as paths into nested values
  (`{$user.name}` needs a `user.name` key). The reference does this as an
  extension; the spec doesn't.
- `timeZone=input` keeps the operand's zone, and converting between zones
  isn't reported as an error.
- `:number select=plural` means cardinal plural rules (the reference passes
  `type: "plural"` through to `Intl.PluralRules`, which rejects it).
- Custom functions return `toParts()` only; `format()` joins the parts.
