# @klarlabs-studio/glossa/capture

Capture mode ([RFC 0004 §3.1](../../../docs/rfcs/0004-context.md)): finding
which pixels of a page belong to which message, for screenshots in Studio's
"Where it appears" pane and for the in-product editor.

This package is **loaded only in a capture or editor session**. `glossa
capture` injects it into the page it screenshots, and the overlay loader loads
it in a preview session. Applications never import it, and nothing in
`@klarlabs-studio/glossa`, `@klarlabs-studio/glossa/elements`, `@klarlabs-studio/glossa/vue` or `@klarlabs-studio/glossa/react`
imports it. That's why it is a package of its own rather than a
`@klarlabs-studio/glossa/capture` entry: a production bundle can't reach it through
any import an application makes, its test tooling (Playwright, ajv, esbuild)
stays out of the runtime, and a test (`src/bundle.test.ts`) bundles an app on
the runtime and every component package and checks that no capture code is in
it. The runtime only has the extension point, `onRender`, which costs under
100 bytes.

```ts
import { startCapture } from "@klarlabs-studio/glossa/capture";
import { probe } from "@klarlabs-studio/glossa/capture/probes"; // optional: the visual probe pass

const session = startCapture(runtime, { probe }); // or [runtimeA, runtimeB] for islands
// …the page re-renders with markers and host attributes…
const { renders, regions, probes } = session.collect(); // captures.v1 fields + findings
session.stop(); // remove the hook and every marker
```

## How messages are found

- **Components** (`<glossa-text>`, Vue `<GlossaText>`, React `<T>`) put
  `data-glossa-id` and `data-glossa-locale` on their host element while a
  session's hook is installed. The Vue and React components have no element
  of their own, so in a session they wrap their content in a
  `<span style="display: contents">`. The region (kind `element`) is the box
  of what the host renders, through `display: contents`, shadow roots and slots.
- **`t()` strings** are wrapped in invisible markers: a start mark, the
  render's index in the session's render log in binary, the text, an end mark.
  The capture script walks text nodes (open shadow roots included), turns
  every marked range into boxes with `Range.getClientRects()`, one region of
  kind `text` per line, and looks the index up in the log. Markers nest, so a
  marked value inside another message yields both regions.
- **Attributes**: a marked `placeholder`, `title`, `aria-label`, `alt` (any
  attribute, in fact) or the `value` of a button or text input is a region of
  kind `attribute`: the element's box.
- A region that renders zero-size, is clipped away by an `overflow`
  container, lies outside the page, or is `visibility: hidden`, `display:
  none` or transparent has `visible: false`, so the gap shows instead of
  being hidden.

Boxes are CSS pixels from the top-left of the full page, whatever the scroll
position. `collect()` returns the `renders` the regions refer to and the
`regions`, exactly as a `glossa.captures/v1` capture holds them
([schema](../../testdata/schemas/captures.v1.schema.json)); `glossa capture`
adds the route, viewport, locale and image. Message IDs that aren't catalog
keys can't be named in that format and are left out; an inline default's
locale is `und`.

### The markers

The characters are the Unicode invisible operators U+2061–U+2064 (start
U+2063, end U+2064, binary digits U+2061/U+2062). They are default-ignorable,
so browsers draw nothing and advance nothing for them; bidi class BN, so the
bidi algorithm ignores them; joining type transparent, so Arabic letters next
to them keep their shapes; and line-break class AL, so they add no break
opportunity inside a word. Zero-width joiners (ZWJ/ZWNJ) would change shaping
and ZWSP would add break opportunities, so they aren't used. The browser tests
check that no box on the fixture page moves by more than 0.01 px when the
markers are added.

Equal renders (same ID, locale, values and output) share one log entry. The
log keeps a digest of the values (FNV-1a of their JSON), never the values.

## The visual probe pass

A session started with `{ probe }` also runs the visual probes of
[RFC 0005 §5](../../../docs/rfcs/0005-quality.md) in `collect()`,
after the regions and before the screenshot, while the page still has layout:
`scrollWidth`, `getComputedStyle`, `document.fonts.check()` and the runtime's
`explain()` exist only while the page is open. They are **semantic assertions
about known regions**, never pixel diffing, OCR, contrast or general
accessibility (§5.3).

| Code | How it decides |
|---|---|
| `text-clipped` | the nearest container above the region with `overflow: hidden\|clip` or an ellipsizing `text-overflow` has `scrollWidth`/`scrollHeight` more than a pixel over its client size |
| `region-overlap` | two regions of different messages intersect by more than 25 % of the smaller, and neither element contains the other |
| `line-growth` | the message covers more line boxes than it did in the `baseline` — the source locale's capture of the same route and viewport. Without a baseline nothing is decided |
| `rtl-not-mirrored` | the runtime that rendered the region reports `dir === "rtl"` and the region's computed `direction` is not `rtl` |
| `missing-glyph` | `document.fonts.check()` refuses the region's computed font for its text |
| `untranslated-on-screen` | `explain()` says the region resolved from a fallback locale, or from the inline default, in a locale the manifest lists — never a heuristic on the text |
| `mixed-locale` | regions resolved from two locales that share no fallback chain; the locale most regions came from is the screen's |
| `runtime-*` | drained from the runtime error channel (SPEC §6) the session listened on: `runtime-format`, `runtime-missing-message` and the four load errors |

Every finding is the one shape of RFC 0005 §2.1
([schema](../../testdata/schemas/finding.v1.schema.json)), at layer `visual`
and **always at severity `warning`** — promotion to `error` needs the same
fingerprint in two consecutive captures, which only the server can see. Two
fields of that shape are left for the ingest to fill, because the page cannot
know them:

- **`fingerprint`** hashes the catalog *message ID* where the caller has one,
  and a browser only ever has the key; computing one here would not match the
  one the server computes, so waivers would stop matching.
- **`locus.capture`** is minted on ingest. The probe names the region within
  this capture as `r_<index into regions>`, and the ingest pairs it with the
  capture — the same way Context fills a locus at report time.

`collect()` also returns `metrics`: the line boxes each message covered, which
is what the next locale's `collect(root, { baseline })` compares against.
`tolerance` is the line boxes a translation may gain before `line-growth` says
so. A probe that throws costs its own finding and nothing else: it can never
break a capture.

**The thresholds are the check policy's, not this package's.** RFC 0005 §5.2
asks for every one of them to be policy-visible, so they live in one place —
`checkpolicy.VisualThresholds` in the platform — and `glossa capture` hands
them to `collect()` with the capture's options: `slack` (CSS pixels of content
over box before `text-clipped`), `overlap` (per cent of the smaller region
before `region-overlap`), `tolerance` and `max` (the findings one capture may
report, RFC 0005 §10). The constants in `src/probes.ts` are what a probe called
without a driver falls back to, and nothing else; a project that changes a
threshold changes it there and both the browser and the server follow. Every
length is in **CSS pixels, never device pixels**, because headless Chrome's
text metrics move with the fonts a runner happens to have.

That is also why a finding this pass reports is never an error on its own. The
platform promotes one only when the same fingerprint comes back in the next
capture of the same (route, viewport, locale) — which is why every probe
finding here is written at `severity: "warning"`.

**The pass is given to a session, never imported by it.** `collect()` is always
reachable on the session object, so a static `import` of `./probes.js` in
`session.ts` would put the probes in every bundle that starts a session —
including `@klarlabs-studio/glossa/overlay`, the in-product editor served to end users, which
measures nothing. So `probe` lives behind its own entry point,
`@klarlabs-studio/glossa/capture/probes`, and is handed to `startCapture(runtimes, { probe })`.
`glossa capture`'s agent passes it and pays the ~1.2 kB; a session without it
collects regions and reports `probes: []`. `src/bundle.test.ts` asserts both
directions, and `pnpm size` budgets a session with the pass (4 kB, RFC 0005
§5.1) and without it (3 kB, unchanged) separately.

## The trade-off

**Markers change string lengths.** During a session, a `t()` string is a few
characters longer than what the page shows: `maxlength` inputs, code that
compares or slices translated strings, `document.title` and strings copied
into non-DOM places (clipboard, analytics, `localStorage`) see the markers.
That's why markers exist only while a session's hook is installed, never in a
normal page view, and why `stop()` removes the hook (frameworks re-render
without markers) and then strips whatever markers are still in the document:
text, attribute values, input values and open shadow roots
(`stripMarkers(root)` does just that part).

The alternatives were worse: matching rendered text back to messages fails on
duplicates ("Save" appears ten times, from different messages) and on
formatted values; marking only elements misses every `t()` call, which is most
Vue and Go-template usage.

## API

| | |
|---|---|
| `startCapture(runtimes, { probe? }) → CaptureSession` | Installs the hook on one runtime or several (they share one log). `probe` comes from `@klarlabs-studio/glossa/capture/probes`; without it a session reports no findings. |
| `session.renders` | The render log: `{ id, locale, digest }`, a marker's index is a position in it. |
| `session.errors` | What the session's runtimes put on their error channels, in order. |
| `session.collect(root?, { baseline?, tolerance?, slack?, overlap?, max? }) → { renders, regions, probes, metrics }` | The capture script and the probe pass, over the document or a subtree. The thresholds come from the check policy; each defaults to RFC 0005 §5.2's number. |
| `session.stop()` | Removes the hooks and strips the markers left in the document. Idempotent. |
| `collectRegions(log, root?, onHost?)` | The capture script on its own, for a log kept elsewhere. |
| `probe(capture, hosts, ctx, options?)` (`@klarlabs-studio/glossa/capture/probes`) | The probe pass on its own, over regions already collected. |
| `stripMarkers(root?)`, `strip(s)` | Remove markers from a DOM tree or a string. |
| `mark(index, text)`, `ranges(s)`, `digest(values)` | The marker format and the values digest. |

## In `glossa capture`

`glossa capture` (RFC 0004 §3.2) can't import this package into the pages it
screenshots, so it injects a bundle of `src/agent.ts` before the page's own
scripts run:

- `install()` puts the page's runtime registry
  (`globalThis[Symbol.for("glossa.runtimes")]`) in place, with a `push` that
  hooks each runtime into the agent's session from its first render, whatever
  framework renders the page. Runtimes created before the call are hooked too.
- The CLI then calls `__glossaCapture.settle()` (fonts loaded, no DOM mutation
  for 300 ms), `status()` (every runtime's active manifest environment and
  locale: the CLI refuses `production` and a page without an active release)
  and `collect()`: the regions, the probes, the document's size, and the boxes
  of the `data-glossa-redact` elements it blacked out. Their content is painted
  black and covered, and regions under them are `visible: false`. The probes
  run before the redaction overlays go in, so they measure the page as it laid
  out rather than as it is blacked out.

`pnpm build:cli` (after `pnpm -r build`) writes the bundle to
`platform/internal/cli/capture/agent.js`, which the Go binary embeds, and the
CLI integration test's fixture app (`src/testing/cli-fixture.ts`) to
`platform/internal/cli/capture/testdata/app/app.js`. Both are checked in;
`src/cli-bundles.test.ts` fails when either differs from a fresh build.

## Tests

- `pnpm test`: markers, the session and the capture script's structure in
  jsdom, with Vue and React apps, validated against captures.v1 with ajv; one
  fixture per probe, with the layout stated (`src/testing/layout.ts`) because
  jsdom has none, validated against finding.v1; the tree-shaking checks,
  including that a session without the probe pass leaves `./probes` out of the
  bundle; and the size budgets, which run `size-limit` over `dist/` and fail
  over 4 kB brotli with the pass or 3 kB without it, so `pnpm -r test` enforces
  what `pnpm size` reports.
- `pnpm test:browser`: geometry in Chromium with Playwright on a fixture page:
  "Speichern" rendered by three different messages, formatted values,
  attributes, hidden and off-screen text, RTL text, wrapped lines, a
  `<glossa-text>` host, scroll independence, and that markers move nothing.
  Needs the workspace built and `pnpm exec playwright install chromium`.
