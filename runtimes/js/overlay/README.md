# @klarlabs-studio/glossa/overlay

The in-product editor ([RFC 0004 §5](../../../docs/rfcs/0004-context.md)): a
translator Alt+clicks a message in a running preview of the product, and a
panel opens on it with the source, the translation in the page's locale, its
history, terminology findings and AI suggestions. An edit is validated by the
API, saved as an **ordinary translation revision** and shown in the page at
once.

**The overlay is never in production.** It is not a package applications
import. Studio serves it at `/overlay/v1/overlay.js`, and a loader that
[`@klarlabs-studio/glossa/unplugin`](../unplugin/README.md#the-in-product-editors-loader) adds
to non-production builds loads it, pinned by an SRI hash, after an explicit
gesture. Three layers keep it out of production (RFC 0004 §5.1): the build
(a production build has no loader, and one that asks for it fails), the
runtime (the loader refuses unless every runtime on the page serves a release
whose signed manifest `environment` isn't `production`, and
`runtime.override()` refuses in production too), and delivery (the script
comes from Studio, so a production page's CSP never allows it).

`activate()` is what the loader calls once it has decided a page may be
edited; nothing else calls it.

```ts
import { activate } from "@klarlabs-studio/glossa/overlay";

const overlay = activate({
  apiBase: "https://studio.example.com", // the API origin (Studio serves /v1)
  token: grant, // an in-context grant; @klarlabs-studio/glossa/dev mints it through Studio's popup
  onAuthFailure: () => grant.invalidate(), // a 401 drops it, so the next call asks again
  tenant: "ten_…",
  project: "prj_…",
  locale: "de", // the locale being edited
  runtimes: [runtime], // the page's runtimes; several for islands
  route: "/checkout/:step", // recorded with every edit; default location.pathname
});
// … Alt+click a message, or Alt+Enter on a focused control …
overlay.deactivate(); // panel, listeners, markers and previews gone
```

## What Studio serves

`pnpm build` writes two things: `dist/`, the package's modules, and
`dist/bundle/overlay.js`, the **one minified ES module Studio publishes** at
`/overlay/v1/overlay.js` (esbuild, Lit and `@klarlabs-studio/glossa/capture` included, licence
notices kept, no source map, so the same sources always give the same bytes
and the same SRI hash). Studio's build copies it and publishes
`/overlay/v1/overlay.json` with its `version` and `integrity` beside it
(see [`studio/README.md`](../../../studio/README.md)).

Running the bundle defines `<glossa-overlay>` and puts `{ version, activate }`
on `globalThis[Symbol.for("glossa.overlay")]`, which is how the loader reaches
it: a module script has no exports to read from the outside, and an inline
script would need `'unsafe-inline'`.

## What it does

- **Finding the message.** `activate()` starts a
  [`@klarlabs-studio/glossa/capture`](../capture/README.md) session on the page's runtimes, so
  `t()` strings carry invisible markers that index the session's render log and
  component hosts carry `data-glossa-id`/`data-glossa-locale`. An Alt+click
  takes the innermost marked text under the pointer (the browser's caret
  position), then a marked attribute of the clicked element (`placeholder`,
  `title`, `aria-label`, an input's value…), then the nearest component host,
  then the one message the clicked element shows. "Speichern" rendered by three
  messages opens the right one each time, and a marked value inside another
  message opens whichever the pointer is on. Alt+Enter does the same for the
  focused element, for keyboard users.
- **Editing.** The textarea is parsed by the API (`POST /v1/message-previews`)
  300 ms after typing stops; errors show inline with the kernel's codes and a
  draft that doesn't parse can't be saved. A valid draft previews live: the
  panel calls `runtime.override(id, locale, model)` with the model the server
  parsed, and the page re-renders (`t()` strings through the page's own
  subscription, components through their providers).
- **Saving.** `PUT …/messages/{key}/translations/{locale}` with `origin: human`
  and `origin_detail.in_context: { route, viewport: { width, height } }`, and
  `If-Match` with the ETag the translation was loaded with (none when creating
  the first one). Workflow, QA and review treat it like any edit; nothing
  bypasses review. The page keeps showing the saved model through the session;
  other people see it after the environment publishes again.
  - `412` (someone saved meanwhile): the draft is kept, and "Load latest
    version" takes the new text and ETag so the next save replaces it
    knowingly.
  - `422 structural_qa_failed`: the findings are listed.
  - `401`, `403`, `404`, `429` and network errors get a sentence each.
- **History**: the translation's revisions, newest first, with provenance;
  in-context edits name their route.
- **Terminology**: the message's findings from
  `GET …/terminology-findings?locale=…&key_prefix=…`.
- **Ask AI**: a pending suggestion for the message and locale if there is one;
  otherwise the fill preview says what a fill would do (a provider call, a
  translation-memory match, or why nothing: current translation, sensitive
  namespace, consent off, no budget). "Request a suggestion" queues a fill for
  this one message (with an `Idempotency-Key`) and polls for its suggestion.
  "Use in editor" puts it in the textarea; saving then **accepts the
  suggestion** (with the edits, which the API records for the metrics) rather
  than writing a human revision, so the provenance stays `ai`.
- **Open in Studio** links to the message in Studio's workspace.
- **Comments** aren't shown: the API has no comments. Nothing anywhere in
  Glossa stores a comment on a message or a translation yet — no table, no
  endpoint — so this is a feature of its own, not a gap in the panel.

## Accessibility

The panel is a modal dialog (`role="dialog"`, `aria-modal`, labelled by its
heading) in a shadow root. Focus moves into it when it opens, Tab and
Shift+Tab stay inside it, Esc closes it and returns focus to where it was, and
Ctrl/⌘+Enter saves. Every control has a visible label; validation results and
save outcomes are live regions; the sections are disclosure buttons with
`aria-expanded`. Colours meet WCAG AA (text ≥ 4.5:1, focus rings and control
borders ≥ 3:1). The browser tests run axe (WCAG 2.1 A and AA) on the page
with the panel open, with every section expanded and an error showing.

## CSP for preview deployments

The overlay needs nothing a strict CSP forbids. Its styles are Lit's
constructable stylesheets (adopted into the shadow root, never `<style>`
elements or `style` attributes), it evaluates no strings as code, and
authentication uses a popup, not a frame. A preview deployment's policy
needs only:

```
script-src 'self' https://studio.example.com      # the Studio origin serving the overlay
connect-src https://api.example.com               # the API origin (Studio's, when it serves /v1)
```

- **No** `'unsafe-inline'` in `script-src` or `style-src`, and **no**
  `'unsafe-eval'`.
- **No** `frame-src`: the in-context grant comes through a popup and
  `postMessage`, not an iframe.
- A production page's CSP never needs to allow any of this, because the
  overlay is never loaded there.

The API side is the in-context grants slice, and it is in place: CORS from the
project's registered preview origins on the overlay's endpoints only — the
operations that declare the `in_context` security scheme, and no others —
bearer grants and never cookies (the overlay sends `credentials: "omit"`, and
`Access-Control-Allow-Credentials` is never sent back), and
`Access-Control-Expose-Headers: ETag` so the overlay can send `If-Match`.

The browser tests serve the fixture page with exactly this policy
(`default-src 'none'` plus the two lines above and `style-src 'self'`) and
fail on any `securitypolicyviolation`.

## Tests

- `pnpm test`: the panel against a fake API (`src/testing/fake-api.ts`) in
  jsdom: opening, validation, live preview, saving with provenance and
  `If-Match`, a `412` conflict, a `422` QA failure, history, terminology, AI
  suggestions (requested, used, accepted with edits), keyboard use and ending
  the session. Every request and response body the fake sees is validated
  against `platform/api/openapi.yaml`, so the fake can't drift from the API.
- `pnpm test:browser`: Chromium with Playwright. `e2e/loader.spec.ts` builds
  a preview deployment with Vite and `@klarlabs-studio/glossa/unplugin` and runs the real
  loader against the real bundle served from a Studio origin: the gesture, the
  injected module script with its `integrity` and `crossorigin`, a tampered
  script that the browser refuses, and a page whose runtime serves a
  production release, which stays uneditable even with `?glossa=edit`.
  `e2e/overlay.spec.ts` covers the panel itself. The app page (from
  `@klarlabs-studio/glossa` and `@klarlabs-studio/glossa/elements`), the overlay script (from a Studio
  origin) and the fake API (cross-origin, with CORS) are served by route
  handlers under the CSP above. Covers Alt+click targeting with real layout
  (nested markers, duplicate text, an attribute, a component host), live
  override on save, the focus trap, axe, and constructable stylesheets. Needs
  the workspace built and `pnpm exec playwright install chromium`.
- `pnpm size`: 16 kB brotli with Lit and `@klarlabs-studio/glossa/capture`, 8.5 kB own code.
  An editor session measures nothing, so it is started without
  `@klarlabs-studio/glossa/capture`'s visual probe pass (RFC 0005 §5.1) and that module never
  enters this bundle.
