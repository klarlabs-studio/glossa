# Glossa runtime and delivery contract (v1)

**Status:** Accepted — 2026-09-19, clarified after the first two implementations (JS, Go); amended 2026-10-01 with staged rollout (§1.4, RFC 0006 §5.2) · **Implements:** RFC 0002 §7–§8, intent §13, §33–§39, §51

This is the contract between the delivery plane (the Release context and `glossa-edge`) and every runtime (JS, Go, Dart, and later Swift and Kotlin). A runtime is conformant when it passes the scenarios in [`testdata/`](./testdata) and follows the MUST rules below. The words MUST, SHOULD and MAY are used as in RFC 2119.

## 0. Implementations

| Runtime | Where | Covers |
|---|---|---|
| JS / TS | [`js/runtime`](./js/runtime) (plus `elements`, `vue`, `react`, `astro`, `unplugin`, `capture`, `overlay`) | all of it |
| Go | [`go`](./go) | all of it |
| Dart / Flutter | [`dart`](./dart) (core) and [`dart/flutter`](./dart/flutter) (`glossa_flutter`) | all of it. The core is pure Dart — no Flutter import, so it compiles for the web and for AOT; `glossa_flutter` adds `GlossaText`, the `markup.json` safe tags as `InlineSpan`s and the asset-bundle layout ([RFC 0005 §6](../docs/rfcs/0005-quality.md)) |

## 1. Artifacts

### 1.1 Manifest

One manifest per (project, environment). It names the release currently served and every artifact in it. Schema: [`testdata/schemas/manifest.schema.json`](./testdata/schemas/manifest.schema.json).

```json
{
  "schema": "glossa.manifest/v1",
  "project": "prj_7Hc2…",
  "environment": "production",
  "release": { "id": "rel_9Qx…", "version": 42, "createdAt": "2026-09-19T08:00:00Z" },
  "sourceLocale": "de",
  "locales": [
    { "code": "de", "direction": "ltr" },
    { "code": "en", "direction": "ltr" },
    { "code": "ar", "direction": "rtl" }
  ],
  "fallback": { "de-AT": ["de"], "en-GB": ["en"], "*": ["en"] },
  "artifacts": {
    "de": { "default": { "sha256": "9f2c…", "size": 18342 } },
    "en": { "default": { "sha256": "41aa…", "size": 17110 } }
  },
  "signatures": [{ "keyId": "k_2026a", "alg": "Ed25519", "sig": "base64url…" }]
}
```

- `locales[].code` MUST be canonical BCP 47 (RFC 5646 §4.5, extensions and private use excluded), exactly as the platform stores it.
- `fallback` maps a locale to its ordered fallback locales. `"*"` is the default chain for any locale without its own entry. Fallback is a graph (intent §39): entries MAY chain (`de-AT → de-CH → de`), and runtimes MUST detect cycles and stop at the first repeat.
- `artifacts[locale][namespace]` names one artifact by the SHA-256 of its exact bytes. The `default` namespace always exists for every listed locale, even if empty. More namespaces arrive with bundle splitting (RFC 0002 §8). Until namespace routing is specified, runtimes load and merge **all** namespaces of each locale in the chain.
- Unknown top-level fields MUST be ignored. A different `schema` major version MUST be rejected, keeping the last good release (§3).
- A manifest MAY carry `rollout`, a candidate release for a share of installations (§1.4). The top-level `release`, `locales`, `fallback` and `artifacts` are always the **stable** release.

### 1.2 Artifact

One artifact holds one namespace of one locale. Schema: [`testdata/schemas/artifact.schema.json`](./testdata/schemas/artifact.schema.json).

```json
{
  "schema": "glossa.artifact/v1",
  "locale": "de",
  "namespace": "default",
  "messages": {
    "cart.checkout": { "type": "message", "declarations": [], "pattern": ["Zur Kasse"] }
  }
}
```

- `messages` values MUST be MessageFormat 2 data-model messages exactly as defined by [`messageformat/testdata/unicode/data-model/message.schema.json`](../messageformat/testdata/unicode/data-model/message.schema.json). They are precompiled, so runtimes contain no parser.
- A message ID is a dotted path of `[a-z0-9_-]` segments (`checkout.payment.submit`).
- A locale's artifact contains only the messages translated *for that locale*. Filling gaps is the runtime's fallback job, so a partly translated locale is never padded with source text.

### 1.3 Integrity and signatures

- Runtimes MUST verify an artifact's bytes against its manifest `sha256` before using it, and MUST discard it on mismatch.
- `signatures[].sig` is an Ed25519 signature over the **RFC 8785 (JCS) canonicalization** of the manifest with the `signatures` member removed.
- A runtime configured with one or more public keys MUST reject a manifest without a valid signature from one of them. A runtime without configured keys MAY skip signature verification; TLS still protects the transport. Server-side runtimes (Go) and mobile OTA SHOULD be configured with keys.

### 1.4 Staged rollout

*Added 2026-10-01 (RFC 0006 §5.2; the owner's decision in RFC 0006 §15 Q5: per-installation cohorts, any percentage per step, manual halt only).* An additive, minor change under §8. A runtime that predates this section ignores `rollout` (§1.1) and serves the stable release; see *Runtimes without rollout support* below.

A rollout serves a **candidate** release to a stable share of installations while every other installation keeps the stable release. The runtime decides which side it is on, from the signed manifest; the edge is unchanged and serves one manifest to everyone.

```json
"rollout": {
  "id": "ro_7Kq…",
  "percent": 10,
  "salt": "Zk3x9QpL0aTq7bWc1nYe2g",
  "candidate": {
    "release": { "id": "rel_9Qy…", "version": 43, "createdAt": "2026-10-01T08:00:00Z" },
    "locales": [ … ],
    "fallback": { … },
    "artifacts": { … }
  }
}
```

- `candidate.release`, `locales`, `fallback` and `artifacts` have exactly the meaning and schema of the top-level members of the same names. `schema`, `project`, `environment`, `sourceLocale` and `signatures` are shared by both releases.
- The **stable view** of a manifest is the manifest without `rollout`. The **candidate view** is the manifest with `release`, `locales`, `fallback` and `artifacts` replaced by the candidate's, and without `rollout`. A runtime activates one view, and everything else in this contract — §3's loading and atomic activation, §4's resolution, §6's `explain()` — applies to the view it activated.
- The signature (§1.3) covers the whole manifest, `rollout` included. A runtime verifies the manifest before it reads anything in `rollout`.
- `percent` is an integer from 0 to 100, written as a JSON number without a fraction or an exponent. Steps are free: any percentage may follow any other.
- `salt` is 22 base64url characters (16 random bytes, unpadded). It is used **as text**: runtimes never decode it. It stays the same for the life of a rollout — advancing changes only `percent` — so an installation in the candidate at one percentage is in it at every higher one. A new rollout gets a new `salt` and a new `id`.
- `id` names the rollout for `explain()` and the audit log. It is not part of the cohort function.
- There is no member that halts a rollout automatically, and none will be added in v1: halting is a person's decision. Aborting removes `rollout` from the manifest; completing makes the candidate the top-level release and removes `rollout`.
- A `rollout` that doesn't match the schema (a `percent` outside 0–100 or not an integer, a `salt` of another form, a `candidate` missing a member) is ignored: the runtime activates the stable view and reports a `schema` error (§6). Unknown members inside `rollout` and `candidate` are ignored, as at the top level.

**Cohort key.** The text that decides an installation's side.

- **Installation id** — client runtimes (browser, mobile, Dart, JS) and the Go runtime by default: 128 bits from a cryptographically secure random source, created the first time a manifest with a `rollout` is read, persisted in the same store as the last-good release (§3), and kept for as long as that store survives. Its key is its text form: **32 lowercase hexadecimal digits**. It is never sent anywhere (§6, telemetry).
- **Per-request key** — the Go runtime only. A server process serves many users, so one installation id would move a whole server in or out. The application MAY attach a key to a request's context (`glossa.WithCohortKey(ctx, key)`); a non-empty key is used as given, with no case folding and no Unicode normalization, and an empty or absent one falls back to the process's installation id. A runtime with per-request keys loads both views and resolves each request against the view its key selects; the activation rules below apply to each view separately.

**Cohort function.** For the manifest's `salt` *s* and a cohort key *k*:

```
digest  = SHA-256( UTF-8(s) ‖ UTF-8(k) )      the two byte strings concatenated, nothing between them
cohort  = ( digest[0]·2²⁴ + digest[1]·2¹⁶ + digest[2]·2⁸ + digest[3] ) mod 10000
                                              the first four bytes as a big-endian unsigned 32-bit integer
side    = candidate  if  cohort < percent × 100,  else stable
```

All arithmetic is on integers; no floating point is involved anywhere. `percent` 0 puts no installation in the candidate and 100 puts every one in. (2³² mod 10000 = 7296, so cohorts 0–7295 are each about one part in 430,000 more likely than the rest — immaterial at any percentage.)

| `salt` | key | cohort |
|---|---|---|
| `AAAAAAAAAAAAAAAAAAAAAA` | `00000000000000000000000000000000` | 1550 |
| `AAAAAAAAAAAAAAAAAAAAAA` | `user-42` | 4935 |
| `AAAAAAAAAAAAAAAAAAAAAA` | `jürgen@example.com` | 4213 |

Every runtime MUST reproduce these vectors and [`testdata/rollout/cohorts.json`](./testdata/rollout/cohorts.json) id for id.

**Activation.**

- A runtime computes its side for every manifest it verifies, wherever the manifest came from (network, persisted, bundled). A persisted manifest is kept as served, `rollout` included, so a restart computes the same side from the same key.
- **Stable side:** the runtime activates the stable view, and MUST NOT fetch any of the candidate's artifacts.
- **Candidate side:** the runtime activates the candidate view under §3's rules — atomically, after every artifact it needs has loaded and verified. If the candidate view can't be activated (an artifact unavailable from every source, an integrity failure, a candidate that fails the schema), the runtime activates the **stable view** of the same manifest instead, under the same rules, and reports the failure (§6). It never serves a half-activated candidate, and it never stays on an older release because a candidate failed when the stable view loads.
- When §3 compares bundled and persisted releases by `release.version`, it compares the versions of the views each would activate.

**Runtimes without rollout support.** Every runtime MUST let the application turn rollout support off. With it off — and in every runtime that predates this section — the runtime ignores `rollout`: it activates the stable view, never fetches a candidate artifact, and reports `rollout: null` in `explain()`. Such a runtime never joins a rollout; it receives the candidate when the rollout completes and the candidate becomes the top-level release. This is safe by construction, and it is why the candidate is nested and the stable release stays at the top level: the opposite layout would put every runtime that predates this section on the candidate at *any* percentage, including ones that cannot fall back from it. The signature still verifies, because it covers the manifest as served. `testdata/loading/rollout-old-runtime.json` pins it, and the runtimes written before this section pass it unchanged.

## 2. Delivery endpoints (`glossa-edge`)

| Request | Response | Caching |
|---|---|---|
| `GET /v1/{deliveryKey}/{environment}/manifest.json` | the manifest | `Cache-Control: public, max-age=60, stale-while-revalidate=300, stale-if-error=86400`; strong `ETag` (manifest digest); `304` on `If-None-Match` |
| `GET /v1/{deliveryKey}/a/{sha256}.json` | artifact bytes | `Cache-Control: public, max-age=31536000, immutable` |

- `deliveryKey` is a **publishable** key: public by design (it ships in browser bundles), scoped to one project, read-only, revocable, and it never grants access to the control plane. A revoked or unknown key answers `404`, never `401`, so key validity can't be probed separately from existence.
- A key reads only the environments in its **scope** (§2.1). A manifest request for any other environment answers `404`, exactly as for an unknown key, so a scope can't be probed either. New keys read `production` only.
- **Branch environments** preview a feature branch's unreleased text (RFC 0004 §4.2). They're named `pr-<number>` for a pull request and `br-<the first 8 hex digits of the SHA-256 of the branch name>` otherwise, and these names are reserved for them. Only a key with `branches` reads them: a **preview key**, which belongs in preview deployments only, never in a production bundle.
- Artifact requests aren't scoped by environment. Artifacts are content-addressed and shared by a project's environments, and a key learns a hash only from a manifest it may read.
- The edge serves only what's in object storage. It has no database and no control-plane dependency (RFC 0002 §3).
- Responses carry `Access-Control-Allow-Origin: *`. The artifacts are public, and credentials are never used.

### 2.1 Key index

The edge resolves a key through its **index object** at `v1/keys/<lowercase hex SHA-256 of the key>.json`, which the Release context writes while the key is active and deletes when it's revoked. Schema: [`testdata/schemas/delivery-key.schema.json`](./testdata/schemas/delivery-key.schema.json).

```json
{
  "schema": "glossa.delivery-key/v1",
  "project": "0192f5a0-7a4e-7cc3-9d1e-3a4b5c6d7e8f",
  "key_id": "0192f5a1-…",
  "environments": ["preview"],
  "branches": true
}
```

- `environments` is the allowlist of environments the key reads by name. It never names a branch environment.
- `branches: true` also allows every branch environment (`pr-<number>`, `br-<8 hex>`). It defaults to `false`.
- An object without `environments` was written before scopes existed. It reads as `development`, `preview`, `staging` and `production` without branches, the scope such keys were migrated to.
- Scopes and revocation reach the edge within its key cache TTL (seconds).

Runtimes don't read index objects and are unaffected by scopes: a request outside the scope fails like any other `404`.

## 3. Loading (reliability order)

A runtime resolves the content it renders from the first available source (intent §51):

1. **Memory**: the release already loaded in this process or page.
2. **Persisted last-good**: the most recent release that loaded and verified completely, including its manifest and every artifact it used. Browser: IndexedDB, or `localStorage` for small catalogs. Go: a cache directory. Mobile: app storage.
3. **Network**: the edge. Revalidate the manifest with `If-None-Match`. Fetch only the artifacts for the locales in the active fallback chain, and only when their `sha256` isn't already cached.
4. **Bundled**: artifacts shipped with the build (`glossa pull --release`), for offline-first apps and cold starts. The bundle layout is fixed so every runtime and the CLI agree: a directory holding `manifest.json` (the manifest exactly as the edge serves it) and `a/<sha256>.json` for each artifact (exact bytes), i.e. the edge's URL space below `/v1/{deliveryKey}/`.
5. **Inline default**: the text the developer wrote at the call site (`<glossa-text key="…">Zur Kasse</glossa-text>`, or the Go `glossa.Default("…")` option). If there's none, the message ID itself, so a missing string is visible and never blank.

**Artifacts are content-addressed**, so any source holding bytes with the manifest's hash is equivalent. Runtimes look for an artifact in memory → persisted cache → bundled → network, and only go to the network for hashes they have nowhere else.

**Bundled vs persisted releases.** When both exist at startup, the one with the higher `release.version` wins, because an app update may ship a newer catalog than the one persisted. Bundled artifacts and manifests are trusted like application code and aren't re-hashed or signature-checked. Persisted ones are verified again when loaded.

Rules:
- A new release becomes active **atomically**: only after its manifest has verified and every artifact needed for the active chain has loaded and verified. Until then, the previous release keeps serving. A half-updated release MUST never be visible.
- A failure at any step falls through to the next step. It never throws into application code and never renders an empty string. Errors go to an observable error channel (§6).
- A manifest whose `environment` differs from the configured environment MUST be rejected (`schema` error).
- One message that can't be read (not a valid data-model message) is reported as a `schema` error with its `messageId` and resolves as missing. It doesn't block the release.
- A rendered result that is the empty string falls through to the inline default or the message ID; runtimes never render `""` for a message that isn't intentionally empty in the source locale.
- Runtimes SHOULD refresh the manifest in the background (default every 5 minutes, and on page visibility or app resume). Long-lived clients MAY also react to the control plane's `release.published` event where they're connected to it (development and preview only).

## 4. Locale resolution and fallback

### 4.1 Negotiation

The application supplies an ordered list of requested locales from a **resolver chain** (intent §13). The default chain is: explicit → user preference → organization preference → request metadata → `Accept-Language` / `navigator.languages` → the manifest's `sourceLocale`. Each resolver is optional and pluggable. Its output is **canonicalized** before matching (`en_us` → `en-US`, `iw` → `he`).

The **active locale** is chosen by RFC 4647 §3.4 *Lookup* over the manifest's `locales`. For each requested tag in order, try the tag, then progressively truncate it (`zh-Hant-TW` → `zh-Hant` → `zh`, dropping a trailing single-character subtag together with the one before it). The first available tag wins. If nothing matches, use `sourceLocale`.

### 4.2 Fallback chain

The chain for the active locale `L` is built like this, dropping duplicates and stopping on cycles:

1. `L`
2. `fallback[L]`, expanded depth-first in order: each entry is followed by *its own* `fallback` entries before the next one. Recursion follows explicit edges only, not truncation or `"*"`.
3. only if `L` has no `fallback` entry: the truncations of `L` that are available locales (`fr-CA` → `fr`)
4. `fallback["*"]`, expanded the same way as step 2
5. `sourceLocale`

### 4.3 Message resolution

To render message `id`, walk the chain and use the first locale whose loaded artifact contains `id`. Format that message **with the locale it was found in** (plural rules and number formats must match the language of the text). If no locale in the chain contains `id`, use the inline default (§3.5).

## 5. Formatting

- Runtimes format with an MF2 interpreter over the data model, as `@klarlabs-studio/glossa` does. They MUST pass the runtime cases of the Unicode MessageFormat suite and `messageformat/testdata/glossa/runtime-format.json` (implementation-defined outputs excepted, and documented).
- Formatting MUST NOT throw. A failing expression renders its MF2 fallback representation (`{$name}`), and the error is reported (§6).
- Bidi isolation is on by default, per the MF2 spec. The active locale's `direction` from the manifest is exposed to the application, so it can set `dir`.
- Runtimes that render MF2 markup as HTML MUST follow [`testdata/markup.json`](./testdata/markup.json): only markup named on its `safeTags` list becomes an element, markup options never become attributes (a translation can't add a link), text is escaped, and other markup renders just its content.

## 6. Observability

Every runtime exposes:

- `explain(id, locales?)` returns, without side effects:
  ```json
  {
    "id": "cart.checkout",
    "requested": ["de-AT", "en"],
    "locale": "de-AT",
    "chain": ["de-AT", "de", "en"],
    "resolvedFrom": "de",
    "release": { "id": "rel_9Qx…", "version": 42 },
    "source": "persisted",
    "steps": [
      { "locale": "de-AT", "outcome": "missing" },
      { "locale": "de", "outcome": "found" }
    ]
  }
  ```
  - `resolvedFrom` is `null` when the inline default was used.
  - `source` is where the **active release** was loaded from: `network`, `persisted` or `bundled`. It becomes `memory` once a later refresh brings nothing new (a `304` or a failure). The startup load (construction plus the first refresh) keeps its original source. It's `inline` whenever the inline default or the message ID is rendered.
  - `steps[].outcome` is `found`, `missing` or `not-loaded` (the locale's artifacts aren't loaded, e.g. `explain` for locales other than the active chain).
  - Fallback targets that aren't in `manifest.locales` stay in the chain and resolve as `missing`.
  - `rollout` (§1.4) is `{ "id", "percent", "cohort", "side" }` when the active release's manifest carries a valid `rollout` and rollout support is on, and `null` otherwise. `cohort` is this installation's (in Go, this request's key's) cohort under that rollout; `side` is the view actually active, `candidate` or `stable` — so `side: "stable"` with `cohort < percent × 100` shows a candidate that failed to activate. `release` is the active view's release.
- An **error channel** for load, verification and format errors. Each error is `{ type, detail, messageId?, locale?, releaseId? }`, with types `network`, `integrity`, `signature`, `schema`, `format` and `missing-message`. Runtimes MUST rate-limit repeats of the same error: errors are the same when all five fields are equal, and a repeat within 60 s is dropped. `missing-message` is reported only while a release is active; a cold start without any release reports `network` once, not one error per message.
- Runtimes MUST NOT send telemetry anywhere unless the application explicitly enables it (intent §17).

## 7. Conformance fixtures

[`testdata/scenarios/*.json`](./testdata/scenarios) each describe a manifest, its artifacts (inline) and a list of cases:

```json
{
  "description": "de-AT falls back to de, then en",
  "manifest": { … },
  "artifacts": { "<sha256>": { … artifact … } },
  "cases": [
    { "requested": ["de-AT"], "id": "cart.checkout", "values": {},
      "exp": "Zur Kasse", "expLocale": "de-AT",
      "expChain": ["de-AT", "de", "en"], "expResolvedFrom": "de" }
  ]
}
```

Loading-order behaviour (persisted last-good, atomic activation, integrity failure, signature rejection, schema version, cold offline start) is covered by `testdata/loading/*.json`. Each file lists a sequence of edge responses and the expected active release after each one. Runtimes drive these through a fake transport. Field reference: [`testdata/README.md`](./testdata/README.md).

Staged rollout (§1.4) is covered by `testdata/loading/rollout-*.json` (an installation on the stable side and on the candidate side, a candidate that falls back to the stable view, an invalid `rollout`, a runtime without rollout support) and by `testdata/rollout/cohorts.json`: 10,000 installation ids with the cohort each gets under one salt, the number in the candidate at several percentages, and boundary and cohort-key vectors. The generator computes those cohorts from this section's formula and shares no code with any runtime, so runtimes that agree with it agree with the SPEC, not with each other.

Every runtime runs both suites in CI. A bug found in any runtime becomes a new case here first.

The edge's side of §2 is covered by `testdata/edge/*.json`: key index objects, the environments that have a manifest, and the status every key must get for each of them. `glossa-edge` runs it in CI.

## 8. Versioning

This contract is versioned by the `schema` fields (`glossa.manifest/v1`, `glossa.artifact/v1`). Additive, optional fields are minor changes. Anything a v1 runtime would misread needs `v2`. The edge MUST then serve both versions until no active runtime asks for v1, which is visible in edge metrics by requested path.
