# RFC 0002 — Platform architecture (the rewrite)

**Status:** Accepted — 2026-09-19
**Supersedes:** RFC 0001 D1 ("evolve in place") and RFC 0001 §4–§5 (slice order and v1 compatibility contract). RFC 0001 D2–D10 carry over as design intent and are restated in the new structure below.
**Constraints:** [`product-intent.md`](../product-intent.md) · [Klarlabs Product Standards v1](https://github.com/klarlabs-studio/klarlabs/blob/main/docs/product-standards.md)

## 1. Decision

Glossa is rebuilt from the ground up as the platform the intent describes, and the Klarlabs products dogfood it. v0.3 keeps serving its current consumers unchanged until each product has moved to the new runtime. It is then retired. There is no compatibility layer between the two: every consumer is in-house, so moving the consumers is cheaper than dragging the v1 data model into the new platform.

Why a rewrite rather than evolving in place (RFC 0001 D1):
- **The v0.3 model is key → string.** Messages, source revisions, provenance, releases, environments, knowledge and context would each be a migration layered onto a schema designed around none of them.
- **Delivery isn't separated from control.** v0.3 serves bundles from the same process and tables as the editor. Separating them is a new deployable either way.
- **Standards alignment.** v0.3 uses JWT plus password auth and a Lit SPA; the Klarlabs standard is `auth-go` (magic link, passkeys, password + TOTP) with Vue frontends on `@klarlabs-studio/ui`.
- **We control every consumer.** There's no external install base whose API we have to keep.

## 2. Principles for the rewrite

1. **The core loop first, end to end** (intent §59, §74.10): message → translation → release → runtime must work for one real product before any context gets breadth.
2. **Dogfood decides order.** A capability ships when a Klarlabs product needs it, and it's done when that product uses it in production.
3. **First-party libraries where they fit.** Where one doesn't fit, the gap is filed against the library, not worked around in Glossa.
4. **Contract-first.** OpenAPI for REST, JSON Schema for artifacts, conformance fixtures for MessageFormat. SDKs are generated or tested against the contract, never hand-synced.
5. **Standards over invention** (intent §62.4): BCP 47, CLDR, Unicode MessageFormat 2, XLIFF 2, TMX, TBX.

## 3. System shape

```text
                           ┌──────────────── CONTROL PLANE ─────────────────┐
  Studio (Vue SPA) ──┐     │  glossa-server  (Go modular monolith)          │
  CLI (Go binary) ───┼────►│   REST /v1 · MCP · SSE/WebSocket (live)        │
  Agents (MCP) ──────┤     │   bounded contexts ─ outbox ─ workers          │
  Git provider ──────┘     │   Postgres (RLS)          Object storage ◄─────┼── publish
                           └────────────────────────────────────────────────┘      │
                                                                                   │ immutable,
                           ┌──────────────── DELIVERY PLANE ────────────────┐      │ signed artifacts
  Runtimes (JS/Vue/Go/…) ─►│  glossa-edge (stateless Go) ─► object storage ◄┼──────┘
                           │  optional CDN in front                          │
                           └─────────────────────────────────────────────────┘
```

- **`glossa-server`** is a modular monolith: one binary, one Postgres, bounded contexts behind explicit ports. Microservices would buy nothing at this scale and would cost a network hop per context boundary. The module boundaries are real, though (§4), so a context can be extracted later if load demands it.
- **`glossa-edge`** is a separate, stateless deployable that only reads published artifacts from object storage. It has no database connection. The control plane can be down, mid-migration or overloaded, and published translations keep loading (intent §34–35, §51).
- **Workers** (AI translation, QA runs, screenshot capture, imports) run inside `glossa-server` as outbox consumers, scaled by replica count. They can move to a separate `glossa-worker` process when needed without code changes. The entrypoint is chosen at startup.

## 4. Bounded contexts

Each context is a Go package tree `internal/<context>/{domain,app,adapters}` with its own tables. Other contexts reach it only through its application ports or through domain events. It never touches another context's tables.

| Context | Owns | Key aggregates | Notes |
|---|---|---|---|
| **Identity** | People, sessions, org membership, API tokens | Tenant (`individual`/`organization`), Member, Role, Token | `auth-go`: magic link, passkeys, password + TOTP. Tenant context derived server-side only (standard §2–3). |
| **Catalog** | What the product says | Project, Application, **Message** (identity, source content, arguments, constraints), SourceRevision | Messages are the aggregate root of the domain (intent §7–8). |
| **Localization** | What each locale says | **Translation**, TranslationRevision (append-only), Locale, FallbackGraph | Provenance on every revision (intent §22). Outdated = made against an older source revision. |
| **Knowledge** | Accumulated linguistic knowledge | TranslationMemory unit, TermEntry (concept → terms), StyleGuide, ProductConcept | Kept as four separate aggregates, never one context blob (intent §19). TM is built from approved revisions. |
| **Intelligence** | Machine translation and review | Provider, RoutingPolicy, PromptVersion, Job, Suggestion (+ confidence) | `agent-go`/`axi-go` for the translation agent, `decisionkit` for explainable review routing. BYO provider keys, sealed at rest. |
| **Quality** | Whether it's right | CheckRun, Finding, Policy | Structural, terminology, linguistic, visual and runtime layers (intent §29). CI and PR gates read policies from here. |
| **Workflow** | Who does what next | WorkflowDefinition (`statekit` statechart), Task, Assignment | Configurable per project (intent §42). The default workflow is data, not code. |
| **Release** | What ships where | Environment, Release (immutable), Artifact, Rollout | Publish, promote, roll back, staged rollout, signing (intent §34–38). |
| **Context** | Where and how messages appear | Usage (file:line, component, route), Screenshot, Capture | Fed by the compiler, runtime telemetry (opt-in) and `scout` captures (intent §16–17, §27). |
| **Integration** | Outside systems | GitConnection, Webhook, ImportJob, ExportJob | GitHub first. XLIFF 2, TMX, TBX, PO, JSON and ICU in and out (intent §31, §48). |
| **Insights** | How healthy localization is | Metric series | `chronos` for time series; OpenTelemetry + Prometheus (intent §43, §70). |

**Events.** Contexts communicate through domain events written to a transactional **outbox** in the same transaction as the state change, then dispatched in-process to subscribers (at-least-once, idempotent handlers). Example: `SourceRevised` → Localization marks translations outdated → Intelligence queues jobs → Quality runs checks → Workflow routes the exceptions. That's intent §72, made mechanical. NATS or another broker is an adapter swap if we ever run the contexts apart.

**Event sourcing is used where history *is* the domain, not everywhere.** Translation and source revisions are append-only logs that current state is projected from (intent §44). Everything else is ordinary state plus the outbox. Full event sourcing across all contexts would buy nothing here and cost complexity.

## 5. Messages: the canonical model

- **Canonical representation: the Unicode MessageFormat 2 data model** (LDML 47, final) as structured JSON. MF2 is the standard going forward and its data model covers variables, functions, selectors, markup and variants explicitly. That's the "structured data over opaque strings" principle (intent §62.2) applied to the message itself.
- **Authoring syntaxes: ICU MessageFormat 1 and MF2 syntax.** Both parse into the canonical model. ICU MF1 stays the default in the editor and in import/export because translators, TMS vendors and every existing catalog speak it. Round-tripping is covered by the conformance suite.
- **Derived metadata, computed on write:** arguments with types (`string`, `number`, `integer`, `date`, `datetime`, `time`, `currency`, `unit`, enumerated `select` cases), plural and ordinal selectors, markup elements and a structural hash. Typed APIs, structural QA and AI prompts all read these.
- **Runtimes ship precompiled messages.** Bundles carry the canonical AST, not source text, so no parser ships to the browser or the device. Formatting delegates to `Intl.*` (JS), `x/text` plus generated CLDR tables (Go) and platform formatters on mobile.
- **One conformance suite** (`messageformat/testdata`): the official Unicode MessageFormat Working Group test suite, plus Glossa's own cases for MF1 → MF2 conversion and argument extraction. Every implementation (Go, TS, later Swift and Kotlin) must pass it in CI. That's what makes "one conceptual model across platforms" (intent §62.11) true.

**Build on the reference implementations; don't write parsers.**

| Where | Engine | Why |
|---|---|---|
| Tooling in TS (Studio preview, bundler plugin, import/export) | [`messageformat`](https://github.com/messageformat/messageformat) v4 (+ `@messageformat/icu-messageformat-1` only to *format* `mf1:` fallback functions in previews) | The MF2 spec editor's implementation, current to LDML 48. |
| Server and Go runtime | [`kaptinlin/messageformat-go`](https://github.com/kaptinlin/messageformat-go) (MF2 data model, parse, validate, format) | A port of the TS library that runs the official conformance suite. Its `mf1` package parses ICU MF1. |
| MF1 → MF2, **everywhere** | Glossa's Go converter (`messageformat.ParseMF1`), on top of `messageformat-go/mf1` | There is exactly **one** MF1 converter. The server, the CLI and the importer are Go; Studio sends MF1 source to the server. Two converters were built in M0 and disagreed on 28 of 63 fixture cases (variable naming, variant expansion, attributes). That's a permanent sync tax for no benefit, so the TS one was removed. Every conversion is verified against the MF1 reference output (`testdata/glossa/gen`). |
| JS runtime | Glossa's own interpreter over the precompiled data model (`Intl.*` only) | The runtime needs no parser, and the reference formatter's size doesn't fit the budget. The official suite keeps the interpreter honest. |

The third-party engines sit behind Glossa ports (`messageformat.Parser`, `messageformat.Formatter`). If one stalls or diverges, it's replaced behind the port, and the conformance suite proves the replacement.

**Cross-implementation proof.** The JS runtime renders every Go-converted model in `mf1-to-mf2.json` and must reproduce the MF1 reference output for each sample. A message authored in MF1, stored by the server and shipped in a release therefore reads exactly as its author meant.

## 6. Data and tenancy

- Postgres 16, shared schema, **`FORCE ROW LEVEL SECURITY`**. The app connects as a non-superuser, and an RLS isolation suite runs on every PR (standard §2).
- Tenants: `individual` (created at registration) and `organization`. The hierarchy is Tenant → Project → Application, where an application is a runtime surface (web, api, ios…). Knowledge is tenant-scoped with optional project scope (intent §19).
- **sqlc + golang-migrate. One migration stream, but each migration touches only its own context's tables.**
- **Object storage** (S3-compatible: MinIO in the product's own namespace on Longhorn volumes, the Klarlabs pattern (owner decision, 2026-09-19); any S3 API works through the adapter) holds release artifacts, screenshots and import/export files.
- Secrets (AI provider keys, git tokens) are AES-GCM sealed with a per-tenant data key under a master key. A KMS or customer-managed key is a later adapter (intent §49).

## 7. Releases and delivery

- **Publish** snapshots eligible translations (an environment policy decides eligibility: production = approved, preview = everything) into per-locale, per-namespace **artifacts**. Each artifact is content-addressed JSON (canonical AST plus metadata), and together they form a signed **release manifest** (ed25519 — intent §38 "signed artifacts").
- **Environments** point at releases. Promote and roll back move a pointer; nothing is rebuilt. **Staged rollout** serves a new release to a percentage of runtime clients by a stable client hash.
- **`glossa-edge`**:
  - `GET /{project}/{environment}/manifest` — short TTL, ETag, signature
  - `GET /a/{sha256}` — immutable, `max-age=31536000`
  - Bundle splitting per locale × namespace keeps downloads small (intent §33). Runtimes fetch the manifest, then only the artifacts they need.
- **Build-time mode**: `glossa pull --release <id>` writes the same artifacts into the repo or build output for apps that bundle catalogs instead of fetching them (intent §31).

## 8. Runtimes (SDKs)

Every runtime implements one **runtime contract**, specified in `runtimes/SPEC.md` and exercised by shared fixtures:

```text
resolve locale (RFC 4647 lookup over a pluggable resolver chain)
  → load: memory cache → persisted last-good release → edge → bundled fallback
  → format: precompiled AST + platform CLDR formatters
  → fall back: fallback graph → source message   (never throw in production)
  → explain(key): locale used, release version, fallback steps taken
```

| Runtime | Package | Shape |
|---|---|---|
| **JS core** | `@klarlabs-studio/glossa` | Framework-agnostic. Formatter, loader, cache, resolver, `explain`. Budgets (brotli): interpreter only 4 kB (3.1 kB measured), full runtime 6.5 kB (5.99 kB measured), IndexedDB storage 0.5 kB. For comparison, intl-messageformat alone is about 8 kB. |
| **Vue** | `@klarlabs-studio/glossa/vue` | Plugin + `useMessages()` + typed accessors. SSR-safe. |
| **Astro** | `@klarlabs-studio/glossa/astro` | Integration: build-time catalogs for static pages, runtime for islands. |
| **Web components** | `@klarlabs-studio/glossa/elements` | `<glossa-text>` and friends on top of the core. Continues v0.3 elements. |
| **Go** | `github.com/klarlabs-studio/glossa/runtime/go` | `glossa.T(ctx, msg, args)`, `net/http` middleware for locale resolution, typed accessors via codegen. |
| **React** | `@klarlabs-studio/glossa/react` | Hooks + typed accessors. |
| **Flutter/Dart** | `glossa` (pub.dev) | Same contract. For Pet Medical's mobile app. |
| **Swift, Kotlin** | later | Same contract. They come when a dogfood product ships a native app. |

The **SDK priority order comes from the dogfood survey (§12)**, not from this table.

**Typed messages** (intent §10): `glossa generate` emits per-language typed accessors (`messages.checkout.pay({ amount })` in TS, `msg.CheckoutPay(amount)` in Go) from the catalog's argument metadata. Missing or mistyped arguments fail at compile time.

**Compiler and extraction** (intent §32): a bundler plugin (`@klarlabs-studio/glossa/unplugin` for Vite, Rollup, webpack and esbuild) records usages with file, line and component, tree-shakes unused messages and splits chunks per route. The Go extractor uses `go/ast`. Both report to the Context context.

## 9. Surfaces

| Surface | Stack | Scope |
|---|---|---|
| **Studio** (`studio/`) | Vue 3 + Vite SPA, TypeScript strict, zod at boundaries, `@klarlabs-studio/ui` tokens + `kl-` components | Translator workspace (keyboard-first, TM/terms/context panes, review queue), localization ops (coverage, releases, workflows), settings. |
| **In-product editor** | Lit web component injected by the runtime in dev/preview environments | Modifier-click a string to inspect, edit, comment or ask AI in place (intent §26). Framework-agnostic because it has to live inside every product. |
| **CLI** (`glossa`) | Go, one static binary (brew, npm wrapper with prebuilt binaries, GitHub releases) | `init login pull push extract generate check status diff release locales messages`, all with `--json` (intent §46). |
| **MCP** | `mcp-go`, served by `glossa-server` | Messages, locales, check, releases, terminology, TM search, explain (intent §47). |
| **REST API** | OpenAPI 3.1-first, `/v1` | Everything Studio and the CLI can do (intent §45). |
| **Site** (`site/`) | Static Astro + `@klarlabs-studio/ui` | Marketing, separate deployable (standard §4). |

Live collaboration in Studio (presence, concurrent-edit warnings, live preview updates) uses Server-Sent Events from `glossa-server`, which is enough for server → client fan-out. Writes stay plain REST with optimistic concurrency (`If-Match` on revision).

## 10. Intelligence

- The **translation agent** (`agent-go`) gets a scoped toolset (`axi-go`): TM lookup, term lookup, style rules, neighbouring messages, usage context, structural validation. It translates *meaning in context*, not isolated strings (intent §20, §54).
- **Structural validation is a hard gate.** Output that doesn't match the source's arguments, selectors and markup never becomes a translation.
- **Confidence and routing.** Signals include the TM match level, terminology compliance, QA findings, model self-assessment and the history of reviewer edits on similar messages. `decisionkit` turns them into an explainable score, and per-project policy maps the score to auto-approve, review recommended or review required (intent §23–24). The explanation is stored with the suggestion.
- **Provenance** on every revision: provider, model, prompt version, knowledge used (TM unit IDs, term IDs, style-guide version) and the score explanation (intent §22).
- **Evals as code.** Golden sets per locale and domain live in the repo, and a prompt or model change must not regress them. Human edit distance and acceptance rate are tracked per locale (intent §70).
- **Learning from corrections** (intent §55): reviewer edits are recorded as structured diffs (terminology, style, meaning). In Phase 5 they become *proposals* for new terms or style rules, which humans approve. Nothing propagates automatically.

## 11. Cross-cutting

- **Observability:** `bolt` (slog) + OpenTelemetry traces + Prometheus metrics. One trace spans an API write → outbox → worker → AI call → QA.
- **Resilience:** `fortify` around every external call (AI providers, Git hosts, object storage): timeout, retry with backoff, circuit breaker, bulkhead per tenant. Per-tenant rate limits (standard §2).
- **Deployment:** GHCR images, k3s, PodSecurity `restricted`, cert-manager and traefik (standard §1), shipped through warden → kiln → RollOps like v0.3. Three images: `glossa-server`, `glossa-edge`, `glossa-studio`.
- **Quality gates:** TDD, table-driven Go tests, Testcontainers Postgres for adapters, the RLS suite, MessageFormat conformance, Playwright for Studio, axe-core accessibility checks, and size budgets for every runtime.

## 12. Dogfood plan and SDK priority

### 12.1 What the products run (survey 2026-09-19)

| Product | Frontend | Backend | Other surfaces | i18n today | Size |
|---|---|---|---|---|---|
| Brotwerk | Astro + Vue islands | Go (gin) | Magic-link email de/en, web push | **Glossa v0.3**: elements + SDK | ~100 keys |
| KraftSport Coach (IRI) | Astro + Vue | Go (net/http) | Drip emails de/en | **Glossa v0.3**: elements + SDK + CLI | ~1,000 keys |
| Pet Medical | Astro SSR + Vue | Go (mux + gRPC) | **Flutter app**, email, digests | **Glossa v0.3**: SDK over bundled JSON | ~2,700 keys |
| pet-medical-www | Astro + Vue | — | — | **Glossa v0.3**: SDK | ~100 keys |
| Nexa | Astro + Vue | Go (gin) | **German tax PDF** | Inline TS dictionary | ~50 keys |
| Lexora | Astro + Vue | Go (gin) | English PDF export | Hard-coded | — |
| Vorhut | Astro SSR + Vue | Go (gin + CLI) | Email, CLI output | Hard-coded | — |
| Senat OS | Vue SPA | Go (gin) | Email, Slack | Hard-coded | — |
| Skene | Vue SPA | Go | — | Hard-coded | — |
| Nomi | React on Tauri | Go (gin + CLI) | WhatsApp, Telegram, email | Hard-coded | — |
| Armada, Dispatch | React (Atlassian Forge) | Node (Forge) | — | Armada: own JSON, **de en es fr ja** | ~640 keys |

What follows from this:
- **Astro + Vue is the platform's primary web target**: seven apps. Static builds with islands dominate; two run SSR.
- **Go backends are the biggest untapped surface.** Ten products render email, PDFs, chat messages and CLI output with strings hard-coded in Go, and none of them localize backend text through Glossa today. The Go runtime belongs in M1, not later.
- **Existing consumers use `<glossa-text>` heavily** (about 1,260 uses in KraftSport alone). The new elements package keeps that API so migrating is a dependency swap plus the v0.3 importer, not a rewrite of templates.
- **Native mobile means Flutter** (Pet Medical), not Swift or Kotlin. React matters for Nomi and the Forge apps.
- **Every product is de + en today.** The one real multi-locale case is Armada (five locales, with es/fr/ja lagging at 416 of 638 keys). It's the test case for "the next language is configuration".

### 12.2 Runtime priority

| Order | Runtime | Needed by | Milestone |
|---|---|---|---|
| 1 | JS core + **web components** (v0.3-compatible `<glossa-text>`) | Brotwerk, KraftSport, Pet Medical, pet-medical-www | M1 |
| 2 | **Vue** + **Astro** integration | All 7 Astro + Vue apps, Senat OS, Skene | M1 |
| 3 | **Go** (incl. `html/template` + text helpers for email, PDF and CLI) | 10 Go backends | M1 |
| 4 | **React** | Nomi, Armada, Dispatch | M3 |
| 5 | **Flutter/Dart** | Pet Medical mobile | M4 |
| — | Swift, Kotlin, Godot | nothing active (BulliApp is stale since 2019) | on demand |

### 12.3 Adoption waves

| Wave | Products | Why this order | With |
|---|---|---|---|
| **Pilot** | **Brotwerk** | Smallest Glossa consumer, web + Go + email: exercises the whole core loop at low risk | M1 |
| **Migrate v0.3** | KraftSport, pet-medical-www, then Pet Medical web | Everything on v0.3 moves, then v0.3 is only kept for rollback | M1 → M2 |
| **Multi-locale** | **Armada** | Five locales with gaps; proves import + TM + AI fill + review by exception | M2 |
| **Backend-heavy** | Nexa (tax PDF), Lexora (PDF), Vorhut, Senat OS | Go runtime for documents, email and CLI; the first products localized *from* hard-coded strings | M2 → M3 |
| **Remaining** | Skene, Nomi, Dispatch, Pet Medical Flutter | React and Flutter runtimes | M3 → M4 |

## 13. Milestones

Each milestone ends with a Klarlabs product using the result in production. That's the definition of done, not "merged".

| # | Milestone | Delivers | Dogfood exit criterion |
|---|---|---|---|
| **M0** | Foundations | Repo layout, `glossa-server` kernel (tenancy, `auth-go`, RLS suite, outbox, observability), MessageFormat kernel (Go + TS) with conformance suite, OpenAPI skeleton, CI | Conformance suite green on both implementations; RLS suite green |
| **M1** | Core loop | Catalog, Localization, Release, `glossa-edge`, JS core + web components + Vue + Astro, Go runtime, CLI (`init push pull extract generate check release`), Studio v0 (editor, locales, releases), v0.3 importer | **Brotwerk** serves production strings from a Glossa release on web *and* in its Go emails |
| **M2** | Knowledge + AI | TM, termbase, style guides, translation agent with provenance, confidence and review routing, Studio translator workspace (keyboard-first, TM/terms panes, review queue), XLIFF/JSON import | **Armada's** es/fr/ja gaps are closed through review by exception; KraftSport and Pet Medical web are off v0.3 |
| **M3** | Context | Bundler plugin usages, in-product editor, preview environments per branch, `scout` screenshot capture, GitHub integration (PR checks), React runtime | Translators see where every message appears; Nexa's tax PDF and Lexora's export render through the Go runtime |
| **M4** | Quality | Layered QA, CI policies, visual QA via `scout`, quality dashboards, MCP complete, Flutter runtime | `glossa check` gates CI in every dogfood product |
| **M5** | Operations | Workflow engine (`statekit`), assignments, vendors, audit export, advanced release policies. Design: [RFC 0006](./0006-operations.md), which reconciles this row with intent Phase 4 (§1.2) | All Klarlabs products migrated; v0.3 retired |

## 14. Repository layout

The rewrite lives in the same repository. The v0.3 code stays where it is until M5, and the new tree grows beside it:

```text
glossa/
├── platform/                 # Go module: glossa-server, glossa-edge, CLI
│   ├── api/openapi.yaml      # /v1 contract (api/openapi.yaml at the root is v0.3)
│   ├── cmd/{glossa-server,glossa-edge,glossa}/
│   ├── internal/<context>/{domain,app,adapters}/
│   ├── internal/kernel/      # tenancy, outbox, observability, config, http
│   └── db/{migrations,queries}/
├── messageformat/            # conformance fixtures + Go and TS implementations
├── runtimes/
│   ├── SPEC.md               # the runtime contract
│   ├── go/
│   ├── js/{runtime,vue,astro,elements,react,unplugin}/
│   └── dart/
├── studio/                   # Vue 3 SPA
├── site/                     # Astro marketing site
├── deploy/                   # Helm chart + RollOps
├── docs/
└── apps/, packages/          # v0.3, retired at M5
```

## 15. Risks

| Risk | Mitigation |
|---|---|
| **`messageformat-go` is a one-maintainer project.** | It sits behind the `messageformat` port. The official conformance suite runs in our CI against the pinned version. If it stalls, we fork it (MIT) or replace it behind the port. Upstream fixes go upstream first. |
| **Rewrite drags on while v0.3 carries production.** | Milestones are product-gated (§13). v0.3 only gets security and data-loss fixes; features go into the new platform. |
| **Two MessageFormat stacks (Go server, JS runtime) diverge.** | One conformance suite is a required check in both CI jobs. Artifacts carry a data-model schema version, and runtimes refuse versions they don't know, falling back to the source message instead of mis-rendering. |
| **Scope: the intent spans a decade.** | Each milestone has a single dogfood exit criterion. Anything that doesn't serve it waits (intent §74.10). |

## 16. Open questions

- **Package naming.** Decided in Amendment 1 below: one npm package, `@klarlabs-studio/glossa`, with subpath exports; Go module path `go.klarlabs.de/glossa`.
- **Glossa's place in the product standard.** Is Glossa a SaaS product (full standard: marketing domain, `app.`/`api.` split, beta labeling, billing) or OSS infrastructure with a managed offering later? That decides §9's site scope and billing.

## Amendment 1: npm package naming and packaging (2026-10-04, amended 2026-10-07)

**Decision (owner).** Glossa moves entirely under Klarlabs before anything is published to npm (decision 2026-10-06, superseding the 2026-10-04 decision to publish under `@felixgeelhaar/glossa-*` first). The rewritten JavaScript packages publish as **one** npm package, `@klarlabs-studio/glossa`, with subpath exports (decision 2026-10-07, amending the first version of this amendment, which published nine `@klarlabs-studio/glossa-*` packages; nothing had reached npm yet); the Go module path is `go.klarlabs.de/glossa` (this closes the open question); images are published under `ghcr.io/klarlabs-studio`; the repository is `klarlabs-studio/glossa`. v0.3 stays `@felixgeelhaar/*` (`glossa-sdk`, `glossa-ui`, `glossa-cli`, `glossa-format`, and v0.3's `glossa-elements` 0.3.x) on branch `release/v0.3`.

| Workspace path (a private source package) | Import it as | Package version |
|---|---|---|
| `runtimes/js/runtime` | `@klarlabs-studio/glossa` (also `/idb`, `/dev`) | 0.4.0 |
| `runtimes/js/elements` | `@klarlabs-studio/glossa/elements` (`/ssr`, `/parts`) | 0.4.0 |
| `runtimes/js/vue` | `@klarlabs-studio/glossa/vue` | 0.4.0 |
| `runtimes/js/react` | `@klarlabs-studio/glossa/react` | 0.4.0 |
| `runtimes/js/astro` | `@klarlabs-studio/glossa/astro` (`/server`, `/client`, `/vue`, `/elements`, `/middleware`) | 0.4.0 |
| `runtimes/js/unplugin` | `@klarlabs-studio/glossa/unplugin` (`/vite`, `/rollup`, `/webpack`, `/esbuild`) | 0.4.0 |
| `runtimes/js/capture` | `@klarlabs-studio/glossa/capture` (`/probes`) | 0.4.0 |
| `runtimes/js/overlay` | `@klarlabs-studio/glossa/overlay` (`/bundle`, the served bundle) | 0.4.0 |
| `messageformat/js` | `@klarlabs-studio/glossa/messageformat` (`/testing`) | 0.4.0 |
| `runtimes/js/glossa` | **`@klarlabs-studio/glossa`**: the one published package, assembled from the nine above | 0.4.0 |

**One package, many subpaths (2026-10-07).**

- **Layout.** The nine source packages stay where they are, with their own tests and tsc builds, and are `private` (their names are internal workspace names, `@klarlabs-studio/glossa-runtime` and so on). `runtimes/js/glossa` is the only publishable package. Its build (`scripts/build.mjs`) copies each package's compiled `dist/` to `dist/<subpath>/` and rewrites every reference to a sibling package (`@klarlabs-studio/glossa-runtime` becomes `@klarlabs-studio/glossa`, `glossa-astro/vue` becomes `@klarlabs-studio/glossa/astro/vue`, and so on) in `.js` and `.d.ts` files. Each source's `exports` map is carried over 1:1 under its prefix; the build fails if the umbrella's map drifts from the sources' maps, if an export target is missing, or if a sibling reference survives.
- **Why rewrite instead of bundling.** The published files are still the unbundled modules the sources compile to, so tree-shaking, side-effect flags and the size budgets behave as before, and nothing is inlined twice. Because every cross-subpath import is the package's own specifier, a consumer resolves exactly one copy of the runtime, and the custom elements and the Lit context are defined once. Two places that name their own package folder are patched explicitly and asserted (Vite's `ssr.noExternal` and `optimizeDeps.exclude` list the package, now `@klarlabs-studio/glossa`; the plugin's tool version reads the umbrella's `package.json`); the overlay loader is resolved by `import.meta.resolve`, which finds `@klarlabs-studio/glossa/dev` in both layouts.
- **Dependencies.** The union of the nine packages' dependencies (`lit`, `@lit/context`, `acorn`, `unplugin`, `@jridgewell/trace-mapping`, `@messageformat/icu-messageformat-1`, `messageformat`, `zod`); `vue`, `react` and `astro` are optional peers (`peerDependenciesMeta.optional`).
- **Budgets** are unchanged and measured on the umbrella's subpath entries (`.size-limit.mjs`; `pnpm test` runs them). Importing `@klarlabs-studio/glossa` pulls in no Lit, Vue, React or capture code (checked by `scripts/verify.mjs`).
- **Trade-offs accepted.** One install downloads every dependency of every subpath (Lit and the MessageFormat parser included) even for an app that uses only the runtime; bundles are unaffected, because only imported modules are bundled, and the frameworks stay optional. In exchange: one name to publish, one trusted-publisher entry, one version, no skew between packages that must share one runtime instance, and one `pnpm add`. A change to any subpath releases the whole package, and the subpaths cannot be versioned apart.
- **Tool name.** `usages.json` names `@klarlabs-studio/glossa` as its producer (the schema's tool name is an npm package name, which a subpath is not).

- **`@glossa/studio`** keeps its name: it is an app that is never published (`private`), so it has no scope to move.
- **`glossa-elements` is a package rename.** v0.3 published `@felixgeelhaar/glossa-elements` 0.3.x; the replacement is the `@klarlabs-studio/glossa/elements` subpath of `@klarlabs-studio/glossa` 0.4.0, so the names no longer collide and §12.1's migration is a package rename plus the provider's attribute changes. v0.3's `packages/elements` and `apps/admin` (which depends on it) left the pnpm workspace on `main`; 0.3.x maintenance continues on branch `release/v0.3`. Since the names no longer collide they could return to the workspace; that is not done here.
- **Rename mechanics (done).** The scope string appeared in `package.json` names and dependency edges, import specifiers, tsconfig paths, size-limit names, CI filters and the CLI's printed hints; all were replaced in one pass. Only `@klarlabs-studio/glossa` needs a trusted-publisher entry for owner `klarlabs-studio`.
- **Go module path.** `go.klarlabs.de/glossa/<module>`; runtime and messageformat are tagged `messageformat/vX.Y.Z` and `runtimes/go/vX.Y.Z`.
