# RFC 0004 — Context (M3)

**Status:** Proposed — 2026-09-19 · owner decisions of 2026-09-20 in §14
**Builds on:** [RFC 0002](./0002-platform-architecture.md) §4 (Context and Integration contexts), §7–§9, §12, §13 (M3) · [RFC 0003](./0003-knowledge-and-intelligence.md) §2.4 · [`runtimes/SPEC.md`](../../runtimes/SPEC.md)
**Intent:** §11–§12, §16–§18, §25–§27, §30–§32, §37, §50, §60 (Phase 2), §67–§68

## 1. Goal

M3 makes the platform **know where every message appears** (intent §68: the translator should rarely need to ask "where does this string appear?"). Context is captured by the build, the CI and the running product, not typed in by developers (intent §16). A translator sees the file, the component, the route and a screenshot with the message outlined, and can edit the text inside the running product (intent §26). Pull requests show what a change does to localization before it merges (intent §30–§31). The Go runtime becomes good enough to render documents, not only emails (intent §12).

M3 is done when two things hold:
- **Context.** Every active message of a fixture app has at least one usage (file, line, component) and at least one screenshot region, and Studio shows both. A pull request against the fixture gets a Glossa check and a branch environment that delivers its copy, and an edit made in the in-product editor — against the shared `preview` deployment, because M3 deploys no application per PR (§14.2) — lands as an ordinary translation revision.
- **Documents.** The Go runtime renders a fixture tax summary and a fixture export in de, en, es, fr and ja, with plurals, currency, dates and bold text, to HTML and to PDF.

The internal exit test is §12. The real exit (Nexa's tax PDF and Lexora's export in production, translators on Brotwerk and KraftSport seeing usages and screenshots) happens in the dogfood phase.

**What exists.** M1 and M2 left a few hooks for this milestone, and none of them is filled yet:
- `domain.MessageContext.Usages` is part of the `message_context` agent tool, but nothing fills it. Neighbours come from the key prefix only.
- `glossa extract` scans with regular expressions and returns key, file, line, column and kind. It has no component, no route and no upload.
- Environments are free-named. Every delivery key can read every environment.
- `<glossa-text>` sets `data-glossa-pending` and `data-glossa-missing` but doesn't mark which message it rendered.
- The Go runtime returns plain strings, so markup disappears (the `en link markup` fixture renders as plain text). It has no parts API, no standalone number or date formatters and no time-zone option. The runtime-format fixture covers only de, en, he and ar.
- The Integration context only runs import and export jobs.

`platform/internal/preview` is the MessageFormat preview behind Studio's editor. It is unrelated to preview environments and keeps its name. Preview environments belong to Release (§4).

## 2. Usages

### 2.1 Collection

Three sources feed the Context context. They all produce the same document (§2.2).

- **Bundler plugin** (`@klarlabs-studio/glossa/unplugin`, new in `runtimes/js/unplugin`). It's built on `unplugin`, so one core serves Vite, Rollup, webpack and esbuild. Astro gets it through `@klarlabs-studio/glossa/astro`, which adds it to Vite.
  - The plugin reads modules **after** the framework transforms (Vue SFC templates compiled, JSX and TS stripped). It parses them with the bundler's own parser and maps every hit back to the original file and line through the combined source map.
  - It recognizes the same call shapes as `glossa extract`: `t()`/`$t()`, `<GlossaText id>`, `<glossa-text key>`, React `<T id>`, and the typed accessors from `glossa generate`.
  - **Component** means the component that defines the call: the SFC or `.astro` file name, or the nearest enclosing capitalized function or class for JSX. The ancestry path from intent §17 (`PaymentFooter > PrimaryButton`) needs the running tree and waits for a later milestone.
  - **Route** is filled where the plugin can know it: Astro's `src/pages/**`, or a `routes` map in the plugin options (route pattern → file globs).
  - The plugin **never makes network calls**. It writes `.glossa/usages.json` next to the build output, outside anything that gets served. The CLI uploads that file (§6.3). Builds stay reproducible, and no credential is ever needed inside a bundler.
- **`glossa extract`** stays the extractor for Go and for anything without a bundler. The Go scanner moves from regular expressions to `go/ast`: calls to `T`, `For(…).T` and the typed accessors, where the component is `pkg.Func` or `pkg.(*Type).Method`. Templates are read with `text/template/parse` (`{{t "…"}}`, `{{td …}}`, `{{th …}}`). Web files keep the lexical scan as the fallback when no plugin runs. `--upload` sends the result.
- **Runtime-reported usages** exist in M3 **only inside capture and in-product editor sessions** (§3.1, §5), in development and preview environments. They go to the control plane over the overlay's authenticated session, never through `glossa-edge`. Production runtimes report nothing: SPEC §6 already says runtimes send no telemetry unless the application enables it, and M3 adds no switch for that. Sampled production usages are **ruled out** for M3, not deferred (§14.4).

**One behaviour, two implementations.** The TS plugin and the Go extractor share a fixture suite in `runtimes/testdata/usages/`: source files plus the usages expected from them, the same way the conformance suites work. A difference between the two becomes a fixture first.

### 2.2 Model

A **usage** is a message × a location × a build. A **build** is one upload for one application at one commit:

```json
{
  "schema": "glossa.usages/v1",
  "application": "web",
  "commit": "9f2c1e7…", "branch": "feat/checkout-copy",
  "tool": { "name": "@klarlabs-studio/glossa", "version": "0.1.0" },
  "usages": [
    { "key": "checkout.pay", "file": "src/checkout/PaymentFooter.vue", "line": 42, "column": 9,
      "component": "PaymentFooter", "route": "/checkout/payment", "kind": "t" }
  ]
}
```

- Context aggregates: **Build** (application, commit, branch, source (`plugin`, `extract`, `runtime` or `capture`), tool, digest), **Usage** (key, message ID, file, line, column, component, route, kind), **Capture** and **Region** (§3). They're tenant-owned under forced RLS in the Context context's own tables, keyed by Catalog's project and application IDs without foreign keys (RFC 0002 §4).
- Usages are **resolved to message IDs at ingest**, so a rename doesn't touch them. A key that isn't in the catalog or the branch overlay (§4.1) is stored with a null ID. PR checks report it as an **unknown key**.
- An upload is **idempotent** by (application, commit, source, digest). Uploading the same file twice is a no-op.
- The **current usages** of a message are the ones from the latest build of each application on the default branch. A branch view uses that branch's latest build, and falls back to the default branch for applications the branch didn't rebuild.
- **Unused messages** are active messages with no usage in any current build. They are reported in Studio and the CLI and never obsoleted automatically, because dynamic IDs can't be seen.
- Events: `context.build.ingested` (Intelligence refreshes context, Integration updates the PR check) and `context.capture.ingested`.
- The `message_context` tool fills `Usages` (`file:line (component, route)`, at most 10, the default branch first). Neighbours prefer messages that **share a route or a capture** with this one, and fall back to the key prefix. This is the M3 slice of the context graph (§8).

### 2.3 Retention and a stateless delivery plane

- For each (application, branch), the latest **3** builds are kept, plus every build that is still current. A branch's builds, and the captures that hang off them, are deleted **7 days** after the branch closes (§4.2). A tenant's stored capture images are capped at **2 GB** (§3.3). A purge job runs daily.
- **Two retentions, two clocks.** This one is Context's, over builds and captures. Catalog's *proposal* retention — a closed branch's proposed messages become obsolete 14 days after it closed (§4.1) — is a different rule over different data, and stays at 14 days: text a translator worked on outlives the screenshots of it.
- Usages never reach the delivery plane. Artifacts and manifests stay exactly as SPEC §1 defines them. `glossa-edge` gains no endpoint, no database and no write path. The runtime's capture support (§3.1) is a render hook that runs in the page and holds no state beyond the page's lifetime.

## 3. Visual context

### 3.1 Capture mode: how rendered messages are found

A screenshot is useful only if it shows **which pixels** belong to a message. The runtime can mark what it renders, but only while a capture or editor session is active.

- `@klarlabs-studio/glossa` gains one extension point, `onRender(hook)`: a hook that sees (id, locale, values digest, output) and may decorate the output. It costs well under 100 bytes and stays inside the 6.5 kB budget. The runtime ships no capture code itself.
- The capture module (loaded only in a session, §5.1) installs a hook that does two things:
  - Component-rendered messages (`<glossa-text>`, `<GlossaText>`, React `<T>`) get `data-glossa-id` and `data-glossa-locale` on the host element. For them, the host element's box is the message region.
  - Strings returned by `t()` are wrapped in **invisible markers**: a start mark, the index of the render in the session's log (encoded in zero-width characters), the text, and an end mark. The capture script walks text nodes and attribute values, turns each marked range into boxes with `Range.getClientRects()`, and looks the index up in the render log. The markers are zero-width, so the layout doesn't move.
- **Trade-off.** Markers change string lengths, so `maxlength` inputs and code that compares strings behave differently during a capture. That's why markers exist only in sessions, never in a normal page view. The alternative, matching rendered text back to messages, fails on duplicates ("Save" appears ten times) and on formatted values. Element-only marking misses every `t()` call, which is most Vue and Go-template usage.
- A message in an attribute (`placeholder`, `title`, `aria-label`) gets a region of kind `attribute`: the element's box. A marked message that renders zero-size or off-screen gets a region with `visible: false`, so the gap shows up instead of being hidden.

### 3.2 Capture: `glossa capture` on `scout`

In M3, **capture runs where the app already runs: in the product's CI**, as `glossa capture`. It uses the `scout` Go library (`go.klarlabs.de/scout` ≥ 1.15): `Engine.NewPage`, `Page.Navigate`, `SetViewport`, `Evaluate` (injects the capture script and collects regions) and `ScreenshotWithOptions` (full page, PNG, `MaxSize`).

- A **capture plan** in `glossa.yaml` (`capture:`) lists the base URL, the routes (a pattern plus a concrete URL), the viewports (default 1280×800 and 390×844), the locales (default: the source locale), optional `scout` playbooks per route for interaction (opening a dialog, for example), and cookies or headers for a **fixture** login.
- Each (route, viewport, locale) becomes one **Capture**: the page image plus its regions. The CLI uploads the captures as part of the build (§6.3).
- **Coverage report.** Messages with a current usage but no region are listed as `not captured`, so a team can add routes where they're missing.
- **Why CI and not a server worker.** RFC 0002 §3 placed capture in server workers. Capturing in CI means Glossa never navigates to customer-supplied URLs (no SSRF; `scout`'s URL validator doesn't cover DNS rebinding), works against `localhost` previews and needs no app credentials at rest. The same capture package can run in a server worker later (M4 visual QA) against allow-listed hosts, from an egress-restricted pod.

### 3.3 Storage

- Images are validated and **re-encoded on the server** before they're stored: PNG only, at most 40 megapixels and 10 MB, with metadata stripped. Nothing uploaded is stored as-is.
- They're stored in MinIO, **content-addressed**: `context/<tenant>/<project>/img/<sha256>.png`. The same pixels across builds are stored once. Regions reference the capture, and the capture references the image digest.
- Captures belong to a build, so they follow the same retention (§2.3). An image is deleted when no capture references it any more. A tenant's deletion purges its prefix.
- A tenant's stored images are capped at **2 GB**, configurable per deployment. An upload whose new pixels would take the tenant past the cap is refused with `storage_quota_exceeded` (413) and stores nothing; pixels the project already holds are deduplicated and cost nothing. Retention (§2.3) frees the quota again.
- Studio reads images through the API (`…/captures/{capture}/image`), tenant-checked, with `Cache-Control: private, max-age=31536000, immutable`. The object store never gets a public or presigned URL.

### 3.4 In Studio

The translator workspace gets a **"Where it appears"** pane next to TM and terms (intent §25):
- usages grouped by application → route → component, each with a repository link to `blob/<commit>/<file>#L<line>` when a Git connection exists (§6)
- screenshots cropped around the region, with the message outlined and its neighbours dimmed, and a full-page lightbox
- a locale toggle between the source-locale capture and a target-locale capture, where one exists
- explicit `not captured` and `unused` states

The message list gains filters by route, component, file, `unused`, `not captured` and `new on branch …`.

## 4. Branches and preview environments

### 4.1 Branch overlay in the Catalog

A feature branch adds and changes source text before it merges. Glossa must let translators work on that text early without polluting the main catalog.

- A **Branch** (Catalog aggregate) has a name, an optional PR number, the head commit, a state (`open`, `merged`, `closed`) and an optional preview URL reported by CI.
- **New keys** pushed from a branch become messages with state **`proposed`**, owned by that branch. Translators and the AI agent (M2 triggers) can fill them like any other message, and their translations are ordinary revisions.
- **Changed source** for an existing key becomes a **source proposal** on the branch, not a source revision, so the append-only source log only records merged history. The branch's preview release renders the proposal in the source locale. Translations stay against the current revision, and the PR check reports how many will become outdated per locale.
- **Removed keys** are only reported. A branch never obsoletes anything.
- **Merge = the default-branch push.** When CI pushes on the default branch, matching proposed messages become `active` and matching proposals become source revisions. That makes translations outdated through the existing events. Correctness therefore doesn't depend on a webhook arriving. The `pull_request.closed` webhook only drives cleanup.
- **Conflicts.** Two open branches proposing the same new key with **different** source fail both PR checks with `key_conflict`. With the same source, they share the proposed message. A closed, unmerged branch's proposed messages become `obsolete` 14 days after closing. A reopened PR, or a later push of the same key, reactivates them with their translations and history.
- **Rejected alternative:** Git-like branches of translations with merge semantics. Translation work happens once, in one place, and a branch only decides *when* source text goes live.

### 4.2 Preview environments

- **A branch environment delivers copy; it does not deploy an app.** It is a release and a manifest the edge serves under its own name, nothing more. M3 deploys no application per pull request (§14.2): what a PR's branch environment is *for* is a running preview the product itself deploys, or a developer's local run, pointed at that environment's delivery key. The in-product editor targets the shared `preview` deployment (§5), and screenshots come from each product's own CI (§3.2).
- Each open branch gets a **branch environment**, `pr-<number>` for GitHub PRs and `br-<8 hex of sha256(branch)>` otherwise. That fits the existing environment name rules. It has `kind: branch` and a fixed preview policy: draft, needs_review and approved, outdated included.
- A branch environment's release is built from the main catalog **plus that branch's overlay only**. Every other environment's build excludes `proposed` messages and source proposals. That's the rule that keeps release eligibility intact.
- Branch releases **can't be promoted** (`branch_release_not_promotable`), because they contain text that doesn't exist outside the branch. Staging and production still receive only releases built for them.
- **Auto-publish.** A branch push, and any translation change on the branch's messages, publishes the branch environment again, debounced by 30 s. Releases stay immutable, and history accumulates as small rows. Artifacts are content-addressed and shared.
- The branch lifecycle is a small `statekit` machine: `open → (pushed)* → merged | closed → destroyed`. Destroying an environment deletes its manifest object, so the edge answers 404. Release rows stay as history. Sweeping unreferenced artifacts is M5.
- The existing `development`, `preview`, `staging` and `production` environments keep their meaning. `preview` remains the shared "everything on the default branch" environment.
- **Limit:** 50 open branch environments per project. More fail with `too_many_branches`.

### 4.3 Delivery keys

Branch environments hold unreleased copy (intent §50), so a production key shipped in a public bundle must not be able to read them.
- The key index object (`v1/keys/<sha256>.json`) gains an `environments` allowlist and a `branches` flag. The edge enforces both and answers 404 otherwise, exactly as it does for an unknown key (SPEC §2).
- Existing keys migrate to the four default environments. New keys default to `production`. A **preview key** (`branches: true`) goes only into preview deployments.
- This is an additive, minor change to SPEC §2. Runtimes don't change.

## 5. In-product editor

**Where it runs in M3.** Against the **shared `preview` deployment** of each product — one long-lived deployment per product, reading the `preview` environment — and against a developer's local run. M3 deploys nothing per pull request (§14.2), so a PR's copy is reviewed in Studio's branch view and in the check (§6.4), and its screenshots come from the product's own CI (§3.2). Per-PR deployments, and the editor over a branch environment, wait for a later milestone.

### 5.1 Never in production

`@klarlabs-studio/glossa/overlay` is a Lit web component (RFC 0002 §9). It lives in a shadow root with constructable stylesheets, and it is **not an npm import applications make**. Three layers keep it out of production:
1. **Build time.** `@klarlabs-studio/glossa/unplugin` injects the overlay loader only when the plugin's `environment` option isn't `production`. It keys on the Glossa environment, not Vite's `mode`, because static preview deployments are built in production mode. A build with `environment: production` and `overlay: true` fails.
2. **Runtime.** The loader activates only after the runtime has loaded a manifest whose (signed) `environment` isn't `production`, and only after an explicit gesture: `?glossa=edit`, or Alt+Shift+E.
3. **Delivery.** The overlay script itself is served from the Studio origin (`/overlay/v1/overlay.js`, with an SRI hash pinned in the loader). A production page's CSP never needs to allow it.

Tests cover each layer: a production fixture build must not contain the loader (a bundle scan), and the runtime must refuse to activate with a production manifest.

### 5.2 Authentication

- The overlay can't use Studio's session cookie: it's cross-site, `SameSite=Lax`/`Strict`, and third-party cookies are going away. Instead, the overlay opens a **popup** to Studio (`/in-context/authorize?project=…&origin=…`).
- Studio checks that the origin is one of the project's registered **preview origins**, then mints an **in-context grant**. It's a 15-minute bearer token:
  - its actor is the person, so the audit trail names the person
  - its permissions are the person's grant intersected with `catalog.read`, `translations.read`, `translations.write` and `intelligence.translate`
  - it's bound to the project and the origin
- The popup returns the token with `postMessage` to that exact `targetOrigin`. The overlay keeps it **in memory only**, never in `localStorage`, and renews it through the popup while the Studio session lives.
- The API allows CORS from registered preview origins **only on the endpoints the overlay uses**, with bearer tokens and never with credentials. The token's origin must match the request's `Origin`.

### 5.3 Behaviour

- **Opening it.** Modifier-click (Alt+click) on a marked message opens the panel (intent §26). The panel lets people inspect source and translation, edit, view history, comment, see terms, ask for an AI suggestion (M2 jobs) and open the message in Studio.
- **Edits are ordinary translation revisions.** They go through the Localization API with provenance `human` and `origin_detail.in_context` (route pattern, viewport). Workflow, QA and review routing treat them like any edit, and nothing bypasses review.
- **Live preview** (intent §27). The runtime's dev-only `override(id, locale, model)` re-renders the edited message at once with the model the server parsed. Other people see the edit after the `preview` environment publishes again.
- Runtime-reported usages (§2.1) are sent in the same session: messages rendered on this route that the build didn't know about.
- **CSP for preview deployments** (documented in the runtime README):
  - `script-src` includes the Studio origin
  - `connect-src` includes the API origin
  - no `unsafe-inline` or `unsafe-eval`
  - no `frame-src`, because the flow uses a popup, not an iframe

## 6. GitHub integration

### 6.1 The App and installations

- **A GitHub App, not OAuth app tokens or PATs.** It is registered **private, under the `klarlabs-studio` organization** (§14.1): installable on our own repositories only, until Glossa's product status is decided. Permissions:
  - `checks: write`
  - `pull_requests: write` (for the PR comment)
  - `metadata: read` (mandatory)
- **No `contents` access.** Glossa never reads code, because the product's CI does the code-side work (§6.3). Webhook events: `installation`, `installation_repositories`, `pull_request` and `check_run` (`rerequested`).
- **Installing.** Studio starts the installation with a signed `state` bound to the tenant and the person. On the callback, Glossa checks through the user's OAuth token (used once, discarded) that the person can actually see that `installation_id`. That prevents claiming someone else's installation. An installation maps to exactly one tenant.
- **Git connection** (Integration aggregate): installation × repository (numeric ID, so a rename doesn't break it) × project × application, plus the default branch and an optional monorepo path. One repository can feed several projects, one per path.
- **Secrets.** The App's private key, the webhook secret and the OAuth client secret are platform configuration, not tenant data: one Kubernetes Secret in the `glossa-platform` namespace, like the release signing key, read through env (12-factor). Installation tokens (1 h) are minted on demand, cached in memory and never stored. Self-hosted instances register their own App through GitHub's manifest flow, and the configuration takes the App ID and key.

### 6.2 Webhooks

- `POST /v1/integrations/github/webhooks` checks `X-Hub-Signature-256` (HMAC-SHA256, constant-time) **before parsing**, and caps bodies at 5 MB.
- It **stores and acknowledges**: the delivery is written to an inbox table keyed by `X-GitHub-Delivery` (a duplicate is a no-op), and the endpoint answers `202` in well under GitHub's 10 s limit. A worker processes the inbox like the outbox: claimed with `FOR UPDATE SKIP LOCKED`, at least once, idempotent handlers.
- Inbox rows are tenantless until the installation is resolved, under a system policy, and move to the tenant afterwards. Delivery IDs are kept for 7 days to block replays. Payloads are dropped after processing.

### 6.3 CI: authentication, push, usages, capture

- **CI authenticates with GitHub Actions OIDC, not a stored secret.**
  - `glossa login` detects `ACTIONS_ID_TOKEN_REQUEST_URL`, requests an ID token with audience `glossa`, and exchanges it at `POST /v1/auth/github-oidc-exchanges`.
  - Identity verifies the issuer, the audience and the expiry, checks `repository_id` against a Git connection, and mints a **30-minute token** with `write` scope on that project only.
  - `auth-go` has no OIDC verifier today, so the gap is filed and filled there first (RFC 0002 §2.3). API tokens stored as a CI secret keep working as the fallback.
  - Pull requests from forks get no OIDC token and no secrets, so their check is `neutral` with an explanation.
- **The CI job**, shipped as a composite action in `.github/actions/glossa` and documented as plain commands:
  ```bash
  glossa push --branch "$GITHUB_HEAD_REF" --pr "$PR_NUMBER"    # default branch: glossa push
  glossa extract --upload                                      # Go and templates
  glossa context push .glossa/usages.json                      # from @klarlabs-studio/glossa/unplugin
  glossa capture --upload                                      # optional, needs a running app
  glossa preview register --url "$PREVIEW_URL"                 # optional
  ```
  Every upload carries the commit, and the server ties the check to that head SHA.

### 6.4 PR checks and comments

- **The check.** When `pull_request` is `opened`, `synchronize` or `reopened`, a **Glossa** check run is created as `queued` for the head SHA. It completes when that SHA's push and usages have been ingested. If nothing arrives within 30 min, it completes as `neutral` with "no Glossa CI run for this commit".
- **What it reports**, per the project's check policy (the same `require_complete` and `fail_on` as `glossa check`):
  - new keys, and invalid messages (structural QA)
  - QA findings (terminology from M2, `max_length`)
  - **untranslated new keys, by locale**
  - unknown keys, with their `file:line`
  - translations the branch will make outdated
  - key conflicts
- Findings with a location become **annotations** (GitHub accepts at most 50 per request, so they're batched). The summary is Markdown with a table per locale.
- **One sticky PR comment** holds the links: the Studio branch view, the product's preview URL, the branch environment's manifest, captures and not-captured counts, and the per-locale table. It's updated in place and never duplicated.
- **Outbox and idempotency.**
  - Domain events (`catalog.branch.pushed`, `context.build.ingested`, `localization.translation.revised` on branch messages) enqueue `integration.github.check_requested`.
  - A worker renders the report and calls GitHub through `fortify`: timeout, retry with backoff that honours `Retry-After` and secondary rate limits, a circuit breaker per installation, and a bulkhead per tenant.
  - Check runs are keyed by (repository, head SHA, name), with our ID as the `external_id`. Updates `PATCH` the stored check run, and the comment is keyed by (repository, PR) with its stored ID. A retry never creates a second check or comment.
  - `check_run.rerequested` re-enqueues the report.

## 7. Runtimes

### 7.1 React (`@klarlabs-studio/glossa/react`)

React is needed for Nomi (React on Tauri), Armada and Dispatch (Atlassian Forge) (RFC 0002 §12.2).
- **API.** `createGlossa(options)` returns an instance for `<GlossaProvider>`. `useGlossa()` returns `{ t, parts, locale, dir, setLocales, explain }`. `useMessages<M>()` gives typed accessors, and `<T id values>{default}</T>` renders a message.
- **Rendering.** Markup goes through `@klarlabs-studio/glossa/elements/parts`, with the same safe-markup rules as Vue. Translation text is never rendered as HTML.
- **SSR and React versions.** It's SSR- and hydration-safe through `useSyncExternalStore` with a server snapshot. It supports React 18.3+ and 19.
- `glossa generate` gains `generate.react`, which writes the `GlossaRegister` augmentation for `@klarlabs-studio/glossa/react`, as `generate.vue` already does for Vue.
- **Contract.** The same bar as `@klarlabs-studio/glossa/vue`:
  - the SPEC scenario and loading fixtures, rendered through React
  - hydration and SSR tests
  - capture-mode attributes (§3.1)
  - `size-limit` budgets: own code ≤ 1.5 kB, with `@klarlabs-studio/glossa` ≤ 8 kB (brotli, React excluded)
- **Forge and Tauri.** Forge's Custom UI needs a declared egress for the edge, so the README recommends bundled releases (`glossa pull --release`) with over-the-air updates optional. Tauri apps persist in `localStorage`.

### 7.2 Go runtime: documents

Nexa and Lexora render PDFs with `go-pdf/fpdf` through core fonts and a CP1252 translator. Nexa formats euros by hand (`%s%s,%02d €`). The Go runtime needs the following:

| Gap | M3 addition |
|---|---|
| Output is a flat string, and markup is lost | `Localizer.Parts(id, args)` over the engine's `FormatToParts`, exposed behind the `messageformat` port. On top of it, `HTML(id, args) template.HTML`, available as the template function `th`, follows the elements' rules: only tags on `SAFE_TAGS` become elements, and attributes are always dropped, so a translation can't add a link. `Runs(id, args) []Run` (text plus bold, italic and underline) maps directly onto fpdf's `SetFont` style. The runtime doesn't depend on fpdf. An example module shows the adapter. |
| No numbers, money or dates outside messages (table cells) | `Number`, `Percent`, `Currency`, `Unit`, `Date`, `Time` and `DateTime` on `Localizer`, and as the template functions `num`, `money` and `date`. Each one formats a synthetic MF2 expression **through the same engine**, so a cell and a sentence can never disagree. |
| Money passes through `float64` | Args accept `glossa.Decimal` (a string-backed exact decimal) and `glossa.Money{Amount, Currency}`. CLDR currency digits apply (JPY 0, EUR 2). A tax amount never touches a float. |
| Time zone is implicit (UTC) | `glossa.TimeZone(loc)`, as a call option or a `Localizer` default, is applied to `:date`, `:time` and `:datetime` placeholders that don't set `timeZone`. |
| Bidi marks in documents | Documents use `BidiIsolation(false)` for LTR output. Isolation stays on by default everywhere else. |
| CLDR coverage beyond de/en/he/ar is unproven | The runtime-format fixture gains es, fr and ja cases (plurals, EUR and JPY, long and short dates, percent, units). JS and Go must pass them, and any skip is recorded with its reason, like the known go-intl German percent gap (fixed upstream, not worked around). |

**Fonts are the product's side.** Correct output contains U+00A0, U+202F (French grouping) and CJK text, which CP1252 core fonts can't draw. Moving Nexa and Lexora onto the runtime therefore includes switching to UTF-8 TrueType fonts (`AddUTF8Font`; Noto Sans, plus Noto Sans JP for ja). That's part of their migration in the dogfood phase, not a Glossa feature.

## 8. Product context graph: the M3 slice

The graph from intent §18 connects concepts, messages, terms, screens, components and features.
- **M3 builds the part the data already supports.** It has three kinds of nodes: **message**, **component** and **route**, with edges from usages and captures. It answers three queries:
  - the messages on this route or in this component
  - where this message appears
  - the messages shown together with this one
- The agent's `message_context` uses those co-located messages as neighbours (§2.2).
- **Later:** concept ↔ message links (RFC 0003 §2.2 already gives a concept an optional product-concept link), features, documentation, and the agent operating over the graph (Phase 5). These need data M3 doesn't collect.

## 9. Surfaces

- **API** (one slice per wave touches `platform/api/openapi.yaml`, §13):
  - context builds (usage upload); usages per message and by route, component and file
  - captures (multipart upload, image read); captures per message
  - branches (upsert from CI, proposals, status report, close); branch environments through the existing environment endpoints, with `kind` and `branch`
  - delivery-key environment scopes
  - GitHub installations and Git connections; the webhook endpoint
  - the OIDC exchange; in-context grants; project preview origins
- **Studio:**
  - the "Where it appears" pane and the new filters (§3.4)
  - a branch switcher in the workspace, and a branch view (status report, check state, proposals)
  - GitHub connection settings and preview origins
- **CLI:**
  - `glossa extract --upload`, `glossa context push` and `glossa capture [--upload]`
  - `glossa push --branch --pr`, `glossa preview register` and `glossa branch status|close`
  - `glossa login` with GitHub OIDC
  - `glossa usages <key>`
  - All of them with `--json`.
- **Runtimes:**
  - `@klarlabs-studio/glossa/react` and `@klarlabs-studio/glossa/unplugin`
  - `@klarlabs-studio/glossa/overlay`, served by Studio
  - `onRender` and dev-only `override` in `@klarlabs-studio/glossa`
  - the Go additions in §7.2

## 10. Privacy and security (intent §17, §50)

- **Usages** reveal source structure (paths, component names). They're tenant data under RLS, never sent to the edge, and never sent to AI providers beyond the `message_context` tool, which already falls under the per-tenant provider setting and the `sensitive` namespace rule (RFC 0003 §7). They are never collected from production either (§2.1, §14.4).
- **Screenshots** are taken in **preview environments with fixture data only**:
  - `glossa capture` refuses a page whose runtime reports a `production` manifest
  - elements marked `data-glossa-redact` are blacked out before the screenshot is taken
  - uploads are re-encoded (§3.3)
  - screenshots are **never** sent to AI providers: multimodal context is ruled out for M3, not deferred (§14.4)
- **Overlay:** production is excluded in three layers (§5.1). The token is origin-bound, short-lived and kept in memory, and CORS is scoped to the overlay's endpoints.
- **GitHub:**
  - least-privilege permissions with no code access
  - signed webhooks with replay protection
  - verified installation ownership and repository IDs, not names
  - no stored installation tokens
  - OIDC instead of long-lived CI secrets
- **Limits:** per-tenant rate limits on the upload endpoints. A build holds at most 100,000 usages and 500 captures, with a body of at most 20 MB for usages and 10 MB per image. A tenant's stored capture images are capped at **2 GB** (`storage_quota_exceeded`, 413), configurable per deployment (§3.3).

## 11. Observability

- **Metrics** (Prometheus): usages and captures ingested, capture bytes stored per tenant, context coverage (share of active messages with usages and with regions) per project, webhook deliveries by event and outcome, GitHub calls by status with the remaining rate limit, check latency from PR event to completed check, branch environments open, and overlay edits.
- **Traces** (OpenTelemetry): one trace per CI upload (ingest → events → check), and per webhook (inbox → worker → GitHub call).
- **Logs** (`bolt`): delivery IDs, installation IDs and check IDs. Never payload bodies, tokens or image bytes.

## 12. Exit test

`platform/internal/systemtest/m3` (`make system-m3`, Docker: Postgres, MinIO, headless Chrome) runs against a real `glossa-server` and writes `REPORT.md`, like M2.

1. **Fixture app.** `testdata/app` has about 150 messages, with de as the source and en, es, fr and ja as targets. It's a Vite + Vue app with one React island and Go templates. It builds with `@klarlabs-studio/glossa/unplugin`, runs `glossa extract --upload`, and runs `glossa capture --upload` over 8 routes in de and ja at two viewports. The test asserts that **every active message has ≥ 1 usage with file, line and component, and ≥ 1 visible region**. A Playwright test then opens the "Where it appears" pane for three messages against the same server.
2. **Pull request.** A fake GitHub server receives signed, recorded webhooks and serves the App endpoints. Nothing deploys an application per PR (§14.2): `pr-7` is a delivery target the test reads through the edge, and the overlay flow runs against the fixture served from the shared `preview` environment. The flow runs like this:
   - Opening a PR creates the branch and `pr-7`.
   - A CI push adds 5 keys and 1 invalid message, so the check is `failure`, with annotations on `file:line`.
   - The invalid message is fixed, and the overlay API (with an in-context grant, from the `preview` deployment's origin) adds the missing es, fr and ja translations. The check becomes `success`. The sticky comment was updated in place, and exactly one exists.
   - Merging plus a default-branch push makes the proposed messages `active`.
   - Closing the PR destroys `pr-7`, and the edge answers 404.
   - A production delivery key never reads `pr-7`.
3. **Overlay guards.** The production build of the fixture contains no loader. The runtime refuses to activate with a production manifest.
4. **Documents.** `runtimes/go/testdata/documents/` holds a tax summary and an export: tables of EUR and JPY amounts, dates in Europe/Berlin and Asia/Tokyo, plural lines, bold labels and a long German compound. They're rendered in de, en, es, fr and ja to HTML and `Runs`, with golden files. An fpdf example with Noto fonts renders all five to PDF as a smoke test. The extended runtime-format fixture passes in JS and Go.

**Dogfood exit, in the dogfood phase:** Nexa's tax PDF and Lexora's export render through the Go runtime in production. Translators on Brotwerk and KraftSport see usages and screenshots for every message they translate.

## 13. Work breakdown

Each slice is about an hour of agent work. At most one slice per wave edits `platform/api/openapi.yaml`, and slices in the same wave don't depend on each other.

| Wave | Task | Touches the API spec |
|---|---|---|
| 1 | Context domain and tables: builds, usages, captures, regions, retention policy (library + migration) | no |
| 1 | `glossa.usages/v1` and captures JSON Schemas; the shared usage fixture suite in `runtimes/testdata/usages/` | no |
| 1 | Go runtime: `Parts`, `HTML`/`th`, `Runs` over the engine's `FormatToParts` | no |
| 1 | Go runtime: standalone formatters, `Decimal`/`Money`, time zones, template `num`/`money`/`date` | no |
| 1 | runtime-format fixture: es/fr/ja cases; JS and Go pass or record skips | no |
| 1 | `@klarlabs-studio/glossa/react`: provider, hooks, `<T>`, SSR, SPEC fixtures, size budgets | no |
| 1 | GitHub adapter: App JWT, installation tokens, checks, comments, webhook verification, against a fake GitHub | no |
| 2 | Context API: build upload, usages queries; `message_context` fills usages and co-located neighbours | yes |
| 2 | `@klarlabs-studio/glossa/unplugin` (Vite, Rollup, webpack, esbuild) + Astro wiring; passes the usage fixtures | no |
| 2 | `glossa extract` on `go/ast` and `text/template/parse`, with components; `--upload`, `context push` | no |
| 2 | Capture mode: `onRender` in `@klarlabs-studio/glossa`, data attributes in elements/Vue/React, markers, capture script | no |
| 2 | Catalog branch overlay: branches, `proposed` messages, source proposals, activation on default-branch push | no |
| 3 | Captures API: multipart upload, validation and re-encoding, MinIO with dedupe, the per-tenant storage quota, image read | yes |
| 3 | `glossa capture` on `scout`: capture plan, viewports, locales, playbooks, redaction, coverage report | no |
| 3 | Release: branch environments (overlay build, not promotable), delivery-key scopes in the key index and the edge | no |
| 3 | Studio: "Where it appears" pane and message filters | no (consume) |
| 4 | Branches API: upsert, proposals, status report, close; environment lifecycle and debounced auto-publish | yes |
| 4 | `@klarlabs-studio/glossa/overlay` (Lit): inspect, edit, history, comment, AI suggestion, against a fake API | no |
| 4 | Go document fixtures in five locales: HTML and `Runs` goldens, fpdf example with Noto fonts | no |
| 5 | GitHub API: install flow with verified ownership, Git connections, webhook endpoint and inbox | yes |
| 5 | Overlay loader: runtime dev entry, unplugin injection, the three production guards and their tests | no |
| 5 | CLI: `push --branch --pr`, `preview register`, `branch status|close`, `usages` | no |
| 6 | In-context grants: popup authorization, origin-bound token, preview origins, scoped CORS | yes |
| 6 | PR check and sticky comment workers: report, annotations, idempotent upserts, timeouts, rerequest | no |
| 6 | Studio: branch switcher and branch view, GitHub connection and preview-origin settings | no (consume) |
| 6 | `auth-go`: OIDC ID-token verifier with a cached JWKS (upstream) | no |
| 7 | GitHub OIDC exchange for CI tokens; `glossa login` in Actions; the composite action | yes |
| 7 | Overlay wiring: popup auth, live `override`, edits as revisions with `in_context` provenance, runtime usages | no |
| 7 | Context purge jobs (builds, images, closed branches) and the §11 metrics | no |
| 8 | M3 exit system test (`make system-m3`) and CI job | no |

**Deferred from the RFC 0002 M3 row:** per-route chunking and tree-shaking in the bundler plugin. That needs namespace routing in SPEC §1.1 first ("until namespace routing is specified, runtimes load and merge all namespaces"). The plugin already reports unused messages, which is what chunking would build on.

## 14. Decisions

The owner settled these on **2026-09-20**. They are decisions, not proposals: the body above follows them.

1. **The GitHub App is private, registered under `klarlabs-studio`.** It is installable on our own repositories only, until Glossa's product status is decided (RFC 0002 §16); publishing it is a later, separate decision. Its private key, webhook secret and OAuth client secret live in a Kubernetes Secret in the `glossa-platform` namespace, like the release signing key, and are read through env (12-factor) — platform configuration, never tenant data (§6.1). Self-hosted instances register their own App, as §6.1 already says.
2. **No per-PR preview deployments in M3.** Nothing deploys an application per pull request. The in-product editor works against the **shared `preview` deployment** of each product (§5), and screenshots come from each product's own CI, `glossa capture` (§3.2). Branch environments (`pr-<n>`) stay as they are — they *deliver* a branch's copy, which already shipped — but they are a manifest, not a deployment (§4.2). Deployed per-PR previews (RollOps on k3s) and the editor over a branch environment are a later milestone.
3. **Screenshot and context retention tighten**, and a quota comes with them (§2.3, §3.3, §10): the latest **3** builds per (application, branch) plus every still-current build; a closed branch's builds and captures purged **7 days** after the branch closes; a per-tenant storage quota of **2 GB**, configurable per deployment, enforced on the capture upload (`storage_quota_exceeded`). Catalog's *proposal* retention is a different rule and **stays 14 days** (§4.1): proposed messages of a closed branch become obsolete after 14 days, whatever happened to the screenshots of them.
4. **Ruled out for M3** — decided, not open:
   - **Runtime-reported usages from production.** Usages come from dev and preview sessions only; no sampled production reporting, and no switch for one (§2.1).
   - **Screenshots as AI context.** Images never go to an AI provider, with or without a per-tenant switch (§10).

   Both may be reopened as their own RFC later; neither is M3 work.
