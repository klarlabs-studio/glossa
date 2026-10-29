# RFC 0001 — Platform foundation (Phase 1)

**Status:** Superseded in part by [RFC 0002](./0002-platform-architecture.md) — 2026-09-19. Glossa is rewritten rather than evolved (D1 and §4–§5 no longer apply). D2–D10 carry over as design intent, restated in RFC 0002.
**Intent sections:** §7–10, §13–15, §22, §30–39, §44, §59–60, §62
**Supersedes:** the parts of [`docs/design.md`](../design.md) that the "Conflicts" table below names

## 1. Why this exists

[`docs/product-intent.md`](../product-intent.md) redefines Glossa: a translation backend for a handful of projects becomes localization infrastructure — messages as structured, versioned product data, delivered through immutable releases to every runtime surface.

Phase 1 of the intent proves one loop:

```text
code → message → translation → release → runtime
```

This RFC decides how the shipped v0.3 system gets there without a rewrite and without breaking the consumers already on it (Brotwerk and IRI, with Kraftsport planned). It fixes the order of work and the handful of decisions every later slice depends on. Each slice gets its own short design note when it starts; this document is the frame they must fit.

## 2. Where v0.3 stands against the intent

| Intent requirement | v0.3 reality | Consequence |
|---|---|---|
| Message is the primitive (§7) | `keys(key, description)` + `translations(value, status)` | Key→string. No arguments, no source revision, no context. |
| Durable identity, history (§8, §44) | Identity by key name ✓. History only via `audit_log` before/after | Can't tell which source text a translation was made against, so stale translations are invisible after a source edit. |
| Standards: BCP 47 (§15, §62.4) | `locales.code VARCHAR(8)`, regex `^[a-z]{2,3}(-[A-Z]{2})?$` | `zh-Hant-TW`, `sr-Latn`, `es-419` are rejected. No script, no direction. |
| MessageFormat semantics (§9) | TS-only ICU subset: variables, plural, select | No `number`/`date`/`time`/`selectordinal`. No server-side parser, so no structural QA and no argument metadata. |
| Provenance (§22) | `audit_log.actor_kind ∈ {user, ai, system}` | No model, prompt version, TM or terminology record. |
| Immutable releases, environments (§34–37) | None. Consumers read live rows. | Consumers receive `ai_translated` and `needs_review` rows (the `ListBundle` comment promises an approved-only filter that the query never applies). No rollback. |
| Control/delivery separation (§35) | One process serves the admin, the API and the bundles | An API outage takes bundle delivery down with it. Only the slot fallback in `<glossa-text>` saves the page. |
| Fallback graph (§39, §51) | Slot content only | No `fr-CA → fr → en` chain. No record of why a string rendered. |
| Typed messages, compiler (§10, §32) | `glossa scan` regex extraction | No generated API, no argument types. |
| Backend first-class (§12) | No Go runtime | Backend surfaces have to reimplement lookup and formatting. |
| TM, terminology (§19) | None | AI fan-out sends isolated strings. That's the pipeline §54 rules out. |
| Localization as CI (§30) | None | No `check` command. Structural defects reach production. |
| Agent interface (§47) | OpenAPI only | — |

What already fits and stays: tenant isolation through RLS from day one (§49), BYO-provider AI with encrypted credentials (§21), review states, the audit log, and fallback-first rendering in the web components (§51).

## 3. Decisions

### D1 — Evolve in place. No rewrite.

The hex layout (`domain → app → interfaces → infra`), sqlc, RLS and the admin SPA are sound. Every slice is an additive migration plus a use case. v1 endpoints keep working until a v2 replacement has shipped and consumers have moved.

### D2 — The message is the aggregate; "key" becomes its identifier

The `translationkey` domain package becomes `message`. The table is renamed `keys → messages` (`ALTER TABLE … RENAME` keeps the RLS policies attached). The wire keeps `key` as the field name for the message ID because it's what consumers already send.

A message gains:

| Field | Purpose |
|---|---|
| `source_revision int` | Bumped on every change to the source text. Identity doesn't change (§8). |
| `arguments jsonb` | `[{name, kind}]` derived from the parsed source (`string`, `number`, `plural`, `select{cases}`, `date`, `time`, `markup`). Feeds typed APIs and structural QA. |
| `max_length int null` | First explicit constraint (§16). More constraints come with context capture in Phase 2. |
| `state` | `active` or `obsolete`. Messages are never hard-deleted while any release references them. |

**Source content** stays where it is today, in the translation row of the project's source locale. The message owns `source_revision`, and every translation records the `source_revision` it was made against. `translation.source_revision < message.source_revision` **is** the definition of *outdated*. That's the hook that lets a source change identify affected translations (§72). It's derived, never stored as a status.

*Rejected:* moving source text onto the message row. It duplicates the source-locale translation. And if the two ever disagree, bundles, TM and the editor would read different truths.

### D3 — ICU MessageFormat (MF1) syntax is the canonical storage format, parsed on both sides

- Canonical text stays ICU MessageFormat. Translators, formatjs, CLDR tooling and every TMS on the import/export path speak it (§48).
- The TS `glossa-format` grows `number`, `date`, `time`, `selectordinal` and tag markup. All formatting is delegated to `Intl.*` (CLDR data). We don't maintain locale data (§14).
- A **Go MessageFormat module** (`format/go`, standalone so the Go SDK can depend on it without the API) provides `Parse`, `Arguments` and `Validate(source, target, locale)`, plus `Format`. The API needs it for argument extraction and structural QA. The Go SDK needs it to render.
- A shared conformance fixture suite (`format/testdata/*.json`) runs against both implementations. That's how "one conceptual model across platforms" (§62.11) is enforced rather than hoped for.
- **MessageFormat 2** (Unicode, final in LDML 47) is the likely long-term canonical model. The parsed AST is kept close to the MF2 data model (pattern / placeholder / selector / variant) so that a later switch is a serializer change, not a schema change. Adopting MF2 now would cost us the import/export ecosystem for no Phase 1 gain.

This supersedes `design.md` §2.2 ("roll own ICU subset… ~3kb"): size stays a budget, but correctness and completeness come first (§74).

### D4 — Locales are BCP 47 language tags, canonicalized, with derived direction

- Parse and canonicalize with `golang.org/x/text/language` (Go) and `Intl.Locale` (TS). Reject anything that doesn't round-trip.
- Columns widen to `VARCHAR(35)`, the RFC 5646 §4.4.1 minimum buffer.
- Language, script, region and **direction** are exposed as distinct fields (§15). Direction comes from the likely script, via CLDR likely subtags.
- Existing codes are all valid BCP 47 already, so the migration only widens columns.

### D5 — Provenance and history are first-class columns, not audit-log archaeology

`translations` gains `origin` (`human | ai | translation_memory | machine_translation | import | adaptation`), `origin_detail jsonb` (provider, model, prompt version, TM match IDs, termbase version) and `source_revision`.

A new append-only `translation_revisions` table records every value change with its origin, actor and source revision. `audit_log` stays the security audit trail. `translation_revisions` is the linguistic history (§44), and it's what TM and correction learning (§55) read from.

Review `status` keeps its four values. Workflows (§42) are Phase 4. Until then nothing new gets hard-coded into the status enum.

### D6 — Releases are immutable snapshots; environments point at them

```text
projects ─┬─ environments (development | preview | staging | production, + custom)
          │     └─ current_release_id ──┐
          └─ releases ◄─────────────────┘
                ├─ version (monotonic per project), parent_release_id, created_by, notes
                ├─ policy snapshot (which statuses were eligible)
                └─ release_bundles (locale, content jsonb, sha256)
```

- **Publish** builds bundles from the current translations and the environment's eligibility policy. Defaults: `production` gets `approved` only; `development` and `preview` get everything. Publishing stores the bundles content-addressed.
- **Promote** and **rollback** move `current_release_id`. Nothing is rebuilt, so rollback is instant and exact.
- Immutability is enforced by the database (a trigger rejects `UPDATE`/`DELETE` on `releases` and `release_bundles`), not by convention.
- The **live SSE channel** stays as the development and preview experience (§27 live preview). Production consumers follow releases. SSE emits `release.published` so long-lived clients can refresh.

### D7 — The delivery plane reads artifacts, never the control-plane tables

- Delivery routes live under their own router group (`/delivery/v1/…`) and a separate app package. Their only dependency is an `ArtifactStore` port:
  - `GET …/{project}/{env}/manifest.json`: short TTL and ETag. Maps locale → bundle URL and records the release version and fallback graph.
  - `GET …/bundles/{sha256}.json`: `Cache-Control: public, max-age=31536000, immutable`.
- The first adapter serves `release_bundles` from Postgres. The second writes the same artifacts to object storage (S3/R2/MinIO). Once that exists, the Glossa API can be down and published translations still load (§34). A CDN then fronts the bucket.
- Delivery auth: read-scoped API keys keep working. A project may also opt into public delivery through an unguessable delivery ID, because bundles are meant to ship to browsers anyway.

### D8 — The runtime fallback order is the reliability principle, made observable

SDKs resolve a message along §51:

```text
cached current release → persisted previous release → fallback-graph locales → source message / inline fallback
```

- The fallback graph is configured per project and shipped in the manifest. Default: truncate the tag (`fr-CA → fr`), then the project source locale.
- Locale negotiation uses RFC 4647 lookup. A pluggable resolver chain covers explicit → user → org → request → `Accept-Language` → default (§13).
- Every SDK exposes `explain(key)`, which returns the locale actually used, the release version and each fallback step taken (§39: fallback must be observable).

### D9 — Typed messages are generated, not hand-written

`glossa compile` reads source messages and their `arguments` (from the API, or from a pulled catalog for offline builds) and emits a typed accessor module:

```ts
messages.checkout.pay({ amount }) // amount: number — omitting it is a type error
```

It generates framework-agnostic TypeScript first. React, Vue and Lit bindings are thin adapters on top. The stringly `t("checkout.pay", …)` API stays for dynamic keys (§10). Extraction starts recognising typed accessor calls, and `scan` records `file:line` usages as the first automatically captured context (§16).

### D10 — Phase 1 AI is knowledge-aware, validated and provenance-stamped

Before Phase 1 closes, the fan-out must:
1. Validate every model output structurally (D3's `Validate`). Invalid output never becomes a translation.
2. Retrieve TM matches (exact, plus trigram fuzzy through `pg_trgm`) and matching termbase entries, and put them in the prompt as explicit constraints (§20, §54).
3. Record provider, model, prompt version and the knowledge used in `origin_detail`.

Translation memory is built from approved `translation_revisions` across the tenant. It's a view over accumulated knowledge, not a second copy people have to maintain. The termbase is its own aggregate (concept → terms per locale, with preferred and forbidden terms and a status) because §19 forbids collapsing it into a generic context blob.

## 4. Slices, in dependency order

Each slice ships on its own, keeps v1 working and ends with its tests green.

| # | Slice | Depends on | Delivers |
|---|---|---|---|
| 1 | **Locale identity** | — | D4: BCP 47 parse/canonicalize, wider columns, script/region/direction on the wire |
| 2 | **Go MessageFormat kernel** | — | D3: `format/go` module, TS formatter parity (`number/date/time/selectordinal`), shared conformance fixtures |
| 3 | **Message model v2** | 1, 2 | D2 + D5: rename, `source_revision`, `arguments`, outdated detection, provenance, `translation_revisions` |
| 4 | **Structural QA + `glossa check`** | 2, 3 | Check endpoint + CLI with human and `--json` output and CI exit codes. AI output validation (D10.1) |
| 5 | **Releases + environments** | 3 | D6: publish / promote / rollback, immutable storage, approved-only production bundles |
| 6 | **Delivery plane** | 5 | D7: manifest + content-addressed bundles, Postgres adapter, object-storage adapter |
| 7 | **TS runtime v2** | 5, 6 | D8 in `glossa-sdk`: release consumption, persisted last-good, fallback graph, `explain`. Elements move onto it |
| 8 | **Typed messages compiler** | 3 | D9: `glossa compile`, typed accessors, usage capture |
| 9 | **Go runtime SDK** | 2, 6 | `i18n.T(ctx, key, args)` with the same resolver, fallback and `explain` semantics |
| 10 | **Termbase + translation memory** | 3 | D10.2–3: termbase aggregate and API, TM lookup, knowledge-aware fan-out |
| 11 | **Agent interface (MCP)** | 4, 5, 10 | Messages, check, locales, releases and terminology as MCP tools |

Slices 1 and 2 have no dependencies and unblock everything else, so they come first.

## 5. Compatibility contract

- `GET /api/v1/projects/:slug/locales/:locale/messages` and the SSE stream stay byte-compatible until the TS runtime v2 (slice 7) has shipped and the known consumers have moved. After that they're deprecated with a `Deprecation` header, then removed in a later major.
- Consumers on v1 keep receiving all statuses, as they do today. The approved-only guarantee arrives through releases, not by silently changing the v1 endpoint under running apps.
- `glossa-elements`' `<glossa-text key>` API stays. It becomes a view over the v2 runtime.

## 6. Conflicts with earlier documents

| Earlier statement | Now |
|---|---|
| `design.md` §2.2 — "Smallest possible runtime. Roll own ICU subset (~3kb)." | D3: full MessageFormat on standard `Intl` data, size as a budget, not a goal |
| `design.md` §2.5 — "Go service uses the same JSON locale bundles" | D6/D7: runtimes consume immutable releases, not live JSON |
| `positioning.md` "Out of scope: not a CAT tool, no translation memory, no glossary" | Superseded by intent §19, §25. Positioning is rewritten |
| `positioning.md` "Not enterprise… no SSO" | Intent §49: not built yet, but the architecture must not preclude it |

## 7. Risks

- **Go date/time formatting.** `x/text` covers numbers and plurals but not CLDR date patterns. Mitigation: the Go SDK formats dates with a small CLDR-generated pattern table for enabled locales, generated at build time, never hand-maintained. It's tracked in slice 9 and the gap is documented until then.
- **Two MessageFormat implementations drifting.** Mitigation: the shared conformance fixtures are a required CI job for both.
- **Release storage growth.** Full per-locale snapshots are simple and exactly reproducible. Content addressing already dedupes unchanged locales. Delta storage waits until real volume demands it.
- **Scope pressure.** The intent is ten years wide. The rule from §74 applies: finish the end-to-end loop before adding breadth.
