# Glossa Studio

The web app for translators, developers and localization managers
(RFC 0002 §9): a Vue 3 + Vite SPA, TypeScript strict, zod at the API
boundary, on the Klarlabs design system. Everything it does goes through
the public `/v1` API (`platform/api/openapi.yaml`); nothing is private to
Studio.

**v0** is the foundation of the translator workspace (product intent §25,
§68): sign-in, projects, locales and the fallback graph, and a
keyboard-first editor with live MessageFormat 2 preview, structural QA
from the server, review and history — and releases: environments,
publish, promote, rollback and delivery keys.

**M2** (RFC 0003 §6) adds knowledge and AI: translation-memory, terms,
style and AI-suggestion panes in the editor, a review queue, termbase and
style-guide editors, and AI settings. See *Knowledge and AI* below. It
also brings files in and out: an import wizard (XLIFF, JSON, PO, TMX,
TBX) with a dry run first, exports with an authenticated download, and
the jobs' history. See *Import and export* below.

## Screens

| Route | What |
|---|---|
| `/auth/sign-in` | Follows what the server offers (`GET /v1/meta`): with email, the magic link leads (the emailed link lands here as `#token=…`), a passkey button beside it when the server has passkeys, password + TOTP under “Other ways”; without email, password + TOTP and passkey are the way in and nothing mentions email. |
| `/auth/register`, `/auth/reset-password` | Registration (without email the new account is signed in right away) and password reset by email (link lands as `#token=…`; without email the page says it isn't available). |
| `/t/:tenant` | Projects of the tenant; create one (source locale, authoring syntax, review requirement). The top bar switches tenants (personal and organizations) and creates organizations. Links to workspace settings. |
| `/t/:tenant/settings/knowledge` | Workspace settings › Translation memory & termbase: the workspace's own memory and termbase, shared by every project — import TMX and TBX into them, export all of them, and their jobs. |
| `/t/:tenant/settings/knowledge/imports/:job` | One import into the workspace's memory or termbase: its results, like a project import's. |
| `…/p/:project/translate` | The translator workspace. Locale, filters and the selected key live in the query string, so a view can be bookmarked. |
| `…/p/:project/review` | The review queue: pending AI suggestions riskiest first, triaged from the keyboard (`?locale=` narrows it). |
| `…/p/:project/terms` | The termbase: concepts and their terms per locale with status; history. |
| `…/p/:project/style` | Style guides by scope (workspace, project, locale, namespace), versions, and the effective style. |
| `…/p/:project/ai` | AI settings: consent, providers, budget and spend, prices, routing; the project's namespace tags, auto-translate and review routing; insights. |
| `…/p/:project/locales` | Locales with BCP 47 validation and direction; the list-based fallback graph editor. |
| `…/p/:project/settings` | Name, slug, syntax, review requirement, applications, in-product editing origins, delivery keys, delete. |
| `…/p/:project/releases` | Environments, publish, promote, rollback, release list (see below). |
| `…/p/:project/releases/:release` | One release: per-locale counts and the diff to its parent, promote. |
| `…/p/:project/files` | Import & export: import and export history (state, requester, counts; cancel, download) for the project or the whole workspace; export a catalog, or the project's translation memory (TMX) or termbase (TBX). |
| `…/p/:project/files/import` | The import wizard: file (drop or choose; format recognized, can be changed), options per format (an XLIFF file's target locale is chosen), mode, upload with progress. |
| `…/p/:project/files/imports/:job` | One import: summary, per-item results with where each item is in the file (line, column, reference), conflicts explained, “Apply this import” after a dry run. |
| `/in-context/authorize` | The in-product editor's authorization popup (RFC 0004 §5.2), opened by a product's preview deployment and outside the app shell. It says who the editor will act as and what it will be allowed to do, mints a fifteen-minute grant on confirmation, `postMessage`s it to the exact origin that asked, and closes. Nothing is minted before the person confirms, and the token is never stored. |
| `/account` | Every passkey of the person on any device (from `GET /v1/me/passkeys`: name, added, last used), removable after confirming; adding one where the server has passkeys; authenticator app (TOTP); sign out everywhere. While the person has no passkey and the server has passkeys, a banner promotes them. |

The app shell carries the persistent **Public Beta** badge (Klarlabs
standard §5), a theme toggle (light/dark) and a skip link.

## Keyboard

Studio is keyboard-first. `?` shows this list in the app; it is rendered
from `src/lib/shortcuts.ts`, and a unit test keeps this table in step.

| Keys | Does | Where |
|---|---|---|
| `?` | Show keyboard shortcuts | Everywhere |
| `/` | Search messages | Translator workspace |
| `j` | Next message | Translator workspace |
| `k` | Previous message | Translator workspace |
| `Enter` | Edit the translation | Translator workspace |
| `Esc` | Leave the editor, back to the message list | Translation editor |
| `⌘ Enter` / `Ctrl Enter` | Save the translation | Translation editor |
| `⌘ ⇧ Enter` / `Ctrl Shift Enter` | Save and approve | Translation editor |
| `⌘ ⌥ 1–9` / `Ctrl Alt 1–9` | Insert translation-memory match 1–9 | Translation editor |
| `⌘ ⌥ 0` / `Ctrl Alt 0` | Edit the AI suggestion, then accept it | Translation editor |
| `⌘ ⌥ Enter` / `Ctrl Alt Enter` | Accept the AI suggestion as it is | Translation editor |
| `j` | Next suggestion | Review queue |
| `k` | Previous suggestion | Review queue |
| `a` | Accept the suggestion | Review queue |
| `e` | Edit the suggestion before accepting it | Review queue |
| `r` | Reject the suggestion | Review queue |
| `⌘ Enter` / `Ctrl Enter` | Accept your edit | Review queue |
| `Esc` | Cancel the edit | Review queue |
| `p` | Publish a release | Releases |
| `i` | Import a file | Import & export |
| `x` | Export files | Import & export |

The message list is a WAI-ARIA listbox, so `↑`, `↓`, `Home`, `End`,
`PageUp` and `PageDown` work in it too. Single-key shortcuts never fire
while you type in a field, and `Enter` still activates a focused link or
button. The `⌘ ⌥` / `Ctrl Alt` chords match the physical digit key, so
they work on any layout, and never fire for AltGr (which types `{`, `@`
and friends on many Windows layouts); a chord for a TM match that isn't
there passes through untouched.

## The translator workspace

- **List**: virtualised (only visible rows are in the DOM), so thousands
  of messages scroll smoothly; pages load progressively. Filters:
  namespace, missing-in / outdated-in the target locale, message state
  (server-side) and search over key, source text and context (client-side).
  Rows show *Missing* / *Outdated* for the target locale, from one bulk
  listing of its translations (`GET …/translations?locale=…`, by message
  ID); the coverage filter and a summary line show the locale's counts
  from `GET …/translation-stats`. *Where it appears* adds filters by
  route, component and file (the project's current builds offer the
  choices), and by `unused` and `not captured`; *New on a branch* is the
  `proposed` state until the branch switcher lands. Every filter lives in
  the URL. Route, component and file are resolved by the server
  (`GET …/usages`) and `unused` by `GET …/unused-messages`; `not
  captured` has no endpoint yet, so Studio asks per message with a usage,
  at most 500 of them, and says how far it looked.
- **Editor**: the developers' context, the source with argument chips from
  its derived metadata (type, selector kind and keys) and markup, the
  canonical MF2 on request, and the target editor with `lang`/`dir` of the
  target locale.
- **Live preview**: source and translation rendered with editable sample
  values, and one-click samples for every plural category of the *target*
  language (Arabic gets zero/one/two/few/many/other). It uses the reference
  formatter (`@klarlabs-studio/glossa/messageformat`), loaded lazily. MF2 text is parsed as
  you type. **MF1 is parsed only by the server** — RFC 0002 §5 keeps exactly
  one MF1 converter, in Go — so MF1 text goes to `POST /v1/message-previews`
  as you type (250 ms after the last keystroke; the latest request wins; the
  saved text needs no request). Text that doesn't parse shows the kernel's
  errors (`mf1-syntax-error`, …) inline before anything is saved; the
  server's rate limit (`429`) pauses the preview and it retries after 1.5 s;
  if the server can't be reached, the preview shows the last saved text and
  says so.
- **QA**: a write that fails structural QA (`422 structural_qa_failed`)
  shows its findings inline, marks the field invalid and keeps focus there;
  stored warnings show under the editor.
- **Outdated**: a badge with the source revisions involved and a word diff
  of the source since the translation was made.
- **Review**: approve, reject and request review, offered only when the
  member's roles and locale scope allow it (mirrored from Identity's role
  matrix; the server decides). `⌘⇧Enter` saves and approves in one step.
- **Where it appears** (RFC 0004 §3.4): a pane beside translation memory
  and terms. The usages of the message grouped application → route →
  component, each with `file:line` — plain text until a Git connection
  makes them `blob/<commit>/<file>#L<line>` links (RFC 0004 §6) — and the
  screenshots that show it, cropped around the message with its
  surroundings dimmed, a select between the locales a screen was captured
  in (the target's leads), and a full-page lightbox on the native
  `<dialog>`. Regions are CSS-pixel boxes on the full-page image, so the
  crop is placed in percentages and scales with the pane. Images come
  from the API on Studio's own origin (`…/captures/{capture}/image`), so
  the session cookie travels with them, they cache for a year, and no
  `blob:` URL is needed — the CSP allows none. Three empty states say
  different things: *no usage data yet* (nothing was uploaded; it names
  `glossa extract --upload`, the bundler plugin and `glossa capture
  --upload`), *unused* and *not captured*.
- **History**: every revision with kind, state, origin, `origin_detail`,
  author and the source revision it was made against.

## Development

```sh
pnpm install
pnpm --filter "@klarlabs-studio/glossa..." build       # Studio imports its build
pnpm --filter @glossa/studio ui:link            # the design system, see below
# run glossa-server on :8080 (platform/README.md; GLOSSA_STUDIO_URL defaults to http://localhost:5173)
pnpm --filter @glossa/studio dev                # http://localhost:5173, /v1 proxied to GLOSSA_API_URL
```

Studio calls the API on its own origin. Vite (dev and preview) proxies
`/v1` to `GLOSSA_API_URL` (default `http://localhost:8080`); in production
the same host routes `/v1` to `glossa-server`. That keeps the
`__Host-glossa_session` cookie first-party and needs no CORS. Unsafe
requests carry `X-CSRF-Token` from the session.

| Script | Does |
|---|---|
| `dev`, `build`, `preview` | Vite (both `dev` and `build` provide the design system first). |
| `lint` | API types staleness check + `vue-tsc` (app, tests) + `tsc` (configs, e2e). |
| `test` | Vitest unit and component tests (happy-dom). |
| `test:e2e` | Playwright against a real server (below). |
| `gen:api` | Regenerate `src/api/schema.d.ts` from `platform/api/openapi.yaml`. |
| `size` | Report the initial JS bundle (gzip) from the build; fails over 250 kB. The overlay isn't part of it: it's served, never imported. |

### The in-product editor Studio serves

Studio publishes [`@klarlabs-studio/glossa/overlay`](../runtimes/js/overlay/README.md) at
**`/overlay/v1/overlay.js`**, with **`/overlay/v1/overlay.json`**
(`{ version, integrity }`) beside it ([RFC 0004
§5.1](../docs/rfcs/0004-context.md)). `build/overlay.ts` is the Vite plugin
that copies the package's bundle and hashes it; `vite dev` and `vite preview`
serve the same two files, so a preview deployment can point at a local Studio.

A preview deployment of a product pins that `integrity` in its own build
(`@klarlabs-studio/glossa/unplugin`'s `studio` option) and loads the script cross-origin with
`crossorigin="anonymous"`, so the image's nginx answers this path — and only
this path — with `Access-Control-Allow-Origin: *` and
`Cross-Origin-Resource-Policy: cross-origin`, without the same-origin headers
the rest of Studio sends. `overlay.js` is cached for five minutes (a new
Studio deploy changes its bytes and its hash, and builds pinned the old one),
`overlay.json` never (`no-store`), because that's what a build reads to pin.
The script is public, read-only JavaScript: nothing about a tenant is in it.

Updating the overlay is therefore a two-step deploy: Studio ships the new
bundle, then the products' builds pin the new hash from `overlay.json`. Until
they do, their pinned hash keeps matching the cached old script for up to five
minutes and then the browser refuses the new one, and the loader says so on
the console — the editor stops working, nothing else does.

### API client

`src/api/schema.d.ts` is generated from the contract with
`openapi-typescript`; `openapi-fetch` is the typed client. `pnpm lint`
fails when the committed file is stale, so a contract change is picked
up in the same PR. Every response Studio trusts is validated with zod
(`src/api/schemas.ts`), and compile-time assertions tie each zod schema to
the generated type. Errors become `ApiError` with the problem's stable
`code` (never its title), field errors and QA findings.

### Design system (`@klarlabs-studio/ui`)

Studio uses the tokens (colours, type, spacing, motion via `--kl-*`),
`kl-badge` (the Public Beta badge) and `kl-theme-toggle`; its own
components consume the tokens. Product accent: see below.

The package is published to GitHub Packages, which needs a token even for
public packages, so an open-source checkout (and CI) can't install it from
there. Until it's on a public registry, Studio depends on it as
`"@klarlabs-studio/ui": "link:.klarlabs-ui"`, and
`scripts/klarlabs-ui.mjs` (`pnpm ui:link`, also run by `dev` and `build`)
makes `.klarlabs-ui` point at a built copy:

- by default it clones the public `klarlabs-studio/klarlabs` repository at
  the commit pinned in `package.json#klarlabsUi`, builds `packages/ui` once
  and links it (both gitignored);
- with `KLARLABS_UI_DIR=/path/to/klarlabs/packages/ui` it links that local,
  built checkout instead.

Type-checking and unit tests don't need it; `dev`, `build` and e2e do.

**Switching to the registry** when that's possible: set
`"@klarlabs-studio/ui": "0.1.0"`, add an `.npmrc` with
`@klarlabs-studio:registry=https://npm.pkg.github.com` and
`//npm.pkg.github.com/:_authToken=${NODE_AUTH_TOKEN}`, drop the `ui:link`
calls from `dev`/`build`, the `klarlabsUi` field and the CI step, and give
CI a token with `read:packages`.

### Accent

Glossa isn't in the standard's accent table (§6); Studio proposes
**Glossa — light and dark — `#2563EB` cobalt** (dark theme `#60A5FA`).
Reasons: no Klarlabs product uses blue in this band (Pet Medical's sky
`#0EA5E9` is a lighter cyan); and a translation tool leans on semantic
colours all day — green approved, amber needs review and warnings, red
rejected and QA errors — so a teal or amber accent would read as a review
state. `#2563EB` passes AA as text on white and under white text; text on
accent tints uses the deeper `#1D4ED8` (`--gs-accent-ink`). The overrides
are in `src/styles/studio.css`.

## Tests

- **Unit and component** (`pnpm test`): BCP 47 checks, fallback-graph
  validation (mirrors the server's cycle rules), permissions, diff, samples
  and plural categories, virtualisation, shortcuts, WebAuthn encoding, the
  API boundary (CSRF, ETags, Idempotency-Key, problem codes, zod), release
  eligibility and rollback targets, runtime snippets, and the list,
  preview, editor, locale input, releases, release detail and delivery-key
  components. The release components run on `src/test/fake-releases.ts`,
  an in-memory `ReleasesPort` with Release's rules (policy coverage,
  rollback history, If-Match, idempotent publish). The knowledge and AI
  screens run on `src/test/fake-knowledge.ts` and
  `src/test/fake-intelligence.ts` (term recognition and terminology QA in
  miniature, write-only provider keys, version-0 settings, fills that
  settle into given suggestions, a risk-ordered queue, decided suggestions
  that can't be decided twice). "Where it appears" and the context
  filters run on `src/test/fake-context.ts` (a message's usages and
  captures from the current builds, unused messages, coverage counts),
  with the crop geometry checked on its own in `src/lib/context.test.ts`.
- **The overlay Studio serves** (`build/overlay.test.ts`): a real `vite build`
  with the delivery plugin writes `/overlay/v1/overlay.js` and an
  `overlay.json` whose `integrity` is the hash of exactly those bytes.
- **End to end** (`pnpm test:e2e`, `e2e/`): the global setup starts
  Postgres 16 with testcontainers, provisions it like production (a
  `CREATEROLE` owner migrates, the server runs as `glossa_app`), builds and
  starts the real `glossa-server` from `../platform` with the log mailer and
  release object storage on the **`dir` driver** (`GLOSSA_STORAGE_DIR` =
  `e2e/.state/objects`, emptied per run; no MinIO needed), and Playwright
  serves the Studio build with `vite preview`. The main test signs in with
  the magic link the dev mailer captured, creates a project, adds locales,
  imports a small catalog through the API, translates with the keyboard,
  hits a QA error for a broken placeholder, fixes it, approves and checks
  history and the shortcut sheet; before that, the live MF1 preview flags
  an unclosed placeholder (`mf1-syntax-error`) while typing, before any
  save. Then it releases: the publish dialog's dry run shows the counts and
  per-locale changes (and storage stays empty), it publishes to
  development, sees it on the environment card, finds that release
  ineligible for production (development ships drafts), publishes to
  staging and promotes that release to production, approves one more
  translation, sees it as the one change in the next dry run, publishes it
  to staging and promotes it, and rolls production back, checking after
  each step the manifest glossa-edge would serve from storage. Last, it creates a delivery key (snippets; the key's index
  object appears in storage) and revokes it (the object goes).
  `@axe-core/playwright` (WCAG 2.2 AA tags) runs on each main screen and
  dialog, light and dark.

  The knowledge and AI test (`e2e/knowledge-ai.spec.ts`) adds a termbase
  concept in the editor, sees the term highlighted in the source and a
  forbidden-term warning while typing, approves, sees that approval come
  back as a fuzzy translation-memory match for a similar message and
  inserts it with `⌘⌥1`; then configures AI in the settings screen (a
  provider with a key that is never shown again, consent after
  confirming, a budget, the legal namespace tagged), fills the locale with
  AI, reads the suggestion's confidence and “Why?”, and triages the review
  queue — riskiest first, accept one with an edit from the keyboard, batch
  accept the recommended ones — and checks the metrics.

  The import and export test (`e2e/import-export.spec.ts`) approves a
  German translation through the API, then imports a small XLIFF file
  through the wizard: the dry run reports the approved translation as a
  conflict (“the approved one is kept”) and changes nothing; *Apply this
  import* merges it — the new translation arrives with origin `import`,
  the approval stays — and the history lists both jobs. Then it exports
  JSON for `en` and `de` narrowed to a namespace chosen from the
  project's, downloads the zip and checks both files in it (the harness
  sets `GLOSSA_INTEGRATION_POLL_INTERVAL=200ms`); every result shows its
  line, column and XLIFF reference. A second test imports an XLIFF file
  whose `trgLang` (`de-CH`) the project lacks as `de`, chosen in the
  wizard, then imports a TMX file into the workspace's memory on its own
  screen (results with `tu[1]` and its line) and exports and downloads
  the whole memory. Axe runs on the list, the wizard, the results, the
  export dialog and the workspace screen.

  The context test (`e2e/context.spec.ts`) uploads a usages document and
  then a capture upload — a real multipart body with PNGs generated in
  the test (`e2e/png.ts`), which the server validates, re-encodes and
  stores — for a seeded project, and reads them back in the workspace:
  the usages grouped by application, route and component, the cropped
  screenshot with the message outlined, the locale toggle, the full-page
  lightbox (closed with Escape), *unused* and *not captured*, and the
  message-list filters. Axe runs on the pane, the lightbox and the
  filters, light and dark.

  **No real AI provider is ever called.** The harness starts
  `e2e/fake-provider.ts`, an OpenAI-compatible `/chat/completions`
  endpoint on loopback (`GLOSSA_E2E_PROVIDER_PORT`, 18318) that answers
  from a cassette, `e2e/fixtures/provider-cassette.json`: a draft per
  source and target locale, a self-assessment per draft. Everything else
  is real — the server's translation agent, its prompts, MF2 parsing,
  validation, terminology QA, scoring, routing, budget and disclosures.
  The test configures it like a tenant would (an `openai_compatible`
  provider with that base URL, and a routing policy pointing every task at
  it), which the server allows because the harness sets
  `GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS=true`. A request the cassette can't
  answer fails with HTTP 400 naming the source, so the job fails loudly
  instead of passing by accident; every request is logged to
  `e2e/.state/provider-requests.jsonl` (the test checks the prompt carried
  the termbase). To cover a new message, add its draft and assessment to
  the cassette.

  Needs Docker, Go and `pnpm build` first:

  ```sh
  pnpm --filter @glossa/studio build
  pnpm --filter @glossa/studio exec playwright install chromium
  pnpm --filter @glossa/studio test:e2e
  ```

  Ports: `GLOSSA_E2E_API_PORT` (18317), `GLOSSA_E2E_STUDIO_PORT` (4317),
  `GLOSSA_E2E_PROVIDER_PORT` (18318).
  `GLOSSA_E2E_SERVER_BIN` uses a prebuilt server. `STUDIO_SCREENSHOTS=<dir>`
  keeps a screenshot of every screen axe checked. CI runs it as the
  `Studio e2e` job.

## Releases

Everything goes through `ReleasesPort` (`src/api/releases.ts`); `main.ts`
provides `apiReleases`, its implementation over the generated client with
every response checked by zod.

- **Environments**: a card per environment (`development`, `preview`,
  `staging`, `production`, then custom ones) with the release it serves,
  when and by whom it last changed (its newest deployment), and its
  eligibility policy. *Policy* edits the policy with `If-Match`;
  *History* lists the environment's deployments.
- **Publish** (`p`): choose the environment and a note. Before confirming
  the dialog runs the server's dry run for that environment
  (`POST …/environments/{env}/release-previews`, nothing stored): the
  policy, messages, locales, artifacts to upload and size, and per locale
  what would ship and what would be added, changed or removed against the
  release the environment serves (or that nothing would change). A catalog
  that can't be released lists every problem and publishing is disabled.
  After publishing, the per-locale diff against the previous release. Each
  confirmation sends one `Idempotency-Key`, reused when the same request
  is retried.
- **The path to production**: `development` and `preview` ship work in
  progress (drafts too), `staging` and `production` approved text only. So
  publish to staging and promote that release to production; the promote
  dialog refuses a development release and says so.
- **Promote** and **rollback** name the pointer move
  (`production: v2 → v3`) and show per locale what is added, changed and
  removed compared with what the environment serves. Promote refuses a
  release whose policy the target doesn't cover (mirrored from Release's
  `Policy.Covers`; the server decides). Rollback preselects what the
  server would pick and always sends that release explicitly, so a retry
  can't walk two steps back.
- **Release detail**: per-locale messages and outdated counts, the diff to
  its parent, artifacts, manifest digest.
- **Delivery keys** (project settings): a new key is shown in full once,
  with a copy button and snippets for `@klarlabs-studio/glossa`, `@klarlabs-studio/glossa/vue`,
  `<glossa-provider>` and the Go runtime, pinned to the project's active
  signing keys; the list shows keys masked; revoke asks first and says
  what runtimes will see.

Publishing, promoting, rolling back, policy changes and key changes need
`releases.publish` (developers, admins, owners); without it the screens
are read-only.

The snippets need glossa-edge's public origin. Studio takes the one the
server announces (`edge_url` in `GET /v1/meta`, from
`GLOSSA_EDGE_PUBLIC_URL`), else `edgeUrl` from the runtime configuration
its image serves at `/config.json` (`GLOSSA_STUDIO_EDGE_URL`), else the
build's `VITE_GLOSSA_EDGE_URL`; without any they show a placeholder and
say so.

## Knowledge and AI

Everything goes through two ports: `KnowledgePort` (`src/api/knowledge.ts`:
translation memory, termbase, terminology checks, style guides) and
`IntelligencePort` (`src/api/intelligence.ts`: providers, settings,
routing, budget, fills, jobs, suggestions, the review queue, insights),
each with its zod schemas in a module of its own. `useKnowledge()` and
`useIntelligence()` fall back to the API adapters when nothing is
provided, so they load with the screens that use them and stay out of the
initial bundle. Money is integer micro-USD end to end; Studio converts
only to show it and to read what people type.

- **Editor panes** (the workspace's third column; below the editor on
  narrower screens): the **AI suggestion** for the message and locale —
  confidence as a band (very high ≥ 0.90, high ≥ 0.75, medium ≥ 0.50,
  low) *and* the number, never a percentage, with “it isn't a promise”
  said once; the routed action, risk tags and the action note; **Why?**
  lists every factor with its signed contribution, largest first, and the
  provenance (provider and model, prompt version, TM units, terms, style
  version, repairs, cost). Accept (`⌘⌥↵`), edit then accept (`⌘⌥0`, sends
  the edit as MF2 `text` so its diff feeds the metrics) or reject with an
  optional reason — for members who may translate and write the locale.
  **Translation memory**: up to five matches with a score badge, the kind
  (same key, exact, fuzzy), a word diff of the remembered source against
  this one (both normalized: placeholders by position), where it came from,
  and insert (`⌘⌥1`–`9`; the target is MF2, so the editor switches to
  MF2). Showing a match doesn't count as a hit. **Terminology**: every
  concept recognized in the source with its definition and the target's
  terms, the ones to use first, the ones to avoid struck through. The terms
  are highlighted in the source as written (the server's offsets are into
  the visible text, so each hit is found in the source by its words,
  occurrence by occurrence). **Style**: the effective guide for the locale
  and the message's namespace. **Concordance**: how a phrase was
  translated before, in the source or the target.
- **Live terminology QA**: 300 ms after the last keystroke the draft is
  checked (`terminology-checks`, the latest request winning); forbidden and
  deprecated terms and missing preferred ones show under the editor and in
  its `aria-describedby`. A draft whose syntax differs from the source's (a
  TM match in MF2 against an MF1 source) is checked as plain text; text
  that doesn't parse keeps the last findings, marked paused.
- **Fill with AI** (in the workspace's filters, with `intelligence.translate`
  for the locale): queues a fill for the current filter — the namespace,
  missing (or with *Also redo outdated*, outdated) messages, and the keys a
  search narrows to — shows the server's warnings up front (consent off,
  no budget, no provider), and follows the fill's jobs (`ai-jobs?fill=`)
  until they settle, with why any failed.
- **Review queue**: the server's order (lowest score, then most risk
  tags), narrowed to the member's locale scope; the focused suggestion
  with its source, context and “Why?”. `j`/`k` move, `a` accepts, `e`
  edits (`⌘↵` accepts the edit, `Esc` cancels), `r` rejects; decided items
  leave the list and the next takes their place. *Accept N recommended*
  accepts only suggestions routed `approve_recommended`, after confirming.
- **Termbase** and **style guides**: see Screens; changing them needs
  `knowledge.write` (developers, admins, owners).
- **AI settings**: consent is off by default and turned on only after a
  confirmation that says what is sent; provider keys are write-only
  (`type=password`, cleared from memory once sent, never shown — the list
  says only *Key stored (sealed)*); the budget with spend per day (one
  series, a table beside it) and per model; price overrides; the routing
  policy per task and locale with fallbacks, for the project or the
  workspace; namespace tags, auto-translate locales and review routing
  with auto-approve off by default and a warning when it goes on; per-locale
  acceptance and edit distance, the disclosures log and the eval baseline.
  Changing any of it needs `intelligence.manage` (admins, owners).

Settings the server hasn't saved yet come at version 0 with the ETag
`"0"`, which the server doesn't accept back in `If-Match` (it only parses
versions ≥ 1), so the adapter sends those writes unconditionally.

## Import and export

Everything goes through `IntegrationPort` (`src/api/integration.ts`, zod
schemas in `integration-schemas.ts`), loaded with the screens that use it.

- **Import wizard** (`i` on Import & export): drop or choose a file. The
  format is recognized by its extension, else by its first bytes, and can
  be changed. Options per format: **XLIFF** imports its translations as a
  target locale of the project chosen in a picker (`options.locale`): the
  file's `trgLang` by default when the project has it; a file whose
  `trgLang` the project lacks (`de-CH` into a project with `de`), or that
  names none, has to be given one before it can be sent (a file without
  translations and without `trgLang` is a source catalog); it can read
  units from other tools as ICU MessageFormat; **JSON** the file's locale (the
  source locale makes it a source catalog; guessed from names like
  `de.json`), syntax and namespace (flat and nested files are both read);
  **PO** the target locale (default: the file's `Language` header), the
  review state for entries without `fuzzy`, namespace and plural variable;
  **TMX/TBX** import into this project (the whole workspace's memory and
  termbase have their own screen, below). Then the mode, each
  with what it does: **dry run** (the default: every check of a merge,
  nothing changes), **merge** (never replaces an approved translation, a
  differing source or concept: those are conflicts) and **overwrite**
  (only with `integration.manage`, after a confirmation that says what it
  replaces). The file is created as a job, `PUT` to its `upload_url` with
  progress (XMLHttpRequest, since fetch reports none; same origin only,
  with the CSRF token), and followed until it ends; it can be cancelled on
  the way.
- **Permissions** are mirrored: `integration.import` is locale-scoped
  like `translations.write`, so a translator's other locales (and the
  source locale, which needs `integration.manage`) are disabled with the
  reason, and a file in one of them can't be sent; TMX and TBX need
  `integration.manage` and `knowledge.write`. The server decides again,
  with the access the requester had when they asked.
- **Results**: counts by status (created, updated, unchanged, conflict,
  invalid) and by kind, then every item in file order — filterable by
  status and kind, paged — with where each item is in the file (line,
  column and its reference in the format's own terms: an XLIFF fragment
  identifier `#/f=…/u=…`, a JSON pointer, a PO `msgctxt`/`msgid`, `tu[n]`,
  `conceptEntry[n]`) and why a conflict or an invalid item is one (“An approved translation
  differs; the approved one is kept.”). After a **dry run**, *Apply this
  import* runs the same file with the same options as a merge: the file
  the wizard sent is kept in memory for that; after a reload, you choose
  it again and Studio checks it's the same by its SHA-256. An import the
  server **reused** (`reused_job_id`: the same file and options imported
  before) says nothing was applied again and links the earlier one.
- **Export** (`x`; *Export translation memory (TMX)* on Import & export,
  *Export termbase (TBX)* on the termbase): format (`po` is import only),
  locales (several make a zip), namespaces — chosen from the project's
  own (`GET …/namespaces`, with their message counts, a page at a time;
  none chosen means all) —, review states, JSON layout and syntax, TMX
  source and target locales; TMX and TBX export this project's own.
  The job is followed until it's written; **Download** fetches the file
  with the session (never a bare link: it needs the cookie), names it from
  `Content-Disposition` and hands the blob to the browser.
- **Jobs**: imports and exports newest first, for this project or the
  whole workspace, with format, mode, state, counts, requester and when;
  they refresh while any job is running. Running imports (they stop after
  the current batch) and queued exports can be cancelled; finished exports
  downloaded until retention deletes their file.
- **The workspace's translation memory and termbase** (workspace
  settings, `/t/:tenant/settings/knowledge`, linked from the projects
  screen and Import & export) live outside any project, on the tenant's
  own routes (`tm-import-jobs`, `termbase-import-jobs`, `tm-export-jobs`,
  `termbase-export-jobs`): import a TMX or TBX file (dry run by default,
  merge, overwrite after confirming; needs `integration.manage` and
  `knowledge.write`, mirrored), export the whole memory (narrowed by a
  source locale and target locales) or termbase and download it, and both
  job lists. Its imports open on a results screen shared with projects.

## Layout

```text
src/api/        generated contract types, client, zod schemas, endpoints, errors; the Releases, Knowledge, Intelligence and Integration ports and their API adapters
src/session/    session store, deployment facts (GET /v1/meta), permission mirror, names for principals
src/lib/        pure logic: bcp47, fallback, diff, samples, preview, shortcuts, virtual, webauthn, releases, snippets, terms, confidence, money, style, integration, …
src/components/ app shell, message list, editor, preview, QA, history, modal dialog, releases/*, assist/* (editor panes), knowledge/*, ai/*, integration/*, …
src/views/      auth, projects, account, project/* (workspace, review queue, termbase, style guides, AI, locales, settings, releases, release detail, import & export, import wizard, import results), workspace/* (workspace settings: translation memory & termbase files, their import results)
src/strings.ts  every user-facing string, ready to become Glossa messages
src/test/       component-test helpers: in-memory Releases, Knowledge, Intelligence and Integration ports, mounting a project screen
e2e/            Playwright specs, the server harness and the fake AI provider with its cassette
```
