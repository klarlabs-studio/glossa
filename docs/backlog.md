
## P1.1 Locale identity (BCP 47)

RFC 0001 D4. Parse and canonicalize locale codes as full BCP 47 tags (golang.org/x/text/language in Go, Intl.Locale in TS). Widen locales.code and projects.default_locale to VARCHAR(35). Expose language, script, region and direction (derived from the likely script via CLDR likely subtags) on the locale wire shape. Acceptance: zh-Hant-TW, sr-Latn, es-419 and ar-EG accepted; ar/he/fa report rtl; non-canonical input (e.g. en_us) is rejected or canonicalized; existing codes still accepted; tests green.

---

## P1.2 Go MessageFormat kernel + TS parity

RFC 0001 D3. Standalone Go module format/go: Parse (AST close to the MF2 data model), Arguments, Validate(source, target, locale) and Format. TS glossa-format gains number, date, time, selectordinal and tag markup via Intl.*. Shared conformance fixtures in format/testdata run against both implementations as a required CI job.

---

## P1.3 Message model v2

RFC 0001 D2 + D5. Rename keys to messages (domain package translationkey to message). Messages gain source_revision, arguments (derived from the parsed source), max_length and state (active/obsolete). Translations gain origin, origin_detail and source_revision; outdated = translation.source_revision below message.source_revision. New append-only translation_revisions table. v1 wire stays compatible.

---

## P1.4 Structural QA + glossa check

RFC 0001 slice 4. Check endpoint + `glossa check` CLI command: missing/extra arguments, invalid MessageFormat, invalid plural categories for the target locale (CLDR), missing and outdated translations per locale. Human and --json output, CI-friendly exit codes. AI fan-out output is validated structurally before it is stored (D10.1).

---

## P1.5 Releases + environments

RFC 0001 D6. Environments (development, preview, staging, production, custom) point at immutable releases. Publish builds per-locale content-addressed bundles under an eligibility policy (production = approved only). Promote and rollback move the pointer. A DB trigger rejects UPDATE/DELETE on releases and release_bundles. SSE emits release.published.

---

## P1.6 Delivery plane

RFC 0001 D7. Separate /delivery/v1 router group and app package behind an ArtifactStore port: manifest.json per (project, env) with ETag and short TTL; bundles/{sha256}.json immutable with a one-year cache. Postgres adapter first, object-storage (S3/R2/MinIO) adapter second, so published translations load while the control plane is down. Read-scoped keys or an opt-in public delivery ID.

---

## P1.7 TS runtime v2

RFC 0001 D8 in glossa-sdk: consume releases from the delivery plane, persist the last good release, resolve through the fallback graph (cached current, previous, fallback locales, source), RFC 4647 locale negotiation with a pluggable resolver chain, explain(key). glossa-elements moves onto it without changing the <glossa-text> API.

---

## P1.8 Typed messages compiler

RFC 0001 D9. `glossa compile` emits a framework-agnostic typed accessor module (messages.checkout.pay({ amount })) from message arguments, from the API or a pulled catalog. Missing arguments are type errors. Scan recognizes typed accessor calls and records file:line usages as context.

---

## P1.9 Go runtime SDK

Intent §12. i18n.T(ctx, key, args) backed by the format/go kernel and the delivery plane, with disk cache, the same resolver, fallback and explain semantics as the TS runtime, and CLDR-generated date patterns for enabled locales.

---

## P1.10 Termbase + translation memory

RFC 0001 D10. Termbase aggregate (concept, definition, per-locale preferred and forbidden terms, status) with API. Translation memory over approved translation_revisions across the tenant: exact plus pg_trgm fuzzy matches. AI fan-out puts TM matches and terms into the prompt as constraints and records provider, model, prompt version and knowledge used in origin_detail.

---

## P1.11 MCP agent interface

Intent §47. MCP server exposing messages (list, create, inspect), check, locales (add), releases (publish, promote, rollback) and terminology lookups, returning structured, explainable results.

---

## M0 Foundations

RFC 0002 §13. Repo layout (platform/, messageformat/, runtimes/, studio/, site/). glossa-server kernel: config, bolt + OpenTelemetry + Prometheus, HTTP server with health probes, Postgres with FORCE RLS and the RLS isolation suite, transactional outbox, tenancy (individual/organization) and auth-go identity. MessageFormat kernel: official MFWG conformance suite vendored; Go engine (messageformat-go behind a port) with MF1→MF2 conversion and argument extraction; TS engine (messageformat v4) plus a tiny runtime interpreter; both green on the suite. OpenAPI 3.1 skeleton. CI for the new tree. Exit: conformance and RLS suites green.

---

## M1 Core loop

RFC 0002 §13. Catalog, Localization and Release contexts; glossa-edge serving signed, content-addressed artifacts from object storage; JS runtime core + web components (v0.3-compatible glossa-text) + Vue + Astro; Go runtime with html/template and text helpers; Go CLI (init push pull extract generate check release, --json); Studio v0 (editor, locales, releases); v0.3 importer. Exit: Brotwerk serves production strings from a Glossa release on web and in its Go emails.

---

## M2 Knowledge + AI

RFC 0002 §13. Translation memory, termbase, style guides; translation agent (agent-go/axi-go) with structural gate, provenance, decisionkit confidence and review routing; evals as code; Studio translator workspace (keyboard-first, TM/terms panes, review queue); XLIFF 2/JSON import. Exit: Armada's es/fr/ja gaps closed through review by exception; KraftSport and Pet Medical web off v0.3.

---

## M3 Context

RFC 0002 §13. Bundler plugin (unplugin) usages and chunking, in-product editor overlay, preview environments per branch, scout screenshot capture, GitHub integration with PR checks, React runtime. Exit: translators see where every message appears; Nexa's tax PDF and Lexora's export render through the Go runtime.

---

## M4 Quality

RFC 0002 §13. Layered QA (structural, terminology, linguistic, visual, runtime), CI policies, visual QA via scout, quality dashboards, complete MCP interface, Flutter runtime. Exit: glossa check gates CI in every dogfood product.

---

## M5 Operations

RFC 0002 §13. Workflow engine on statekit, assignments, vendors, audit export, advanced release policies (staged rollout policies, approvals). Exit: all Klarlabs products migrated; v0.3 retired. Design: [RFC 0006](./rfcs/0006-operations.md).

---
